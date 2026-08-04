package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// feedMix is how much of the home feed each source of new material gets.
//
// Three numbers, because three is what a person can hold in their head while
// deciding: the channels I follow, more of what I already watch, and something
// I have not seen before. The ranker has nine reasons and they overlap; asking
// somebody to tune those would be asking them to tune a vocabulary rather than
// a feed.
//
// Held by the gateway, like the translator's settings, and sent down with the
// request — so recsys keeps no configuration of its own and there is no file
// for the two of them to disagree about.
//
// One setting for the household rather than one per viewer. There are two
// seeded accounts and no sign-up screen (CLAUDE.md §7), so per-person taste is
// a privacy nobody has asked for, bought with a table, a migration and an RPC.
// If that changes, only the line that loads this has to.
type feedMix struct {
	Subscribed int `json:"subscribedPercent"`
	Affinity   int `json:"affinityPercent"`
	Discovery  int `json:"discoveryPercent"`
}

// defaultFeedMix mirrors usecase.DefaultFeedMix, and must keep mirroring it.
//
// Duplicated rather than imported: the gateway does not link the ranker, and a
// REST layer reaching into another service's use cases for a constant is the
// dependency this architecture exists to prevent. The number is the same one
// the old fixed quota produced, so installing this changes nothing until a
// slider moves.
var defaultFeedMix = feedMix{Subscribed: 25, Affinity: 60, Discovery: 15}

func (g *Gateway) feedMixPath() string {
	return filepath.Join(g.configDir, "feed-mix.json")
}

// loadFeedMix reads what was saved, falling back to the defaults.
//
// Any unreadable or nonsensical file gives the defaults rather than an error:
// this decides the order of a grid, and there is no version of "the feed will
// not load" that is better than "the feed looks like it always did".
func loadFeedMix(path string) feedMix {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return defaultFeedMix
	}
	var saved feedMix
	if err := json.Unmarshal(raw, &saved); err != nil {
		return defaultFeedMix
	}
	if !saved.valid() {
		return defaultFeedMix
	}
	return saved
}

// valid rejects only what cannot be acted on: negatives, and all-zero.
//
// All three at zero is the one combination with no sensible reading — it asks
// for a feed of nothing new at all, which is an empty page rather than a
// preference. Anything else is honoured by ratio, so a household that sets
// 3/2/1 gets the same feed as one that sets 50/33/17.
func (m feedMix) valid() bool {
	if m.Subscribed < 0 || m.Affinity < 0 || m.Discovery < 0 {
		return false
	}
	return m.Subscribed+m.Affinity+m.Discovery > 0
}

func saveFeedMix(path string, m feedMix) error {
	blob, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return withFileLock(path, func() error { return writeFileAtomic(path, blob) })
}

func (g *Gateway) handleGetFeedMix(w http.ResponseWriter, _ *http.Request) {
	mix := loadFeedMix(g.feedMixPath())
	writeJSON(w, http.StatusOK, map[string]any{
		"subscribedPercent": mix.Subscribed,
		"affinityPercent":   mix.Affinity,
		"discoveryPercent":  mix.Discovery,
		// Sent rather than duplicated in the browser, so "Reset to default" and
		// the note explaining the default cannot drift from what the server
		// actually falls back to.
		"defaults": map[string]any{
			"subscribedPercent": defaultFeedMix.Subscribed,
			"affinityPercent":   defaultFeedMix.Affinity,
			"discoveryPercent":  defaultFeedMix.Discovery,
		},
	})
}

func (g *Gateway) handleSaveFeedMix(w http.ResponseWriter, r *http.Request) {
	var submitted feedMix
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&submitted); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if !submitted.valid() {
		http.Error(w, "a feed of nothing is not a setting", http.StatusBadRequest)
		return
	}
	if err := saveFeedMix(g.feedMixPath(), submitted); err != nil {
		g.logger.Warn("feed mix save", "error", err)
		http.Error(w, "could not save", http.StatusServiceUnavailable)
		return
	}
	g.logger.Info("feed mix saved",
		"subscribed", submitted.Subscribed,
		"affinity", submitted.Affinity,
		"discovery", submitted.Discovery)
	writeJSON(w, http.StatusOK, map[string]any{
		"subscribedPercent": submitted.Subscribed,
		"affinityPercent":   submitted.Affinity,
		"discoveryPercent":  submitted.Discovery,
	})
}
