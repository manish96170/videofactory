//go:build !darwin && !linux

package worker

import "os/exec"

func platformSupported() bool            { return false }
func configureGroup(cmd *exec.Cmd)       {}
func validGroup(group ProcessGroup) bool { return false }
func killGroup(group ProcessGroup) error { return ErrUnsupported }
