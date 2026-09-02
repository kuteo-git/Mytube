package api

import (
	"net/http"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	recsysv1 "github.com/lucnguyen/local-youtube/gen/go/recsys/v1"
)

// defaultMissedWindowHours is how far back "did I miss anything" looks.
//
// Duplicated from recsys rather than imported, like defaultFeedMix and for the
// same reason: the gateway does not link the ranker, and a REST layer reaching
// into another service's use cases for a constant is the dependency this
// architecture exists to prevent. Zero would also work — recsys reads an absent
// window as its own default — but a number written here is a number somebody
// changing this can see.
const defaultMissedWindowHours = 24

// maxMissedPageSize is catalog's own batch limit, which the hydration step here
// runs straight into. Duplicated for the reason every constant crossing this
// boundary is: the gateway does not link catalog's use cases.
const maxMissedPageSize = 200

// handleMissed answers "what did the channels I follow post that I have not
// watched".
//
// The same two steps as handleFeed — rank in recsys, hydrate in catalog — and
// deliberately the same response shape, so every client that can already read a
// feed page can read this one with no new parsing.
//
// **No impressions are recorded and no library expansion is triggered.** Both
// belong to the feed: an impression means "this was offered as part of the mix",
// and recording one here would teach the ranker that a video has been shown
// when the whole point of this list is that it has not been dealt with yet.
// Expansion is likewise the feed running low on material, which this cannot be
// — an empty answer here means nothing was missed, not that the library is
// short.
func (g *Gateway) handleMissed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := g.userID(r)

	// The window: the config file, overridden per request by ?hours=.
	//
	// Read per request rather than held in memory, like the feed mix — this is a
	// small local file, and it means changing the day takes effect on the next
	// request instead of the next restart.
	hours := int32(defaultMissedWindowHours)
	if configured := g.loadRanking().MissedWindowHours; configured != nil && *configured > 0 {
		hours = int32(*configured)
	}
	if asked := intParam(r, "hours", 0); asked > 0 {
		hours = asked
	}

	// Capped at what catalog will hydrate in one batch (200), because the ids
	// this asks for go straight into BatchGetVideos. Without it a caller asking
	// for 500 gets "at most 200 ids per batch" — an error about an internal
	// limit, for a request that was merely greedy. Measured, with pageSize=500.
	pageSize := intParam(r, "pageSize", 24)
	if pageSize > maxMissedPageSize {
		pageSize = maxMissedPageSize
	}

	ranked, err := g.recsys.GetMissed(ctx, connect.NewRequest(&recsysv1.GetMissedRequest{
		UserId:      userID,
		WithinHours: hours,
		PageSize:    pageSize,
		PageToken:   r.URL.Query().Get("pageToken"),
		Languages:   r.URL.Query()["lang"],
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	ids := make([]string, 0, len(ranked.Msg.GetVideos()))
	reasonByID := make(map[string]string, len(ranked.Msg.GetVideos()))
	for _, v := range ranked.Msg.GetVideos() {
		ids = append(ids, v.GetVideoId())
		reasonByID[v.GetVideoId()] = trimEnumPrefix(v.GetReason().String(), "RECOMMENDATION_REASON_")
	}

	// Nothing missed. An empty list, not an error and not an absent field: the
	// client draws its chip from whether this is empty, so "nothing" has to be
	// something it can read.
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, feedResponse{Videos: []videoDTO{}})
		return
	}

	videos, err := g.catalog.BatchGetVideos(ctx, connect.NewRequest(&catalogv1.BatchGetVideosRequest{
		VideoIds: ids,
		UserId:   userID,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	// Ordered by the ranking, not by whatever order the batch came back in.
	// BatchGetVideos answers a set, and a list whose whole claim is "most worth
	// your time first" cannot take its order from a database.
	byID := make(map[string]*catalogv1.Video, len(videos.Msg.GetVideos()))
	for _, v := range videos.Msg.GetVideos() {
		byID[v.GetId()] = v
	}
	out := make([]videoDTO, 0, len(ids))
	for _, id := range ids {
		v, ok := byID[id]
		if !ok {
			continue
		}
		dto := toVideoDTO(v)
		dto.Reason = reasonByID[id]
		out = append(out, dto)
	}

	writeJSON(w, http.StatusOK, feedResponse{
		Videos:        out,
		NextPageToken: ranked.Msg.GetNextPageToken(),
	})
}
