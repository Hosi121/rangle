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
			ID, Profile, File, Model string
			Accept                   bool
			WireBinding              bool `json:"wire_binding"`
			Recipe                   struct {
				Kind  string
				Bytes int
			}
			Context struct {
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
		if c.Profile != "rgs" && !c.WireBinding {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			path, frames, fps := c.File, c.Context.FrameCount, c.Context.FrameRate
			if c.WireBinding {
				path = "rgs/" + c.Model + ".rgs"
			}
			data, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				t.Fatal(err)
			}
			if c.Recipe.Kind == "pad-to-size" {
				padded := make([]byte, c.Recipe.Bytes)
				copy(padded, data)
				data = padded
			}
			err = validateTemporalOutput(data, frames, fps)
			if (err == nil) != c.Accept {
				t.Fatalf("accept=%t want %t: %v", err == nil, c.Accept, err)
			}
		})
	}
}
