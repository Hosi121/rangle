package publication

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"

	"github.com/Hosi121/rangle/internal/stream"
)

// This transport profile changes interval coverage, never the RGS1 wire clock.
const segmentedCodec = "rangle-temporal-rgs1-segments-v1"

var (
	indexedModelName   = regexp.MustCompile(`^model-[0-9]{5}\.rgs$`)
	indexedMetricsName = regexp.MustCompile(`^metrics-[0-9]{5}\.json$`)
)

func segmentNames(index int) (string, string) {
	if index == 0 {
		return "model.rgs", "metrics.json"
	}
	return fmt.Sprintf("model-%05d.rgs", index), fmt.Sprintf("metrics-%05d.json", index)
}

func validateSegmentedModels(files map[string][]byte, e entry, a Approval, prefix string) error {
	var s scene
	if err := decode(files[prefix+"scene.json"], &s); err != nil {
		return fmt.Errorf("segmented scene: %w", err)
	}
	if s.FrameCount < 2 || s.FrameCount > 15000 || !finite(s.FrameRate) || s.FrameRate <= 0 || s.FrameRate > 240 {
		return fmt.Errorf("invalid segmented scene clock")
	}
	// Each independently decoded model has N samples and the existing RGS1
	// sample span (N-1)/FPS. Transport covers N/FPS, holding the final sample
	// until the next independent interval. Do not reinterpret legacy durations.
	coverage := float64(s.FrameCount) / s.FrameRate
	local := e
	local.Manifest = stream.Manifest{Representations: []stream.Representation{{Segments: []stream.Segment{{DurationSeconds: float64(s.FrameCount-1) / s.FrameRate}}}}}
	segments := e.Manifest.Representations[0].Segments
	for i, segment := range segments {
		if math.Abs(segment.DurationSeconds-coverage) > 1e-6 || math.Abs(segment.StartSeconds-float64(i)*coverage) > 1e-6 {
			return fmt.Errorf("segment %d coverage differs from shared scene clock", i)
		}
		modelName, metricsName := segmentNames(i)
		metrics := files[prefix+metricsName]
		if err := validateSceneModel(files[prefix+"scene.json"], files[prefix+modelName], metrics, local, a); err != nil {
			return fmt.Errorf("segment %d: %w", i, err)
		}
		// Existing metrics checks retain wire/raw SHA, local sample times and
		// approved source/license. Window input hashes may legitimately differ.
		var binding struct {
			Index *int     `json:"segment_index"`
			Start *float64 `json:"global_start_seconds"`
		}
		if err := json.Unmarshal(metrics, &binding); err != nil || binding.Index == nil || binding.Start == nil ||
			*binding.Index != segment.Index || !finite(*binding.Start) || *binding.Start < 0 || math.Abs(*binding.Start-segment.StartSeconds) > 1e-6 {
			return fmt.Errorf("segment %d metrics lack matching global binding", i)
		}
	}
	// The ordinary video covers all N samples of all intervals. This remains
	// an EBML/timestamp gate, not proof of successful video frame decoding.
	return validateWebM(files[prefix+"preview.webm"], len(segments)*s.FrameCount, float32At(files[prefix+"model.rgs"][20:24]))
}
