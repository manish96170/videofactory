//go:build darwin || linux

package artifact

import (
	"os"
	"syscall"
)

// O_NOFOLLOW rejects leaf symlinks atomically. Parent components are checked by
// checkedPath under the controlled-root ownership contract. O_NONBLOCK prevents
// a FIFO substituted for a regular file from blocking before its type check.
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
			return nil, ErrStoreIO
		}
		return nil, ErrUnsafePath
	}
	info, err := f.Stat()
	if err != nil || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		f.Close()
		return nil, ErrUnsafePath
	}
	return f, nil
}
