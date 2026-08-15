package ytdlp

import (
	"fmt"
	"os"
	"sync"

	"github.com/lrstanley/go-ytdlp"
)

// A yt-dlp command, built in one place.
//
// Every call into yt-dlp used to start with a bare `ytdlp.New()`, nine of them
// across this package, which meant that anything to be said to all of them had
// to be said nine times and would be said eight times by whoever came next.
// The two things that need saying — who we are asking as, and with whose
// cookies — are exactly the things a refusal is answered with, and a refusal is
// not a moment to be editing nine call sites.
//
// The split below is the point of the type. What upstream refuses is not evenly
// spread: the requests that fetch *media* and comments are the ones that meet
// 403, while flat listings are cheap, unauthenticated and almost never refused
// — and they are also, by a wide margin, the most numerous. The scanner alone
// walks 93 sources every hour. Attaching an account to that traffic is the
// fastest way to lose the account, so it is deliberately not done.
type purpose int

const (
	// purposeListing is metadata about many videos at once: search, flat
	// playlists, channel info. High volume, low value to upstream, rarely
	// refused. No credentials are attached, on purpose.
	purposeListing purpose = iota
	// purposeMedia is anything that touches a single video's actual content:
	// resolving a stream, downloading it, its subtitles, its comments. These
	// are what get refused, so these are what carry whatever helps.
	purposeMedia
)

// ytdlpConfig is read once from the environment.
//
// Environment rather than a settings screen: this is a credential and a
// diagnostic lever, not a preference, and CLAUDE.md §5 has no room for a
// control whose effect nobody can see from the sofa.
type ytdlpConfig struct {
	// cookiesFile is a Netscape-format cookies.txt, or "" for none.
	//
	// A file rather than --cookies-from-browser: this runs as a background
	// service on a Mac whose browser is being used by a person at the same
	// time, and on macOS reading Chrome's jar goes through the Keychain and
	// fails while Chrome holds it. A thing that breaks when somebody opens a
	// browser is a thing that breaks unobserved.
	cookiesFile string
	// playerClient is passed to yt-dlp as youtube:player_client, or "" to let
	// yt-dlp choose.
	//
	// Unmeasured, deliberately. The refusals this exists for come in waves —
	// the same URL answered 206, then 403, then 206 again within an hour — so
	// there was no way to tell one client from another on the day this was
	// written. It is a lever to pull during the next wave, and it does nothing
	// until it is set.
	playerClient string
}

var (
	configOnce sync.Once
	config     ytdlpConfig
)

func loadConfig() ytdlpConfig {
	configOnce.Do(func() { config = readConfig(os.Getenv) })
	return config
}

// readConfig is the reading itself, kept apart from the caching so it can be
// tested — a sync.Once run by whichever test happened to go first would decide
// the answer for all of them.
func readConfig(getenv func(string) string) ytdlpConfig {
	cfg := ytdlpConfig{
		cookiesFile:  getenv("YTDLP_COOKIES"),
		playerClient: getenv("YTDLP_PLAYER_CLIENT"),
	}
	// A cookies file that is not there is worse than none: yt-dlp fails the
	// request outright rather than carrying on without it, so a typo in an env
	// var would take down every download in the library instead of doing
	// without the cookies it never had.
	if cfg.cookiesFile != "" {
		if _, err := os.Stat(cfg.cookiesFile); err != nil {
			cfg.cookiesFile = ""
		}
	}
	return cfg
}

// newCommand starts a yt-dlp command carrying whatever this kind of request
// should carry.
func newCommand(p purpose) *ytdlp.Command {
	cmd := ytdlp.New()
	if p != purposeMedia {
		return cmd
	}
	cfg := loadConfig()
	if cfg.cookiesFile != "" {
		cmd = cmd.Cookies(cfg.cookiesFile)
	}
	if cfg.playerClient != "" {
		cmd = cmd.ExtractorArgs(fmt.Sprintf("youtube:player_client=%s", cfg.playerClient))
	}
	return cmd
}
