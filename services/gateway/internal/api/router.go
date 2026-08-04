package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
	recsysv1 "github.com/lucnguyen/local-youtube/gen/go/recsys/v1"
	"github.com/lucnguyen/local-youtube/gen/go/recsys/v1/recsysv1connect"
)

type Gateway struct {
	catalog catalogv1connect.CatalogServiceClient
	recsys  recsysv1connect.RecommendationServiceClient
	ingest  ingestv1connect.IngestServiceClient
	logger  *slog.Logger
	// devUserID is used until the identity service exists. Phase 1 ships two
	// seeded accounts and no signup screen, so a header is enough.
	devUserID string
	// One expansion at a time. Concurrent passes would double the request rate
	// against YouTube for material the first pass is already fetching.
	expanding atomic.Bool
	// ingestBaseURL and streamClient exist for the muxed stream, which is raw
	// bytes over plain HTTP rather than an RPC. The client has no timeout: the
	// response lasts as long as the video plays.
	ingestBaseURL string
	streamClient  *http.Client
	// mediaRoot is where narration translations are kept, beside the media they
	// belong to. It can be an external SSD that is not mounted (CLAUDE.md §8.1),
	// so every read of it treats absence as an empty cache rather than an error.
	mediaRoot string
	// configDir holds settings edited from the app, as opposed to the ones
	// edited by hand in .env.local. Separate from mediaRoot: that lives on an
	// external drive which may not be mounted (CLAUDE.md §8.1), and a setting
	// nobody can save because a disk is unplugged would be its own bug.
	configDir string
}

func NewGateway(
	catalog catalogv1connect.CatalogServiceClient,
	recsys recsysv1connect.RecommendationServiceClient,
	ingest ingestv1connect.IngestServiceClient,
	logger *slog.Logger,
	devUserID string,
	ingestBaseURL string,
	mediaRoot string,
	configDir string,
) *Gateway {
	return &Gateway{
		catalog:       catalog,
		recsys:        recsys,
		ingest:        ingest,
		logger:        logger,
		devUserID:     devUserID,
		ingestBaseURL: ingestBaseURL,
		streamClient:  &http.Client{},
		mediaRoot:     mediaRoot,
		configDir:     configDir,
	}
}

func (g *Gateway) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/feed", g.handleFeed)
	mux.HandleFunc("GET /api/search", g.handleSearch)
	mux.HandleFunc("GET /api/suggest", g.handleSuggest)
	mux.HandleFunc("GET /api/discover", g.handleDiscover)
	mux.HandleFunc("POST /api/videos/external", g.handleEnsureExternal)
	mux.HandleFunc("GET /api/topics", g.handleTopics)
	mux.HandleFunc("POST /api/topics/refresh", g.handleRefreshTopics)
	mux.HandleFunc("POST /api/topics/backfill", g.handleBackfillTopics)
	mux.HandleFunc("GET /api/topics/backfill", g.handleBackfillStatus)
	mux.HandleFunc("POST /api/tts", g.handleTTS)
	mux.HandleFunc("POST /api/translate/batch", g.handleTranslateBatch)
	mux.HandleFunc("GET /api/translate/config", g.handleGetTranslateConfig)
	mux.HandleFunc("POST /api/translate/config", g.handleSaveTranslateConfig)
	mux.HandleFunc("GET /api/translate/models", g.handleTranslateModels)
	mux.HandleFunc("POST /api/translate/test", g.handleTranslateTest)
	mux.HandleFunc("GET /api/tts/voices", g.handleTTSVoices)
	mux.HandleFunc("GET /api/videos/{id}/narration-cache", g.handleGetNarrationCache)
	mux.HandleFunc("POST /api/videos/{id}/narration-cache", g.handlePutNarrationCache)
	mux.HandleFunc("POST /api/videos/{id}/narration-cues", g.handlePutNarrationCues)
	mux.HandleFunc("POST /api/videos/{id}/narration-vtt", g.handlePutNarrationVTT)
	mux.HandleFunc("DELETE /api/videos/{id}/narration-vtt", g.handleDeleteNarrationVTT)
	mux.HandleFunc("POST /api/videos/{id}/download/cancel", g.handleCancelVideoDownload)
	mux.HandleFunc("GET /api/topics/scan-status", g.handleScanStatus)
	mux.HandleFunc("GET /api/history", g.handleHistory)
	mux.HandleFunc("GET /api/pinned", g.handlePinned)
	mux.HandleFunc("POST /api/videos/{id}/pinned", g.handleSetPinned)
	mux.HandleFunc("POST /api/videos/{id}/not-interested", g.handleNotInterested)
	mux.HandleFunc("GET /api/storage", g.handleStorage)
	mux.HandleFunc("GET /api/subscriptions", g.handleListSubscriptions)
	mux.HandleFunc("GET /api/collections/top-played", g.handleTopPlayed)
	mux.HandleFunc("GET /api/collections/popular", g.handlePopular)

	mux.HandleFunc("GET /api/channels/{id}", g.handleGetChannel)
	mux.HandleFunc("GET /api/channels/{id}/videos", g.handleChannelVideos)
	mux.HandleFunc("POST /api/channels/{id}/subscription", g.handleSetSubscription)

	mux.HandleFunc("GET /api/videos/{id}", g.handleGetVideo)
	mux.HandleFunc("GET /api/videos/{id}/up-next", g.handleUpNext)
	mux.HandleFunc("GET /api/videos/{id}/comments", g.handleListComments)
	mux.HandleFunc("POST /api/videos/{id}/comments", g.handleCreateComment)
	mux.HandleFunc("POST /api/videos/{id}/progress", g.handleProgress)
	mux.HandleFunc("POST /api/videos/{id}/reaction", g.handleReaction)

	mux.HandleFunc("GET /api/videos/{id}/stream", g.handleStream)
	mux.HandleFunc("GET /api/videos/{id}/remux", g.handleRemuxStream)

	// Downloads are never requested directly: they are a side effect of asking
	// to play something. These endpoints only report on them.
	mux.HandleFunc("GET /api/ingest/jobs", g.handleListJobs)
	mux.HandleFunc("POST /api/ingest/jobs/{id}/cancel", g.handleCancelJob)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	return mux
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (g *Gateway) userID(r *http.Request) string {
	if id := r.Header.Get("X-User-Id"); id != "" {
		return id
	}
	return g.devUserID
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeErr maps Connect codes back onto HTTP status codes so the browser sees
// a 404 as a 404 rather than an opaque 500.
func (g *Gateway) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError

	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		switch connectErr.Code() {
		case connect.CodeNotFound:
			status = http.StatusNotFound
		case connect.CodeInvalidArgument:
			status = http.StatusBadRequest
		case connect.CodeUnauthenticated:
			status = http.StatusUnauthorized
		case connect.CodePermissionDenied:
			status = http.StatusForbidden
		}
	}

	if status >= 500 {
		g.logger.Error("request failed", "path", r.URL.Path, "error", err)
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func intParam(r *http.Request, key string, fallback int32) int32 {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return int32(v)
}

// ---------------------------------------------------------------------------
// Feed — the composition that justifies having a gateway at all
// ---------------------------------------------------------------------------

// expandThreshold is how few remaining videos count as "running low". Two pages
// of headroom is enough to refill before a scroller reaches the end, and the
// refill itself is metadata-only so it costs nothing on disk.
const expandThreshold = 48

func (g *Gateway) handleFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := g.userID(r)
	pageSize := intParam(r, "pageSize", 24)

	// 1. Ranking, from the service that owns ranking.
	ranked, err := g.recsys.GetFeed(ctx, connect.NewRequest(&recsysv1.GetFeedRequest{
		UserId:     userID,
		Category:   r.URL.Query().Get("topic"),
		PageSize:   pageSize,
		PageToken:  r.URL.Query().Get("pageToken"),
		ClientHour: int32(time.Now().Hour()),
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

	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, feedResponse{Videos: []videoDTO{}})
		return
	}

	// 2. Hydration, from the service that owns the data.
	videos, err := g.catalog.BatchGetVideos(ctx, connect.NewRequest(&catalogv1.BatchGetVideosRequest{
		VideoIds: ids,
		UserId:   userID,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]videoDTO, 0, len(videos.Msg.GetVideos()))
	for _, v := range videos.Msg.GetVideos() {
		dto := toVideoDTO(v)
		dto.Reason = reasonByID[v.GetId()]
		out = append(out, dto)
	}

	// 3. Impressions are recorded after the response is composed, so a slow or
	// failing write never delays or breaks the grid.
	go g.recordImpressions(userID, ids)

	// Running low: go and find more. Fire-and-forget, because a viewer must
	// never wait on a network round trip to YouTube to get the page they asked
	// for — the new material lands in the next page instead.
	if ranked.Msg.GetRemainingCount() < expandThreshold {
		go g.expandLibrary(r.URL.Query().Get("topic"), ids)
	}

	writeJSON(w, http.StatusOK, feedResponse{
		Videos:        out,
		NextPageToken: ranked.Msg.GetNextPageToken(),
	})
}

func (g *Gateway) recordImpressions(userID string, ids []string) {
	ctx, cancel := contextWithTimeout(5 * time.Second)
	defer cancel()

	if _, err := g.recsys.RecordImpressions(ctx, connect.NewRequest(&recsysv1.RecordImpressionsRequest{
		UserId:   userID,
		VideoIds: ids,
	})); err != nil {
		g.logger.Warn("record impressions", "error", err)
	}
}

func (g *Gateway) expandLibrary(topic string, seedVideoIDs []string) {
	if !g.expanding.CompareAndSwap(false, true) {
		return
	}
	defer g.expanding.Store(false)

	// Every genuinely new video now costs a full metadata fetch (~2s) to learn
	// its YouTube category (CLAUDE.md §7), on top of the listing calls — a
	// 40-video batch can take a couple of minutes on its own.
	ctx, cancel := contextWithTimeout(5 * time.Minute)
	defer cancel()

	// A handful of seeds is plenty; every one is a separate round trip.
	if len(seedVideoIDs) > 3 {
		seedVideoIDs = seedVideoIDs[:3]
	}

	resp, err := g.ingest.ExpandLibrary(ctx, connect.NewRequest(&ingestv1.ExpandLibraryRequest{
		Topic:        topic,
		SeedVideoIds: seedVideoIDs,
	}))
	if err != nil {
		g.logger.Warn("expand library", "topic", topic, "error", err)
		return
	}
	g.logger.Info("library expanded", "topic", topic, "added", resp.Msg.GetVideosAdded())
}

func (g *Gateway) handleUpNext(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := g.userID(r)

	ranked, err := g.recsys.GetUpNext(ctx, connect.NewRequest(&recsysv1.GetUpNextRequest{
		UserId:         userID,
		CurrentVideoId: r.PathValue("id"),
		ChannelFilter:  r.URL.Query().Get("channel"),
		PageSize:       intParam(r, "pageSize", 20),
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

	out := make([]videoDTO, 0, len(videos.Msg.GetVideos()))
	for _, v := range videos.Msg.GetVideos() {
		dto := toVideoDTO(v)
		dto.Reason = reasonByID[v.GetId()]
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, feedResponse{Videos: out})
}

// ---------------------------------------------------------------------------
// Straight pass-throughs
// ---------------------------------------------------------------------------

func (g *Gateway) handleGetVideo(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.GetVideo(r.Context(), connect.NewRequest(&catalogv1.GetVideoRequest{
		VideoId: r.PathValue("id"),
		UserId:  g.userID(r),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	dto := toVideoDTO(resp.Msg.GetVideo())
	g.attachMachineTranslation(&dto)
	writeJSON(w, http.StatusOK, dto)
}

func (g *Gateway) handleSearch(w http.ResponseWriter, r *http.Request) {
	userID := g.userID(r)
	query := r.URL.Query().Get("q")

	resp, err := g.catalog.SearchVideos(r.Context(), connect.NewRequest(&catalogv1.SearchVideosRequest{
		Query:     query,
		UserId:    userID,
		PageSize:  intParam(r, "pageSize", 24),
		PageToken: r.URL.Query().Get("pageToken"),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	// A search is itself a behaviour signal; recsys asked for it explicitly.
	if query != "" {
		go g.recordSignal(userID, recsysv1.SignalType_SIGNAL_TYPE_SEARCH, "", query, 0)
	}

	out := make([]videoDTO, 0, len(resp.Msg.GetVideos()))
	for _, v := range resp.Msg.GetVideos() {
		out = append(out, toVideoDTO(v))
	}
	writeJSON(w, http.StatusOK, feedResponse{
		Videos:        out,
		NextPageToken: resp.Msg.GetNextPageToken(),
	})
}

// handleSuggest backs the search box type-ahead. Suggestions come from the
// local library only: proposing terms the library cannot answer would send
// every one of them to an empty result page.
func (g *Gateway) handleSuggest(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.Suggest(r.Context(), connect.NewRequest(&catalogv1.SuggestRequest{
		Query: r.URL.Query().Get("q"),
		Limit: intParam(r, "limit", 10),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]suggestionDTO, 0, len(resp.Msg.GetSuggestions()))
	for _, s := range resp.Msg.GetSuggestions() {
		out = append(out, suggestionDTO{
			Text:       s.GetText(),
			Kind:       trimEnumPrefix(s.GetKind().String(), "SUGGESTION_KIND_"),
			VideoCount: s.GetVideoCount(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

func (g *Gateway) handleTopics(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.ListTopics(r.Context(), connect.NewRequest(&catalogv1.ListTopicsRequest{
		MinVideoCount: intParam(r, "minVideoCount", 1),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]topicDTO, 0, len(resp.Msg.GetTopics()))
	for _, t := range resp.Msg.GetTopics() {
		out = append(out, topicDTO{Name: t.GetName(), VideoCount: t.GetVideoCount()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"topics": out})
}

func (g *Gateway) handleHistory(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.ListHistory(r.Context(), connect.NewRequest(&catalogv1.ListHistoryRequest{
		UserId:    g.userID(r),
		PageSize:  intParam(r, "pageSize", 24),
		PageToken: r.URL.Query().Get("pageToken"),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]videoDTO, 0, len(resp.Msg.GetVideos()))
	for _, v := range resp.Msg.GetVideos() {
		out = append(out, toVideoDTO(v))
	}
	writeJSON(w, http.StatusOK, feedResponse{Videos: out, NextPageToken: resp.Msg.GetNextPageToken()})
}

func (g *Gateway) handlePinned(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.ListPinnedVideos(r.Context(), connect.NewRequest(&catalogv1.ListPinnedVideosRequest{
		UserId:    g.userID(r),
		PageSize:  intParam(r, "pageSize", 24),
		PageToken: r.URL.Query().Get("pageToken"),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]videoDTO, 0, len(resp.Msg.GetVideos()))
	for _, v := range resp.Msg.GetVideos() {
		out = append(out, toVideoDTO(v))
	}
	writeJSON(w, http.StatusOK, feedResponse{Videos: out, NextPageToken: resp.Msg.GetNextPageToken()})
}

func (g *Gateway) handleSetPinned(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pinned bool `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	if _, err := g.catalog.SetPinned(r.Context(), connect.NewRequest(&catalogv1.SetPinnedRequest{
		VideoId: r.PathValue("id"),
		Pinned:  body.Pinned,
	})); err != nil {
		g.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleNotInterested(w http.ResponseWriter, r *http.Request) {
	userID := g.userID(r)
	videoID := r.PathValue("id")
	go g.recordSignal(userID, recsysv1.SignalType_SIGNAL_TYPE_DISLIKE, videoID, "", 0)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleStorage(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.GetStorageUsage(r.Context(), connect.NewRequest(&catalogv1.GetStorageUsageRequest{}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	candidates := make([]videoDTO, 0, len(resp.Msg.GetEvictionCandidates()))
	for _, v := range resp.Msg.GetEvictionCandidates() {
		candidates = append(candidates, toVideoDTO(v))
	}
	writeJSON(w, http.StatusOK, storageResponse{
		UsedBytes:          resp.Msg.GetUsedBytes(),
		BudgetBytes:        resp.Msg.GetBudgetBytes(),
		DiskFreeBytes:      resp.Msg.GetDiskFreeBytes(),
		VideoCount:         resp.Msg.GetVideoCount(),
		EvictedCount:       resp.Msg.GetEvictedCount(),
		EvictionCandidates: candidates,
	})
}

func (g *Gateway) handleListComments(w http.ResponseWriter, r *http.Request) {
	sort := catalogv1.CommentSort_COMMENT_SORT_TOP
	if r.URL.Query().Get("sort") == "newest" {
		sort = catalogv1.CommentSort_COMMENT_SORT_NEWEST
	}

	resp, err := g.catalog.ListComments(r.Context(), connect.NewRequest(&catalogv1.ListCommentsRequest{
		VideoId:   r.PathValue("id"),
		Sort:      sort,
		PageSize:  intParam(r, "pageSize", 20),
		PageToken: r.URL.Query().Get("pageToken"),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]commentDTO, 0, len(resp.Msg.GetComments()))
	for _, c := range resp.Msg.GetComments() {
		out = append(out, toCommentDTO(c))
	}
	writeJSON(w, http.StatusOK, commentsResponse{
		Comments:      out,
		TotalCount:    resp.Msg.GetTotalCount(),
		NextPageToken: resp.Msg.GetNextPageToken(),
	})
}

// ---------------------------------------------------------------------------
// Channels & subscriptions
// ---------------------------------------------------------------------------

func (g *Gateway) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.GetChannel(r.Context(), connect.NewRequest(&catalogv1.GetChannelRequest{
		ChannelId: r.PathValue("id"),
		UserId:    g.userID(r),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channel":    toChannelDTO(resp.Msg.GetChannel()),
		"videoCount": resp.Msg.GetVideoCount(),
	})
}

// recordChannelAvatar stores a channel picture discovered while listing it.
//
// Detached from the request because the listing must not wait on it, and given
// its own context for the same reason: the request's is cancelled the moment
// the response is written.
func (g *Gateway) recordChannelAvatar(channelID, name, handle, avatarURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := g.catalog.UpsertChannel(ctx, connect.NewRequest(&catalogv1.UpsertChannelRequest{
		Channel: &catalogv1.Channel{
			Id:         channelID,
			Name:       name,
			Handle:     handle,
			AvatarPath: avatarURL,
		},
	})); err != nil {
		g.logger.Warn("recording channel avatar", "channel", channelID, "error", err)
	}
}

func (g *Gateway) handleChannelVideos(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	channelID := r.PathValue("id")

	// Ask YouTube, not the catalog. A scan only ever brings in the newest few
	// dozen uploads, so serving this page from the catalog would cap a channel
	// at that number for reasons the viewer cannot see.
	channel, err := g.catalog.GetChannel(ctx, connect.NewRequest(&catalogv1.GetChannelRequest{
		ChannelId: channelID,
		UserId:    g.userID(r),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	// The id, not the handle.
	//
	// This used to prefer the handle on the belief that YouTube resolved it
	// more reliably. The opposite is true: a UC… id *is* an InnerTube browseId
	// and needs no resolving, while a handle needs a lookup that fails often
	// enough to matter — measured on @tinhte, where resolution failed and the
	// listing fell back to a flat playlist. That fallback works, but flat
	// listings carry neither upload dates nor view counts, so the whole channel
	// page rendered with no date and zero views on every card while other
	// channels showed both.
	lookup := channelID
	if lookup == "" {
		lookup = channel.Msg.GetChannel().GetHandle()
	}

	resp, err := g.ingest.ListChannelUploads(ctx, connect.NewRequest(&ingestv1.ListChannelUploadsRequest{
		Channel:   lookup,
		PageToken: r.URL.Query().Get("pageToken"),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	// Record the picture if this channel had none.
	//
	// It arrives in the listing already, so this costs nothing beyond the write
	// — and artwork is otherwise only collected for the channels named in
	// topics.yaml, which left 225 of 288 with no picture anywhere in the
	// interface. Not fatal if it fails: a missing avatar falls back to the
	// lettered circle, which is what it was there for.
	if avatar := resp.Msg.GetAvatarUrl(); avatar != "" &&
		channel.Msg.GetChannel().GetAvatarPath() == "" {
		// The name goes along because catalog requires one — it has no way to
		// tell a fragment meant to add a picture from a fragment that has lost
		// everything else.
		go g.recordChannelAvatar(
			channelID,
			channel.Msg.GetChannel().GetName(),
			channel.Msg.GetChannel().GetHandle(),
			avatar,
		)
	}

	out := make([]externalVideoDTO, 0, len(resp.Msg.GetVideos()))
	for _, v := range resp.Msg.GetVideos() {
		dto := externalVideoDTO{
			ID:              v.GetId(),
			Title:           v.GetTitle(),
			ChannelName:     v.GetChannelName(),
			DurationSeconds: v.GetDurationSeconds(),
			ViewCount:       v.GetViewCount(),
			ThumbnailURL:    v.GetThumbnailUrl(),
			SourceURL:       v.GetSourceUrl(),
			InLibrary:       v.GetInLibrary(),
		}
		// Sent as an empty string when unknown, so the client omits the line
		// rather than printing an invented date.
		if ts := v.GetPublishedAt(); ts != nil && ts.AsTime().Unix() > 0 {
			dto.PublishedAt = ts.AsTime().UTC().Format("2006-01-02T15:04:05Z")
		}
		out = append(out, dto)
	}

	sorts := make([]sortOptionDTO, 0, len(resp.Msg.GetSortOptions()))
	for _, o := range resp.Msg.GetSortOptions() {
		sorts = append(sorts, sortOptionDTO{Label: o.GetLabel(), Token: o.GetToken()})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"videos":        out,
		"sortOptions":   sorts,
		"nextPageToken": resp.Msg.GetNextPageToken(),
	})
}

type setSubscriptionRequest struct {
	Subscribed bool `json:"subscribed"`
}

func (g *Gateway) handleSetSubscription(w http.ResponseWriter, r *http.Request) {
	var body setSubscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	userID := g.userID(r)
	channelID := r.PathValue("id")

	if _, err := g.catalog.SetSubscription(r.Context(), connect.NewRequest(&catalogv1.SetSubscriptionRequest{
		UserId:     userID,
		ChannelId:  channelID,
		Subscribed: body.Subscribed,
	})); err != nil {
		g.writeErr(w, r, err)
		return
	}

	// Subscribing is a behaviour signal too — recsys already weighs it.
	signalType := recsysv1.SignalType_SIGNAL_TYPE_SUBSCRIBE
	if !body.Subscribed {
		signalType = recsysv1.SignalType_SIGNAL_TYPE_UNSUBSCRIBE
	}
	go g.recordSignal(userID, signalType, "", "", 0)

	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.ListSubscriptions(r.Context(), connect.NewRequest(&catalogv1.ListSubscriptionsRequest{
		UserId: g.userID(r),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]channelDTO, 0, len(resp.Msg.GetChannels()))
	for _, c := range resp.Msg.GetChannels() {
		out = append(out, toChannelDTO(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}
