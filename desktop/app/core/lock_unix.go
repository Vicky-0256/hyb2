//go:build !windows

package core

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// lockRunDir takes the exclusive owner lock of a task's run directory. The
// kernel drops a flock when its holder exits, however it exits.
func lockRunDir(runDir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(runDir, ownerLockFile), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
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
