package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the production CLI plus the actual Python producer's verify->Go
// handoff. Python receives constructed objects, not raw external JSON syntax.
func TestSharedLiveProducerContracts(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(root, "testdata", "contracts", "v1")
	var corpus struct {
		Context struct{ Origin string }
		Cases   []struct {
			ID, Profile, File, Model, Scene, Origin string
			Accept                                  bool
			ProducerObject                          *bool `json:"producer_object"`
			Recipe                                  struct{ Bytes int }
		}
	}
	body, err := os.ReadFile(filepath.Join(fixtures, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &corpus); err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(t.TempDir(), "rangle-live-check")
	build := exec.Command("go", "build", "-o", gate, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	for _, c := range corpus.Cases {
		if c.Profile != "live" && c.Profile != "bundle" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "index.json")
			var data []byte
			if c.Recipe.Bytes > 0 {
				data = []byte(strings.Repeat(" ", c.Recipe.Bytes))
			} else {
				var err error
				data, err = os.ReadFile(filepath.Join(fixtures, c.File))
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			assetRoot := ""
			if c.Profile == "bundle" {
				assetRoot = dir
				var index struct {
					Sessions []struct {
						Segments []struct {
							SceneURL string `json:"scene_url"`
						}
					}
				}
				if err := json.Unmarshal(data, &index); err != nil {
					t.Fatal(err)
				}
				prefix := filepath.Join(dir, strings.TrimPrefix(index.Sessions[0].Segments[0].SceneURL, "/"))
				if err := os.MkdirAll(filepath.Dir(prefix), 0700); err != nil {
					t.Fatal(err)
				}
				for _, entry := range []struct{ Src, Dst string }{{"scene/" + c.Scene + ".json", prefix}, {"rgs/" + c.Model + ".rgs", filepath.Join(filepath.Dir(prefix), "model.rgs")}} {
					b, err := os.ReadFile(filepath.Join(fixtures, entry.Src))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(entry.Dst, b, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			origin := corpus.Context.Origin
			if c.Origin != "" {
				origin = c.Origin
			}
			if err := check(path, origin, assetRoot); (err == nil) != c.Accept {
				t.Fatalf("Go gate accept=%t want %t: %v", err == nil, c.Accept, err)
			}
			if c.ProducerObject != nil && !*c.ProducerObject {
				return
			}
			command := exec.Command("python3", filepath.Join(root, "ml/tests/live_contract_oracle.py"), gate, path, origin, assetRoot)
			output, err := command.CombinedOutput()
			if (err == nil) != c.Accept {
				t.Fatalf("Python producer gate accept=%t want %t: %v %s", err == nil, c.Accept, err, output)
			}
		})
	}
}
