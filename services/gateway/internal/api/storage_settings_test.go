package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func verify(t *testing.T, path string) rootCheck {
	t.Helper()
	return inspectRoot(path)
}

// A relative path is refused rather than resolved.
//
// Three services read this setting, and they share a working directory only
// because of how dev.sh happens to launch them. "./media" would mean three
// different directories the day one of them starts another way, and that
// failure looks like a library that has lost half its files.
func TestARelativePathIsRefusedWithSomethingToActOn(t *testing.T) {
	got := verify(t, "./media")
	if got.OK {
		t.Fatal("a relative path was accepted")
	}
	if got.Problem == "" {
		t.Error("refused without saying why")
	}
}

// Not created on demand. A typo becomes an empty library at
// /Volumes/Data2/Youtub, and the first anyone knows of it is that everything
// needs downloading again.
func TestAMissingFolderIsRefusedRatherThanCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-there")

	got := verify(t, path)
	if got.OK {
		t.Fatal("a missing folder was accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("it created the folder")
	}
}

func TestAFileIsNotAFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if verify(t, path).OK {
		t.Error("a file was accepted as the media root")
	}
}

// Writable is proved by writing, and the proof is cleaned up after itself.
func TestAWritableFolderIsAcceptedAndLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()

	got := verify(t, dir)
	if !got.OK {
		t.Fatalf("a writable folder was refused: %s", got.Problem)
	}
	if got.FreeBytes <= 0 {
		t.Error("free space was not reported")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the write probe left %d files behind", len(entries))
	}
}

// The count is the reason to press the button: pointing at a disk used before
// brings the whole library back, and that should be visible before saving
// rather than after restarting.
func TestItCountsWhatIsAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"rCpFNSGB0uo", "pHTfMBgSvIE", "6JSXvUV4Uns"} {
		if err := os.Mkdir(filepath.Join(dir, id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Not a video, and not counted.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := verify(t, dir).VideoCount; got != 3 {
		t.Errorf("videoCount = %d, want 3", got)
	}
}

// Streaming only is read from disk on every request, which is what lets it take
// effect without a restart — unlike the path, which three processes hold from
// start-up.
func TestTheCacheSettingIsReadFreshAndDefaultsToKeeping(t *testing.T) {
	g := &Gateway{configDir: t.TempDir(), logger: discardLogger()}

	if g.cacheDisabled() {
		t.Error("with no setting saved it should keep videos, as it always has")
	}

	if err := g.setCacheDisabled(true); err != nil {
		t.Fatal(err)
	}
	if !g.cacheDisabled() {
		t.Error("the saved setting was not read back")
	}

	// A file nobody can parse is not an instruction to stop keeping videos.
	// Failing towards what the household already had is the safer direction.
	if err := os.WriteFile(filepath.Join(g.configDir, cacheSettingsFile), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if g.cacheDisabled() {
		t.Error("a malformed file silently switched caching off")
	}
}

func TestVerifyRouteAnswersWithoutChangingAnything(t *testing.T) {
	dir := t.TempDir()
	g := &Gateway{configDir: t.TempDir(), logger: discardLogger()}

	rec := httptest.NewRecorder()
	g.handleVerifyStorageRoot(rec,
		httptest.NewRequest(http.MethodGet, "/api/settings/storage/verify?path="+dir, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Error("verifying changed the folder")
	}
}
