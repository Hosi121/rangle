// abr-sim demonstrates a finite, invented ABR trace. It performs no network,
// codec, GPU, or real device measurements.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"

	"github.com/Hosi121/rangle/internal/abr"
	"github.com/Hosi121/rangle/internal/stream"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "abr-sim")
	if err := run(os.Stdout); err != nil {
		logger.Error("simulation failed", "error", err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	capacities := []float64{2e6, 2e6, 4e6, 4e6, 4e6, 0.4e6, 0.3e6, 1e6, 4e6, 4e6, 4e6, 4e6}
	manifest := syntheticManifest(len(capacities))
	controller, err := abr.NewController(manifest, abr.DefaultConfig())
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Synthetic ABR trace: invented segment sizes, decode costs, and network capacities.\n"+
		"This is a policy demonstration, not a 4DGS asset or a measured performance result.\n"+
		"wait_s includes initial acquisition and any later buffer exhaustion.\n"+
		"seg  capacity_Mbps estimate_Mbps quality bytes buffer_s wait_s reason"); err != nil {
		return err
	}
	buffer, current := 0.0, ""
	initCached := make(map[string]bool)
	for index, capacity := range capacities {
		decision, err := controller.Select(abr.Request{SegmentIndex: index, CurrentID: current, BufferSeconds: buffer, InitCached: initCached})
		if err != nil {
			return err
		}
		representation := manifest.Representations[decision.RepresentationIndex]
		// The synthetic transfer includes a fixed 20 ms request latency.
		transferSeconds := float64(decision.RequiredBytes)*8/capacity + 0.02
		readySeconds := transferSeconds + representation.DecodeTimeMS/1000
		wait := math.Max(0, readySeconds-buffer)
		buffer = math.Max(0, buffer-readySeconds) + representation.Segments[index].DurationSeconds
		if err := controller.Observe(abr.Sample{Bytes: decision.RequiredBytes, DurationSeconds: transferSeconds}); err != nil {
			return err
		}
		initCached[decision.RepresentationID] = true
		current = decision.RepresentationID
		if _, err := fmt.Fprintf(out, "%3d %14.2f %13.2f %7s %5d %8.2f %7.2f %s\n", index, capacity/1e6, decision.EstimateBPS/1e6, current, decision.RequiredBytes, buffer, wait, decision.Reason); err != nil {
			return err
		}
	}
	return nil
}

func syntheticManifest(count int) stream.Manifest {
	m := stream.Manifest{Version: stream.Version, ClipID: "synthetic", Revision: "v1", Codec: "synthetic-metadata-only"}
	for i, id := range []string{"low", "mid", "high"} {
		bitrate := []int64{400_000, 1_000_000, 2_000_000}[i]
		prefix := "/assets/synthetic/v1/" + id + "/"
		r := stream.Representation{ID: id, BitrateBPS: bitrate, InitURL: prefix + "init.bin", InitSizeBytes: int64(1000 * (i + 1)), PeakDecodedBytes: int64(16<<20) << i, DecodeTimeMS: float64(10 * (i + 1))}
		for index := 0; index < count; index++ {
			r.Segments = append(r.Segments, stream.Segment{Index: index, StartSeconds: float64(index), DurationSeconds: 1, URL: fmt.Sprintf("%ssegment-%d.bin", prefix, index), SizeBytes: bitrate / 8, Independent: true, SHA256: strings.Repeat("0", 64)})
		}
		m.Representations = append(m.Representations, r)
	}
	return m
}
