// rangle-live-check verifies one bounded index and optionally all local RGS1 assets.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/Hosi121/rangle/internal/live"
	"github.com/Hosi121/rangle/internal/publication"
)

func main() {
	index := flag.String("index", "", "local live index JSON")
	origin := flag.String("origin", "", "approved external media origin")
	root := flag.String("root", "", "optional worker output root for full asset verification")
	flag.Parse()
	if err := check(*index, *origin, *root); err != nil {
		fmt.Fprintln(os.Stderr, "live-check:", err)
		os.Exit(1)
	}
}

func read(root *os.Root, name string, limit int64) ([]byte, error) {
	for i, parts := 0, strings.Split(name, "/"); i < len(parts); i++ {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink in live publication")
		}
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid live asset type or size")
	}
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(body) > int(limit) {
		return nil, fmt.Errorf("live asset exceeds limit")
	}
	return body, err
}

func check(index, origin, directory string) error {
	file, err := os.Open(index)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(file, live.MaxIndexBytes+1))
	file.Close()
	if err != nil {
		return err
	}
	parsed, err := live.ParseIndex(body, origin)
	if err != nil {
		return err
	}
	files := 0
	var totalBytes int64
	if directory != "" {
		root, err := os.OpenRoot(directory)
		if err != nil {
			return err
		}
		defer root.Close()
		for _, session := range parsed.Sessions {
			var camera json.RawMessage
			for _, segment := range session.Segments {
				u, _ := url.Parse(segment.SceneURL)
				if u.IsAbs() {
					return fmt.Errorf("local assets require local scene URLs")
				}
				scenePath := strings.TrimPrefix(u.Path, "/")
				modelPath := strings.TrimSuffix(scenePath, "scene.json") + "model.rgs"
				scene, err := read(root, scenePath, 64<<10)
				if err != nil {
					return err
				}
				model, err := read(root, modelPath, 32<<20)
				if err != nil {
					return err
				}
				totalBytes += int64(len(model) + len(scene))
				if totalBytes > 512<<20 {
					return fmt.Errorf("live publication exceeds total byte budget")
				}
				if int64(len(model)) != int64(segment.ModelBytes) || fmt.Sprintf("%x", sha256.Sum256(model)) != segment.ModelSHA256 {
					return fmt.Errorf("live model proof differs from index")
				}
				if err := publication.ValidateLiveScene(scene, model, session.Title, session.Source.URL, session.Source.License, segment.EndSeconds-segment.StartSeconds); err != nil {
					return err
				}
				var metadata struct {
					Camera json.RawMessage `json:"camera"`
				}
				if err := json.Unmarshal(scene, &metadata); err != nil {
					return err
				}
				var normalized any
				if err := json.Unmarshal(metadata.Camera, &normalized); err != nil {
					return err
				}
				stable, _ := json.Marshal(normalized)
				if camera != nil && string(camera) != string(stable) {
					return fmt.Errorf("live camera/world convention changed")
				}
				camera = stable
				files += 2
			}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"version": 1, "sessions": len(parsed.Sessions), "verified_asset_files": files, "index_sha256": fmt.Sprintf("%x", sha256.Sum256(body))})
}
