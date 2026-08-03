package api

import (
	"os"
	"path/filepath"
	"sync"
)

// fileLocks serialises writers per path.
//
// Not paranoia about parallel users — one viewer is enough. The translation
// pass writes its cache and its subtitle file after every batch and does not
// wait for either, so two saves for the same video overlap routinely. Both the
// cache and the config are read-modify-write over a whole file, and unserialised
// that loses whichever writer read first: the pass believes those lines are
// saved and never translates them again.
var fileLocks sync.Map

func lockFor(path string) *sync.Mutex {
	actual, _ := fileLocks.LoadOrStore(path, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// withFileLock runs fn with exclusive access to path.
//
// Held across the read as well as the write, because the read is where a lost
// update begins.
func withFileLock(path string, fn func() error) error {
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

// writeFileAtomic replaces path's contents, leaving no half-written file behind.
//
// The temp file gets a unique name from os.CreateTemp rather than the fixed
// "<path>.tmp" this used to use. That fixed name was shared by every writer of
// the file: the first rename moved it away and the second had nothing left to
// rename, which reached the browser as a 503 and, for the cache, meant a batch
// of translations went nowhere.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Created in the destination directory so the rename stays on one
	// filesystem, where it is atomic.
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		// Harmless once the rename has succeeded; the point is the paths that
		// return early below, which would otherwise litter the video's folder.
		_ = os.Remove(name)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// 0644 to match what the media beside it is served with; callers holding a
	// credential tighten this themselves.
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// writeFileAtomicMode is writeFileAtomic for a file that should not be readable
// by anyone else — the translation config, which holds an API key.
func writeFileAtomicMode(path string, data []byte, mode os.FileMode) error {
	if err := writeFileAtomic(path, data); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
