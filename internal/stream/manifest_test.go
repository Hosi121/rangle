package stream

import (
	"math"
	"strings"
	"testing"
)

func validManifest() Manifest {
	m := Manifest{Version: Version, ClipID: "clip", Revision: "rev1", Codec: "synthetic-transport-v1"}
	for _, id := range []string{"low", "high"} {
		prefix := "/assets/clip/rev1/" + id + "/"
		m.Representations = append(m.Representations, Representation{
			ID: id, BitrateBPS: 1024, PeakDecodedBytes: 4096, DecodeTimeMS: 2,
			InitURL: prefix + "init.bin", InitSizeBytes: 2,
			Segments: []Segment{
				{Index: 0, StartSeconds: 0, DurationSeconds: 1, URL: prefix + "0.bin", SizeBytes: 128, Independent: true, SHA256: strings.Repeat("a", 64)},
				{Index: 1, StartSeconds: 1, DurationSeconds: 1, URL: prefix + "1.bin", SizeBytes: 128, Independent: true, SHA256: strings.Repeat("b", 64)},
			},
		})
	}
	return m
}

func TestValidateManifest(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"unsupported version", func(m *Manifest) { m.Version++ }},
		{"invalid clip", func(m *Manifest) { m.ClipID = "../clip" }},
		{"invalid revision", func(m *Manifest) { m.Revision = "rev?x=1" }},
		{"missing codec", func(m *Manifest) { m.Codec = " " }},
		{"no representations", func(m *Manifest) { m.Representations = nil }},
		{"duplicate representation", func(m *Manifest) { m.Representations[1].ID = "low" }},
		{"zero bitrate", func(m *Manifest) { m.Representations[0].BitrateBPS = 0 }},
		{"missing memory bound", func(m *Manifest) { m.Representations[0].PeakDecodedBytes = 0 }},
		{"nonfinite decode time", func(m *Manifest) { m.Representations[0].DecodeTimeMS = math.NaN() }},
		{"orphan init size", func(m *Manifest) { m.Representations[0].InitURL = "" }},
		{"unversioned init", func(m *Manifest) { m.Representations[0].InitURL = "/assets/init.bin" }},
		{"no segments", func(m *Manifest) { m.Representations[0].Segments = nil }},
		{"unequal counts", func(m *Manifest) { m.Representations[1].Segments = m.Representations[1].Segments[:1] }},
		{"wrong index", func(m *Manifest) { m.Representations[0].Segments[0].Index = 1 }},
		{"negative start", func(m *Manifest) { m.Representations[0].Segments[0].StartSeconds = -1e-8 }},
		{"gap", func(m *Manifest) { m.Representations[0].Segments[1].StartSeconds = 2 }},
		{"nonfinite duration", func(m *Manifest) { m.Representations[0].Segments[0].DurationSeconds = math.Inf(1) }},
		{"nonpositive duration", func(m *Manifest) { m.Representations[0].Segments[0].DurationSeconds = 0 }},
		{"dependent segment", func(m *Manifest) { m.Representations[0].Segments[0].Independent = false }},
		{"invalid checksum", func(m *Manifest) { m.Representations[0].Segments[0].SHA256 = strings.Repeat("z", 64) }},
		{"unversioned URL", func(m *Manifest) { m.Representations[0].Segments[0].URL = "/assets/0.bin" }},
		{"encoded traversal", func(m *Manifest) { m.Representations[0].Segments[0].URL = "/assets/clip/rev1/low/%2e%2e" }},
		{"wrong quality URL", func(m *Manifest) { m.Representations[0].Segments[0].URL = "/assets/clip/rev1/high/0.bin" }},
		{"duplicate asset", func(m *Manifest) { m.Representations[0].Segments[1].URL = m.Representations[0].Segments[0].URL }},
		{"negative size", func(m *Manifest) { m.Representations[0].Segments[0].SizeBytes = -1 }},
		{"unaligned variants", func(m *Manifest) {
			m.Representations[1].Segments[0].DurationSeconds = 0.5
			m.Representations[1].Segments[1].StartSeconds = 0.5
			m.Representations[1].Segments[1].DurationSeconds = 1.5
		}},
	}
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("valid fixture: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.mutate(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestManifestWithoutInitialization(t *testing.T) {
	m := validManifest()
	for i := range m.Representations {
		m.Representations[i].InitURL = ""
		m.Representations[i].InitSizeBytes = 0
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if got, want := m.URL(), "/manifests/clip/rev1.json"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}
