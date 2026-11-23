package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestRuntimeUndeletableStage(t *testing.T) {
	cache := t.TempDir()
	archive, digest := makeArchive(t, t.TempDir(), runtimeEntries())
	dir := UnpackedRuntimeDir(cache, digest)
	stage := filepath.Join(filepath.Dir(dir), ".unpacking")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(stage, "partial")
	if err := os.WriteFile(partial, []byte("abandoned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// A handle opened without delete sharing prevents removal on Windows.
		pin, err := tryFileLock(partial)
		if err != nil {
			t.Fatal(err)
		}
		defer pin.Close()
	} else {
		if err := os.Chmod(stage, 0o555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(stage, 0o700) // allow t.TempDir cleanup
	}
	if err := os.Remove(partial); err == nil {
		t.Skip("this user can delete the protected file")
	}
	b, err := UnpackRuntime(archive, digest, cache)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(b.Root) != dir || !RuntimeReady(cache, digest) {
		t.Fatalf("did not publish a usable, separate generation: %+v", b)
	}
	if _, err := os.Stat(partial); err != nil {
		t.Fatalf("protected stale work was not left alone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "partial")); !os.IsNotExist(err) {
		t.Fatalf("stale work appeared in the published generation: %v", err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("staging directory was not private: %v %v", info, err)
		}
	}
}

func TestRuntimePublisherWaitAndRetry(t *testing.T) {
	cache := t.TempDir()
	archive, digest := makeArchive(t, t.TempDir(), runtimeEntries())
	pool := filepath.Dir(UnpackedRuntimeDir(cache, digest))
	stage := filepath.Join(pool, ".unpacking")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(pool, "publish.lock")
	holder, err := tryFileLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	synctest.Test(t, func(t *testing.T) {
		// Only the clock is virtual. Release the real lock after one minute
		// so a regression to unbounded polling fails rather than hanging CI.
		guard := time.AfterFunc(time.Minute, func() { holder.Close() })
		defer guard.Stop()
		start := time.Now()
		b, err := UnpackRuntime(archive, digest, cache)
		if err == nil || b != nil || !strings.Contains(err.Error(), "retry") {
			t.Fatalf("wanted actionable lock timeout, got bridge=%+v err=%v after %v", b, err, time.Since(start))
		}
		if time.Since(start) == 0 {
			t.Fatal("did not wait for the active publisher")
		}
	})
	if RuntimeReady(cache, digest) || !isDir(stage) {
		t.Fatal("waiter modified the active publisher's work")
	}
	if other, err := tryFileLock(lockPath); !errors.Is(err, errLockBusy) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("waiter broke the publisher's lock: %v", err)
	}
	if err := holder.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackRuntime(archive, digest, cache); err != nil || !RuntimeReady(cache, digest) {
		t.Fatalf("retry after release: %v", err)
	}
}
