package privatepreview

import (
	"os"
	"syscall"
)

func openFile(root *os.Root, name string) (*os.File, error) {
	if !assetName.MatchString(name) {
		return nil, errInvalidAsset
	}
	// Root intentionally resolves symlinks even with O_NOFOLLOW. For the single
	// allowlisted filename, openat on the pinned directory descriptor refuses a
	// raced final symlink and avoids blocking on a raced FIFO before f.Stat.
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}
