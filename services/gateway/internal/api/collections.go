package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	recsysv1 "github.com/lucnguyen/local-youtube/gen/go/recsys/v1"
)

// Collections are ordered lists of videos meant to be played straight through,
// as opposed to the feed, which is a grid to browse. Both hydrate ranked ids
// from catalog, the same composition the feed does.

// popularRotation is how long the "Popular with you" row keeps the same
// selection. Rotating on a fixed clock rather than per request means reloading
// the page does not reshuffle the row under the reader, while coming back later
// in the day shows something different.
const popularRotation = 6 * time.Hour

// handleTopPlayed returns the videos this user has spent the most time on, in
// order, ready to be played as a queue.
func (g *Gateway) handleTopPlayed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := g.userID(r)

	ranked, err := g.recsys.GetMostWatched(ctx, connect.NewRequest(&recsysv1.GetMostWatchedRequest{
		UserId: userID,
		Limit:  intParam(r, "limit", 50),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	ids := make([]string, 0, len(ranked.Msg.GetVideos()))
	for _, v := range ranked.Msg.GetVideos() {
		ids = append(ids, v.GetVideoId())
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, feedResponse{Videos: []videoDTO{}})
		return
	}

	writeJSON(w, http.StatusOK, feedResponse{Videos: g.hydrate(ctx, ids, userID)})
}

// hydrate turns ranked ids into full videos, preserving the ranking order.
// BatchGetVideos is documented to return them in the order asked for, which is
// what keeps a queue a queue.
func (g *Gateway) hydrate(ctx context.Context, ids []string, userID string) []videoDTO {
	videos, err := g.catalog.BatchGetVideos(ctx, connect.NewRequest(&catalogv1.BatchGetVideosRequest{
		VideoIds: ids,
		UserId:   userID,
	}))
	if err != nil {
		g.logger.Warn("hydrate collection", "error", err)
		return []videoDTO{}
	}

	out := make([]videoDTO, 0, len(videos.Msg.GetVideos()))
	for _, v := range videos.Msg.GetVideos() {
		out = append(out, toVideoDTO(v))
	}
	return out
}

// handlePopular is the "Popular with you" row: widely-watched videos, but only
// from the topics and channels this viewer actually watches.
//
// Filtering by taste is what separates it from a global trending list, which on
// a self-curated library would mostly surface whatever happens to have the
// biggest numbers regardless of whether anyone here cares about it.
//
// The selection rotates on a fixed clock rather than per request: reloading a
// page should not reshuffle the row being read, but coming back later should
// show something else.
func (g *Gateway) handlePopular(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := g.userID(r)
	limit := int(intParam(r, "limit", 12))

	// The feed already knows this viewer's taste — it is ranked by it. Taking a
	// broad slice of it and re-sorting by view count turns "what suits you"
	// into "what suits you and lots of people watched".
	ranked, err := g.recsys.GetFeed(ctx, connect.NewRequest(&recsysv1.GetFeedRequest{
		UserId:     userID,
		PageSize:   100,
		ClientHour: int32(time.Now().Hour()),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	ids := make([]string, 0, len(ranked.Msg.GetVideos()))
	for _, v := range ranked.Msg.GetVideos() {
		ids = append(ids, v.GetVideoId())
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, feedResponse{Videos: []videoDTO{}})
		return
	}

	candidates := g.hydrate(ctx, ids, userID)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].ViewCount > candidates[j].ViewCount
	})

	// Rotate by taking a different window of the ranking each period, rather
	// than shuffling: the row stays ordered by popularity, it just starts
	// somewhere else.
	if len(candidates) > limit {
		period := time.Now().Unix() / int64(popularRotation.Seconds())
		windows := (len(candidates) + limit - 1) / limit
		start := int(period%int64(windows)) * limit
		if start >= len(candidates) {
			start = 0
		}
		end := start + limit
		if end > len(candidates) {
			end = len(candidates)
		}
		candidates = candidates[start:end]
	}

	writeJSON(w, http.StatusOK, feedResponse{Videos: candidates})
}
