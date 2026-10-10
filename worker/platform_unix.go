//go:build darwin || linux

package worker

import (
	"os/exec"
	"syscall"
)

func platformSupported() bool            { return true }
func configureGroup(cmd *exec.Cmd)       { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func validGroup(group ProcessGroup) bool { return group.ID > 1 && group.ID != syscall.Getpgrp() }
func killGroup(group ProcessGroup) error {
	if !validGroup(group) {
		return ErrCleanup
	}
	err := syscall.Kill(-group.ID, syscall.SIGKILL)
	if err != nil && err != syscall.ESRCH {
		return ErrCleanup
	}
	return nil
}
