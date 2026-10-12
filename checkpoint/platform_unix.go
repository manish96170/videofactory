//go:build darwin || linux

package checkpoint

import (
	"os"
	"syscall"
)

// No-follow and nonblocking open prevent symlinks and substituted FIFOs from
// escaping or hanging a read. Only regular files and directories are admitted.
func openNoFollow(name string, directory bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if directory {
		flags |= syscall.O_DIRECTORY
	}
	f, err := os.OpenFile(name, flags, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		if os.IsPermission(err) {
			return nil, ErrIO
		}
		return nil, ErrUnsafePath
	}
	i, err := f.Stat()
	if err != nil || directory && !i.IsDir() || !directory && !i.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrUnsafePath
	}
	return f, nil
}
