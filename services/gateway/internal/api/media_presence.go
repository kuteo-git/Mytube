package api

import (
	"os"
	"path/filepath"
)

// mediaPresence is what the disk says about a file the catalog claims to have.
type mediaPresence int

const (
	// mediaPresent: the file is there. The ordinary answer.
	mediaPresent mediaPresence = iota
	// mediaMissing: the root is mounted and readable, and the file is not in
	// it. Somebody deleted it by hand — the catalog is now wrong and can be
	// corrected.
	mediaMissing
	// mediaRootUnavailable: MEDIA_ROOT itself cannot be read, so the disk is
	// not answering questions about individual files at all.
	mediaRootUnavailable
)

// checkMedia asks the disk whether a video's file is really there.
//
// It exists because the stream endpoint used to take the catalog's word for it:
// media_state READY and a media_path was enough to hand the browser a /media
// URL. Delete the file by hand and the row still says READY, so the player gets
// a URL that 404s and the video simply does not play — with nothing on screen
// to say why.
//
// The root is checked first, and that ordering is the whole safety of this.
// MEDIA_ROOT is an external SSD (CLAUDE.md §2), and an unplugged drive makes
// every file in the library missing at once. Without this distinction, one
// loose cable would mark the entire catalog evicted and blank every media_path
// — losing far more than the deleted file this was written to recover.
//
// An empty path is treated as unavailable rather than missing for the same
// reason: with no root configured, "the file is not there" is not something
// this can know.
func checkMedia(root, mediaPath string) mediaPresence {
	if root == "" || mediaPath == "" {
		return mediaRootUnavailable
	}
	if _, err := os.Stat(root); err != nil {
		// Unplugged, unmounted, or renamed. Not a statement about any one file.
		return mediaRootUnavailable
	}
	if _, err := os.Stat(filepath.Join(root, mediaPath)); err != nil {
		return mediaMissing
	}
	return mediaPresent
}
