//go:build windows

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	errLockBusy              syscall.Errno = 32 // ERROR_SHARING_VIOLATION
	fileFlagOpenReparsePoint               = 0x00200000
)

// lockRunDir opens the run directory's owner lock file with no sharing:
// nobody else can open it until the handle closes, which Windows does when
// the holder exits, however it exits.
func lockRunDir(runDir string) (*os.File, error) {
	return tryFileLock(filepath.Join(runDir, ownerLockFile))
}

func tryFileLock(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL|fileFlagOpenReparsePoint, 0)
	if err != nil {
		return nil, &os.PathError{Op: "lock", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("lock %s is not a regular file", path)
	}
	return f, nil
}

// runDirLocked reports whether a live owner (in any process) holds the run
// directory's lock. No lock file means no owner.
func runDirLocked(runDir string) bool {
	name, err := syscall.UTF16PtrFromString(filepath.Join(runDir, ownerLockFile))
	if err != nil {
		return false
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL|fileFlagOpenReparsePoint, 0)
	if err != nil {
		return err == errLockBusy
	}
	syscall.CloseHandle(h)
	return false
}
