//go:build linux

package appserver

import (
	"os"
	"syscall"
)

// ctime catches same-size in-place writes even if a development tool restores
// mtime. Ignore atime: merely reading a file must not invalidate a sidecar.
func sameFileVersion(before, after os.FileInfo) bool {
	a, okA := before.Sys().(*syscall.Stat_t)
	b, okB := after.Sys().(*syscall.Stat_t)
	return okA && okB && before.Mode().IsRegular() && after.Mode().IsRegular() &&
		os.SameFile(before, after) && before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime()) && a.Ctim == b.Ctim
}
