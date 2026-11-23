//go:build !windows

package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const errLockBusy = syscall.EWOULDBLOCK

// lockRunDir takes the exclusive owner lock of a task's run directory. The
// kernel drops a flock when its holder exits, however it exits.
func lockRunDir(runDir string) (*os.File, error) {
	return tryFileLock(filepath.Join(runDir, ownerLockFile))
}

func tryFileLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o644)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("lock %s is not a regular file", path)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// inheritLocks hands the task child its own copy of each lock (fd 3 on). A
// flock belongs to the open file description, so it stays held until the
// app and the child have both closed theirs: a child that outlives the app
// keeps its run owned. The child need not know about the fds.
func inheritLocks(cmd *exec.Cmd, locks ...*os.File) (release func(), err error) {
	for _, lock := range locks {
		if lock != nil {
			cmd.ExtraFiles = append(cmd.ExtraFiles, lock)
		}
	}
	return func() {}, nil
}

// runDirLocked reports whether a live owner (in any process) holds the run
// directory's lock. No lock file means no owner.
func runDirLocked(runDir string) bool {
	// Not followed or blocked on: a copied project may plant anything here.
	f, err := os.OpenFile(filepath.Join(runDir, ownerLockFile), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return false
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(fd, syscall.LOCK_UN)
	return false
}
