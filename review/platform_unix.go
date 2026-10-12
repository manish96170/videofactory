//go:build darwin || linux

package review

import (
	"os"
	"syscall"
)

// Parent directories are controlled by the caller. Leaf symlinks are rejected
// atomically; nonblocking opens also prevent a substituted FIFO from hanging.
func openNoFollow(name string, directory bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if directory {
		flags |= syscall.O_DIRECTORY
	}
	f, err := os.OpenFile(name, flags, 0)
	if err != nil {
		return nil, ErrUnsafePath
	}
	i, err := f.Stat()
	if err != nil || directory && !i.IsDir() || !directory && !i.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrUnsafePath
	}
	return f, nil
}
