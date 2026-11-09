package publication

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/stream"
)

type fixture struct {
	t      *testing.T
	opts   Options
	doc    document
	ledger Ledger
	files  map[string][]byte
	prefix string
}

func jsonBytes(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{t: t, opts: Options{CatalogPath: filepath.Join(dir, "catalog.json"), ApprovalsPath: filepath.Join(dir, "approvals.json"), Root: filepath.Join(dir, "public"), Origin: "https://media.example.com"}, prefix: "/assets/clip-a/rev-a/full/", files: map[string][]byte{}}
	if err := os.MkdirAll(f.opts.Root, 0700); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 32+56)
	copy(raw, "RANGL4D1")
	binary.LittleEndian.PutUint32(raw[8:12], 1)
	binary.LittleEndian.PutUint32(raw[12:16], 56)
	binary.LittleEndian.PutUint32(raw[16:20], 2)
	binary.LittleEndian.PutUint32(raw[20:24], math.Float32bits(25))
	binary.LittleEndian.PutUint32(raw[24:28], math.Float32bits(.04))
	for _, i := range []int{3, 4, 5, 12, 13} {
		binary.LittleEndian.PutUint32(raw[32+4*i:32+4*i+4], math.Float32bits(1))
	}
	f.files[f.prefix+"model.rgs"] = raw
	f.files[f.prefix+"scene.json"] = jsonBytes(map[string]any{"version": 1, "frameCount": 2, "frameRate": 25,
		"camera":      map[string]any{"position": []int{0, 0, 1}, "lookAt": []int{0, 0, 0}, "up": []int{0, 1, 0}, "fovYDegrees": 40},
		"attribution": map[string]any{"label": "test clip", "source": "https://source.example/dataset", "license": "CC BY 4.0"}, "temporal": map[string]any{"url": "model.rgs"}})
	f.files[f.prefix+"metrics.json"] = jsonBytes(map[string]any{"mode": "dynamic", "clip_id": "clip-a", "source": "https://source.example/dataset", "license": "CC BY 4.0",
		"input_sha256": strings.Repeat("a", 64), "model_bytes": len(raw), "model_sha256": hash(raw), "raw_model_bytes": len(raw), "raw_model_sha256": hash(raw),
		"frame_count": 2, "frame_rate": 25, "duration_seconds": .04, "frame_times_seconds": []float64{0, .04}, "evaluation_extra": map[string]any{"psnr": 20}})
	f.files[f.prefix+"preview.webm"] = testWebM(.08)
	m := stream.Manifest{Version: 1, ClipID: "clip-a", Revision: "rev-a", Codec: "rangle-temporal-rgs1", Representations: []stream.Representation{{ID: "full", BitrateBPS: 100000, InitURL: f.prefix + "scene.json", PeakDecodedBytes: 100000, DecodeTimeMS: 10, Segments: []stream.Segment{{Index: 0, StartSeconds: 0, DurationSeconds: .04, URL: f.prefix + "model.rgs", Independent: true}}}}}
	f.doc = document{Version: 1, Clips: []entry{{Title: "test clip", Sport: "basketball", Description: "Creators / CC BY 4.0; reconstructed", PreviewURL: f.opts.Origin + f.prefix + "preview.webm", SceneURL: f.opts.Origin + f.prefix + "scene.json", Manifest: m}}}
	f.ledger = Ledger{Version: 1, Clips: []Approval{{ClipID: "clip-a", Revision: "rev-a", Status: "approved", Actor: "codex", Basis: "operator records existing approved scope; no personal user review", SourceURL: "https://source.example/dataset", License: "CC BY 4.0", LicenseURL: "https://creativecommons.org/licenses/by/4.0/", Modifications: "temporal reconstruction and compression", Attribution: "Creators / CC BY 4.0", Caveat: "calibration supplied by authors; no separate archive license", Files: map[string]Proof{}}}, Retained: []Retained{}}
	f.save()
	return f
}

func (f *fixture) write(filename string, data []byte) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) save() {
	f.t.Helper()
	m := &f.doc.Clips[0].Manifest
	m.Representations[0].InitSizeBytes = int64(len(f.files[f.prefix+"scene.json"]))
	s := &m.Representations[0].Segments[0]
	s.SizeBytes = int64(len(f.files[f.prefix+"model.rgs"]))
	s.SHA256 = hash(f.files[f.prefix+"model.rgs"])
	f.files[m.URL()] = jsonBytes(*m)
	f.ledger.Clips[0].Files = map[string]Proof{}
	for filename, data := range f.files {
		f.write(filepath.Join(f.opts.Root, strings.TrimPrefix(filename, "/")), data)
		f.ledger.Clips[0].Files[filename] = Proof{int64(len(data)), hash(data)}
	}
	f.write(f.opts.CatalogPath, jsonBytes(f.doc))
	f.write(f.opts.ApprovalsPath, jsonBytes(f.ledger))
}

func (f *fixture) changeJSON(filename string, change func(map[string]any)) {
	var data map[string]any
	if err := json.Unmarshal(f.files[filename], &data); err != nil {
		f.t.Fatal(err)
	}
	change(data)
	f.files[filename] = jsonBytes(data)
}

func (f *fixture) verify() (*Receipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return Verify(ctx, f.opts)
}

func TestVerifyFullSnapshotAndIdempotentReceipt(t *testing.T) {
	f := newFixture(t)
	before, err := os.ReadFile(f.opts.CatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.verify()
	if err != nil {
		t.Fatal(err)
	}
	if r.Scope != "local" || r.CheckedFiles != 5 || len(r.Checked) != 1 || r.CatalogSHA256 != hash(before) {
		t.Fatalf("incorrect receipt: %+v", r)
	}
	output := filepath.Join(t.TempDir(), "receipt.json")
	if err := WriteReceipt(output, r); err != nil {
		t.Fatal(err)
	}
	r2, err := f.verify()
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(output, r2); err != nil {
		t.Fatal(err)
	}
	r2.CatalogSHA256 = strings.Repeat("b", 64)
	if err := WriteReceipt(output, r2); err == nil {
		t.Fatal("replaced a different receipt")
	}
	after, _ := os.ReadFile(f.opts.CatalogPath)
	if !bytes.Equal(before, after) {
		t.Fatal("verification modified catalog")
	}
}

func TestGateRejectsInvalidPublication(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fixture)
	}{
		{"unapproved", func(f *fixture) { f.ledger.Clips[0].Status = "pending"; f.save() }},
		{"no approval", func(f *fixture) { f.ledger.Clips = []Approval{}; f.write(f.opts.ApprovalsPath, jsonBytes(f.ledger)) }},
		{"missing actor", func(f *fixture) { f.ledger.Clips[0].Actor = ""; f.save() }},
		{"missing basis", func(f *fixture) { f.ledger.Clips[0].Basis = ""; f.save() }},
		{"missing license", func(f *fixture) { f.ledger.Clips[0].License = ""; f.save() }},
		{"missing license URL", func(f *fixture) { f.ledger.Clips[0].LicenseURL = ""; f.save() }},
		{"missing modifications", func(f *fixture) { f.ledger.Clips[0].Modifications = ""; f.save() }},
		{"wrong revision approval", func(f *fixture) { f.ledger.Clips[0].Revision = "other"; f.save() }},
		{"missing file", func(f *fixture) {
			if err := os.Remove(filepath.Join(f.opts.Root, f.prefix, "model.rgs")); err != nil {
				f.t.Fatal(err)
			}
		}},
		{"tampered file", func(f *fixture) { f.write(filepath.Join(f.opts.Root, f.prefix, "model.rgs"), []byte("tampered")) }},
		{"same size tamper", func(f *fixture) {
			data := bytes.Clone(f.files[f.prefix+"model.rgs"])
			data[len(data)-1] ^= 1
			f.write(filepath.Join(f.opts.Root, f.prefix, "model.rgs"), data)
		}},
		{"wrong declared size", func(f *fixture) {
			p := f.ledger.Clips[0].Files[f.prefix+"preview.webm"]
			p.Bytes++
			f.ledger.Clips[0].Files[f.prefix+"preview.webm"] = p
			f.write(f.opts.ApprovalsPath, jsonBytes(f.ledger))
		}},
		{"raw NPZ injection", func(f *fixture) { f.write(filepath.Join(f.opts.Root, "private.npz"), []byte("private")) }},
		{"declared weight injection", func(f *fixture) { f.files[f.prefix+"weights.pt"] = []byte("private"); f.save() }},
		{"symlink escape", func(f *fixture) {
			if err := os.Symlink(f.opts.CatalogPath, filepath.Join(f.opts.Root, "private-link")); err != nil {
				f.t.Fatal(err)
			}
		}},
		{"unapproved attribution", func(f *fixture) { f.doc.Clips[0].Description = "omitted"; f.save() }},
		{"unapproved scene source", func(f *fixture) {
			f.changeJSON(f.prefix+"scene.json", func(m map[string]any) { m["attribution"].(map[string]any)["source"] = "https://other.example" })
			f.save()
		}},
		{"scene temporal escape", func(f *fixture) {
			f.changeJSON(f.prefix+"scene.json", func(m map[string]any) { m["temporal"].(map[string]any)["url"] = "https://private.example/model.rgs" })
			f.save()
		}},
		{"scene timeline", func(f *fixture) {
			f.changeJSON(f.prefix+"scene.json", func(m map[string]any) { m["frameCount"] = 3 })
			f.save()
		}},
		{"scene camera null", func(f *fixture) {
			f.changeJSON(f.prefix+"scene.json", func(m map[string]any) { m["camera"].(map[string]any)["intrinsics"] = nil })
			f.save()
		}},
		{"transport timeline gap", func(f *fixture) { f.doc.Clips[0].Manifest.Representations[0].Segments[0].StartSeconds = .1; f.save() }},
		{"transport timeline mismatch", func(f *fixture) {
			f.doc.Clips[0].Manifest.Representations[0].Segments[0].DurationSeconds = .2
			f.save()
		}},
		{"preview other revision", func(f *fixture) {
			f.doc.Clips[0].PreviewURL = "https://media.example.com/assets/clip-a/other/full/preview.webm"
			f.save()
		}},
		{"preview other origin", func(f *fixture) { f.doc.Clips[0].PreviewURL = "https://private.example/preview.webm"; f.save() }},
		{"scene equals preview", func(f *fixture) { f.doc.Clips[0].SceneURL = f.doc.Clips[0].PreviewURL; f.save() }},
		{"bad preview", func(f *fixture) { f.files[f.prefix+"preview.webm"] = []byte("not WebM"); f.save() }},
		{"preview wrong duration", func(f *fixture) { f.files[f.prefix+"preview.webm"] = testWebM(3); f.save() }},
		{"preview wrong frame time", func(f *fixture) {
			f.files[f.prefix+"preview.webm"] = bytes.Replace(f.files[f.prefix+"preview.webm"], []byte{0x81, 0, 40, 0, 1}, []byte{0x81, 0, 60, 0, 1}, 1)
			f.save()
		}},
		{"preview hidden extra frames", func(f *fixture) {
			f.files[f.prefix+"preview.webm"] = bytes.Replace(f.files[f.prefix+"preview.webm"], []byte{0x81, 0, 40, 0, 1}, []byte{0x81, 0, 40, 2, 1}, 1)
			f.save()
		}},
		{"preview missing keyframe", func(f *fixture) {
			f.files[f.prefix+"preview.webm"] = bytes.Replace(f.files[f.prefix+"preview.webm"], []byte{0x81, 0, 0, 0x80, 1}, []byte{0x81, 0, 0, 0, 1}, 1)
			f.save()
		}},
		{"wrong header", func(f *fixture) { f.files[f.prefix+"model.rgs"][0] = 'X'; f.save() }},
		{"header frames mismatch", func(f *fixture) { binary.LittleEndian.PutUint32(f.files[f.prefix+"model.rgs"][16:20], 3); f.save() }},
		{"header reserved", func(f *fixture) { f.files[f.prefix+"model.rgs"][28] = 1; f.save() }},
		{"nonfinite primitive", func(f *fixture) {
			binary.LittleEndian.PutUint32(f.files[f.prefix+"model.rgs"][32:36], math.Float32bits(float32(math.NaN())))
			f.save()
		}},
		{"zero lifetime", func(f *fixture) { binary.LittleEndian.PutUint32(f.files[f.prefix+"model.rgs"][80:84], 0); f.save() }},
		{"opacity outside unit", func(f *fixture) {
			binary.LittleEndian.PutUint32(f.files[f.prefix+"model.rgs"][84:88], math.Float32bits(2))
			f.save()
		}},
		{"metrics raw proof mismatch", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics.json", func(m map[string]any) { m["raw_model_sha256"] = strings.Repeat("b", 64) })
			f.save()
		}},
		{"metrics wrong clip", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics.json", func(m map[string]any) { m["clip_id"] = "wrong" })
			f.save()
		}},
		{"metrics wrong timeline", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics.json", func(m map[string]any) { m["frame_times_seconds"] = []float64{0, .08} })
			f.save()
		}},
		{"missing standalone manifest field", func(f *fixture) {
			var m map[string]any
			json.Unmarshal(f.files[f.doc.Clips[0].Manifest.URL()], &m)
			delete(m, "codec")
			f.files[f.doc.Clips[0].Manifest.URL()] = jsonBytes(m)
			f.write(filepath.Join(f.opts.Root, f.doc.Clips[0].Manifest.URL()), f.files[f.doc.Clips[0].Manifest.URL()])
			p := f.files[f.doc.Clips[0].Manifest.URL()]
			f.ledger.Clips[0].Files[f.doc.Clips[0].Manifest.URL()] = Proof{int64(len(p)), hash(p)}
			f.write(f.opts.ApprovalsPath, jsonBytes(f.ledger))
		}},
		{"duplicate JSON key", func(f *fixture) {
			data, _ := os.ReadFile(f.opts.ApprovalsPath)
			data = bytes.Replace(data, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			f.write(f.opts.ApprovalsPath, data)
		}},
		{"unknown approval field", func(f *fixture) {
			data, _ := os.ReadFile(f.opts.ApprovalsPath)
			data = bytes.Replace(data, []byte(`"version":1`), []byte(`"version":1,"approve_everything":true`), 1)
			f.write(f.opts.ApprovalsPath, data)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.change(f)
			candidate, _ := os.ReadFile(f.opts.CatalogPath)
			if r, err := f.verify(); err == nil {
				t.Fatalf("accepted invalid input: %+v", r)
			}
			after, _ := os.ReadFile(f.opts.CatalogPath)
			if !bytes.Equal(candidate, after) {
				t.Fatal("failure modified candidate catalog")
			}
		})
	}
}

func TestPinnedPreviousRetentionAndRemoval(t *testing.T) {
	f := newFixture(t)
	old := f.doc
	old.Clips = append([]entry(nil), f.doc.Clips...)
	old.Clips[0].Manifest.Revision = "previous"
	old.Clips[0].Manifest.Representations = append([]stream.Representation(nil), old.Clips[0].Manifest.Representations...)
	r := &old.Clips[0].Manifest.Representations[0]
	r.InitURL = "/assets/clip-a/previous/full/scene.json"
	r.Segments = append([]stream.Segment(nil), r.Segments...)
	r.Segments[0].URL = "/assets/clip-a/previous/full/model.rgs"
	data := jsonBytes(old)
	f.opts.PreviousCatalogPath = filepath.Join(t.TempDir(), "previous.json")
	f.opts.PreviousSHA256 = hash(data)
	f.write(f.opts.PreviousCatalogPath, data)
	if _, err := f.verify(); err != nil {
		t.Fatal(err)
	}
	// Retention is explicit, content-identical, and fetches no old asset bytes.
	f.doc = old
	f.ledger = Ledger{Version: 1, Clips: []Approval{}, Retained: []Retained{{ClipID: "clip-a", Revision: "previous", Actor: "codex", Basis: "inherit pinned previous; asset and permission review not repeated"}}}
	f.opts.Root = t.TempDir()
	f.write(f.opts.CatalogPath, data)
	f.write(f.opts.ApprovalsPath, jsonBytes(f.ledger))
	receipt, err := f.verify()
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Checked) != 0 || len(receipt.Retained) != 1 || receipt.Retained[0].Validation != "pinned-previous-only" || receipt.Retained[0].EntrySHA256 == "" {
		t.Fatalf("misleading retention receipt: %+v", receipt)
	}
	t.Run("pinned body differs", func(t *testing.T) {
		opts := f.opts
		opts.PreviousSHA256 = strings.Repeat("c", 64)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := Verify(ctx, opts); err == nil {
			t.Fatal("accepted unpinned body")
		}
	})
	f.doc.Clips[0].Description = "modified retained metadata"
	f.write(f.opts.CatalogPath, jsonBytes(f.doc))
	if _, err := f.verify(); err == nil {
		t.Fatal("accepted changed retained entry")
	}
	f.doc.Clips = []entry{}
	f.ledger.Retained = []Retained{}
	f.write(f.opts.CatalogPath, jsonBytes(f.doc))
	f.write(f.opts.ApprovalsPath, jsonBytes(f.ledger))
	receipt, err = f.verify()
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Removed) != 1 || receipt.Removed[0].ClipID != "clip-a" {
		t.Fatal("removal not recorded")
	}
}

func TestExistingRevisionCannotBeRepublished(t *testing.T) {
	f := newFixture(t)
	data := jsonBytes(f.doc)
	f.opts.PreviousCatalogPath = filepath.Join(t.TempDir(), "previous.json")
	f.opts.PreviousSHA256 = hash(data)
	f.write(f.opts.PreviousCatalogPath, data)
	if _, err := f.verify(); err == nil {
		t.Fatal("republished existing revision")
	}
	f.doc.Clips[0].Title = "changed same revision"
	f.save()
	if _, err := f.verify(); err == nil {
		t.Fatal("overwrote existing revision")
	}
}

func TestPackedRGS1RoundTripAndCorruption(t *testing.T) {
	f := newFixture(t)
	raw := f.files[f.prefix+"model.rgs"]
	// Count > 1 catches accidental AoS validation of packed column bytes.
	raw = append(bytes.Clone(raw), raw[32:]...)
	binary.LittleEndian.PutUint32(raw[8:12], 2)
	columns := make([]byte, len(raw)-32)
	for col := 0; col < 56; col++ {
		for row := 0; row < 2; row++ {
			columns[col*2+row] = raw[32+row*56+col]
		}
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	z.Write(columns)
	z.Close()
	packed := append(bytes.Clone(raw[:32]), compressed.Bytes()...)
	copy(packed, "RANGL4Z1")
	decoded, err := temporalModel(packed, 2, 25)
	if err != nil || !bytes.Equal(decoded, raw) {
		t.Fatalf("column decode mismatch: %v", err)
	}
	f.files[f.prefix+"model.rgs"] = packed
	f.changeJSON(f.prefix+"metrics.json", func(m map[string]any) {
		m["model_bytes"] = len(packed)
		m["model_sha256"] = hash(packed)
		m["raw_model_bytes"] = len(raw)
		m["raw_model_sha256"] = hash(raw)
	})
	f.save()
	if _, err := f.verify(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{packed[:len(packed)-1], append(bytes.Clone(packed), 1), append(bytes.Clone(packed), compressed.Bytes()...)} {
		if _, err := temporalModel(bad, 2, 25); err == nil {
			t.Fatal("accepted corrupt/trailing gzip")
		}
	}
}

func testWebM(seconds float64) []byte {
	e := func(id []byte, value []byte) []byte {
		if len(value) > 126 {
			panic("fixture too large")
		}
		return append(append(append([]byte{}, id...), byte(0x80|len(value))), value...)
	}
	header := e([]byte{0x1a, 0x45, 0xdf, 0xa3}, e([]byte{0x42, 0x82}, []byte("webm")))
	d := make([]byte, 8)
	binary.BigEndian.PutUint64(d, math.Float64bits(seconds*1000))
	info := e([]byte{0x15, 0x49, 0xa9, 0x66}, e([]byte{0x44, 0x89}, d))
	track := append(e([]byte{0xd7}, []byte{1}), e([]byte{0x83}, []byte{1})...)
	track = append(track, e([]byte{0x86}, []byte("V_VP9"))...)
	tracks := e([]byte{0x16, 0x54, 0xae, 0x6b}, e([]byte{0xae}, track))
	blocks := append(e([]byte{0xa3}, []byte{0x81, 0, 0, 0x80, 1}), e([]byte{0xa3}, []byte{0x81, 0, 40, 0, 1})...)
	cluster := e([]byte{0x1f, 0x43, 0xb6, 0x75}, append(e([]byte{0xe7}, []byte{0}), blocks...))
	return append(header, e([]byte{0x18, 0x53, 0x80, 0x67}, append(append(info, tracks...), cluster...))...)
}
