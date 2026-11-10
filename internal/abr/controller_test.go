package abr

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/stream"
)

func TestControllerStartup(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		buffer    float64
		observe   bool
		maxMemory int64
		want      string
	}{
		{"no current quality", "", 8, true, 8 << 20, "low"},
		{"no bandwidth samples", "high", 8, false, 8 << 20, "low"},
		{"buffer below startup threshold", "high", 0.9, true, 8 << 20, "low"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			config.MaxDecodedBytes = test.maxMemory
			c := mustController(t, testManifest(8), config)
			if test.observe {
				observeRate(t, c, 8_000_000, 1)
			}
			d := selectOK(t, c, Request{CurrentID: test.current, BufferSeconds: test.buffer})
			if d.RepresentationID != test.want || d.Reason != ReasonStartup {
				t.Fatalf("decision = %+v", d)
			}
		})
	}
}

func TestControllerCollapseRecoveryTrace(t *testing.T) {
	config := DefaultConfig()
	config.Estimator = EstimatorConfig{FastHalfLifeSeconds: 1, SlowHalfLifeSeconds: 4}
	config.UpshiftHeadroom = 1.1
	c := mustController(t, testManifest(20), config)
	current := "low"
	trace := []struct {
		bps, seconds float64
		want         string
		reason       Reason
	}{
		{8_000_000, 1, "low", ReasonHold},
		{8_000_000, 1, "mid", ReasonUpshift},
		{8_000_000, 1, "mid", ReasonHold},
		{8_000_000, 1, "high", ReasonUpshift},
		{1_000_000, 2, "mid", ReasonDownshift},
		{200_000, 4, "low", ReasonBandwidthFallback},
		{8_000_000, 1, "low", ReasonHold},
		{8_000_000, 1, "mid", ReasonUpshift},
		{8_000_000, 1, "mid", ReasonHold},
		{8_000_000, 1, "mid", ReasonHold},
		{8_000_000, 1, "high", ReasonUpshift},
	}
	for i, step := range trace {
		observeRate(t, c, step.bps, step.seconds)
		d := selectOK(t, c, Request{SegmentIndex: i, CurrentID: current, BufferSeconds: 8})
		if d.RepresentationID != step.want || d.Reason != step.reason {
			t.Fatalf("trace step %d: got %+v; want %s (%s)", i, d, step.want, step.reason)
		}
		if step.reason == ReasonBandwidthFallback && d.WithinDeliveryBudget {
			t.Fatalf("fallback did not report budget miss: %+v", d)
		}
		current = d.RepresentationID
	}
}

func TestControllerInitAndActualSegmentSizes(t *testing.T) {
	tests := []struct {
		name                    string
		cached                  bool
		initBytes, segmentBytes int64
		want                    string
		wantBytes               int64
	}{
		{"ordinary next segment", false, 0, 500_000, "mid", 500_000},
		{"uncached init consumes deadline", false, 1_200_000, 500_000, "low", 250_000},
		{"cached init consumes no network", true, 1_200_000, 500_000, "mid", 500_000},
		{"large actual segment despite nominal bitrate", false, 0, 2_000_000, "low", 250_000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := testManifest(2)
			mid := &manifest.Representations[1]
			mid.InitSizeBytes = test.initBytes
			if test.initBytes > 0 {
				mid.InitURL = "/assets/clip/r1/mid/init.bin"
			}
			mid.Segments[1].SizeBytes = test.segmentBytes
			config := DefaultConfig()
			config.UpshiftConfirmations = 1
			c := mustController(t, manifest, config)
			observeRate(t, c, 4_000_000, 1)
			d := selectOK(t, c, Request{SegmentIndex: 1, CurrentID: "low", BufferSeconds: 4, InitCached: map[string]bool{"mid": test.cached}})
			if d.RepresentationID != test.want || d.RequiredBytes != test.wantBytes {
				t.Fatalf("decision = %+v, want %s with %d bytes", d, test.want, test.wantBytes)
			}
		})
	}
	// An initialization download remains part of the selected transfer's accounting.
	manifest := testManifest(1)
	manifest.Representations[0].InitURL = "/assets/clip/r1/low/init.bin"
	manifest.Representations[0].InitSizeBytes = 123
	c := mustController(t, manifest, DefaultConfig())
	if d := selectOK(t, c, Request{}); d.RequiredBytes != 250_123 {
		t.Fatalf("omitted init bytes: %+v", d)
	}
}

func TestControllerBufferAndDecodeDeadline(t *testing.T) {
	tests := []struct {
		name                   string
		buffer, middleDecodeMS float64
		want                   string
	}{
		{"ample horizon", 8, 200, "high"},
		{"buffer narrows horizon", 1.4, 200, "mid"},
		{"decode also consumes horizon", 1.4, 900, "low"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := testManifest(1)
			manifest.Representations[1].DecodeTimeMS = test.middleDecodeMS
			config := DefaultConfig()
			config.StartupBufferSeconds = 0
			config.MaxDecodeTimeMS = 1000
			c := mustController(t, manifest, config)
			observeRate(t, c, 8_000_000, 1)
			d := selectOK(t, c, Request{CurrentID: "high", BufferSeconds: test.buffer})
			if d.RepresentationID != test.want || !d.WithinDeliveryBudget {
				t.Fatalf("decision = %+v; want %s", d, test.want)
			}
		})
	}
}

func TestControllerSmallBufferAndShortVOD(t *testing.T) {
	c := mustController(t, testManifest(4), DefaultConfig())
	observeRate(t, c, 8_000_000, 1)
	for _, buffer := range []float64{0.1, 0.5, 0.75} {
		d := selectOK(t, c, Request{CurrentID: "low", BufferSeconds: buffer})
		if d.HorizonSeconds > buffer || d.AcquisitionOnly || d.WithinDeliveryBudget {
			t.Fatalf("small positive buffer inflated into acquisition allowance: %+v", d)
		}
	}
	d := selectOK(t, c, Request{})
	if !d.AcquisitionOnly || d.HorizonSeconds != DefaultConfig().EmptyBufferHorizonSeconds {
		t.Fatalf("empty-buffer acquisition not identified: %+v", d)
	}
	// Four short intervals can leave startup at the default one-second threshold.
	for index := 0; index < 2; index++ {
		d = selectOK(t, c, Request{SegmentIndex: index, CurrentID: "low", BufferSeconds: 1.5})
	}
	if d.RepresentationID != "mid" || d.Reason != ReasonUpshift {
		t.Fatalf("short VOD could not leave startup: %+v", d)
	}
}

func TestControllerVBRDoesNotBypassUpshiftHysteresis(t *testing.T) {
	manifest := testManifest(3)
	for i := range manifest.Representations[0].Segments {
		manifest.Representations[0].Segments[i].SizeBytes = 4_000_000
		manifest.Representations[1].Segments[i].SizeBytes = 5_000_000
	}
	c := mustController(t, manifest, DefaultConfig())
	observeRate(t, c, 8_000_000, 1)
	first := selectOK(t, c, Request{CurrentID: "low", BufferSeconds: 4})
	if first.RepresentationID != "low" || first.Reason != ReasonBandwidthFallback || first.WithinDeliveryBudget {
		t.Fatalf("VBR immediately bypassed upshift confirmation: %+v", first)
	}
	second := selectOK(t, c, Request{SegmentIndex: 1, CurrentID: "low", BufferSeconds: 4})
	if second.RepresentationID != "high" || second.Reason != ReasonUpshift || !second.WithinDeliveryBudget {
		t.Fatalf("confirmed, smaller higher VBR segment was not selected: %+v", second)
	}
	// A feasible lower quality is used promptly while a higher one is confirmed.
	manifest = testManifest(3)
	for i := range manifest.Representations[1].Segments {
		manifest.Representations[1].Segments[i].SizeBytes = 5_000_000
	}
	c = mustController(t, manifest, DefaultConfig())
	observeRate(t, c, 8_000_000, 1)
	first = selectOK(t, c, Request{CurrentID: "mid", BufferSeconds: 4})
	if first.RepresentationID != "low" || first.Reason != ReasonDownshift || !first.WithinDeliveryBudget {
		t.Fatalf("waiting for VBR upshift did not promptly downshift: %+v", first)
	}
}

func TestControllerHardLimits(t *testing.T) {
	tests := []struct {
		name      string
		memory    int64
		decodeMS  float64
		want      string
		wantError bool
	}{
		{"inclusive memory cap removes current high", 2 << 20, 500, "mid", false},
		{"inclusive decode cap removes current high", 8 << 20, 200, "mid", false},
		{"only lowest memory fits", 1 << 20, 500, "low", false},
		{"nothing fits memory", (1 << 20) - 1, 500, "", true},
		{"nothing fits decode", 8 << 20, 99, "", true},
		{"zero decode budget", 8 << 20, 0, "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			config.MaxDecodedBytes, config.MaxDecodeTimeMS = test.memory, test.decodeMS
			c := mustController(t, testManifest(1), config)
			observeRate(t, c, 100_000_000, 1)
			d, err := c.Select(Request{CurrentID: "high", BufferSeconds: 8})
			if test.wantError {
				if !errors.Is(err, ErrNoFeasibleRepresentation) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || d.RepresentationID != test.want || d.Reason != ReasonResourceLimit {
				t.Fatalf("decision = %+v, error = %v", d, err)
			}
		})
	}
	// Zero is also a useful strict budget for a zero-cost decoder.
	manifest := testManifest(1)
	manifest.Representations[0].DecodeTimeMS = 0
	config := DefaultConfig()
	config.MaxDecodeTimeMS = 0
	c := mustController(t, manifest, config)
	if d := selectOK(t, c, Request{}); d.RepresentationID != "low" {
		t.Fatal(d)
	}
}

func TestControllerUpshiftHeadroomAndConfirmations(t *testing.T) {
	config := DefaultConfig()
	config.UpshiftHeadroom = 1.2
	c := mustController(t, testManifest(10), config)
	observeRate(t, c, 2_750_000, 1) // Safe 2.2 Mbps fits mid, but has no upshift headroom.
	if d := selectOK(t, c, Request{CurrentID: "low", BufferSeconds: 8}); d.RepresentationID != "low" {
		t.Fatal(d)
	}
	// Restart at high bandwidth to isolate decision hysteresis from EWMA recovery.
	c = mustController(t, testManifest(10), config)
	observeRate(t, c, 8_000_000, 1)
	steps := []struct {
		index int
		want  string
	}{
		{0, "low"}, {0, "low"}, {2, "low"}, // Repeats do not count; skips reset.
		{3, "mid"}, {3, "mid"}, // Retrying the same recommendation is stable.
		{1, "low"}, {2, "mid"}, // Seeking starts another confirmation sequence.
	}
	for _, step := range steps {
		d := selectOK(t, c, Request{SegmentIndex: step.index, CurrentID: "low", BufferSeconds: 8})
		if d.RepresentationID != step.want {
			t.Fatalf("index %d: %+v, want %s", step.index, d, step.want)
		}
	}
}

func TestControllerRejectsInvalidRequests(t *testing.T) {
	c := mustController(t, testManifest(2), DefaultConfig())
	for _, request := range []Request{
		{SegmentIndex: -1}, {SegmentIndex: 2}, {SegmentIndex: math.MaxInt}, {SegmentIndex: math.MinInt},
		{BufferSeconds: -1}, {BufferSeconds: math.NaN()}, {BufferSeconds: math.Inf(1)},
		{CurrentID: "unknown"},
	} {
		if _, err := c.Select(request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("request %+v: %v", request, err)
		}
	}
	observeRate(t, c, 8_000_000, 1)
	selectOK(t, c, Request{CurrentID: "low", BufferSeconds: 8})
	if _, err := c.Select(Request{SegmentIndex: -1}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if d := selectOK(t, c, Request{SegmentIndex: 1, CurrentID: "low", BufferSeconds: 8}); d.RepresentationID != "mid" {
		t.Fatalf("invalid request changed confirmation state: %+v", d)
	}
}

func TestControllerOverflowAndSnapshot(t *testing.T) {
	manifest := testManifest(1)
	manifest.Representations[0].InitURL = "/assets/clip/r1/low/init.bin"
	manifest.Representations[0].InitSizeBytes = math.MaxInt64
	if _, err := NewController(manifest, DefaultConfig()); !errors.Is(err, ErrTransferSizeOverflow) {
		t.Fatalf("size overflow error = %v", err)
	}
	manifest = testManifest(1)
	manifest.Representations = manifest.Representations[:1]
	manifest.Representations[0].Segments[0].SizeBytes = math.MaxInt64
	c := mustController(t, manifest, DefaultConfig())
	if err := c.Observe(Sample{Bytes: math.MaxInt64, DurationSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	d := selectOK(t, c, Request{CurrentID: "low", BufferSeconds: 8})
	if d.RequiredBytes != math.MaxInt64 || !closeRate(d.DownloadSeconds, 1.25) || !d.WithinDeliveryBudget {
		t.Fatalf("byte-to-bit multiplication overflowed: %+v", d)
	}
	manifest = testManifest(1)
	manifest.Representations[0], manifest.Representations[2] = manifest.Representations[2], manifest.Representations[0]
	c = mustController(t, manifest, DefaultConfig())
	manifest.Representations[2].Segments[0].SizeBytes = 1
	manifest.Representations[2].PeakDecodedBytes = math.MaxInt64
	d = selectOK(t, c, Request{})
	if d.RepresentationID != "low" || d.RepresentationIndex != 2 || d.RequiredBytes != 250_000 {
		t.Fatalf("manifest ordering or later mutation affected snapshot: %+v", d)
	}
}

func TestControllerRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{"zero safety", func(c *Config) { c.SafetyFactor = 0 }},
		{"unsafe factor", func(c *Config) { c.SafetyFactor = 1.1 }},
		{"NaN safety", func(c *Config) { c.SafetyFactor = math.NaN() }},
		{"zero startup bandwidth", func(c *Config) { c.StartupBandwidthBPS = 0 }},
		{"infinite startup buffer", func(c *Config) { c.StartupBufferSeconds = math.Inf(1) }},
		{"negative reserve", func(c *Config) { c.BufferReserveSeconds = -1 }},
		{"zero empty-buffer horizon", func(c *Config) { c.EmptyBufferHorizonSeconds = 0 }},
		{"inverted horizon", func(c *Config) { c.MaxDownloadHorizonSeconds = 0.5 }},
		{"no upshift headroom", func(c *Config) { c.UpshiftHeadroom = 1 }},
		{"no confirmations", func(c *Config) { c.UpshiftConfirmations = 0 }},
		{"no memory", func(c *Config) { c.MaxDecodedBytes = 0 }},
		{"negative decode", func(c *Config) { c.MaxDecodeTimeMS = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			test.change(&config)
			if _, err := NewController(testManifest(1), config); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	manifest := testManifest(1)
	manifest.Representations[0].Segments[0].Independent = false
	if _, err := NewController(manifest, DefaultConfig()); err == nil {
		t.Fatal("accepted dependent segment")
	}
}

func testManifest(count int) stream.Manifest {
	m := stream.Manifest{Version: stream.Version, ClipID: "clip", Revision: "r1", Codec: "synthetic"}
	for i, id := range []string{"low", "mid", "high"} {
		bitrate := int64(1_000_000 << i)
		r := stream.Representation{ID: id, BitrateBPS: bitrate, PeakDecodedBytes: int64(1<<20) << i, DecodeTimeMS: float64(100 * (i + 1))}
		for j := 0; j < count; j++ {
			r.Segments = append(r.Segments, stream.Segment{Index: j, StartSeconds: float64(2 * j), DurationSeconds: 2,
				URL: fmt.Sprintf("/assets/clip/r1/%s/segment-%d.bin", id, j), SizeBytes: bitrate / 4, Independent: true, SHA256: strings.Repeat("0", 64)})
		}
		m.Representations = append(m.Representations, r)
	}
	return m
}

func mustController(t *testing.T, manifest stream.Manifest, config Config) *Controller {
	t.Helper()
	c, err := NewController(manifest, config)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func observeRate(t *testing.T, c *Controller, bps, seconds float64) {
	t.Helper()
	if err := c.Observe(Sample{Bytes: int64(bps * seconds / 8), DurationSeconds: seconds}); err != nil {
		t.Fatal(err)
	}
}

func selectOK(t *testing.T, c *Controller, request Request) Decision {
	t.Helper()
	d, err := c.Select(request)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
