package mediaroot

import (
	"os"
	"path/filepath"
	"testing"
)

// Which of two answers wins, and why it is this way round.
//
// `scripts/dev.sh` always exports MEDIA_ROOT. If the environment took
// precedence, the setting on the Storage page would be saved, survive a
// restart, and do nothing — with nothing anywhere to say why. Somebody would
// change it, watch downloads keep landing on the old disk, and have no way to
// tell the setting from a bug.
//
// So the file wins when it exists, and the environment is what a machine that
// has never been set up falls back to.
func TestTheSavedPathBeatsTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"mediaRoot":"/Volumes/Elsewhere"}`)

	got, source := Resolve(dir, "/Volumes/Data2/Youtube")

	if got != "/Volumes/Elsewhere" {
		t.Errorf("root = %q, want the saved one", got)
	}
	if source != FromFile {
		t.Errorf("source = %v, want FromFile", source)
	}
}

// A machine nobody has configured behaves exactly as it did before this
// existed: dev.sh's export, or the built-in default under it.
func TestTheEnvironmentIsTheFallback(t *testing.T) {
	got, source := Resolve(t.TempDir(), "/Volumes/Data2/Youtube")

	if got != "/Volumes/Data2/Youtube" {
		t.Errorf("root = %q, want the environment's", got)
	}
	if source != FromEnv {
		t.Errorf("source = %v, want FromEnv", source)
	}
}

// A file that is empty, blank, or not JSON is not an answer.
//
// Falling back rather than failing: this runs at startup in three services, and
// a malformed settings file must not stop the library from serving. The same
// rule `loadProfiles` already follows for the same reason.
func TestAFileThatSaysNothingUsefulFallsBack(t *testing.T) {
	for _, body := range []string{``, `{}`, `{"mediaRoot":""}`, `{"mediaRoot":"   "}`, `{ not json`} {
		dir := t.TempDir()
		write(t, dir, body)

		got, source := Resolve(dir, "/fallback")
		if got != "/fallback" || source != FromEnv {
			t.Errorf("Resolve with %q = (%q, %v), want the fallback", body, got, source)
		}
	}
}

// A relative path in the file is refused rather than resolved.
//
// Three services read this, and they are only guaranteed to share a working
// directory by how `dev.sh` happens to launch them. A relative root would mean
// three different directories the day one of them is started another way, and
// the failure would look like a library that lost half its files.
func TestARelativePathInTheFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `{"mediaRoot":"./media"}`)

	got, source := Resolve(dir, "/fallback")
	if got != "/fallback" || source != FromEnv {
		t.Errorf("a relative saved path was used: (%q, %v)", got, source)
	}
}

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
