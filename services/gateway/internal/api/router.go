package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	// downloadsAsked keeps the player's five-second poll from asking ingest to
	// schedule the same download twelve times a minute. See ensureDownload.
	downloadsAsked askedRecently
	// skipLocalTier withholds the file on disk from the stream answer, so that
	// the streaming tiers are what the player has to use. Debugging only.
	//
	// It exists because those tiers are almost impossible to observe otherwise:
	// a download lands in a median of thirteen seconds, and from then on every
	// request answers `local` and plays from the disk. A fault in the streaming
	// path is therefore visible for a few seconds, once, on a video nobody has
	// fetched yet — and testing the same video twice is impossible, because the
	// first attempt fetched it. Half of one morning's evidence was lost that
	// way: a stream request typed by hand scheduled the download that then hid
	// what it was meant to show.
	//
	// Never on by default, and it says so at startup and on every request it
	// changes, because a forgotten flag here looks exactly like a serious bug —
	// a library full of downloaded videos that all insist on streaming.
	skipLocalTier bool
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
	// Read here rather than threaded through the signature: this is a debugging
	// switch with a deliberately short life, and it should be removable without
	// touching how a gateway is built.
	skipLocal := os.Getenv("DEBUG_SKIP_LOCAL_TIER") == "1"
	if skipLocal {
		logger.Warn("DEBUG_SKIP_LOCAL_TIER is set: downloaded files will not be offered, every video streams")
	}

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
		skipLocalTier: skipLocal,
	}
}

func (g *Gateway) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/feed", g.handleFeed)
	mux.HandleFunc("GET /api/feed/explain", g.handleExplainFeed)
	mux.HandleFunc("GET /api/profiles", g.handleProfiles)
	mux.HandleFunc("GET /api/settings/youtube-account", g.handleYouTubeAccount)
	mux.HandleFunc("PUT /api/settings/youtube-account", g.handleYouTubeAccount)
	mux.HandleFunc("DELETE /api/settings/youtube-account", g.handleYouTubeAccount)
	mux.HandleFunc("POST /api/settings/youtube-account/scan", g.handleScanAccounts)
	mux.HandleFunc("GET /api/settings/youtube-account/scan", g.handleAccountScanStatus)
	mux.HandleFunc("POST /api/profiles", g.handleProfiles)
	// What deleting one would take, and then the deletion itself.
	mux.HandleFunc("GET /api/profiles/{id}/usage", g.handleProfileUsage)
	mux.HandleFunc("DELETE /api/profiles/{id}", g.handleDeleteProfile)
	mux.HandleFunc("GET /api/settings/feed-mix/buckets", g.handleFeedMixBuckets)
	mux.HandleFunc("GET /api/settings/ranking", g.handleGetRanking)
	mux.HandleFunc("POST /api/settings/ranking", g.handleSaveRanking)
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
	// The page's own log, so what the browser decided lands beside what the
	// services did — see client_log.go.
	mux.HandleFunc("POST /api/client-log", g.handleClientLog)
	mux.HandleFunc("GET /api/settings/feed-mix", g.handleGetFeedMix)
	mux.HandleFunc("POST /api/settings/feed-mix", g.handleSaveFeedMix)
	mux.HandleFunc("GET /api/translate/config", g.handleGetTranslateConfig)
	mux.HandleFunc("POST /api/translate/config", g.handleSaveTranslateConfig)
	mux.HandleFunc("GET /api/translate/models", g.handleTranslateModels)
	mux.HandleFunc("POST /api/translate/test", g.handleTranslateTest)
	// Where speech is synthesised, in the translate settings' mould.
	//
	// No voices route: OpenAI publishes no endpoint that lists them, so asking
	// would work against one provider and 404 against the one this app now
	// claims to speak. The voice is typed.
	mux.HandleFunc("GET /api/settings/tts", g.handleGetTTSConfig)
	mux.HandleFunc("POST /api/settings/tts", g.handleSaveTTSConfig)
	mux.HandleFunc("POST /api/settings/tts/test", g.handleTestTTS)
	// Where captions can be asked for when this address is being refused. See
	// proxy_config.go: YouTube refuses by public address, so the only thing
	// that helps is asking from a different one. This replaced the transcript
	// settings, which asked which *machine* to ask — measured, and another
	// machine in the same house is the same address.
	mux.HandleFunc("GET /api/settings/proxy", g.handleGetProxyConfig)
	mux.HandleFunc("POST /api/settings/proxy", g.handleSaveProxyConfig)
	mux.HandleFunc("POST /api/settings/proxy/test", g.handleTestProxy)
	mux.HandleFunc("GET /api/videos/{id}/narration-cache", g.handleGetNarrationCache)
	mux.HandleFunc("POST /api/videos/{id}/narration-cache", g.handlePutNarrationCache)
	mux.HandleFunc("POST /api/videos/{id}/narration-cues", g.handlePutNarrationCues)
	mux.HandleFunc("POST /api/videos/{id}/narration-vtt", g.handlePutNarrationVTT)
	mux.HandleFunc("DELETE /api/videos/{id}/narration-vtt", g.handleDeleteNarrationVTT)
	// Clearing the synthesised clips, for when the voice changes. Separate from
	// the translation cache next to it, which the new voice reads unchanged.
	mux.HandleFunc("DELETE /api/videos/{id}/narration-tts", g.handleDeleteTTSCache)
	mux.HandleFunc("POST /api/videos/{id}/download/cancel", g.handleCancelVideoDownload)
	mux.HandleFunc("GET /api/topics/scan-status", g.handleScanStatus)
	mux.HandleFunc("GET /api/history", g.handleHistory)
	mux.HandleFunc("GET /api/pinned", g.handlePinned)
	// Read-only, both of them: Watch later and the playlists are a mirror of the
	// member's YouTube account, refreshed on every account scan. A write route
	// here would offer an edit the next pass reverts, so there is none — the
	// importer reaches catalog directly.
	mux.HandleFunc("GET /api/watch-later", g.handleWatchLater)
	// Broadcasts on air now. Not under /feed: it is a list, not a ranking.
	mux.HandleFunc("GET /api/live", g.handleLiveList)
	mux.HandleFunc("GET /api/playlists", g.handleListPlaylists)
	mux.HandleFunc("GET /api/playlists/{id}", g.handleGetPlaylist)
	mux.HandleFunc("POST /api/videos/{id}/pinned", g.handleSetPinned)
	mux.HandleFunc("POST /api/videos/{id}/not-interested", g.handleNotInterested)
	mux.HandleFunc("GET /api/storage", g.handleStorage)
	// Where the library lives, and whether it is kept. Read together because
	// that is where a person looks for them; they share nothing else.
	mux.HandleFunc("GET /api/settings/storage", g.handleStorageSettings)
	mux.HandleFunc("POST /api/settings/storage", g.handleStorageSettings)
	mux.HandleFunc("GET /api/settings/storage/verify", g.handleVerifyStorageRoot)
	mux.HandleFunc("GET /api/subscriptions", g.handleListSubscriptions)
	mux.HandleFunc("GET /api/collections/top-played", g.handleTopPlayed)
	mux.HandleFunc("GET /api/collections/popular", g.handlePopular)

	// Before the {id} route, or "resolve" would be read as a channel id.
	mux.HandleFunc("GET /api/channels/resolve", g.handleResolveChannel)
	mux.HandleFunc("GET /api/channels/{id}", g.handleGetChannel)
	mux.HandleFunc("GET /api/channels/{id}/videos", g.handleChannelVideos)
	mux.HandleFunc("POST /api/channels/{id}/subscription", g.handleSetSubscription)

	mux.HandleFunc("GET /api/videos/{id}", g.handleGetVideo)
	mux.HandleFunc("GET /api/videos/{id}/up-next", g.handleUpNext)
	mux.HandleFunc("GET /api/videos/{id}/comments", g.handleListComments)
	mux.HandleFunc("POST /api/videos/{id}/comments", g.handleCreateComment)
	mux.HandleFunc("POST /api/videos/{id}/comments/fetch", g.handleFetchComments)
	mux.HandleFunc("POST /api/videos/{id}/progress", g.handleProgress)
	mux.HandleFunc("POST /api/videos/{id}/reaction", g.handleReaction)

	mux.HandleFunc("GET /api/videos/{id}/stream", g.handleStream)
	mux.HandleFunc("GET /api/videos/{id}/instant", g.handleInstantStream)
	mux.HandleFunc("GET /api/videos/{id}/remux", g.handleRemuxStream)
	// The browser combines the two adaptive tracks itself; this only carries the
	// playlists and the byte ranges they name. See ingest's httpapi/hls.go.
	mux.HandleFunc("GET /api/videos/{id}/hls/{name}", g.handleHLS)
	// A broadcast still on air. Nothing here is built from a file: these are
	// YouTube's own HLS playlists, proxied because their URLs are signed to
	// this address and because googlevideo sends no CORS header.
	mux.HandleFunc("GET /api/live/{id}/master.m3u8", g.handleLiveMaster)
	mux.HandleFunc("GET /api/live/{id}/playlist.m3u8", g.handleLivePlaylist)
	mux.HandleFunc("GET /api/live/{id}/segment", g.handleLiveSegment)
	mux.HandleFunc("GET /api/videos/{id}/remux/start", g.handleRemuxStart)

	// Downloads are never requested directly: they are a side effect of asking
	// to play something. These endpoints only report on them.
	mux.HandleFunc("GET /api/ingest/jobs", g.handleListJobs)
	mux.HandleFunc("POST /api/ingest/jobs/{id}/cancel", g.handleCancelJob)
	mux.HandleFunc("POST /api/ingest/jobs/{id}/dismiss", g.handleDismissJob)
	mux.HandleFunc("POST /api/ingest/jobs/{id}/retry", g.handleRetryJob)
	mux.HandleFunc("POST /api/scans/clear", g.handleClearScans)
	mux.HandleFunc("GET /api/scans", g.handleListScans)
	mux.HandleFunc("POST /api/ingest/dismiss-jobs", g.handleDismissJobs)

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
		case connect.CodeAborted:
			// Upstream refused for good — members-only, private, removed. A
			// conflict, not a fault: the request was well formed and the system
			// worked, and the answer is that this video cannot be fetched. It
			// was a 500, which reads as "try again" to every layer above it.
			status = http.StatusConflict
		}
	}

	if status >= 500 {
		g.logger.Error("request failed", "path", r.URL.Path, "error", err)
	}
	if status == http.StatusConflict {
		// A machine-readable answer, because the browser has to decide what to
		// draw from it: no retry button, no comments section, and a sentence
		// naming the reason. Parsing English out of an error message would be
		// the alternative.
		writeJSON(w, status, map[string]string{
			"error":  err.Error(),
			"code":   "video_unavailable",
			"reason": unavailableReason(err.Error()),
		})
		return
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

	// Read per request rather than held in memory: this is a small file, the
	// read is local, and it means a change takes effect on the next request
	// instead of the next restart.
	mix := loadFeedMix(g.feedMixPath())

	// Language preferences, per call rather than stored on the server.
	// ?lang=en&lang=vi means "show only English and Vietnamese videos".
	// Omitted entirely means "show everything".
	languages := r.URL.Query()["lang"]

	// 1. Ranking, from the service that owns ranking.
	ranked, err := g.recsys.GetFeed(ctx, connect.NewRequest(&recsysv1.GetFeedRequest{
		UserId:     userID,
		Category:   r.URL.Query().Get("topic"),
		PageSize:   pageSize,
		PageToken:  r.URL.Query().Get("pageToken"),
		ClientHour: int32(time.Now().Hour()),
		Mix: &recsysv1.FeedMix{
			SubscribedPercent: int32(mix.Subscribed),
			AffinityPercent:   int32(mix.Affinity),
			DiscoveryPercent:  int32(mix.Discovery),
		},
		Languages: languages,
		Tuning:    g.loadRanking().toProto(),
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
		PageToken:      intParam(r, "pageToken", 0),
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
	var nextPageToken string
	if ranked.Msg.GetNextPageToken() > 0 {
		nextPageToken = strconv.FormatInt(int64(ranked.Msg.GetNextPageToken()), 10)
	}
	writeJSON(w, http.StatusOK, feedResponse{Videos: out, NextPageToken: nextPageToken})
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

	// A pasted address is looked up by id, not matched as text.
	//
	// Full-text search runs over titles and channels, which an address never
	// matches — so pasting a link to a video sitting on this disk answered
	// "Nothing here matches", the one case where the library certainly has the
	// answer. It is also not recorded as a search signal: an address says
	// nothing about taste, and recsys would be learning a URL.
	if id, isAddress := videoIDFromSearch(query); isAddress {
		g.searchOne(w, r, id, userID)
		return
	}

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

// searchOne answers the "In your library" half of a pasted address: the one
// video if it is here, and nothing if it is not. Nothing is the ordinary
// outcome — a link is usually to something the library has never seen — and it
// is not an error, so the upstream half is left to report on the address.
func (g *Gateway) searchOne(w http.ResponseWriter, r *http.Request, videoID, userID string) {
	empty := feedResponse{Videos: []videoDTO{}}
	if videoID == "" {
		writeJSON(w, http.StatusOK, empty)
		return
	}

	resp, err := g.catalog.GetVideo(r.Context(), connect.NewRequest(&catalogv1.GetVideoRequest{
		VideoId: videoID,
		UserId:  userID,
	}))
	if err != nil {
		writeJSON(w, http.StatusOK, empty)
		return
	}
	writeJSON(w, http.StatusOK, feedResponse{Videos: []videoDTO{toVideoDTO(resp.Msg.GetVideo())}})
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

// handleLiveList answers with the broadcasts this member's channels have on
// air, newest confirmation first.
//
// No paging, unlike every other list here. The set is a few dozen at the
// outside, and "everything that is on air" is a promise a page token would
// quietly break — the Live chip is meant to be the whole answer, not the first
// screenful of it.
func (g *Gateway) handleLiveList(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.ListLive(r.Context(), connect.NewRequest(&catalogv1.ListLiveRequest{
		UserId: g.userID(r),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]videoDTO, 0, len(resp.Msg.GetVideos()))
	for _, v := range resp.Msg.GetVideos() {
		out = append(out, toVideoDTO(v))
	}
	writeJSON(w, http.StatusOK, feedResponse{Videos: out})
}

func (g *Gateway) handleWatchLater(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.ListWatchLater(r.Context(), connect.NewRequest(&catalogv1.ListWatchLaterRequest{
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
		UserId:  g.userID(r),
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
		KeptCount:          resp.Msg.GetKeptCount(),
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

// handleFetchComments loads YouTube comments for a video and imports them into
// the catalog. It short-circuits when comments already exist, so pressing play
// twice does not fetch twice.
func (g *Gateway) handleFetchComments(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("id")
	ctx := r.Context()

	// Skip if comments are already in the database.
	check, err := g.catalog.ListComments(ctx, connect.NewRequest(&catalogv1.ListCommentsRequest{
		VideoId:  videoID,
		PageSize: 1,
	}))
	if err == nil && check.Msg.GetTotalCount() > 0 {
		writeJSON(w, http.StatusOK, fetchCommentsResponse{Imported: 0, Skipped: true})
		return
	}

	// Fetch from YouTube.
	comments, err := g.ingest.FetchComments(ctx, connect.NewRequest(&ingestv1.FetchCommentsRequest{
		VideoId: videoID,
	}))
	if err != nil {
		// Comments are the one thing on this page nothing depends on: the video
		// plays, the description is there, up-next is there. So a refusal is
		// reported as a refusal and not as a fault.
		//
		// It used to be a 500, which is a claim that this system is broken —
		// and what upstream actually said was "HTTP Error 403", a temporary no
		// to a request nobody needed. The console went red on a page where
		// everything worked.
		//
		// 200 rather than the 409 a dead *video* gets (CLAUDE.md §4): that 409
		// means "permanent, do not retry, name the reason", and this is the
		// opposite — the same video answers on the next press. Recording a
		// temporary refusal as a permanent one is the mistake the charter
		// already learnt once, at the cost of 83 failed jobs.
		g.logger.Warn("fetch comments refused", "video", videoID, "error", err)
		writeJSON(w, http.StatusOK, fetchCommentsResponse{Imported: 0, Unavailable: true})
		return
	}

	// Map to catalog import format.
	in := make([]*catalogv1.ImportComment, len(comments.Msg.GetComments()))
	for i, c := range comments.Msg.GetComments() {
		in[i] = &catalogv1.ImportComment{
			Id:              c.GetId(),
			ParentId:        c.GetParentId(),
			AuthorHandle:    c.GetAuthor(),
			Text:            c.GetText(),
			PublishedAtUnix: c.GetPublishedAtUnix(),
			LikeCount:       c.GetLikeCount(),
			PinnedBy:        c.PinnedBy,
		}
	}

	// Store in catalog.
	resp, err := g.catalog.ImportComments(ctx, connect.NewRequest(&catalogv1.ImportCommentsRequest{
		VideoId:  videoID,
		Comments: in,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, fetchCommentsResponse{
		Imported: resp.Msg.GetImported(),
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
	go g.recordSignal(userID, signalType, channelID, "", 0)

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

// handleExplainFeed exposes the ranker's working over HTTP.
//
// A debugging surface with no UI behind it, and no plans for one. It exists
// because the feed's constants have always been tuned by looking at a page and
// forming an impression, and an impression cannot separate "this weight is
// wrong" from "this weight is right and something further down is throwing it
// away". The second of those went unnoticed for months.
//
// Returns metadata-free output — ids, numbers and slot names — for the same
// reason recsys itself does: hydrating titles here would mean a catalog round
// trip for every video in the library, on an endpoint nobody is waiting on.
func (g *Gateway) handleExplainFeed(w http.ResponseWriter, r *http.Request) {
	mix := loadFeedMix(g.feedMixPath())

	resp, err := g.recsys.ExplainFeed(r.Context(), connect.NewRequest(&recsysv1.ExplainFeedRequest{
		UserId:    g.userID(r),
		Category:  r.URL.Query().Get("topic"),
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

	// ?video=<id> narrows to one video, which is the form the question usually
	// takes: not "explain the feed" but "why is this one not in it".
	wanted := r.URL.Query().Get("video")

	type explanation struct {
		VideoID    string             `json:"videoId"`
		Position   int32              `json:"position"`
		Score      float64            `json:"score"`
		Slot       string             `json:"slot"`
		Reason     string             `json:"reason"`
		Excluded   string             `json:"excluded,omitempty"`
		Components map[string]float64 `json:"components,omitempty"`
	}

	out := make([]explanation, 0, len(resp.Msg.GetVideos()))
	for _, v := range resp.Msg.GetVideos() {
		if wanted != "" && v.GetVideoId() != wanted {
			continue
		}
		out = append(out, explanation{
			VideoID:    v.GetVideoId(),
			Position:   v.GetPosition(),
			Score:      v.GetScore(),
			Slot:       v.GetSlot(),
			Reason:     v.GetReason().String(),
			Excluded:   v.GetExcludedReason(),
			Components: v.GetComponents(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"videos": out})
}

// unavailableReason reads the reason back out of the error the ingest service
// sent.
//
// The reason is a word from a closed set, and it travels at the front of that
// service's message (see domain.Unavailable.Error). Read here rather than added
// to the proto: the alternative is a new field on every RPC that can meet
// upstream, to carry one word that already crosses in the error.
func unavailableReason(message string) string {
	for _, reason := range []string{"members_only", "private", "removed", "unavailable"} {
		if strings.Contains(message, reason) {
			return reason
		}
	}
	return "unavailable"
}

// ---------------------------------------------------------------------------
// Playlists
// ---------------------------------------------------------------------------

func (g *Gateway) handleListPlaylists(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.ListPlaylists(r.Context(), connect.NewRequest(&catalogv1.ListPlaylistsRequest{
		UserId: g.userID(r),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]playlistDTO, 0, len(resp.Msg.GetPlaylists()))
	for _, p := range resp.Msg.GetPlaylists() {
		out = append(out, toPlaylistDTO(p))
	}
	writeJSON(w, http.StatusOK, playlistsResponse{Playlists: out})
}

func (g *Gateway) handleGetPlaylist(w http.ResponseWriter, r *http.Request) {
	resp, err := g.catalog.GetPlaylist(r.Context(), connect.NewRequest(&catalogv1.GetPlaylistRequest{
		PlaylistId: r.PathValue("id"),
		UserId:     g.userID(r),
		PageSize:   intParam(r, "pageSize", 24),
		PageToken:  r.URL.Query().Get("pageToken"),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	videos := make([]videoDTO, 0, len(resp.Msg.GetVideos()))
	for _, v := range resp.Msg.GetVideos() {
		videos = append(videos, toVideoDTO(v))
	}
	writeJSON(w, http.StatusOK, playlistPageResponse{
		Playlist:      toPlaylistDTO(resp.Msg.GetPlaylist()),
		Videos:        videos,
		NextPageToken: resp.Msg.GetNextPageToken(),
	})
}
