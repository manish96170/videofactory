//go:build !darwin && !linux

package checkpoint

import "os"

func openNoFollow(string, bool) (*os.File, error) { return nil, ErrUnsupported }
