package privatepreview

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestNonblockingOpenRefusesSpecialFilesAndSymlinks(t *testing.T) {
	h, dir, _ := handlerFixture(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "scene-a/frame-003.splat"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := request(t, h, "GET", PathPrefix+"scene-a/frame-003.splat", nil); w.Code != 404 {
		t.Fatal("FIFO accepted")
	}
	root, err := os.OpenRoot(filepath.Join(dir, "scene-a"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{"../scene.json", "/etc/passwd", ".secret", "other.splat"} {
		if f, err := openFile(root, name); err == nil {
			f.Close()
			t.Fatal("non-allowlisted direct filename accepted")
		}
	}
	if err := root.Symlink("frame-000.splat", "frame-004.splat"); err != nil {
		t.Fatal(err)
	}
	if f, err := openFile(root, "frame-004.splat"); err == nil {
		f.Close()
		t.Fatal("final symlink followed")
	}
}
