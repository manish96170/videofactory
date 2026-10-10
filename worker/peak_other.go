//go:build !darwin && !linux

package worker

import "os"

func processPeak(s *os.ProcessState) (int64, bool) { return 0, false }
