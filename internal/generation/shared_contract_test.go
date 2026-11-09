package generation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedGeneratedWireContract(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "contracts", "v1")
	var corpus struct {
		Cases []struct {
			ID, Profile, File string
			Accept            bool
			Context           struct {
				FrameCount int
				FrameRate  float64
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	for _, c := range corpus.Cases {
		if c.Profile != "rgs" && c.ID != "S13" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			path, frames, fps := c.File, c.Context.FrameCount, c.Context.FrameRate
			if c.ID == "S13" {
				path, frames, fps = "rgs/fps-mismatch.rgs", 3, 25
			}
			data, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				t.Fatal(err)
			}
			err = validateTemporalOutput(data, frames, fps)
			if (err == nil) != c.Accept {
				t.Fatalf("accept=%t want %t: %v", err == nil, c.Accept, err)
			}
		})
	}
}
