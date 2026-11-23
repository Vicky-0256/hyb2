package core

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Stat the open handle: on Windows os.Stat(path) may defer the ID lookup
// until SameFile, after the path has changed. Close before the other launch
// so the observation itself doesn't prevent replacement on Windows.
func snapshotFile(t *testing.T, path string) os.FileInfo {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestPublicationSnapshotDetectsReplacement(t *testing.T) {
	dir := t.TempDir()
	path, replacement := filepath.Join(dir, "python"), filepath.Join(dir, "other")
	for _, p := range []string{path, replacement} {
		if err := os.WriteFile(p, []byte("identical contents"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotFile(t, path)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, snapshotFile(t, path)) {
		t.Fatal("did not detect replacement at the same path")
	}
}

// The parent holds this resolver open until the competing launch has returned.
// Re-open the original path then: a sidecar started later needs that same tree.
func TestRuntimeResolverChild(t *testing.T) {
	resources := os.Getenv("HYB2_TEST_RUNTIME_RESOURCES")
	if resources == "" {
		return
	}
	await := func() {
		if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("ready")
	await()
	archive, digest, _, err := PackedRuntime(resources)
	if err != nil {
		t.Fatal(err)
	}
	b, err := UnpackRuntime(archive, digest, os.Getenv("HYB2_TEST_RUNTIME_CACHE"))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotFile(t, b.Python)
	fmt.Println(b.Python)
	await()
	if !os.SameFile(before, snapshotFile(t, b.Python)) {
		t.Fatal("active interpreter replaced")
	}
	if runtime.GOOS != "windows" {
		cmd := exec.Command(b.Python)
		cmd.Dir = b.Root
		if out, err := cmd.CombinedOutput(); err != nil || string(out) != "A" {
			t.Fatalf("late child launch: %q, %v", out, err)
		}
	}
}

func TestRuntimePublicationAcrossProcesses(t *testing.T) {
	for _, version := range []string{"same", "repair", "different"} {
		t.Run(version, func(t *testing.T) {
			resources, cache := t.TempDir(), t.TempDir()
			entries := runtimeEntries()
			entries[2].body = "#!/bin/sh\nprintf A\n"
			// A small archive with enough filesystem work for both cold resolvers to race.
			for i := range 64 {
				entries = append(entries, entry{name: fmt.Sprintf("data/%d", i), body: "x"})
			}
			archive, digest := makeArchive(t, resources, entries)
			switch version {
			case "repair":
				b, err := UnpackRuntime(archive, digest, cache)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(b.Python); err != nil {
					t.Fatal(err)
				}
			case "different":
				entries[2].body = "#!/bin/sh\nprintf B\n"
				archive, digest = makeArchive(t, t.TempDir(), entries)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeResolverChild$")
			cmd.Env = append(os.Environ(), "HYB2_TEST_RUNTIME_RESOURCES="+resources, "HYB2_TEST_RUNTIME_CACHE="+cache)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(output)
			t.Cleanup(func() {
				input.Close()
				rest, _ := io.ReadAll(reader)
				if err := cmd.Wait(); err != nil {
					t.Errorf("resolver process: %v\n%s", err, rest)
				}
			})
			line := func() string {
				s, err := reader.ReadString('\n')
				if err != nil {
					t.Fatalf("resolver output %q: %v", s, err)
				}
				return strings.TrimSpace(s)
			}
			if s := line(); s != "ready" {
				t.Fatal(s)
			}
			if _, err := input.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			if version == "different" {
				// B starts only after the independent A process has obtained its bridge.
				if path := line(); !isFile(path) {
					t.Fatalf("A interpreter: %s", path)
				}
			}
			b, err := UnpackRuntime(archive, digest, cache)
			if err != nil {
				t.Fatal(err)
			}
			before := snapshotFile(t, b.Python)
			if version != "different" {
				if path := line(); path != b.Python {
					t.Fatalf("resolvers did not share one generation: %q vs %q", path, b.Python)
				}
				if !os.SameFile(before, snapshotFile(t, b.Python)) {
					t.Fatal("parent interpreter replaced")
				}
			}
			if _, err := input.Write([]byte{2}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeRepair(t *testing.T) {
	cases := []string{"missing python", "missing bridge", "wrong marker", "missing marker"}
	if runtime.GOOS != "windows" {
		cases = append(cases, "non-executable python")
		if os.Geteuid() != 0 { // root can execute 0655 via the other execute bits
			cases = append(cases, "owner cannot execute python")
		}
	}
	for _, damage := range cases {
		t.Run(damage, func(t *testing.T) {
			cache := t.TempDir()
			archive, digest := makeArchive(t, t.TempDir(), runtimeEntries())
			b, err := UnpackRuntime(archive, digest, cache)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(filepath.Dir(b.Root), runtimeComplete)
			switch damage {
			case "missing python":
				err = os.Remove(b.Python)
			case "non-executable python":
				err = os.Chmod(b.Python, 0o644)
			case "owner cannot execute python":
				err = os.Chmod(b.Python, 0o655)
			case "missing bridge":
				err = os.Remove(filepath.Join(b.Root, "desktop/runtime/bridge.py"))
			case "wrong marker":
				err = os.WriteFile(marker, []byte(strings.Repeat("0", 64)+"\n"), 0o644)
			case "missing marker":
				err = os.Remove(marker)
			}
			if err != nil {
				t.Fatal(err)
			}
			if RuntimeReady(cache, digest) {
				t.Error("damaged generation still ready")
			}
			repaired, err := UnpackRuntime(archive, digest, cache)
			if err != nil {
				t.Fatal(err)
			}
			if !RuntimeReady(cache, digest) || !isFile(repaired.Python) || !isFile(filepath.Join(repaired.Root, "desktop/runtime/bridge.py")) {
				t.Fatal("did not repair the damaged cache")
			}
			if b.Root == repaired.Root || !isFile(filepath.Join(b.Root, "python/lib/numpy/.dylibs/libopenblas.dylib")) {
				t.Fatal("repair replaced an existing generation")
			}
			if err := os.Remove(archive); err != nil {
				t.Fatal(err)
			}
			warm, err := UnpackRuntime(archive, digest, cache)
			if err != nil || *warm != *repaired {
				t.Fatalf("did not reuse the repair: %+v %v", warm, err)
			}
		})
	}
}

func TestRuntimeIncompleteArchiveNeverReady(t *testing.T) {
	cache := t.TempDir()
	archive, digest := makeArchive(t, t.TempDir(), []entry{{name: "desktop/runtime/bridge.py", body: "# no Python"}})
	if _, err := UnpackRuntime(archive, digest, cache); err == nil || RuntimeReady(cache, digest) {
		t.Fatalf("unusable archive published: ready=%v err=%v", RuntimeReady(cache, digest), err)
	}
}

func TestRuntimeRestartAfterInterruptedUnpack(t *testing.T) {
	cache := t.TempDir()
	archive, digest := makeArchive(t, t.TempDir(), runtimeEntries())
	pool := filepath.Dir(UnpackedRuntimeDir(cache, digest))
	stages := []string{filepath.Join(pool, ".unpacking"), filepath.Join(pool, ".unpacking-1234")}
	for _, stage := range stages {
		if err := os.MkdirAll(stage, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, "partial"), []byte("abandoned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b, err := UnpackRuntime(archive, digest, cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append(stages, filepath.Join(filepath.Dir(b.Root), "partial")) {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("partial unpack survived: %s (%v)", name, err)
		}
	}
}
