//go:build !linux

package privatepreview

import "os"

func openFile(root *os.Root, name string) (*os.File, error) {
	if !assetName.MatchString(name) {
		return nil, errInvalidAsset
	}
	return root.Open(name)
}
