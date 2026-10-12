//go:build !darwin && !linux

package artifact

import "os"

// Fail closed where nonblocking, no-follow access is not implemented.
func openNoFollow(string, bool) (*os.File, error) { return nil, ErrStoreUnsupported }
