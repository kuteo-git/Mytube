// Package mediaroot answers one question for three services: where the library
// lives on disk.
//
// ingest writes media there, catalog deletes it there, and the gateway serves
// it. All three used to read `MEDIA_ROOT` and nothing else, which meant the
// answer could only be changed by editing `scripts/dev.sh`.
//
// It is one package rather than three copies of the same six lines because the
// interesting part is a rule, not a lookup — see Resolve — and a rule written
// out three times is a rule that will be right in two places.
package mediaroot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the settings file, kept beside feed-mix.json and ranking.json in
// the gateway's config directory.
const FileName = "storage.json"

// Source says where the answer came from, so the Storage page can show it. A
// viewer who cannot tell a saved setting from an environment default cannot
// tell a working setting from one that is being ignored.
type Source string

const (
	FromFile Source = "file"
	FromEnv  Source = "environment"
)

// Settings is the file's shape. One field today; a struct because the file will
// outlive that.
type Settings struct {
	MediaRoot string `json:"mediaRoot"`
}

// Resolve returns the media root and where it came from.
//
// **The file wins.** `scripts/dev.sh` always exports MEDIA_ROOT, so if the
// environment took precedence the setting on the Storage page would be saved,
// survive a restart, and change nothing — with nothing anywhere to say why.
// Somebody would watch downloads keep landing on the old disk and have no way
// to tell a setting from a bug. The environment is what a machine that has never
// been set up falls back to, which is every machine before this existed.
//
// Anything unusable in the file — missing, empty, malformed, or relative —
// falls back rather than failing. This runs at startup in three services and a
// bad settings file must not stop the library from serving; `loadProfiles`
// already takes the same view for the same reason.
//
// Relative paths are refused rather than resolved. The three services only share
// a working directory because of how dev.sh happens to launch them, so a
// relative root would mean three different directories the day one of them is
// started another way — and that failure looks like a library that has lost
// half its files.
func Resolve(configDir, fromEnv string) (string, Source) {
	if saved := read(configDir); saved != "" {
		return saved, FromFile
	}
	return fromEnv, FromEnv
}

// Read returns the saved root, or "" when there is not a usable one.
//
// Exported for the gateway, which has to tell the Storage page whether a
// setting exists at all — not merely what the effective root happens to be.
func Read(configDir string) string { return read(configDir) }

func read(configDir string) string {
	raw, err := os.ReadFile(filepath.Join(configDir, FileName))
	if err != nil {
		return ""
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	root := strings.TrimSpace(s.MediaRoot)
	if root == "" || !filepath.IsAbs(root) {
		return ""
	}
	return root
}

// Save writes the root, creating the config directory if it is not there.
func Save(configDir, root string) error {
	raw, err := json.MarshalIndent(Settings{MediaRoot: root}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir, FileName), raw, 0o644)
}
