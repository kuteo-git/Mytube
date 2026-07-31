package api

import (
	"context"
	"math"
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

// popularRotation is unused; kept for reference but the popular row now uses a
// composite hotness score without rotation windows.
// const popularRotation = 6 * time.Hour

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

// popularMaxAge is how old a video can be and still appear in the popular row.
const popularMaxAge = 30 * 24 * time.Hour

// recencyMultiplier decays from 1.0 (just added) to 0.3 (at popularMaxAge).
// The floor keeps older material from vanishing entirely in a small library.
func recencyMultiplier(addedAt time.Time, now time.Time) float64 {
	age := now.Sub(addedAt)
	if age < 0 {
		age = 0
	}
	if age > popularMaxAge {
		return 0
	}
	fraction := age.Seconds() / popularMaxAge.Seconds()
	return 0.3 + 0.7*(1.0-fraction)
}

// handlePopular returns widely-watched videos ranked by a composite hotness
// score that balances view count, recency and actual watch-through.
//
// "Popular" is not a global list — it is first filtered to this viewer's taste
// by the recsys, then re-ordered. Without the taste filter, a self-curated
// library would mostly surface whatever happens to have the biggest YouTube
// numbers regardless of whether anyone here watches that category.
func (g *Gateway) handlePopular(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := g.userID(r)
	limit := int(intParam(r, "limit", 12))
	now := time.Now()

	ranked, err := g.recsys.GetFeed(ctx, connect.NewRequest(&recsysv1.GetFeedRequest{
		UserId:     userID,
		PageSize:   100,
		ClientHour: int32(now.Hour()),
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
	if len(candidates) == 0 {
		writeJSON(w, http.StatusOK, feedResponse{Videos: []videoDTO{}})
		return
	}

	type scored struct {
		dto   videoDTO
		score float64
	}
	var hot []scored

	for _, dto := range candidates {
		if dto.MediaState != "READY" {
			continue
		}

		addedAt, err := time.Parse(time.RFC3339, dto.AddedAt)
		if err != nil {
			continue
		}

		if now.Sub(addedAt) > popularMaxAge {
			continue
		}

		mult := recencyMultiplier(addedAt, now)
		score := float64(dto.ViewCount) * mult * math.Log2(float64(dto.DurationSeconds)+1)

		if dto.PublishedAt != "" {
			pubAt, err := time.Parse(time.RFC3339, dto.PublishedAt)
			if err == nil {
				days := now.Sub(pubAt).Hours() / 24
				if days > 0 {
					score *= math.Exp(-days / 365)
				}
			}
		}

		hot = append(hot, scored{dto: dto, score: score})
	}

	sort.SliceStable(hot, func(i, j int) bool {
		return hot[i].score > hot[j].score
	})

	if len(hot) > limit {
		hot = hot[:limit]
	}

	out := make([]videoDTO, 0, len(hot))
	for _, s := range hot {
		out = append(out, s.dto)
	}
	writeJSON(w, http.StatusOK, feedResponse{Videos: out})
}
