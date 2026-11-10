package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var generatedID = regexp.MustCompile(`^generated-[a-f0-9]{32}$`)

// ValidateGenerated validates public viewer metadata, independently of the
// strict transport manifests required by Load. A scene is not a transport
// manifest and must never be advertised as one.
func (c PublicClip) ValidateGenerated() error {
	if err := c.Context.Validate(); err != nil {
		return err
	}
	if !generatedID.MatchString(c.ID) || c.Revision != strings.TrimPrefix(c.ID, "generated-") ||
		c.Codec != "" || c.ManifestURL != "" || strings.TrimSpace(c.Title) == "" || len(c.Title) > 256 ||
		strings.TrimSpace(c.Sport) == "" || len(c.Sport) > 128 || len(c.Description) > 4096 ||
		!utf8.ValidString(c.Title+c.Sport+c.Description) || c.SceneURL == "" || !validMediaURL(c.SceneURL) ||
		(c.PreviewURL != "" && !validMediaURL(c.PreviewURL)) || len(c.Tags) > 10 {
		return fmt.Errorf("invalid generated clip metadata")
	}
	seen := map[string]bool{}
	for _, tag := range c.Tags {
		key := strings.ToLower(tag)
		if !validTag(tag) || seen[key] {
			return fmt.Errorf("invalid generated clip tags")
		}
		seen[key] = true
	}
	return nil
}

// WithPublicClips overlays a small durable generated registry on a prevalidated
// static feed. Each origin request reads the current registry; no local cache
// can hide a cross-instance publication after a client no-cache revalidation.
// Static metadata buffers are shared, and only the <=10 generated rows are
// encoded. Registry failures return 503, never an apparently empty new feed.
func WithPublicClips(base http.Handler, load func(context.Context) ([]PublicClip, error)) (http.Handler, error) {
	static, ok := base.(*handler)
	if !ok || load == nil {
		return nil, fmt.Errorf("a static feed and public registry are required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(catalogSHAHeader, static.catalogSHA256)
		if r.URL.EscapedPath() != feedPath || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			static.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		rows, err := load(ctx)
		if err == nil && len(rows) > 10 {
			err = fmt.Errorf("generated registry exceeds its release budget")
		}
		merged := *static
		if err == nil {
			rows = slices.Clone(rows)
			slices.SortFunc(rows, func(a, b PublicClip) int { return strings.Compare(a.ID, b.ID) })
			for index, row := range rows {
				if err = row.ValidateGenerated(); err != nil {
					break
				}
				if _, exists := static.byID[row.ID]; exists || (index > 0 && rows[index-1].ID == row.ID) {
					err = fmt.Errorf("duplicate published clip ID")
					break
				}
				data, encodeErr := json.Marshal(row)
				if encodeErr != nil {
					err = encodeErr
					break
				}
				merged.extra = append(merged.extra, newJSONDocument(data))
				merged.extraSearch = append(merged.extraSearch, newSearchEntry(row))
			}
		}
		if err != nil {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			writeError(w, r, 503, "feed_unavailable")
			return
		}
		if len(rows) > 0 {
			canonical, _ := json.Marshal(rows)
			sum := sha256.Sum256(append([]byte(static.snapshot+"\n"), canonical...))
			merged.snapshot = hex.EncodeToString(sum[:])
		}
		merged.ServeHTTP(w, r)
	}), nil
}

// GeneratedTags keeps arbitrary reviewed sport labels out of the tag grammar.
func GeneratedTags(sport, model string) []string {
	tags := []string{model}
	if validTag(sport) && !strings.EqualFold(sport, model) {
		tags = append([]string{sport}, tags...)
	}
	return tags
}
