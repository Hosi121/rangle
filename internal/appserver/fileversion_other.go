//go:build !linux

package appserver

import "os"

// Platforms without a change counter still require the streaming SHA check.
func sameFileVersion(before, after os.FileInfo) bool {
	return before.Mode().IsRegular() && after.Mode().IsRegular() &&
		os.SameFile(before, after) && before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime())
}
