package publication

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/live"
)

func TestSharedMediaContracts(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "contracts", "v1")
	var corpus struct {
		Context struct{ Origin, Title, Source, License string }
		Cases   []struct {
			ID, Profile, File, Model, Origin string
			Accept                           bool
			Recipe                           struct {
				Kind  string
				Bytes int
			}
			Context struct {
				FrameCount int
				FrameRate  float64
			}
			Normalized struct {
				FPS, End  float64
				RawSHA256 string `json:"raw_sha256"`
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
	read := func(name string) []byte {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(root, name))
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	for _, c := range corpus.Cases {
		if c.Profile == "bundle" || c.Profile == "clock" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			var body []byte
			if c.Recipe.Kind == "pad-to-size" {
				body = make([]byte, c.Recipe.Bytes)
				copy(body, read(c.File))
			} else if c.Recipe.Bytes > 0 {
				body = []byte(strings.Repeat(" ", c.Recipe.Bytes))
			} else {
				body = read(c.File)
			}
			var failure error
			switch c.Profile {
			case "live":
				origin := corpus.Context.Origin
				if c.Origin != "" {
					origin = c.Origin
				}
				_, failure = live.ParseIndex(body, origin)
			case "manifest":
				_, failure = catalog.Load(strings.NewReader(string(body)))
			case "scene":
				model := read("rgs/" + c.Model + ".rgs")
				var timeline struct {
					FrameRate  float64
					FrameCount int
				}
				if err := json.Unmarshal(body, &timeline); err != nil {
					failure = err
					break
				}
				failure = ValidateLiveScene(body, model, corpus.Context.Title, corpus.Context.Source, corpus.Context.License, float64(timeline.FrameCount-1)/timeline.FrameRate)
			case "rgs":
				var raw []byte
				raw, failure = temporalModel(body, c.Context.FrameCount, c.Context.FrameRate)
				if failure == nil && c.Accept {
					actualFPS := float32At(raw[20:24])
					end := float64(c.Context.FrameCount-1) / actualFPS
					if actualFPS != c.Normalized.FPS || math.Abs(end-c.Normalized.End) > 1e-12 || fmt.Sprintf("%x", sha256.Sum256(raw)) != c.Normalized.RawSHA256 {
						t.Fatal("normalized wire values differ")
					}
				}
			default:
				t.Fatalf("unhandled profile %s", c.Profile)
			}
			if (failure == nil) != c.Accept {
				t.Fatalf("accept=%t, want %t: %v", failure == nil, c.Accept, failure)
			}
		})
	}
}
