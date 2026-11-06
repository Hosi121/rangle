package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/Hosi121/rangle/internal/stream"
)

// DemoFixture returns fresh metadata and tiny deterministic transport payloads.
// These bytes are synthetic placeholders, not encoded 4D Gaussian splats or a
// playable media format. Decode and working-memory estimates are invented.
func DemoFixture() (stream.Manifest, map[string][]byte) {
	manifest := stream.Manifest{
		Version:  stream.Version,
		ClipID:   "demo",
		Revision: "synthetic-v1",
		Codec:    "synthetic-transport-v1",
	}
	assets := make(map[string][]byte)
	qualities := []struct {
		id       string
		sizes    [4]int
		initSize int
		memory   int64
		decodeMS float64
	}{
		{"low", [4]int{128, 160, 144, 128}, 24, 64 * 1024, 2},
		{"medium", [4]int{384, 448, 416, 384}, 32, 128 * 1024, 4},
		{"high", [4]int{768, 896, 832, 768}, 48, 256 * 1024, 8},
	}
	for _, quality := range qualities {
		prefix := "/assets/" + manifest.ClipID + "/" + manifest.Revision + "/" + quality.id + "/"
		representation := stream.Representation{
			ID:               quality.id,
			BitrateBPS:       int64(quality.sizes[1] * 8),
			InitURL:          prefix + "init.bin",
			InitSizeBytes:    int64(quality.initSize),
			PeakDecodedBytes: quality.memory,
			DecodeTimeMS:     quality.decodeMS,
		}
		assets[representation.InitURL] = syntheticBytes("synthetic init "+quality.id+"\n", quality.initSize)
		for index, size := range quality.sizes {
			url := fmt.Sprintf("%ssegment-%d.bin", prefix, index)
			data := syntheticBytes(fmt.Sprintf("synthetic transport %s segment %d\n", quality.id, index), size)
			sum := sha256.Sum256(data)
			assets[url] = data
			representation.Segments = append(representation.Segments, stream.Segment{
				Index:           index,
				StartSeconds:    float64(index),
				DurationSeconds: 1,
				URL:             url,
				SizeBytes:       int64(size),
				Independent:     true,
				SHA256:          hex.EncodeToString(sum[:]),
			})
		}
		manifest.Representations = append(manifest.Representations, representation)
	}
	return manifest, assets
}

func syntheticBytes(label string, size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = label[i%len(label)]
	}
	return data
}
