//go:build darwin

package worker

import (
	"os"
	"syscall"
)

func processPeak(s *os.ProcessState) (int64, bool) {
	if s == nil {
		return 0, false
	}
	r, ok := s.SysUsage().(*syscall.Rusage)
	if !ok || r == nil {
		return 0, false
	}
	return normalizePeak(r.Maxrss, false)
}
