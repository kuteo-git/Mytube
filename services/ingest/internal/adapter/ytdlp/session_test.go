package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withConfig installs a configuration for one test. loadConfig is a sync.Once,
// so the environment cannot be used here — the first test to run would decide
// for all of them.
func withConfig(t *testing.T, c ytdlpConfig) {
	t.Helper()
	configOnce.Do(func() {})
	previous := config
	config = c
	t.Cleanup(func() { config = previous })
}

func args(t *testing.T, p purpose) string {
	t.Helper()
	// The builder holds no reader for its own flags, so it is asked to build the
	// command it would run and that is read instead.
	return strings.Join(newCommand(p).BuildCommand(context.Background()).Args, " ")
}

// Credentials go to the requests that get refused, and to nothing else.
//
// The scanner walks 93 sources every hour and the backfill 200 videos a pass;
// that traffic is the most bot-like thing this system does, and it is also the
// least often refused. Attaching an account to it would be spending the one
// thing that cannot be replaced on the one job that does not need it.
func TestListingsCarryNoCredentials(t *testing.T) {
	dir := t.TempDir()
	jar := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(jar, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withConfig(t, ytdlpConfig{cookiesFile: jar, playerClient: "web_safari"})

	if got := args(t, purposeListing); strings.Contains(got, "cookies") {
		t.Errorf("a listing carried cookies: %s", got)
	}
	if got := args(t, purposeListing); strings.Contains(got, "player_client") {
		t.Errorf("a listing carried a player client: %s", got)
	}

	media := args(t, purposeMedia)
	if !strings.Contains(media, jar) {
		t.Errorf("media request did not carry the cookies file: %s", media)
	}
	if !strings.Contains(media, "youtube:player_client=web_safari") {
		t.Errorf("media request did not carry the player client: %s", media)
	}
}

// Nothing is attached until something is configured, so the default behaviour
// is exactly what it was before this existed.
func TestNothingIsAttachedByDefault(t *testing.T) {
	withConfig(t, ytdlpConfig{})

	for _, p := range []purpose{purposeListing, purposeMedia} {
		if got := args(t, p); strings.Contains(got, "cookies") || strings.Contains(got, "player_client") {
			t.Errorf("unconfigured command carried something: %s", got)
		}
	}
}

// A cookies file that is not on disk is dropped rather than passed on.
//
// yt-dlp fails the whole request when told to read a jar it cannot find, so a
// typo in an env var would take down every download in the library rather than
// quietly do without the cookies it never had.
func TestAMissingCookiesFileIsIgnored(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.txt")
	cfg := readConfig(func(name string) string {
		if name == "YTDLP_COOKIES" {
			return absent
		}
		return ""
	})
	if cfg.cookiesFile != "" {
		t.Errorf("cookiesFile = %q, want empty", cfg.cookiesFile)
	}
}
