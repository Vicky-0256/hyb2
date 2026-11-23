package core

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The staged runtime ships as one archive (desktop/scripts/pack-runtime.py)
// because MyGo leaves hidden files out of resources, and the runtime needs
// some (numpy/.dylibs on macOS, numpy/.libs on Windows). It is unpacked
// once into the cache, keyed by the archive's SHA-256.
const (
	RuntimeArchive       = "native.tar.gz"
	RuntimeArchiveSHA256 = "native.tar.gz.sha256"
	runtimeComplete      = ".complete"
	runtimeStage         = ".unpacking"
	runtimePublishWait   = 30 * time.Second
)

// PackedRuntime reports the archive under resources and its expected
// SHA-256 (from the side file), or ok=false when the app has none.
func PackedRuntime(resources string) (archive, digest string, ok bool, err error) {
	archive = filepath.Join(resources, RuntimeArchive)
	if !isFile(archive) {
		return "", "", false, nil
	}
	raw, err := os.ReadFile(filepath.Join(resources, RuntimeArchiveSHA256))
	if err != nil {
		return "", "", false, fmt.Errorf("the bundled runtime has no %s: %w", RuntimeArchiveSHA256, err)
	}
	if fields := strings.Fields(string(raw)); len(fields) > 0 {
		digest = strings.ToLower(fields[0])
	}
	if _, err := hex.DecodeString(digest); err != nil || len(digest) != sha256.Size*2 {
		return "", "", false, fmt.Errorf("%s is not a SHA-256", RuntimeArchiveSHA256)
	}
	return archive, digest, true, nil
}

// UnpackedRuntimeDir returns the usable generation, or the next generation's
// path if this digest is not yet ready. The runtime is its native/ subdirectory.
func UnpackedRuntimeDir(cache, digest string) string {
	dir, _, _ := runtimeGeneration(cache, digest)
	return dir
}

// RuntimeReady checks the completion marker and the files needed to start Python.
func RuntimeReady(cache, digest string) bool {
	_, b, _ := runtimeGeneration(cache, digest)
	return b != nil
}

// Pick a fresh slot for repairs: even a generation with a damaged marker
// may have live users. Older launchers remove runtime-* on startup, so keep
// all new generations outside that namespace and leave the old ones alone.
func runtimeGeneration(cache, digest string) (string, *Bridge, error) {
	if !filepath.IsAbs(cache) {
		return "", nil, errors.New("the bundled runtime needs an absolute cache directory")
	}
	if _, err := hex.DecodeString(digest); len(digest) != sha256.Size*2 || err != nil {
		return "", nil, errors.New("invalid runtime SHA-256")
	}
	pool := filepath.Join(cache, "packed-"+digest[:16])
	entries, err := os.ReadDir(pool)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	next := 0
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil || n < 0 {
			continue // the lock and unpublished staging tree are not generations
		}
		next = max(next, n+1)
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(pool, e.Name())
		marker := filepath.Join(dir, runtimeComplete)
		if info, err := os.Stat(marker); err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(digest)+1) {
			continue
		}
		if data, err := os.ReadFile(marker); err == nil && string(data) == digest+"\n" {
			if b, err := requiredBridge(dir); err == nil {
				return dir, b, nil
			}
		}
	}
	return filepath.Join(pool, strconv.Itoa(next)), nil, nil
}

func requiredBridge(dir string) (*Bridge, error) {
	b, err := BundledBridge(dir)
	if err == nil && b == nil {
		err = errors.New("the bundled runtime archive has no desktop/runtime/bridge.py")
	}
	return b, err
}

// UnpackRuntime reuses an immutable, published generation. On a cold launch
// only the owner of the per-digest OS lock extracts; a waiting launcher checks
// again after taking the lock. Process exit releases the lock, not its inode.
func UnpackRuntime(archive, digest, cache string) (*Bridge, error) {
	dir, b, err := runtimeGeneration(cache, digest)
	if err != nil || b != nil {
		return b, err
	}
	pool := filepath.Dir(dir)
	if err := os.MkdirAll(pool, 0o700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(runtimePublishWait)
	for {
		lock, err := tryFileLock(filepath.Join(pool, "publish.lock"))
		if errors.Is(err, errLockBusy) {
			if remaining := time.Until(deadline); remaining > 0 {
				time.Sleep(min(50*time.Millisecond, remaining))
				continue
			}
			// A publisher may have paused after the rename but before unlock.
			if _, b, err := runtimeGeneration(cache, digest); err != nil || b != nil {
				return b, err
			}
			return nil, fmt.Errorf("timed out after %s waiting for another HYB2 Desktop instance to prepare the runtime; let it finish, or close it if it is unresponsive, then retry", runtimePublishWait)
		}
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		break
	}
	dir, b, err = runtimeGeneration(cache, digest)
	if err != nil || b != nil {
		return b, err
	}
	// Only the lock holder can reclaim interrupted work. Cleanup is optional:
	// a read-only or open leftover must not prevent a fresh private unpack.
	stages, _ := os.ReadDir(pool)
	for _, stage := range stages {
		if stage.IsDir() && (stage.Name() == runtimeStage || strings.HasPrefix(stage.Name(), runtimeStage+"-")) {
			_ = os.RemoveAll(filepath.Join(pool, stage.Name()))
		}
	}
	tmp, err := os.MkdirTemp(pool, runtimeStage+"-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := extractVerified(archive, digest, filepath.Join(tmp, "native")); err != nil {
		return nil, err
	}
	if _, err := requiredBridge(tmp); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, runtimeComplete), []byte(digest+"\n"), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return nil, err
	}
	return requiredBridge(dir)
}

// extractVerified unpacks a .tar.gz into dest while hashing it, and fails
// (leaving dest to be discarded) when the digest differs or an entry would
// land outside dest.
func extractVerified(archive, digest, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	hashed := io.TeeReader(f, hash)
	gz, err := gzip.NewReader(hashed)
	if err != nil {
		return fmt.Errorf("the bundled runtime archive is unreadable: %w", err)
	}
	// one gzip stream: bytes after it (zero padding) are hashed, not parsed
	gz.Multistream(false)
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	inside := func(path string) bool {
		rel, err := filepath.Rel(root, path)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	tr := tar.NewReader(gz)
	var links []*tar.Header
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("the bundled runtime archive is damaged: %w", err)
		}
		name := filepath.FromSlash(h.Name)
		if absoluteEntry(h.Name) {
			return fmt.Errorf("unsafe path in the runtime archive: %q", h.Name)
		}
		target := filepath.Join(root, name)
		if !inside(target) && target != root {
			return fmt.Errorf("unsafe path in the runtime archive: %q", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if absoluteEntry(h.Linkname) || !inside(filepath.Join(filepath.Dir(target), filepath.FromSlash(h.Linkname))) {
				return fmt.Errorf("unsafe link in the runtime archive: %q -> %q", h.Name, h.Linkname)
			}
			links = append(links, h) // after the files, so Windows can copy targets
		default:
			return fmt.Errorf("unsupported entry in the runtime archive: %q", h.Name)
		}
	}
	// Drain to the end through the hash: the tar padding and the gzip
	// trailer after the end marker, then anything after the gzip stream.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return fmt.Errorf("the bundled runtime archive is damaged: %w", err)
	}
	if _, err := io.Copy(io.Discard, hashed); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != digest {
		return fmt.Errorf("the bundled runtime archive is corrupt (SHA-256 %s, expected %s); reinstall HYB2 Desktop", got, digest)
	}
	// Each link target was checked as text; a link made through an earlier
	// link would resolve elsewhere, so none may sit under another, and each
	// must still resolve inside dest once made.
	made := map[string]bool{}
	for _, h := range links {
		name := path.Clean(h.Name)
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if made[dir] {
				return fmt.Errorf("unsafe link in the runtime archive: %q is under the link %q", h.Name, dir)
			}
		}
		made[name] = true
	}
	for _, h := range links {
		target := filepath.Join(root, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := makeLink(h.Linkname, target); err != nil {
			return err
		}
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	for _, h := range links {
		resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(h.Name)))
		if err != nil {
			continue // dangling: it points nowhere, so not outside either
		}
		if rel, err := filepath.Rel(realRoot, resolved); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe link in the runtime archive: %q resolves outside it", h.Name)
		}
	}
	return nil
}

// absoluteEntry reports archive names and link targets that are absolute
// on any platform: "/x" (not absolute for Windows' filepath), "C:x",
// backslashes, or UNC paths.
func absoluteEntry(name string) bool {
	return name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") ||
		strings.Contains(name, ":") || filepath.IsAbs(name)
}

// makeLink creates a symlink, or copies the target file where symlinks are
// not allowed (Windows without developer mode).
func makeLink(linkname, target string) error {
	if err := os.Symlink(linkname, target); err == nil {
		return nil
	}
	src := filepath.Join(filepath.Dir(target), filepath.FromSlash(linkname))
	info, err := os.Stat(src)
	if err != nil || info.IsDir() {
		return fmt.Errorf("cannot create %s -> %s", target, linkname)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
