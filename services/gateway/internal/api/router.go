package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
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
}

func NewGateway(
	catalog catalogv1connect.CatalogServiceClient,
	recsys recsysv1connect.RecommendationServiceClient,
	ingest ingestv1connect.IngestServiceClient,
	logger *slog.Logger,
	devUserID string,
) *Gateway {
	return &Gateway{
		catalog:   catalog,
		recsys:    recsys,
		ingest:    ingest,
		logger:    logger,
		devUserID: devUserID,
	}
}

func (g *Gateway) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/feed", g.handleFeed)
	mux.HandleFunc("GET /api/search", g.handleSearch)
	mux.HandleFunc("GET /api/suggest", g.handleSuggest)
	mux.HandleFunc("GET /api/topics", g.handleTopics)
	mux.HandleFunc("POST /api/topics/refresh", g.handleRefreshTopics)
	mux.HandleFunc("GET /api/topics/scan-status", g.handleScanStatus)
	mux.HandleFunc("GET /api/history", g.handleHistory)
	mux.HandleFunc("GET /api/storage", g.handleStorage)

	mux.HandleFunc("GET /api/videos/{id}", g.handleGetVideo)
	mux.HandleFunc("GET /api/videos/{id}/up-next", g.handleUpNext)
	mux.HandleFunc("GET /api/videos/{id}/comments", g.handleListComments)
	mux.HandleFunc("POST /api/videos/{id}/comments", g.handleCreateComment)
	mux.HandleFunc("POST /api/videos/{id}/progress", g.handleProgress)
	mux.HandleFunc("POST /api/videos/{id}/reaction", g.handleReaction)

	mux.HandleFunc("GET /api/videos/{id}/stream", g.handleStream)

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
	writeJSON(w, http.StatusOK, toVideoDTO(resp.Msg.GetVideo()))
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
