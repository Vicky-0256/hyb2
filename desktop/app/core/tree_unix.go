//go:build !windows

package core

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

// newProcessGroup makes the task child lead its own process group, so
// killTree reaches every process it starts.
func newProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree sends SIGKILL to the child's whole process group. A group that
// is already gone is not an error, nor is one whose only member exited and
// awaits reaping (macOS answers EPERM for such a group).
func killTree(pid int) error {
	if pid <= 0 {
		return errors.New("could not cancel process tree: no process")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("could not cancel process tree: %w", err)
	}
	return nil
}
