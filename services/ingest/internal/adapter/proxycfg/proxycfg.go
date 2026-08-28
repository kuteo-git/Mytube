// Package proxycfg reads the outbound proxy the household configured, and says
// which traffic is allowed through it.
//
// # Why a package and not a variable
//
// YouTube refuses by **public address**. Measured 2026-08-28, four videos, one
// minute, one machine: asking for captions directly was `IpBlocked` 4 of 4, and
// asking through a rotating residential proxy succeeded 4 of 4. Nothing about
// the shape of a request changes that, so the only lever is the address a
// request leaves by.
//
// # Why it is read per call
//
// The same reason remotetranscript's Config is, and it is written down there
// too: a caption fetch can be started by the retry sweep, on a timer, with
// nobody waiting on a request. There is nothing to carry a setting on. A value
// read once at start-up would mean saving the settings form did nothing until
// somebody restarted the service — the trap `internal/mediaroot` exists to
// document.
//
// The file is small, the reads are not in a loop, and the alternative has
// already been a bug in this codebase.
//
// # Why the gateway writes it and this reads it
//
// The gateway owns the settings screen. This is a file under CONFIG_DIR, not
// another service's database, so §3's immovable rule is untouched — and it is
// exactly the arrangement transcript-config.json already had.
package proxycfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is the stored shape, and it matches the gateway's proxyConfig field
// for field.
type Config struct {
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`

	ForCaptions bool `json:"forCaptions"`
	ForListings bool `json:"forListings"`
	ForMedia    bool `json:"forMedia"`
	ForComments bool `json:"forComments"`
}

// Use names a kind of outbound traffic.
//
// Separate switches because a residential proxy is metered by the gigabyte and
// these differ by three orders of magnitude: a caption file is tens of
// kilobytes, a 1080p video is hundreds of megabytes. One switch covering both
// would let somebody turn on captions and lose a month's bandwidth by morning.
type Use int

const (
	// Captions is the timedtext endpoint, through the local helper. Tens of
	// kilobytes, and the one this was built for.
	Captions Use = iota
	// Listings is the scanner, search, channel and playlist walks. High volume
	// — 93 sources an hour — but small, and rarely refused.
	Listings
	// Media is resolving and downloading a video, and proxying its segments.
	// Hundreds of megabytes apiece. Off by default and warned about in the UI.
	Media
	// Comments is one video's comment thread.
	Comments
)

// Reader answers the question at the moment it is asked.
type Reader struct {
	path string
}

func New(configDir string) *Reader {
	return &Reader{path: filepath.Join(configDir, "proxy.json")}
}

// URLFor is the proxy to use for this kind of traffic, or "" for none.
//
// One function rather than a Config handed out for callers to inspect: "is the
// proxy on, and is it on for *this*" is two conditions that must be asked
// together, and a caller that asks only the first is a bandwidth bill.
func (r *Reader) URLFor(use Use) string {
	cfg := r.load()
	if !cfg.Enabled {
		return ""
	}
	on := false
	switch use {
	case Captions:
		on = cfg.ForCaptions
	case Listings:
		on = cfg.ForListings
	case Media:
		on = cfg.ForMedia
	case Comments:
		on = cfg.ForComments
	}
	if !on {
		return ""
	}
	return strings.TrimSpace(cfg.URL)
}

func (r *Reader) load() Config {
	raw, err := os.ReadFile(r.path)
	if err != nil {
		// No file is the ordinary state: every install starts without a proxy.
		return Config{}
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		// A corrupt file means no proxy rather than no library.
		return Config{}
	}
	return cfg
}
