package publication

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/stream"
)

var heldoutName = regexp.MustCompile(`^heldout-[0-9]+-frame-[0-9]+\.webp$`)

type fetchAsset func(context.Context, string, Proof, string) ([]byte, error)

// Verify validates local bytes and, when requested, the same bytes at the fixed
// HTTPS origin. The caller must provide a deadline for this finite operation.
func Verify(ctx context.Context, opts Options) (*Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, fmt.Errorf("verification requires a context deadline")
	}
	origin, err := originURL(opts.Origin)
	if err != nil {
		return nil, err
	}
	var fetch fetchAsset
	if opts.Remote {
		r := newRemoteReader(origin)
		defer r.client.CloseIdleConnections()
		fetch = r.read
	}
	return verify(ctx, opts, origin, fetch)
}

func loadCatalog(filename string) (document, []byte, error) {
	data, err := readDocument(filename)
	if err != nil {
		return document{}, nil, err
	}
	var doc document
	if err := decode(data, &doc); err != nil {
		return doc, nil, err
	}
	if _, err := catalog.Load(bytes.NewReader(data)); err != nil {
		return doc, nil, err
	}
	if len(doc.Clips) > 256 {
		return doc, nil, fmt.Errorf("catalog exceeds 256 clips")
	}
	return doc, data, nil
}

func verify(ctx context.Context, opts Options, origin *url.URL, fetch fetchAsset) (*Receipt, error) {
	doc, catalogData, err := loadCatalog(opts.CatalogPath)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	ledgerData, err := readDocument(opts.ApprovalsPath)
	if err != nil {
		return nil, fmt.Errorf("approvals: %w", err)
	}
	var ledger Ledger
	if err := decode(ledgerData, &ledger); err != nil {
		return nil, fmt.Errorf("approvals: %w", err)
	}
	if ledger.Version != 1 || ledger.Clips == nil || ledger.Retained == nil || len(ledger.Clips)+len(ledger.Retained) != len(doc.Clips) {
		return nil, fmt.Errorf("approvals require version 1 and exactly one clip or retained row per catalog entry")
	}
	receipt := &Receipt{Version: 1, Verifier: "rangle-publish-v1", Scope: "local", Origin: origin.String(),
		CatalogSHA256: hash(catalogData), ApprovalsSHA256: hash(ledgerData),
		Checked: []CheckedClip{}, Retained: []RetainedClip{}, Removed: []RemovedClip{}}
	if fetch != nil {
		receipt.Scope = "local-and-remote"
	}
	previous := map[string]entry{}
	if opts.PreviousCatalogPath != "" || opts.PreviousSHA256 != "" {
		if opts.PreviousCatalogPath == "" || !validHash(opts.PreviousSHA256) {
			return nil, fmt.Errorf("previous catalog requires its exact body SHA256")
		}
		old, data, err := loadCatalog(opts.PreviousCatalogPath)
		if err != nil {
			return nil, fmt.Errorf("previous catalog: %w", err)
		}
		if hash(data) != opts.PreviousSHA256 {
			return nil, fmt.Errorf("previous catalog body SHA256 mismatch")
		}
		receipt.PreviousCatalogSHA256 = opts.PreviousSHA256
		for _, e := range old.Clips {
			previous[e.Manifest.ClipID] = e
		}
	}
	approved := map[string]Approval{}
	retained := map[string]Retained{}
	allFiles := map[string]Proof{}
	for _, a := range ledger.Clips {
		if _, exists := approved[a.ClipID]; exists {
			return nil, fmt.Errorf("duplicate approval %q", a.ClipID)
		}
		if err := validateApproval(a); err != nil {
			return nil, fmt.Errorf("approval %q: %w", a.ClipID, err)
		}
		approved[a.ClipID] = a
		for filename, p := range a.Files {
			if _, exists := allFiles[filename]; exists {
				return nil, fmt.Errorf("duplicate file %q", filename)
			}
			allFiles[filename] = p
			receipt.CheckedBytes += p.Bytes
		}
	}
	for _, r := range ledger.Retained {
		if _, exists := retained[r.ClipID]; exists {
			return nil, fmt.Errorf("duplicate retained row")
		}
		if _, exists := approved[r.ClipID]; exists {
			return nil, fmt.Errorf("clip cannot be both checked and retained")
		}
		if strings.TrimSpace(r.Actor) == "" || strings.TrimSpace(r.Basis) == "" || len(r.Actor) > 256 || len(r.Basis) > 4096 {
			return nil, fmt.Errorf("retained row needs actor and explicit inheritance basis")
		}
		retained[r.ClipID] = r
	}
	if len(allFiles) > maxFiles || receipt.CheckedBytes > maxTotalBytes {
		return nil, fmt.Errorf("asset snapshot exceeds file/byte limits")
	}
	receipt.CheckedFiles = len(allFiles)
	if err := checkRoot(ctx, opts.Root, allFiles); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, e := range doc.Clips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, revision := e.Manifest.ClipID, e.Manifest.Revision
		old, existed := previous[id]
		if r, ok := retained[id]; ok {
			if !existed || r.Revision != revision || contentHash(old) != contentHash(e) {
				return nil, fmt.Errorf("retained clip %q differs from pinned previous entry", id)
			}
			receipt.Retained = append(receipt.Retained, RetainedClip{r, contentHash(e), "pinned-previous-only"})
			delete(retained, id)
		} else {
			a, ok := approved[id]
			if !ok || a.Revision != revision {
				return nil, fmt.Errorf("clip %q lacks a matching approved revision", id)
			}
			if existed && old.Manifest.Revision == revision {
				return nil, fmt.Errorf("existing revision %q cannot be republished or changed; use explicit retention", id)
			}
			if err := verifyEntry(ctx, e, a, origin, root, fetch); err != nil {
				return nil, fmt.Errorf("clip %q: %w", id, err)
			}
			receipt.Checked = append(receipt.Checked, CheckedClip{id, revision, contentHash(e), a})
			delete(approved, id)
		}
		delete(previous, id)
	}
	if len(approved) != 0 || len(retained) != 0 {
		return nil, fmt.Errorf("approval row absent from catalog")
	}
	ids := make([]string, 0, len(previous))
	for id := range previous {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := previous[id]
		receipt.Removed = append(receipt.Removed, RemovedClip{id, e.Manifest.Revision, contentHash(e)})
	}
	return receipt, nil
}

func validateApproval(a Approval) error {
	if a.Status != "approved" || strings.TrimSpace(a.ClipID) == "" || strings.TrimSpace(a.Revision) == "" {
		return fmt.Errorf("revision must be approved")
	}
	for _, value := range []string{a.Actor, a.Basis, a.SourceURL, a.License, a.LicenseURL, a.Modifications, a.Attribution} {
		if strings.TrimSpace(value) == "" || len(value) > 4096 {
			return fmt.Errorf("missing or oversized provenance/permission field")
		}
	}
	if len(a.Caveat) > 4096 {
		return fmt.Errorf("oversized caveat")
	}
	for _, value := range []string{a.SourceURL, a.LicenseURL} {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
			return fmt.Errorf("source and license need explicit HTTPS URLs")
		}
	}
	if len(a.Files) == 0 || len(a.Files) > 64 {
		return fmt.Errorf("revision needs 1..64 public files")
	}
	prefix := "/assets/" + a.ClipID + "/" + a.Revision + "/full/"
	for filename, p := range a.Files {
		if p.Bytes <= 0 || !validHash(p.SHA256) {
			return fmt.Errorf("invalid file size/SHA256")
		}
		limit := int64(256 << 10)
		switch {
		case filename == "/manifests/"+a.ClipID+"/"+a.Revision+".json":
		case strings.HasPrefix(filename, prefix):
			name := strings.TrimPrefix(filename, prefix)
			switch name {
			case "model.rgs":
				limit = maxModel
			case "scene.json":
				limit = 64 << 10
			case "metrics.json":
				limit = 256 << 10
			case "preview.webm":
				limit = 16 << 20
			default:
				if indexedModelName.MatchString(name) {
					limit = maxModel
					break
				}
				if indexedMetricsName.MatchString(name) {
					limit = 256 << 10
					break
				}
				if !heldoutName.MatchString(name) {
					return fmt.Errorf("file %q is outside public artifact allowlist", filename)
				}
				limit = 4 << 20
			}
		default:
			return fmt.Errorf("file %q escapes revision allowlist", filename)
		}
		if path.Clean(filename) != filename || strings.ContainsAny(filename, "\\%?#") || p.Bytes > limit {
			return fmt.Errorf("invalid or oversized public file %q", filename)
		}
	}
	return nil
}

func checkRoot(ctx context.Context, root string, expected map[string]Proof) error {
	seen := map[string]bool{}
	visited := 0
	err := filepath.WalkDir(root, func(filename string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > maxFiles*6 {
			return fmt.Errorf("publication root exceeds directory/file traversal limit")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in publication root: %s", filename)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("non-regular file in publication root")
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		key := "/" + filepath.ToSlash(relative)
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("undeclared file in publication root: %s", key)
		}
		seen[key] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("publication root is missing declared files")
	}
	return nil
}

func verifyEntry(ctx context.Context, e entry, a Approval, origin *url.URL, root *os.Root, fetch fetchAsset) error {
	m := e.Manifest
	segmented := m.Codec == segmentedCodec
	if (m.Codec != "rangle-temporal-rgs1" && !segmented) || len(m.Representations) != 1 || m.Representations[0].ID != "full" {
		return fmt.Errorf("new publication supports one full RGS1 representation")
	}
	r := m.Representations[0]
	prefix := "/assets/" + m.ClipID + "/" + m.Revision + "/full/"
	if r.InitURL != prefix+"scene.json" || len(r.Segments) == 0 || (!segmented && len(r.Segments) != 1) || (segmented && len(r.Segments) < 2) {
		return fmt.Errorf("scene/model must share the exact clip revision and profile")
	}
	for i, segment := range r.Segments {
		model, _ := segmentNames(i)
		if segment.URL != prefix+model {
			return fmt.Errorf("segment %d has a noncanonical model path", i)
		}
	}
	for _, pair := range [][2]string{{e.SceneURL, prefix + "scene.json"}, {e.PreviewURL, prefix + "preview.webm"}} {
		actual, err := assetPath(pair[0], origin)
		if err != nil || actual != pair[1] {
			return fmt.Errorf("scene/preview must share the exact clip revision")
		}
	}
	if !strings.Contains(e.Description, a.Attribution) || !strings.Contains(a.Attribution, a.License) {
		return fmt.Errorf("catalog description lacks approved attribution/license")
	}
	required := map[string]bool{m.URL(): true, prefix + "scene.json": true, prefix + "preview.webm": true}
	for i, segment := range r.Segments {
		model, metrics := segmentNames(i)
		required[prefix+model], required[prefix+metrics] = true, true
		if a.Files[segment.URL] != (Proof{segment.SizeBytes, segment.SHA256}) {
			return fmt.Errorf("transport and approval proofs differ at segment %d", i)
		}
	}
	for filename := range required {
		if _, ok := a.Files[filename]; !ok {
			return fmt.Errorf("missing proof for %s", filename)
		}
	}
	for filename := range a.Files {
		if !required[filename] && !heldoutName.MatchString(path.Base(filename)) {
			return fmt.Errorf("unreferenced public artifact %s", filename)
		}
	}
	if a.Files[r.InitURL].Bytes != r.InitSizeBytes {
		return fmt.Errorf("transport and approval initialization proofs differ")
	}
	names := make([]string, 0, len(a.Files))
	for filename := range a.Files {
		names = append(names, filename)
	}
	sort.Strings(names)
	files := make(map[string][]byte, len(names))
	for _, filename := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		proof := a.Files[filename]
		f, err := root.Open(strings.TrimPrefix(filename, "/"))
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != proof.Bytes {
			f.Close()
			return fmt.Errorf("local %s: incorrect file type/size", filename)
		}
		data, readErr := io.ReadAll(io.LimitReader(f, proof.Bytes+1))
		closeErr := f.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if int64(len(data)) != proof.Bytes || hash(data) != proof.SHA256 {
			return fmt.Errorf("local %s: size/SHA256 mismatch", filename)
		}
		if fetch != nil {
			if _, err := fetch(ctx, filename, proof, mediaType(filename)); err != nil {
				return err
			}
		}
		files[filename] = data
	}
	var manifest stream.Manifest
	// Decode with the real type as well as duplicate-key checking; a standalone
	// manifest cannot disagree with its embedded catalog transport metadata.
	if err := decode(files[m.URL()], &manifest); err != nil {
		return fmt.Errorf("standalone manifest: %w", err)
	}
	if contentHash(manifest) != contentHash(e.Manifest) {
		return fmt.Errorf("standalone manifest differs from catalog")
	}
	if segmented {
		if err := validateSegmentedModels(files, e, a, prefix); err != nil {
			return err
		}
	} else {
		if err := validateSceneModel(files[prefix+"scene.json"], files[prefix+"model.rgs"], files[prefix+"metrics.json"], e, a); err != nil {
			return err
		}
		model := files[prefix+"model.rgs"]
		if err := validateWebM(files[prefix+"preview.webm"], int(little32(model[16:20])), float32At(model[20:24])); err != nil {
			return err
		}
	}
	for filename, data := range files {
		if heldoutName.MatchString(path.Base(filename)) {
			if len(data) < 21 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" || int64(len(data)) != int64(little32(data[4:8]))+8 ||
				(string(data[12:16]) != "VP8 " && string(data[12:16]) != "VP8L" && string(data[12:16]) != "VP8X") || little32(data[16:20]) == 0 || int64(little32(data[16:20]))+20 > int64(len(data)) {
				return fmt.Errorf("invalid heldout WebP header")
			}
		}
	}
	return nil
}

func mediaType(filename string) string {
	switch path.Ext(filename) {
	case ".json":
		return "application/json"
	case ".rgs":
		return "application/octet-stream"
	case ".webm":
		return "video/webm"
	case ".webp":
		return "image/webp"
	}
	return ""
}
