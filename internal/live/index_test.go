package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"
)

func validIndex() Index {
	start := time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	return Index{Version: 1, Sessions: []Session{{
		ID: "basketball-live", Title: "バスケットボール ライブ検証", State: "live", StartedAt: start, UpdatedAt: start.Add(40 * time.Second),
		TargetLatencySeconds: 30, LatestInputTimeSeconds: 2, DroppedWindows: 0,
		Source:   Source{Label: "VRU Basketball gz", URL: "https://huggingface.co/datasets/BestWJH/VRU_Basketball", License: "CC BY 4.0", Caveat: "Supplied calibration may contain temporal information; arrived image optimization is causal."},
		Segments: []Segment{{Sequence: 1, StartSeconds: 0, EndSeconds: .76, SceneURL: "/live-assets/basketball-live/000001-aaaaaaaaaaaaaaaa/scene.json", ModelSHA256: strings.Repeat("a", 64), ModelBytes: 1000000, CapturedAt: start.Add(time.Second), AvailableAt: start.Add(2 * time.Second), TrainingSeconds: 1, MeanPackedPSNR: 20, MeanPackedSSIM: .8}},
	}}}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSessionIDMatchesSchemaGrammar(t *testing.T) {
	grammar := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	values := []string{"", "basketball-live", "AZaz09_-", "日本語", "é", "a/b", "a%2Fb", "a\nb", "a\x00b"}
	// Exercise every byte at both edges and inside a valid ID, plus lengths
	// surrounding both schema bounds. The regexp is an independent oracle.
	for n := 0; n <= 65; n++ {
		values = append(values, strings.Repeat("a", n))
	}
	for n := 0; n < 256; n++ {
		value := string([]byte{byte(n)})
		values = append(values, value, value+"valid", "valid"+value, "valid"+value+"id")
	}
	for _, value := range values {
		if got, want := validID(value), grammar.MatchString(value); got != want {
			t.Fatalf("validID(%q) = %t, schema wants %t", value, got, want)
		}
	}
}

func TestParseLiveIndexAndCloudIdentity(t *testing.T) {
	index := validIndex()
	parsed, err := ParseIndex(marshal(t, index), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sessions) != 1 || parsed.Sessions[0].Title != index.Sessions[0].Title {
		t.Fatal("lost session metadata")
	}
	index.Sessions[0].Segments[0].SceneURL = "https://media.example.com/live/basketball-live/000001-aaaaaaaaaaaaaaaa/scene.json"
	if _, err := ParseIndex(marshal(t, index), "https://media.example.com/"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseIndex(marshal(t, index), ""); err == nil {
		t.Fatal("accepted unconfigured remote origin")
	}
	if _, err := ParseIndex([]byte(`{"version":1,"sessions":[]}`), ""); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"starting", "live", "ended", "failed"} {
		index := validIndex()
		index.Sessions[0].State = state
		index.Sessions[0].Segments = []Segment{}
		if err := index.Validate(""); err != nil {
			t.Fatalf("empty %s session rejected: %v", state, err)
		}
	}
}

func TestLiveSchemaRequiresEveryFieldAndRejectsNull(t *testing.T) {
	var object map[string]any
	if err := json.Unmarshal(marshal(t, validIndex()), &object); err != nil {
		t.Fatal(err)
	}
	paths := [][]string{{}, {"sessions", "0"}, {"sessions", "0", "source"}, {"sessions", "0", "segments", "0"}}
	for _, path := range paths {
		for name := range atObject(object, path) {
			for _, null := range []bool{false, true} {
				t.Run(strings.Join(path, "/")+"/"+name+fmt.Sprint(null), func(t *testing.T) {
					var changed map[string]any
					json.Unmarshal(marshal(t, object), &changed)
					target := atObject(changed, path)
					if null {
						target[name] = nil
					} else {
						delete(target, name)
					}
					if _, err := ParseIndex(marshal(t, changed), ""); err == nil {
						t.Fatal("accepted missing/null field")
					}
				})
			}
		}
	}
}

func atObject(object map[string]any, path []string) map[string]any {
	var current any = object
	for _, part := range path {
		if part == "0" {
			current = current.([]any)[0]
		} else {
			current = current.(map[string]any)[part]
		}
	}
	return current.(map[string]any)
}

func TestLiveIndexRejectsCorruptMetadata(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Index)
	}{
		{"version", func(i *Index) { i.Version = 2 }},
		{"duplicate session", func(i *Index) { i.Sessions = append(i.Sessions, i.Sessions[0]) }},
		{"invalid ID", func(i *Index) { i.Sessions[0].ID = "../escape" }},
		{"empty title", func(i *Index) { i.Sessions[0].Title = " " }},
		{"oversized title", func(i *Index) { i.Sessions[0].Title = strings.Repeat("x", 257) }},
		{"state", func(i *Index) { i.Sessions[0].State = "ready" }},
		{"update before start", func(i *Index) { i.Sessions[0].UpdatedAt = i.Sessions[0].StartedAt.Add(-time.Second) }},
		{"target zero", func(i *Index) { i.Sessions[0].TargetLatencySeconds = 0 }},
		{"latest negative", func(i *Index) { i.Sessions[0].LatestInputTimeSeconds = -1 }},
		{"dropped negative", func(i *Index) { i.Sessions[0].DroppedWindows = -1 }},
		{"source missing", func(i *Index) { i.Sessions[0].Source.Label = "" }},
		{"source HTTP", func(i *Index) { i.Sessions[0].Source.URL = "http://example.com/input" }},
		{"source credentials", func(i *Index) { i.Sessions[0].Source.URL = "https://secret@example.com/input" }},
		{"source query", func(i *Index) { i.Sessions[0].Source.URL = "https://example.com/input?token=secret" }},
		{"license missing", func(i *Index) { i.Sessions[0].Source.License = "" }},
		{"caveat missing", func(i *Index) { i.Sessions[0].Source.Caveat = "" }},
		{"sequence zero", func(i *Index) { i.Sessions[0].Segments[0].Sequence = 0 }},
		{"sequence overflow", func(i *Index) { i.Sessions[0].Segments[0].Sequence = 1000000 }},
		{"negative start", func(i *Index) { i.Sessions[0].Segments[0].StartSeconds = -1 }},
		{"empty duration", func(i *Index) { i.Sessions[0].Segments[0].EndSeconds = 0 }},
		{"future input", func(i *Index) { i.Sessions[0].Segments[0].EndSeconds = 3 }},
		{"wrong hash", func(i *Index) { i.Sessions[0].Segments[0].ModelSHA256 = "bad" }},
		{"uppercase hash", func(i *Index) { i.Sessions[0].Segments[0].ModelSHA256 = strings.Repeat("A", 64) }},
		{"zero bytes", func(i *Index) { i.Sessions[0].Segments[0].ModelBytes = 0 }},
		{"oversized model", func(i *Index) { i.Sessions[0].Segments[0].ModelBytes = MaxModelBytes + 1 }},
		{"capture after availability", func(i *Index) { i.Sessions[0].Segments[0].CapturedAt = i.Sessions[0].UpdatedAt }},
		{"available after updated", func(i *Index) { i.Sessions[0].Segments[0].AvailableAt = i.Sessions[0].UpdatedAt.Add(time.Second) }},
		{"available before session", func(i *Index) {
			i.Sessions[0].Segments[0].AvailableAt = i.Sessions[0].StartedAt.Add(-time.Second)
			i.Sessions[0].Segments[0].CapturedAt = i.Sessions[0].StartedAt.Add(-2 * time.Second)
		}},
		{"negative training", func(i *Index) { i.Sessions[0].Segments[0].TrainingSeconds = -1 }},
		{"negative PSNR", func(i *Index) { i.Sessions[0].Segments[0].MeanPackedPSNR = -1 }},
		{"SSIM range", func(i *Index) { i.Sessions[0].Segments[0].MeanPackedSSIM = 1.1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := validIndex()
			tt.change(&index)
			if _, err := ParseIndex(marshal(t, index), ""); err == nil {
				t.Fatal("accepted corrupt index")
			}
		})
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for field := 0; field < 8; field++ {
			index := validIndex()
			session := &index.Sessions[0]
			segment := &session.Segments[0]
			fields := []*float64{&session.TargetLatencySeconds, &session.LatestInputTimeSeconds, &segment.StartSeconds, &segment.EndSeconds, &segment.TrainingSeconds, &segment.MeanPackedPSNR, &segment.MeanPackedSSIM}
			if field == len(fields) {
				continue
			}
			*fields[field] = value
			if err := index.Validate(""); err == nil {
				t.Fatal("accepted non-finite metadata")
			}
		}
	}
}

func TestLiveURLIdentityAndChronology(t *testing.T) {
	for _, value := range []string{
		"https://evil.example/live/basketball-live/000001-aaaaaaaaaaaaaaaa/scene.json",
		"https://media.example.com/generated/job/scene.json",
		"http://media.example.com/live/basketball-live/000001-aaaaaaaaaaaaaaaa/scene.json",
		"//media.example.com/live/basketball-live/000001-aaaaaaaaaaaaaaaa/scene.json",
		"/live-assets/other/000001-aaaaaaaaaaaaaaaa/scene.json",
		"/live-assets/basketball-live/000002-aaaaaaaaaaaaaaaa/scene.json",
		"/live-assets/basketball-live/000001-bbbbbbbbbbbbbbbb/scene.json",
		"/live-assets/basketball-live/000001-aaaaaaaaaaaaaaaa/scene.json?x=1",
		"/live-assets/basketball-live/000001-aaaaaaaaaaaaaaaa/scene.json#x",
		"/live-assets/basketball-live/000001-aaaaaaaaaaaaaaaa/%73cene.json",
	} {
		index := validIndex()
		index.Sessions[0].Segments[0].SceneURL = value
		if err := index.Validate("https://media.example.com"); err == nil {
			t.Errorf("accepted escaped URL %q", value)
		}
	}
	index := validIndex()
	next := index.Sessions[0].Segments[0]
	next.Sequence = 3
	next.StartSeconds = .4
	next.EndSeconds = 1.16
	next.SceneURL = "/live-assets/basketball-live/000003-aaaaaaaaaaaaaaaa/scene.json"
	next.CapturedAt = next.CapturedAt.Add(time.Second)
	next.AvailableAt = next.AvailableAt.Add(time.Second)
	index.Sessions[0].Segments = append(index.Sessions[0].Segments, next)
	if err := index.Validate(""); err != nil {
		t.Fatalf("valid overlap/gap rejected: %v", err)
	}
	for _, change := range []func(*Segment){
		func(s *Segment) {
			s.Sequence = 1
			s.SceneURL = "/live-assets/basketball-live/000001-aaaaaaaaaaaaaaaa/scene.json"
		},
		func(s *Segment) { s.StartSeconds = -.1 }, func(s *Segment) { s.EndSeconds = .76 },
		func(s *Segment) { s.CapturedAt = s.CapturedAt.Add(-2 * time.Second) },
		func(s *Segment) { s.AvailableAt = s.AvailableAt.Add(-2 * time.Second) },
	} {
		copy := index
		copy.Sessions = append([]Session(nil), index.Sessions...)
		copy.Sessions[0].Segments = append([]Segment(nil), index.Sessions[0].Segments...)
		change(&copy.Sessions[0].Segments[1])
		if err := copy.Validate(""); err == nil {
			t.Fatal("accepted regressing segment")
		}
	}
}

func TestLiveIndexBoundsAndInvalidJSON(t *testing.T) {
	index := validIndex()
	for n := 1; n < MaxSessions; n++ {
		s := index.Sessions[0]
		s.ID = fmt.Sprintf("session-%d", n)
		s.Segments = []Segment{}
		index.Sessions = append(index.Sessions, s)
	}
	if err := index.Validate(""); err != nil {
		t.Fatal(err)
	}
	index.Sessions = append(index.Sessions, Session{})
	if err := index.Validate(""); err == nil {
		t.Fatal("accepted ninth session")
	}
	index = validIndex()
	index.Sessions[0].LatestInputTimeSeconds = 100
	for n := 2; n <= MaxSegments; n++ {
		s := index.Sessions[0].Segments[0]
		s.Sequence = n
		s.StartSeconds = float64(n)
		s.EndSeconds = float64(n) + .76
		s.SceneURL = fmt.Sprintf("/live-assets/basketball-live/%06d-aaaaaaaaaaaaaaaa/scene.json", n)
		index.Sessions[0].Segments = append(index.Sessions[0].Segments, s)
	}
	if err := index.Validate(""); err != nil {
		t.Fatal(err)
	}
	index.Sessions[0].Segments = append(index.Sessions[0].Segments, Segment{})
	if err := index.Validate(""); err == nil {
		t.Fatal("accepted 33rd segment")
	}
	valid := marshal(t, validIndex())
	for _, data := range [][]byte{
		bytes.Repeat([]byte(" "), MaxIndexBytes+1),
		append(bytes.Clone(valid), []byte(` {}`)...),
		bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		bytes.Replace(valid, []byte(`"version":1`), []byte(`"unknown":1,"version":1`), 1),
		bytes.Replace(valid, []byte(`2026-11-09T05:00:00Z`), []byte(`2026-11-09T05:00:00+09:00`), 1),
		bytes.Replace(valid, []byte(`2026-11-09T05:00:00Z`), []byte(`2026-11-09T05:00:00,1Z`), 1),
		bytes.Replace(valid, []byte(`"training_seconds":1`), []byte(`"training_seconds":1e9999`), 1),
		[]byte("{\xff}"), []byte(`[]`), []byte(`null`),
	} {
		if _, err := ParseIndex(data, ""); err == nil {
			t.Fatalf("accepted invalid JSON: %.100s", data)
		}
	}
	for _, origin := range []string{"http://media.example.com", "https://user@media.example.com", "https://media.example.com/path", "https://media.example.com?token=x", "https://media.example.com#fragment", "https://media.example.com:8443"} {
		if _, err := ParseIndex(valid, origin); err == nil {
			t.Errorf("accepted bad origin %q", origin)
		}
	}
}
