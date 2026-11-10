package publication

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/stream"
)

func newSegmentedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	m := &f.doc.Clips[0].Manifest
	m.Codec = segmentedCodec
	r := &m.Representations[0]
	r.Segments[0].DurationSeconds = .08 // Two samples, not the .04s sample span.
	model, metrics := segmentNames(1)
	raw := bytes.Clone(f.files[f.prefix+"model.rgs"])
	binary.LittleEndian.PutUint32(raw[32:36], math.Float32bits(1))
	f.files[f.prefix+model] = raw
	f.files[f.prefix+metrics] = bytes.Clone(f.files[f.prefix+"metrics.json"])
	r.Segments = append(r.Segments, stream.Segment{Index: 1, StartSeconds: .08, DurationSeconds: .08,
		URL: f.prefix + model, SizeBytes: int64(len(raw)), SHA256: hash(raw), Independent: true})
	for i, name := range []string{"metrics.json", metrics} {
		f.changeJSON(f.prefix+name, func(m map[string]any) {
			m["segment_index"], m["global_start_seconds"] = i, float64(i)*.08
			if i == 1 {
				m["input_sha256"] = strings.Repeat("b", 64) // Independent source windows may differ.
				m["model_sha256"], m["raw_model_sha256"] = hash(raw), hash(raw)
			}
		})
	}
	f.files[f.prefix+"preview.webm"] = segmentedPreview()
	f.save()
	return f
}

func TestSegmentedPublicationUsesRealGateWithoutChangingModelBytes(t *testing.T) {
	f := newSegmentedFixture(t)
	before := bytes.Clone(f.files[f.prefix+"model-00001.rgs"])
	receipt, err := f.verify()
	if err != nil {
		t.Fatal(err)
	}
	if receipt.CheckedFiles != 7 || len(receipt.Checked) != 1 || receipt.Scope != "local" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	after, err := os.ReadFile(filepath.Join(f.opts.Root, f.prefix, "model-00001.rgs"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("verification changed independent RGS1 bytes")
	}
	for i := range 2 {
		model, _ := segmentNames(i)
		if err := ValidateTemporalModel(f.files[f.prefix+model], 2, 25); err != nil {
			t.Fatalf("segment %d cannot decode independently: %v", i, err)
		}
	}
}

func TestSegmentedPublicationRejectsReapprovedSemanticTampering(t *testing.T) {
	tests := []struct {
		name   string
		change func(*fixture)
	}{
		{"legacy codec with segments", func(f *fixture) { f.doc.Clips[0].Manifest.Codec = "rangle-temporal-rgs1" }},
		{"new codec with one model", func(f *fixture) {
			f.doc.Clips[0].Manifest.Representations[0].Segments = f.doc.Clips[0].Manifest.Representations[0].Segments[:1]
		}},
		{"sample span advertised as coverage", func(f *fixture) {
			r := &f.doc.Clips[0].Manifest.Representations[0]
			r.Segments[0].DurationSeconds, r.Segments[1].StartSeconds, r.Segments[1].DurationSeconds = .04, .04, .04
		}},
		{"non-independent segment", func(f *fixture) { f.doc.Clips[0].Manifest.Representations[0].Segments[1].Independent = false }},
		{"wrong canonical model path", func(f *fixture) {
			f.doc.Clips[0].Manifest.Representations[0].Segments[1].URL = f.prefix + "model-00002.rgs"
		}},
		{"unreferenced approved model", func(f *fixture) { f.files[f.prefix+"model-00002.rgs"] = bytes.Clone(f.files[f.prefix+"model.rgs"]) }},
		{"missing metrics", func(f *fixture) {
			delete(f.files, f.prefix+"metrics-00001.json")
			if err := os.Remove(filepath.Join(f.opts.Root, f.prefix, "metrics-00001.json")); err != nil {
				f.t.Fatal(err)
			}
		}},
		{"missing index on first metrics", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics.json", func(m map[string]any) { delete(m, "segment_index") })
		}},
		{"null index", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics-00001.json", func(m map[string]any) { m["segment_index"] = nil })
		}},
		{"fractional index", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics-00001.json", func(m map[string]any) { m["segment_index"] = .5 })
		}},
		{"wrong index", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics-00001.json", func(m map[string]any) { m["segment_index"] = 0 })
		}},
		{"missing global start", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics.json", func(m map[string]any) { delete(m, "global_start_seconds") })
		}},
		{"null global start", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics.json", func(m map[string]any) { m["global_start_seconds"] = nil })
		}},
		{"negative global start within float tolerance", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics.json", func(m map[string]any) { m["global_start_seconds"] = -1e-7 })
		}},
		{"wrong global start", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics-00001.json", func(m map[string]any) { m["global_start_seconds"] = 0 })
		}},
		{"global metrics sample times", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics-00001.json", func(m map[string]any) { m["frame_times_seconds"] = []float64{.08, .12} })
		}},
		{"wrong raw proof", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics-00001.json", func(m map[string]any) { m["raw_model_sha256"] = strings.Repeat("f", 64) })
		}},
		{"different source", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics-00001.json", func(m map[string]any) { m["source"] = "https://other.example/data" })
		}},
		{"different license", func(f *fixture) {
			f.changeJSON(f.prefix+"metrics-00001.json", func(m map[string]any) { m["license"] = "other" })
		}},
		{"different local frame count", func(f *fixture) { binary.LittleEndian.PutUint32(f.files[f.prefix+"model-00001.rgs"][16:20], 3) }},
		{"different local fps", func(f *fixture) {
			binary.LittleEndian.PutUint32(f.files[f.prefix+"model-00001.rgs"][20:24], math.Float32bits(24))
		}},
		{"invalid later primitive", func(f *fixture) { binary.LittleEndian.PutUint32(f.files[f.prefix+"model-00001.rgs"][80:84], 0) }},
		{"preview covers only first segment", func(f *fixture) { f.files[f.prefix+"preview.webm"] = testWebM(.08) }},
		{"changed camera attribution", func(f *fixture) {
			f.changeJSON(f.prefix+"scene.json", func(m map[string]any) { m["attribution"].(map[string]any)["source"] = "https://other.example/data" })
		}},
		{"private extra file", func(f *fixture) { f.files[f.prefix+"training.npz"] = []byte("private") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSegmentedFixture(t)
			tt.change(f)
			for i := range f.doc.Clips[0].Manifest.Representations[0].Segments {
				segment := &f.doc.Clips[0].Manifest.Representations[0].Segments[i]
				if model, ok := f.files[segment.URL]; ok {
					segment.SizeBytes, segment.SHA256 = int64(len(model)), hash(model)
				}
			}
			f.save() // Update transport and approval proofs: semantic checks must still reject.
			if _, err := f.verify(); err == nil {
				t.Fatal("accepted invalid segmented publication")
			}
		})
	}
}

func TestLegacyPublicationStillRejectsUnreferencedIndexedArtifacts(t *testing.T) {
	f := newFixture(t)
	f.files[f.prefix+"model-00001.rgs"] = bytes.Clone(f.files[f.prefix+"model.rgs"])
	f.save()
	if _, err := f.verify(); err == nil {
		t.Fatal("legacy approval gained an unreferenced model")
	}
}

func TestSegmentedPublicationAcceptsExistingZ1WireAndRejectsUnprovedChanges(t *testing.T) {
	f := newSegmentedFixture(t)
	modelName := f.prefix + "model-00001.rgs"
	raw := bytes.Clone(f.files[modelName])
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	// This fixture has one primitive, so column transposition is byte-identical.
	if _, err := z.Write(raw[32:]); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	packed := append(bytes.Clone(raw[:32]), compressed.Bytes()...)
	copy(packed, "RANGL4Z1")
	f.files[modelName] = packed
	segment := &f.doc.Clips[0].Manifest.Representations[0].Segments[1]
	segment.SizeBytes, segment.SHA256 = int64(len(packed)), hash(packed)
	f.changeJSON(f.prefix+"metrics-00001.json", func(m map[string]any) {
		m["model_bytes"], m["model_sha256"] = len(packed), hash(packed)
	})
	f.save()
	if _, err := f.verify(); err != nil {
		t.Fatal(err)
	}
	changed := bytes.Clone(packed)
	changed[len(changed)-1] ^= 1
	f.write(filepath.Join(f.opts.Root, modelName), changed)
	if _, err := f.verify(); err == nil {
		t.Fatal("accepted a later model differing from manifest/approval proof")
	}
}

// Four synthetic VP9-tagged blocks exercise container/timestamps only. These
// bytes are deliberately not a claim about decodable video or image quality.
func segmentedPreview() []byte {
	e := func(id []byte, value []byte) []byte {
		return append(append(bytes.Clone(id), byte(0x80|len(value))), value...)
	}
	header := e([]byte{0x1a, 0x45, 0xdf, 0xa3}, e([]byte{0x42, 0x82}, []byte("webm")))
	d := make([]byte, 8)
	binary.BigEndian.PutUint64(d, math.Float64bits(160))
	info := e([]byte{0x15, 0x49, 0xa9, 0x66}, e([]byte{0x44, 0x89}, d))
	track := append(e([]byte{0xd7}, []byte{1}), e([]byte{0x83}, []byte{1})...)
	track = append(track, e([]byte{0x86}, []byte("V_VP9"))...)
	tracks := e([]byte{0x16, 0x54, 0xae, 0x6b}, e([]byte{0xae}, track))
	cluster := e([]byte{0xe7}, []byte{0})
	for i := range 4 {
		cluster = append(cluster, e([]byte{0xa3}, []byte{0x81, 0, byte(i * 40), 0x80, 1})...)
	}
	cluster = e([]byte{0x1f, 0x43, 0xb6, 0x75}, cluster)
	return append(header, e([]byte{0x18, 0x53, 0x80, 0x67}, append(append(info, tracks...), cluster...))...)
}
