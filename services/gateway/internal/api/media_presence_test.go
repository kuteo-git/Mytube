package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAFileThatIsThereIsPresent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "abc", "1080p.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := checkMedia(root, "abc/1080p.mp4"); got != mediaPresent {
		t.Fatalf("checkMedia = %v, want mediaPresent", got)
	}
}

func TestAFileDeletedByHandIsMissing(t *testing.T) {
	// The bug this exists for: the catalog goes on saying READY, so the player
	// is handed a /media URL that 404s and the video does not play at all.
	root := t.TempDir()

	if got := checkMedia(root, "abc/1080p.mp4"); got != mediaMissing {
		t.Fatalf("checkMedia = %v, want mediaMissing", got)
	}
}

func TestAWholeFolderDeletedIsStillJustAMissingFile(t *testing.T) {
	// Deleting the video's folder is the natural way to delete a video by hand,
	// and it must not read as the drive having gone away.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "kept"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := checkMedia(root, "removed/1080p.mp4"); got != mediaMissing {
		t.Fatalf("checkMedia = %v, want mediaMissing", got)
	}
}

func TestAnUnmountedDriveIsNotAMissingFile(t *testing.T) {
	// The distinction the whole guard rests on. MEDIA_ROOT is an external SSD,
	// and an unplugged one makes every file in the library missing at once —
	// marking them all evicted would blank the catalog over a loose cable.
	missingRoot := filepath.Join(t.TempDir(), "not-mounted")

	if got := checkMedia(missingRoot, "abc/1080p.mp4"); got != mediaRootUnavailable {
		t.Fatalf("checkMedia = %v, want mediaRootUnavailable", got)
	}
}

func TestNoRootConfiguredCannotProveAnythingMissing(t *testing.T) {
	if got := checkMedia("", "abc/1080p.mp4"); got != mediaRootUnavailable {
		t.Errorf("checkMedia with no root = %v, want mediaRootUnavailable", got)
	}
	if got := checkMedia(t.TempDir(), ""); got != mediaRootUnavailable {
		t.Errorf("checkMedia with no path = %v, want mediaRootUnavailable", got)
	}
}
