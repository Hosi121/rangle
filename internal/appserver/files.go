package appserver

import (
	"fmt"
	"os"
	"strings"
)

func openRelative(root *os.Root, name string, directory bool) (*os.File, error) {
	if !validRelative(name) {
		return nil, fmt.Errorf("invalid relative file path")
	}
	// Exclude symlinks below the configured root, including links to hidden
	// files within it. os.Root additionally prevents a raced link escaping root.
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("published path contains a symlink")
		}
		if i == len(parts)-1 && (directory && !info.IsDir() || !directory && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("published path has the wrong file type")
		}
	}
	return root.Open(name)
}
