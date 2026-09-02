package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"connectrpc.com/connect"

	recsysv1 "github.com/lucnguyen/local-youtube/gen/go/recsys/v1"
)

// rankingConfig is the handful of ranking constants the household can move.
//
// Every field is a pointer, and the reason is the same one that put optional on
// the wire: zero is a real answer for several of these. A session blend of zero
// says "ignore what I am watching right now"; a missing session blend says
// "whatever the ranker thinks". A struct of bare values could not tell a file
// that sets everything to zero from a file that sets nothing.
//
// Held here rather than in recsys, like the feed mix and for the same reason —
// recsys keeps no configuration of its own (CLAUDE.md §3). The practical effect
// is that this file can be edited by hand and takes effect on the next request.
type rankingConfig struct {
	SessionBlend           *float64 `json:"sessionBlend,omitempty"`
	FreshSubscribedPercent *int     `json:"freshSubscribedPercent,omitempty"`
	FreshnessWindowHours   *int     `json:"freshnessWindowHours,omitempty"`
	MaxPublishedAgeDays    *int     `json:"maxPublishedAgeDays,omitempty"`
	RecencyHalfLifeDays    *float64 `json:"recencyHalfLifeDays,omitempty"`
	SoftmaxTemperature     *float64 `json:"softmaxTemperature,omitempty"`
	SamplePoolSize         *int     `json:"samplePoolSize,omitempty"`
	// How far back /api/feed/missed looks, in hours.
	//
	// Here rather than in recsys because that service keeps no configuration of
	// its own (CLAUDE.md §3), and here rather than in the app because a
	// household that decides a day is too short should not need a release to
	// say so. It is deliberately *not* in toProto: this is not a ranking
	// constant recsys applies to the feed, it is one number the missed route
	// passes down with its request.
	MissedWindowHours *int `json:"missedWindowHours,omitempty"`
}

func (g *Gateway) rankingPath() string {
	return filepath.Join(g.configDir, "ranking.json")
}

// loadRanking reads what was saved, and treats anything it cannot read as an
// empty config — which recsys answers with its built-in constants.
//
// Not an error, deliberately. This decides the order of a grid, and a file
// somebody has been editing by hand is exactly the file most likely to have a
// trailing comma in it. "The feed ranks the way it always did" beats every
// version of "the feed will not load".
func (g *Gateway) loadRanking() rankingConfig {
	raw, err := os.ReadFile(g.rankingPath())
	if err != nil || len(raw) == 0 {
		return rankingConfig{}
	}
	var saved rankingConfig
	if err := json.Unmarshal(raw, &saved); err != nil {
		g.logger.Warn("ranking config unreadable, using built-in values", "error", err)
		return rankingConfig{}
	}
	return saved
}

// toProto hands the pointers straight through. Clamping happens in recsys, not
// here: the bounds belong beside the constants they protect, and a value written
// into the file by hand has to be clamped anyway.
func (c rankingConfig) toProto() *recsysv1.RankingTuning {
	return &recsysv1.RankingTuning{
		SessionBlend:           c.SessionBlend,
		FreshSubscribedPercent: int32Ptr(c.FreshSubscribedPercent),
		FreshnessWindowHours:   int32Ptr(c.FreshnessWindowHours),
		MaxPublishedAgeDays:    int32Ptr(c.MaxPublishedAgeDays),
		RecencyHalfLifeDays:    c.RecencyHalfLifeDays,
		SoftmaxTemperature:     c.SoftmaxTemperature,
		SamplePoolSize:         int32Ptr(c.SamplePoolSize),
	}
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	out := int32(*v)
	return &out
}

func (g *Gateway) handleGetRanking(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": g.loadRanking(),
	})
}

func (g *Gateway) handleSaveRanking(w http.ResponseWriter, r *http.Request) {
	var submitted rankingConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&submitted); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	blob, err := json.MarshalIndent(submitted, "", "  ")
	if err != nil {
		http.Error(w, "could not save", http.StatusServiceUnavailable)
		return
	}
	path := g.rankingPath()
	if err := withFileLock(path, func() error { return writeFileAtomic(path, blob) }); err != nil {
		g.logger.Warn("ranking config save", "error", err)
		http.Error(w, "could not save", http.StatusServiceUnavailable)
		return
	}
	g.logger.Info("ranking config saved")
	writeJSON(w, http.StatusOK, map[string]any{"settings": submitted})
}

// handleFeedMixBuckets reports how many videos each share currently has to
// choose from.
//
// The failure it exists to make visible: the sliders divide a page, but a share
// can only be filled from a bucket that has videos in it. On this library the
// affinity slot held twenty-five videos against three and a half thousand
// subscribed ones, so a 60% affinity share spent half of every page scraping
// that bucket's floor — and there was no way to see it from the settings screen,
// or from the feed, or from anywhere else.
//
// Separate from GET /api/settings/feed-mix, which is a 147-byte file read. This
// one is a full ranking pass, and folding it in would make the sliders wait on
// it and make a ranking failure look like a missing setting.
func (g *Gateway) handleFeedMixBuckets(w http.ResponseWriter, r *http.Request) {
	mix := loadFeedMix(g.feedMixPath())

	resp, err := g.recsys.ExplainFeed(r.Context(), connect.NewRequest(&recsysv1.ExplainFeedRequest{
		UserId:    g.userID(r),
		Languages: r.URL.Query()["lang"],
		Mix: &recsysv1.FeedMix{
			SubscribedPercent: int32(mix.Subscribed),
			AffinityPercent:   int32(mix.Affinity),
			DiscoveryPercent:  int32(mix.Discovery),
		},
		Tuning: g.loadRanking().toProto(),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	counts := map[string]int{}
	for _, v := range resp.Msg.GetVideos() {
		// Only videos that are actually in the running. An excluded video is not
		// material the share can draw on, and counting it would answer a question
		// nobody asked with a number that looks like the one they did ask.
		if v.GetExcludedReason() != "" {
			continue
		}
		counts[v.GetSlot()]++
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": counts})
}
