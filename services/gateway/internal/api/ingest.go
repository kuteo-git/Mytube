package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
)

// Ingest-facing endpoints.
//
// Nothing here lets a user ask for a download directly. Content is discovered
// by the topic scanner, and a copy is fetched as a side effect of pressing
// play. These endpoints only resolve playback and report on the queue.

type jobDTO struct {
	ID              string  `json:"id"`
	SourceURL       string  `json:"sourceUrl"`
	VideoID         string  `json:"videoId"`
	Title           string  `json:"title"`
	State           string  `json:"state"`
	Progress        float32 `json:"progress"`
	DownloadedBytes int64   `json:"downloadedBytes"`
	TotalBytes      int64   `json:"totalBytes"`
	ErrorMessage    string  `json:"errorMessage,omitempty"`
	CreatedAt       string  `json:"createdAt"`
}

// The resolution the muxed stream is assembled at. Fixed rather than
// negotiated: ingest has one configured height, and telling the client a
// different number would put a wrong label on the picture.
//
// 720 since ingest's LIVE_HEIGHT became 720 — this was left at 1080 in that
// change and spent a release labelling the picture with a number nothing was
// producing. It is a copy of a value that lives in another service, which is
// why it went wrong; it stays a copy because the alternative is the gateway
// asking ingest what height it uses on every stream request.
const remuxHeight = 720

// sourceDTO is one way of playing a video.
type sourceDTO struct {
	URL      string `json:"url"`
	Height   int32  `json:"height,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	// False only for the muxed-on-the-fly stream, which has no index. The
	// player uses this to decide whether the seek bar can be trusted.
	Seekable  bool   `json:"seekable"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// streamDTO lists every way a video can be played right now, rather than
// picking one.
//
// The gateway used to choose, and could not keep choosing well: the best source
// depends on how far the viewer has watched and how much has buffered, and only
// the player knows either. So the gateway states what exists and the player
// climbs — from an instant low-resolution start to the downloaded file — which
// is also why one request can answer for the whole session instead of being
// re-asked at every transition.
type streamDTO struct {
	// Progressive upstream, playable and seekable immediately but capped at
	// 360p by what YouTube still publishes muxed.
	//
	// **Never set since 2026-08-18.** The rendition it names stopped serving
	// anything past the head of the file; see the note where it used to be
	// filled in. The field stays so that a client from before this change reads
	// the same JSON it always did — one tier fewer — rather than a shape it has
	// never seen.
	// True when the household has asked for streaming only. The player stops
	// polling for a download and stops drawing its progress.
	CacheDisabled bool       `json:"cacheDisabled,omitempty"`
	Instant       *sourceDTO `json:"instant,omitempty"`
	// The same two adaptive tracks the mux combines, described as HLS so the
	// browser combines them instead.
	//
	// It is offered beside `remux`, not instead of it, because what can play it
	// differs by device and nothing here should have to guess: iPhone and Safari
	// play HLS natively, Chrome does not and needs a library. The player picks.
	//
	// Why it matters more than a second option usually would — measured
	// 2026-08-20 on the household's iPhone (iOS 18.7), the same video minutes
	// apart: the muxed stream reached `play()` and never produced a picture,
	// while this played, reported a real duration (641.8s) and seeked twice.
	// The mux is unindexed and Safari will not have it, and iOS has no
	// MediaSource to put anything else behind it. So on a phone this is not a
	// better tier, it is the only one that works before the file lands.
	HLS *sourceDTO `json:"hls,omitempty"`
	// Full resolution, muxed on the fly from the adaptive tracks. No index, so
	// not seekable. Absent once the local file exists.
	Remux *sourceDTO `json:"remux,omitempty"`
	// A broadcast that has not begun. No source, and none is missing: YouTube
	// publishes nothing for a stream until it starts.
	Upcoming bool `json:"upcoming,omitempty"`
	// A broadcast still on air.
	//
	// Exclusive: when this is set nothing else is. Every other source here
	// begins from a finished file, and a live video publishes none — which is
	// why resolving one the ordinary way produced nothing at all.
	Live *sourceDTO `json:"live,omitempty"`
	// The downloaded file. Present only once it is on disk; the best source
	// whenever it is there.
	Local *sourceDTO `json:"local,omitempty"`
	// When every source is unavailable — membership, age restriction, geo-block,
	// or YouTube outage — this carries the yt-dlp error so the player can tell
	// the viewer why rather than sitting blank.
	StreamError string `json:"streamError,omitempty"`
	// True when this request corrected a catalog row that claimed a file the
	// disk does not have. The client refetches the video once on seeing it.
	Repaired bool `json:"repaired,omitempty"`
	// Set when upstream has refused this video for good. Distinct from
	// StreamError, which is a sentence about something that went wrong and
	// might not next time: this is an answer, and the player draws no retry
	// from it.
	Unavailable *unavailableDTO `json:"unavailable,omitempty"`
}

// unavailableDTO says why a video cannot be fetched, in a word the client can
// branch on rather than a message it would have to read.
type unavailableDTO struct {
	// members_only | private | removed | unavailable
	Reason string `json:"reason"`
}

func toJobDTO(j *ingestv1.Job) jobDTO {
	return jobDTO{
		ID:              j.GetId(),
		SourceURL:       j.GetSourceUrl(),
		VideoID:         j.GetVideoId(),
		Title:           j.GetTitle(),
		State:           trimEnumPrefix(j.GetState().String(), "JOB_STATE_"),
		Progress:        j.GetProgress(),
		DownloadedBytes: j.GetDownloadedBytes(),
		TotalBytes:      j.GetTotalBytes(),
		ErrorMessage:    j.GetErrorMessage(),
		CreatedAt:       j.GetCreatedAt().AsTime().UTC().Format("2006-01-02T15:04:05Z"),
	}
}

type externalVideoDTO struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	ChannelName     string `json:"channelName"`
	DurationSeconds int32  `json:"durationSeconds"`
	ViewCount       int64  `json:"viewCount"`
	ThumbnailURL    string `json:"thumbnailUrl"`
	SourceURL       string `json:"sourceUrl"`
	// Empty when unknown. A flat listing carries no upload date, and printing a
	// made-up one would render as "1 minute ago" on every card.
	PublishedAt string `json:"publishedAt,omitempty"`
	// True when the video already has a catalog row, which is what decides
	// whether opening it plays immediately or starts a download.
	InLibrary bool `json:"inLibrary"`
}

// sortOptionDTO is one ordering a channel offers, with the opaque token that
// selects it. The set comes from YouTube, so a channel that offers fewer
// orderings simply renders fewer controls.
type sortOptionDTO struct {
	Label string `json:"label"`
	Token string `json:"token"`
}

// handleDiscover searches upstream. It runs on every search, not only when the
// library comes up empty: topics decide what the feed offers, and searching is
// how someone deliberately looks past that.
func (g *Gateway) handleDiscover(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"videos": []externalVideoDTO{}})
		return
	}

	// A pasted address names one video, so it is fetched rather than searched
	// for. See youtube_url.go for why searching is the wrong verb here.
	if id, isAddress := videoIDFromSearch(query); isAddress {
		g.discoverOne(w, r, id)
		return
	}

	resp, err := g.ingest.Search(r.Context(), connect.NewRequest(&ingestv1.SearchRequest{
		Query: query,
		Limit: intParam(r, "limit", 20),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]externalVideoDTO, 0, len(resp.Msg.GetVideos()))
	for _, v := range resp.Msg.GetVideos() {
		out = append(out, externalVideoDTO{
			ID:              v.GetId(),
			Title:           v.GetTitle(),
			ChannelName:     v.GetChannelName(),
			DurationSeconds: v.GetDurationSeconds(),
			ViewCount:       v.GetViewCount(),
			ThumbnailURL:    v.GetThumbnailUrl(),
			SourceURL:       v.GetSourceUrl(),
			InLibrary:       v.GetInLibrary(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"videos": out})
}

// discoverOne answers the "On YouTube" half of a pasted address.
//
// The catalog is asked first, and a hit ends the request without touching
// YouTube at all: the video is already on the page, under "In your library",
// which is where something on this disk belongs. That check is the whole reason
// this lives at the gateway — it is the only place holding both answers.
func (g *Gateway) discoverOne(w http.ResponseWriter, r *http.Request, videoID string) {
	empty := map[string]any{"videos": []externalVideoDTO{}}

	// An address to YouTube that names no video — a channel, a playlist, the
	// front page. Nothing to fetch, and running its text as a search would spend
	// a counted request on a string nobody typed as a question.
	if videoID == "" {
		writeJSON(w, http.StatusOK, empty)
		return
	}

	if _, err := g.catalog.GetVideo(r.Context(), connect.NewRequest(&catalogv1.GetVideoRequest{
		VideoId: videoID,
		UserId:  g.userID(r),
	})); err == nil {
		writeJSON(w, http.StatusOK, empty)
		return
	}

	resp, err := g.ingest.PreviewVideo(r.Context(), connect.NewRequest(&ingestv1.PreviewVideoRequest{
		Url: "https://www.youtube.com/watch?v=" + videoID,
	}))
	if err != nil {
		// A link to a video that is private, removed, or simply mistyped. The
		// page already says "Could not reach YouTube" for this, which is the
		// truthful thing to say: the address was asked about and did not answer.
		g.writeErr(w, r, err)
		return
	}

	v := resp.Msg.GetVideo()
	writeJSON(w, http.StatusOK, map[string]any{"videos": []externalVideoDTO{{
		ID:              v.GetId(),
		Title:           v.GetTitle(),
		ChannelName:     v.GetChannelName(),
		DurationSeconds: v.GetDurationSeconds(),
		ViewCount:       v.GetViewCount(),
		ThumbnailURL:    v.GetThumbnailUrl(),
		SourceURL:       v.GetSourceUrl(),
	}}})
}

type ensureExternalRequest struct {
	URL string `json:"url"`
}

// handleEnsureExternal turns a search result into something the watch page can
// open. Only metadata is written; the download starts when play is pressed,
// exactly as for any other video.
func (g *Gateway) handleEnsureExternal(w http.ResponseWriter, r *http.Request) {
	var body ensureExternalRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	resp, err := g.ingest.EnsureVideo(r.Context(), connect.NewRequest(&ingestv1.EnsureVideoRequest{
		Url: body.URL,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"videoId": resp.Msg.GetVideoId()})
}

type scanStatusDTO struct {
	StartedAt      string   `json:"startedAt"`
	DurationMs     int64    `json:"durationMs"`
	SourcesScanned int32    `json:"sourcesScanned"`
	SourcesFailed  int32    `json:"sourcesFailed"`
	VideosSeen     int32    `json:"videosSeen"`
	VideosAdded    int32    `json:"videosAdded"`
	Errors         []string `json:"errors"`
	Running        bool     `json:"running"`
}

func toScanStatusDTO(s *ingestv1.ScanStatus) scanStatusDTO {
	errs := s.GetErrors()
	if errs == nil {
		errs = []string{}
	}
	return scanStatusDTO{
		StartedAt:      s.GetStartedAt().AsTime().UTC().Format("2006-01-02T15:04:05Z"),
		DurationMs:     s.GetDurationMs(),
		SourcesScanned: s.GetSourcesScanned(),
		SourcesFailed:  s.GetSourcesFailed(),
		VideosSeen:     s.GetVideosSeen(),
		VideosAdded:    s.GetVideosAdded(),
		Errors:         errs,
		Running:        s.GetRunning(),
	}
}

// handleRefreshTopics rescans topics.yaml on demand. A scan walks every source
// and takes minutes, so it runs against the request context and returns only
// when done — the caller is a person who pressed Refresh and expects a result.
func (g *Gateway) handleRefreshTopics(w http.ResponseWriter, r *http.Request) {
	resp, err := g.ingest.Refresh(r.Context(), connect.NewRequest(&ingestv1.RefreshRequest{}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toScanStatusDTO(resp.Msg.GetStatus()))
}

// handleBackfillTopics starts assigning YouTube's category to videos with none.
//
// Answers as soon as the pass has started, not when it finishes. A full pass
// runs for hours — YouTube throttles sustained metadata fetches — and no HTTP
// request survives that: an earlier synchronous version died on the gateway's
// own ten-minute client deadline every time, while the work carried on
// invisibly behind a request that had already reported failure. Progress is
// polled from GET on the same path.
//
// `?limit=` bounds a run; the pass selects on "has no topic", so a bounded run
// is resumed simply by asking again.
func (g *Gateway) handleBackfillTopics(w http.ResponseWriter, r *http.Request) {
	var limit int32
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = int32(parsed)
		}
	}

	resp, err := g.ingest.BackfillTopics(r.Context(), connect.NewRequest(&ingestv1.BackfillTopicsRequest{
		Limit: limit,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBackfillDTO(resp.Msg.GetStatus()))
}

// handleBackfillStatus reports the current or most recent pass, for polling.
func (g *Gateway) handleBackfillStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := g.ingest.GetBackfillStatus(r.Context(), connect.NewRequest(&ingestv1.GetBackfillStatusRequest{}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toBackfillDTO(resp.Msg.GetStatus()))
}

type backfillDTO struct {
	Running    bool   `json:"running"`
	Examined   int32  `json:"examined"`
	Updated    int32  `json:"updated"`
	Failed     int32  `json:"failed"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

func toBackfillDTO(msg *ingestv1.BackfillStatus) backfillDTO {
	dto := backfillDTO{
		Running:  msg.GetRunning(),
		Examined: msg.GetExamined(),
		Updated:  msg.GetUpdated(),
		Failed:   msg.GetFailed(),
	}
	if ts := msg.GetStartedAt(); ts != nil {
		dto.StartedAt = ts.AsTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	if ts := msg.GetFinishedAt(); ts != nil {
		dto.FinishedAt = ts.AsTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	return dto
}

// handleCancelVideoDownload stops the transfer for a video, if one is running.
//
// Sent when the watch page is left. The copy exists so the video plays from
// disk next time, but one nobody is waiting for is a request to YouTube nobody
// is waiting for either — and this address has already been blocked once for
// making too many of those.
func (g *Gateway) handleCancelVideoDownload(w http.ResponseWriter, r *http.Request) {
	resp, err := g.ingest.CancelVideoDownload(r.Context(), connect.NewRequest(
		&ingestv1.CancelVideoDownloadRequest{VideoId: r.PathValue("id")},
	))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int32{"cancelled": resp.Msg.GetCancelled()})
}

func (g *Gateway) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := g.ingest.GetScanStatus(r.Context(), connect.NewRequest(&ingestv1.GetScanStatusRequest{}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toScanStatusDTO(resp.Msg.GetStatus()))
}

func (g *Gateway) handleListJobs(w http.ResponseWriter, r *http.Request) {
	activeOnly, _ := strconv.ParseBool(r.URL.Query().Get("activeOnly"))

	// Off unless asked for. The Activity page asks; the player, which reads this
	// same list to learn its download has landed, must not have jobs hidden from
	// it by somebody tidying a page (CLAUDE.md §8b).
	hideDismissed, _ := strconv.ParseBool(r.URL.Query().Get("hideDismissed"))

	resp, err := g.ingest.ListJobs(r.Context(), connect.NewRequest(&ingestv1.ListJobsRequest{
		ActiveOnly:    activeOnly,
		Limit:         intParam(r, "limit", 50),
		HideDismissed: hideDismissed,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]jobDTO, 0, len(resp.Msg.GetJobs()))
	for _, j := range resp.Msg.GetJobs() {
		out = append(out, toJobDTO(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

// ensureDownload schedules a cached copy. Failures are logged and dropped: the
// user is already watching from upstream, so a queueing problem must not
// surface as a playback error.
// How long one asking is enough for.
//
// The player re-asks the stream answer every five seconds while there is no
// local copy, and every ask used to schedule a download. Enqueue is idempotent
// while a job is QUEUED or RUNNING, so this was invisible for as long as things
// worked — and the moment one failed, the next poll started another. Three jobs
// in twenty-six seconds, measured.
//
// Ingest refuses a URL that has just failed regardless of who asks (see
// failureCooldown), which is the rule; this is the manners. There is no reason
// to send twelve requests a minute across a service boundary to be told no
// eleven times.
const submitCooldown = time.Minute

// asked remembers which URLs have been sent to ingest recently.
//
// In memory and not persisted: a gateway restart forgetting this costs one
// extra Submit, and ingest's own rule is what actually protects the queue.
type askedRecently struct {
	mu   sync.Mutex
	when map[string]time.Time
}

func (a *askedRecently) claim(url string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.when == nil {
		a.when = map[string]time.Time{}
	}
	if last, found := a.when[url]; found && now.Sub(last) < submitCooldown {
		return false
	}
	// Swept here rather than on a timer: a household watches a handful of
	// videos at a time, and a map that only grows while the process lives is a
	// leak however slow.
	if len(a.when) > 256 {
		for k, t := range a.when {
			if now.Sub(t) >= submitCooldown {
				delete(a.when, k)
			}
		}
	}
	a.when[url] = now
	return true
}

func (g *Gateway) ensureDownload(sourceURL, userID string) {
	if !g.downloadsAsked.claim(sourceURL, time.Now()) {
		return
	}

	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()

	if _, err := g.ingest.Submit(ctx, connect.NewRequest(&ingestv1.SubmitRequest{
		Url:         sourceURL,
		RequestedBy: userID,
	})); err != nil {
		g.logger.Warn("schedule download", "url", sourceURL, "error", err)
	}
}

func (g *Gateway) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if _, err := g.ingest.CancelJob(r.Context(), connect.NewRequest(&ingestv1.CancelJobRequest{
		JobId: r.PathValue("id"),
	})); err != nil {
		g.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleDismissJob(w http.ResponseWriter, r *http.Request) {
	if _, err := g.ingest.DismissJob(r.Context(), connect.NewRequest(&ingestv1.DismissJobRequest{
		JobId: r.PathValue("id"),
	})); err != nil {
		g.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) handleDismissJobs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		g.writeErr(w, r, fmt.Errorf("reading body: %w", err))
		return
	}
	resp, err := g.ingest.DismissJobs(r.Context(), connect.NewRequest(&ingestv1.DismissJobsRequest{
		State: body.State,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"dismissed": resp.Msg.GetDismissed()})
}

func (g *Gateway) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	resp, err := g.ingest.RetryJob(r.Context(), connect.NewRequest(&ingestv1.RetryJobRequest{
		JobId:       r.PathValue("id"),
		RequestedBy: g.userID(r),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toJobDTO(resp.Msg.GetJob()))
}

func (g *Gateway) handleListScans(w http.ResponseWriter, r *http.Request) {
	resp, err := g.ingest.ListScans(r.Context(), connect.NewRequest(&ingestv1.ListScansRequest{
		Limit:  intParam(r, "limit", 10),
		Offset: intParam(r, "offset", 0),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	out := make([]scanStatusDTO, 0, len(resp.Msg.GetScans()))
	for _, s := range resp.Msg.GetScans() {
		out = append(out, toScanStatusDTO(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scans": out,
		"total": resp.Msg.GetTotal(),
	})
}

func (g *Gateway) handleClearScans(w http.ResponseWriter, r *http.Request) {
	if _, err := g.ingest.ClearScans(r.Context(), connect.NewRequest(&ingestv1.ClearScansRequest{})); err != nil {
		g.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleStream lists every way the client could play a video right now.
//
// `?prefetch=1` means the viewer has only hovered a card, not pressed play: the
// upstream URL is resolved and cached so a later press starts instantly, but no
// download is scheduled. Without that split, drifting the mouse across a feed
// would fill a disk that has a hard ceiling.
func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	videoID := r.PathValue("id")
	prefetch := r.URL.Query().Get("prefetch") == "1"

	video, err := g.catalog.GetVideo(ctx, connect.NewRequest(&catalogv1.GetVideoRequest{
		VideoId: videoID,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	v := video.Msg.GetVideo()

	// A broadcast still on air answers here and goes no further.
	//
	// Everything below this point is about a file: whether the disk has one,
	// whether to schedule fetching one, which of two ways to describe one that
	// exists upstream. A live video has none and will never have one — §4
	// refuses a broadcast as a download job, because it has no end to download
	// to, and one that was allowed through held the single worker slot for
	// hours while every later job sat queued at 0%.
	//
	// So this returns early rather than adding a fourth source beside the
	// others. The player's live tier is exclusive for the same reason and it
	// must be told so honestly: offering `hls` alongside would have it climb
	// toward a playlist built from adaptive tracks the broadcast does not
	// publish — measured, all seven of a live video's formats are m3u8_native
	// and not one is a plain https file.
	//
	// No download is scheduled and no subtitles are fetched. Captions are
	// generated after a broadcast ends, so asking now is asking for something
	// that does not exist yet.
	if v.GetIsLiveNow() {
		g.logger.Info("stream offered", "video", videoID, "tier", "live")
		writeJSON(w, http.StatusOK, streamDTO{
			Live: &sourceDTO{
				URL:      "/api/live/" + url.PathEscape(videoID) + "/master.m3u8",
				MimeType: "application/vnd.apple.mpegurl",
				// Seekable, and measured: the window is YouTube's own and ran
				// 0..3605 — an hour of rewind — with playback resuming 0.4s
				// after a seek back. It costs nothing to offer; it is already
				// in the playlist.
				Seekable: true,
			},
			// There is no copy coming, which is exactly what this flag means to
			// the player: stop polling for one. Without it a broadcast would be
			// asked about every five seconds for as long as somebody watched.
			CacheDisabled: true,
		})
		return
	}

	// A broadcast that has not started yet answers here and goes no further.
	//
	// It is an ordinary-looking video in Home — it has a title, a thumbnail and
	// a channel — and pressing it used to be answered with `hls` and `remux`,
	// both built from adaptive tracks YouTube has not published and will not
	// until the broadcast begins. Measured on mYPF7KARk5Q: yt-dlp answers "This
	// live event will begin in a few moments" and the viewer got a generic
	// failure with nothing to say what had happened or that waiting would fix
	// it.
	//
	// Not `unavailable`: that means permanent, offers no retry and names a
	// reason. This is the opposite — nothing is wrong, and the answer changes
	// on its own. The player says so and the poll it already runs picks up the
	// change.
	//
	// Read from live_status directly rather than through a freshness window,
	// unlike is_live_now. Being wrong here costs at most one scan interval of
	// "starting soon" over a broadcast that just began, and it corrects itself;
	// the alternative is the failure above.
	if v.GetLiveStatus() == "is_upcoming" {
		g.logger.Info("stream offered", "video", videoID, "tier", "none:upcoming")
		// CacheDisabled is deliberately *not* set, unlike the live branch below.
		//
		// Its one job is to stop the player re-asking, and here re-asking is
		// exactly what has to keep happening: the broadcast starting is a
		// change in this answer and nothing else will report it. Setting it
		// would have made the message on screen — "it will begin playing on its
		// own" — a straightforward lie.
		writeJSON(w, http.StatusOK, streamDTO{Upcoming: true})
		return
	}

	// Whether the row is telling the truth about the disk.
	//
	// Repaired here rather than by a sweep because this is the one place that
	// holds both answers at once — the catalog says READY, the disk says no —
	// and it sits on the path somebody has just pressed play on, so the lie can
	// be corrected in the request that found it.
	repaired := false
	// Set only by the debugging switch below: the file really is on the disk,
	// it is simply not being offered. Without it the fall-through would treat a
	// downloaded video as one that still needs fetching and re-download the
	// library a video at a time.
	onDisk := false
	if v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_READY && v.GetMediaPath() != "" {
		switch checkMedia(g.mediaRoot, v.GetMediaPath()) {
		case mediaPresent:
			// Debugging: pretend the file is not there, so the player has to
			// use the streaming tiers. See Gateway.skipLocalTier — this is the
			// only way to look at those tiers twice, because the first look
			// downloads the video and every look after it answers `local`.
			if g.skipLocalTier {
				g.logger.Warn("withholding local tier (DEBUG_SKIP_LOCAL_TIER)",
					"video", videoID, "path", v.GetMediaPath())
				onDisk = true
				break
			}
			// On disk: nothing upstream is worth offering beside it. Touching
			// last_accessed_at happens through watch progress, so the eviction
			// sweep sees actual viewing rather than mere resolution.
			g.logger.Info("stream offered",
				"video", videoID, "tier", "local", "path", v.GetMediaPath(), "prefetch", prefetch)
			writeJSON(w, http.StatusOK, streamDTO{
				Local: &sourceDTO{
					URL:      "/media/" + v.GetMediaPath(),
					MimeType: "video/mp4",
					Seekable: true,
				},
			})
			return

		case mediaMissing:
			// Deleted by hand. EVICTED is the state the rest of the system
			// already understands for "the metadata is here and the bytes are
			// not" — the feed skips it, the card offers to fetch it again — so
			// this is a correction rather than a new condition.
			//
			// Done even for a prefetch. The expensive, counted thing is a
			// request upstream, and that boundary is untouched: hovering still
			// downloads nothing. Declining to record what the disk just said
			// would only mean discovering it again on the next hover.
			if _, err := g.catalog.SetMediaState(ctx, connect.NewRequest(&catalogv1.SetMediaStateRequest{
				VideoId:    videoID,
				MediaState: catalogv1.MediaState_MEDIA_STATE_EVICTED,
			})); err != nil {
				// The playback path must not fail over bookkeeping: the video
				// can still be streamed and fetched again regardless.
				g.logger.Warn("mark missing media evicted", "video", videoID, "error", err)
			} else {
				repaired = true
				g.logger.Info("media file missing, marked evicted",
					"video", videoID, "path", v.GetMediaPath())
			}
			// Falls through to the upstream sources below, and to the download
			// that puts the file back.

		case mediaRootUnavailable:
			// The drive is not answering, so every file in the library looks
			// missing and none of them are. Nothing is written: one loose cable
			// must not blank the whole catalog (CLAUDE.md §8, risk 1).
			g.logger.Warn("media root unavailable", "root", g.mediaRoot, "video", videoID)
			writeJSON(w, http.StatusOK, streamDTO{
				StreamError: "The media drive is not available. Check that it is connected.",
			})
			return
		}
	}

	// Nothing to fetch and nothing to play. Answered before the download is
	// scheduled and before any upstream source is offered: every one of them
	// would be a request against a video YouTube has already refused, which is
	// how thirteen jobs for one video happened in two minutes.
	if v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_UNAVAILABLE {
		writeJSON(w, http.StatusOK, streamDTO{
			Unavailable: &unavailableDTO{Reason: unavailableReasonOf(ctx, g, videoID)},
		})
		return
	}

	// Pressing play is what schedules a download. Enqueue is idempotent per
	// source URL, so repeated resolves attach to the running job.
	//
	// Unless the household has asked for streaming only, in which case this is
	// the one thing that stops — not the captions, which are fetched here
	// instead, and not Retry on /activity, which is somebody saying "I want this
	// one" and would otherwise become a dead button exactly while the mode is on.
	//
	// Captions cost a few tens of kilobytes against a few hundred megabytes,
	// roughly four thousand to one, and they are what the translation and the
	// read-aloud are built on. Losing them to save that would be the worst trade
	// in the app.
	cacheOff := g.cacheDisabled()
	if !prefetch && !onDisk && v.GetSourceUrl() != "" {
		if cacheOff {
			go g.fetchSubtitlesOnly(v.GetSourceUrl())
		} else {
			go g.ensureDownload(v.GetSourceUrl(), g.userID(r))
		}
	}

	out := streamDTO{
		// No copy is coming, so the player can stop asking for one.
		//
		// It polls this answer every five seconds until `local` appears, which
		// is how it notices a download landing. With caching off `local` never
		// arrives: a three-hour video would ask two thousand times about
		// something that is never coming, and the progress bar would sit at
		// nothing for the whole film.
		CacheDisabled: cacheOff,
		// Set when this request found the catalog claiming a file the disk does
		// not have, and corrected it. The player has already been handed a video
		// row saying READY, and it stops asking for a new one once the state
		// looks settled — so without being told, it would go on showing the
		// video as downloaded for as long as the page stayed open.
		Repaired: repaired,
		// Full resolution before the copy lands means muxing YouTube's separate
		// video and audio tracks ourselves. No index, so no seeking — which is
		// why it is the fallback rather than the opening move.
		// Seekable, because a media playlist is an index: the browser knows
		// where every segment begins and asks for the one it wants. That single
		// difference is what the mux's offsets, marks, leads and reopens all
		// exist to work around.
		HLS: &sourceDTO{
			URL:      "/api/videos/" + url.PathEscape(videoID) + "/hls/master.m3u8",
			Height:   remuxHeight,
			MimeType: "application/vnd.apple.mpegurl",
			Seekable: true,
		},
		Remux: &sourceDTO{
			URL:    "/api/videos/" + url.PathEscape(videoID) + "/remux",
			Height: remuxHeight,
			// Not seekable in the sense the browser means it: there is no index
			// to move within. The player seeks by asking for the stream again
			// from a new offset, which is why it needs to know the difference.
			MimeType: "video/mp4",
			Seekable: false,
		},
	}

	// The instant tier is no longer offered, and no longer resolved for.
	//
	// itag 18 — the one progressive rendition YouTube still publishes, and the
	// tier every video used to open on — now serves the head of the file and
	// refuses the middle. Measured 2026-08-18 across 16 videos of this library
	// on freshly resolved URLs, one request each: a mid-file range answered
	// **403 twelve times out of fourteen** and 206 not once, while the same
	// videos' adaptive 720p video and AAC audio answered 206 at head and middle
	// alike, 13 of 14.
	//
	// That is the whole of "pressing play does nothing": a video either would
	// not open (`MEDIA_ELEMENT_ERROR: Format error`) or opened and lost its
	// source a megabyte in (`PIPELINE_ERROR_READ`), and the player, standing on
	// a dying tier, could not climb off it.
	//
	// The route and its proxy stay (see handleInstantStream). Nothing is broken
	// about them; upstream simply stopped serving what they fetch, and §4's
	// rule is that a tier measured to be dead is not offered — not that its
	// code is burnt. The day progressive serves again, this becomes an offer
	// again.
	//
	// Nor is ResolveStream called merely to learn whether a video is available.
	// It costs a full metadata fetch, three times over (`resolveAttempts`),
	// against the address §8 risk 6 is about, to ask a question the download
	// scheduled a few lines above answers on its own — every path that meets
	// upstream records an unavailable video, and the catalog check at the top
	// of this handler is what the next poll reads.

	// What this request actually offered, said once, on the way out.
	//
	// "Did the player even get a tier?" was answerable only from the browser,
	// and the browser reports a missing tier as a blank picture — the same
	// thing a refused one looks like. With the progressive tier withdrawn there
	// is normally exactly one answer here, so a request that produced none is
	// worth being able to see from the log alone.
	g.logger.Info("stream offered",
		"video", videoID,
		"tier", offeredTier(out),
		"height", remuxHeightOf(out),
		"repaired", out.Repaired,
		"prefetch", prefetch)

	writeJSON(w, http.StatusOK, out)
}

// offeredTier names what a stream answer gives the player, for the log.
func offeredTier(out streamDTO) string {
	switch {
	case out.Unavailable != nil:
		return "none:unavailable"
	case out.StreamError != "":
		return "none:error"
	case out.Live != nil:
		return "live"
	case out.Local != nil:
		return "local"
	case out.HLS != nil && out.Remux != nil:
		return "hls+remux"
	case out.HLS != nil:
		return "hls"
	case out.Remux != nil:
		return "remux"
	default:
		return "none"
	}
}

func remuxHeightOf(out streamDTO) int32 {
	if out.Remux == nil {
		return 0
	}
	return out.Remux.Height
}

// handleRemuxStart asks ingest where a muxed stream opened at `t` will really
// begin. A seek lands on the nearest keyframe at or before the mark, so the
// answer is up to a group of pictures earlier than asked — and the player needs
// it, because that is the zero of the stream it is about to be handed.
//
// A small JSON reply rather than a header on the stream itself: the stream goes
// to a <video> element, and script never sees its response.
func (g *Gateway) handleRemuxStart(w http.ResponseWriter, r *http.Request) {
	target := g.ingestBaseURL + "/stream/" + url.PathEscape(r.PathValue("id")) + "/start"
	if v := r.URL.Query().Get("t"); v != "" {
		target += "?" + url.Values{"t": {v}}.Encode()
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	resp, err := g.streamClient.Do(req)
	if err != nil {
		g.logger.Warn("remux start proxy", "video", r.PathValue("id"), "error", err)
		http.Error(w, "cannot resolve stream start", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// handleHLS passes the HLS routes through to ingest, which owns the signed
// URLs these playlists point at.
//
// The Range header goes with it untouched: the player takes its ranges from a
// playlist ingest wrote, so they are already exact, and rewriting them here
// would only be a second place to get the arithmetic wrong.
func (g *Gateway) handleHLS(w http.ResponseWriter, r *http.Request) {
	target := g.ingestBaseURL + "/hls/" + url.PathEscape(r.PathValue("id")) + "/" + url.PathEscape(r.PathValue("name"))

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	if v := r.Header.Get("Range"); v != "" {
		req.Header.Set("Range", v)
	}

	resp, err := g.streamClient.Do(req)
	if err != nil {
		g.logger.Warn("hls proxy", "video", r.PathValue("id"), "error", err)
		http.Error(w, "cannot read media", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control"} {
		if v := resp.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	copyStream(w, resp.Body)
}

// handleRemuxStream proxies the muxed stream from ingest, which owns yt-dlp and
// ffmpeg. Streamed straight through rather than buffered: the body is a whole
// video, and holding it in memory to forward it would be pointless.
func (g *Gateway) handleRemuxStream(w http.ResponseWriter, r *http.Request) {
	target := g.ingestBaseURL + "/stream/" + url.PathEscape(r.PathValue("id"))
	// height picks the rendition; t is where to start. The second is how the
	// player seeks in a stream that cannot be seeked: it asks for a new one.
	forwarded := url.Values{}
	for _, name := range []string{"height", "t", "audioAt"} {
		if v := r.URL.Query().Get(name); v != "" {
			forwarded.Set(name, v)
		}
	}
	if len(forwarded) > 0 {
		target += "?" + forwarded.Encode()
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	// A dedicated client with no timeout: this response lasts as long as the
	// video does, and the shared clients would cut it off mid-playback.
	resp, err := g.streamClient.Do(req)
	if err != nil {
		g.logger.Warn("remux proxy", "video", r.PathValue("id"), "error", err)
		http.Error(w, "cannot open stream", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for _, header := range []string{"Content-Type", "Cache-Control", "Accept-Ranges"} {
		if v := resp.Header.Get(header); v != "" {
			w.Header().Set(header, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Flushing as the bytes arrive is what lets playback begin before the whole
	// video has been muxed.
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

// handleInstantStream proxies the progressive upstream file rather than
// handing its URL to the browser.
//
// The URL googlevideo.com returns is signed to the IP that requested it. That
// is this server's IP, not the viewer's — and on any LAN behind CGNAT (the
// common case, not a corner one) those two IPs differ per connection. A
// browser sent the raw URL gets a request YouTube refuses, which shows up as
// a generic "format error" with no useful reason attached, and a video that
// has no local copy yet — everything not already downloaded — never starts.
// Fetching it here keeps the request on the IP the signature was issued for.
//
// Resolved fresh on every request rather than cached in the gateway: ingest
// already caches and refreshes the signed URL (resolvecache.go) for as long
// as it stays valid, so a second cache here would only be another place for
// it to go stale.
func (g *Gateway) handleInstantStream(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("id")

	resp, err := g.fetchInstant(r, videoID, false)
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	if resp == nil {
		http.Error(w, "cannot open stream", http.StatusBadGateway)
		return
	}

	// An upstream refusal arrives as a perfectly successful round trip: err is
	// nil and the status carries the bad news. Passing it straight through left
	// no trace anywhere, and the player reports it only as a generic format
	// error — which is how a run of 403s stayed unexplained for a day.
	//
	// The refusal is a property of the URL, not of the video: a signed URL is
	// occasionally handed over already dead, redirecting to a host that answers
	// 403 for every request until the URL expires — measured at 20 of 20 —
	// while resolving again yields a working one on the first try. Without this
	// retry the dead URL stays cached for the best part of an hour, so that one
	// video is unplayable while every other video plays, which is exactly how
	// this looked from the sofa.
	if resp.StatusCode >= http.StatusBadRequest {
		g.logger.Warn("instant proxy refused, resolving again",
			"video", videoID,
			"status", resp.StatusCode,
			"range", r.Header.Get("Range"),
			"url", resp.Request.URL.String())
		_ = resp.Body.Close()

		resp, err = g.fetchInstant(r, videoID, true)
		if err != nil {
			g.writeErr(w, r, err)
			return
		}
		if resp == nil {
			http.Error(w, "cannot open stream", http.StatusBadGateway)
			return
		}
		if resp.StatusCode >= http.StatusBadRequest {
			// Twice in a row is no longer a poisoned URL. Pass it on rather than
			// keep asking: a third request would only add to whatever count
			// upstream is keeping against this address.
			g.logger.Warn("instant proxy refused after re-resolve",
				"video", videoID,
				"status", resp.StatusCode,
				"url", resp.Request.URL.String())
		}
	}
	defer func() { _ = resp.Body.Close() }()

	// A client that asked for no range is owed the whole file under a 200, and
	// answering it with the single bounded piece fetched upstream would hand it
	// eight megabytes of a video and call that the end. A browser's <video>
	// always sends a range, but a television's may not, and that is the screen
	// this is all eventually for.
	if r.Header.Get("Range") == "" && resp.StatusCode == http.StatusPartialContent {
		g.streamWholeInstant(w, r, videoID, resp)
		return
	}

	for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control"} {
		if v := resp.Header.Get(header); v != "" {
			w.Header().Set(header, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	copyStream(w, resp.Body)
}

// streamWholeInstant answers a request that carried no range, by fetching the
// file a bounded piece at a time and writing them out as one continuous 200.
//
// first is the piece already fetched, and is consumed here.
func (g *Gateway) streamWholeInstant(w http.ResponseWriter, r *http.Request, videoID string, first *http.Response) {
	total, ok := totalFromContentRange(first.Header.Get("Content-Range"))
	if !ok {
		// Without the total there is no way to say where the file ends, so the
		// piece in hand is all this can honestly offer.
		g.logger.Warn("instant proxy: no content-range on a partial response",
			"video", videoID, "content_range", first.Header.Get("Content-Range"))
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	if v := first.Header.Get("Content-Type"); v != "" {
		w.Header().Set("Content-Type", v)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(http.StatusOK)

	written := copyStream(w, first.Body)
	_ = first.Body.Close()

	for written < total {
		next := r.Clone(r.Context())
		next.Header.Set("Range", fmt.Sprintf("bytes=%d-", written))

		resp, err := g.fetchInstant(next, videoID, false)
		if err != nil || resp == nil || resp.StatusCode >= http.StatusBadRequest {
			// A refusal here truncates the video in silence — the status was
			// written long ago and says 200 — so the piece is worth asking for
			// twice, on a freshly resolved URL, exactly as the opening one is.
			if resp != nil {
				_ = resp.Body.Close()
			}
			resp, err = g.fetchInstant(next, videoID, true)
			if err != nil || resp == nil || resp.StatusCode >= http.StatusBadRequest {
				if resp != nil {
					_ = resp.Body.Close()
				}
				g.logger.Warn("instant proxy: piece refused mid-file",
					"video", videoID, "from", written, "error", err)
				return
			}
		}
		n := copyStream(w, resp.Body)
		_ = resp.Body.Close()
		if n == 0 {
			return // no progress: stop rather than loop forever
		}
		written += n
	}
}

// totalFromContentRange reads the file's size out of "bytes 0-8388607/34801931".
func totalFromContentRange(contentRange string) (int64, bool) {
	_, size, found := strings.Cut(contentRange, "/")
	if !found || size == "*" {
		return 0, false
	}
	total, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	if err != nil || total <= 0 {
		return 0, false
	}
	return total, true
}

// copyStream forwards a body, flushing as it goes so the player receives bytes
// while they are still arriving rather than at the end. Returns how many bytes
// reached the client.
func copyStream(w http.ResponseWriter, body io.Reader) int64 {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64*1024)
	var written int64
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			count, writeErr := w.Write(buf[:n])
			written += int64(count)
			if writeErr != nil {
				return written
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return written
		}
	}
}

// How much of the file one upstream request may ask for.
//
// Measured, not guessed. Asking for 1 MiB or 2 MiB was answered 206 every time
// — 10 of 10 across fresh URLs — while asking for 8 MiB tracked the open-ended
// request exactly, succeeding and failing in step with it. Past some size
// googlevideo stops treating a range as a range and answers with the redirect
// that leads to a 403, so the size is the safeguard and it has to stay under
// that line rather than near it.
//
// **This is 2 MiB and ffmpeg's `-request_size` is 1 MiB, deliberately.** They
// were briefly made equal, on the reasoning that one number is easier to keep
// right than two. The reasoning was wrong, and worth recording so it is not
// repeated: the measurement that lowered ffmpeg to 1 MiB was taken on the
// **adaptive audio track** of the 1080p mux — 206 at ≤1 MiB, 403 at ≥2 MiB, 8
// of 8 — and this tier does not serve that file. It serves itag 18, a
// progressive 360p rendition, whose own measurement is the 10 of 10 above and
// which has never once been seen to refuse 2 MiB.
//
// Two formats, two measurements, two numbers. Carrying one format's evidence
// across to another was the mistake; halving the chunk size doubled this tier's
// request count to guard against something never observed in it.
//
// 2 MiB is around 40 seconds of the 360p rendition, and the player asks for the
// next piece the moment it needs one.
const instantChunkBytes = 2 << 20

// boundedRange turns whatever the browser asked for into a range with an end.
//
// A suffix range ("bytes=-500", the last N bytes) is passed through: it is
// already bounded, and it is how a player reads a trailing index.
func boundedRange(browserRange string) string {
	const prefix = "bytes="
	spec, found := strings.CutPrefix(strings.TrimSpace(browserRange), prefix)
	if !found {
		// No range at all: the browser wants the file from the start.
		return fmt.Sprintf("%s0-%d", prefix, instantChunkBytes-1)
	}
	// Only the first range of a multi-range request is honoured, which is what
	// this proxy has always done — googlevideo does not serve multipart ranges.
	spec, _, _ = strings.Cut(spec, ",")
	spec = strings.TrimSpace(spec)

	start, end, ok := strings.Cut(spec, "-")
	if !ok || start == "" {
		return prefix + spec
	}
	if end != "" {
		return prefix + spec // already bounded
	}
	first, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return prefix + spec
	}
	return fmt.Sprintf("%s%d-%d", prefix, first, first+instantChunkBytes-1)
}

// fetchInstant resolves the upstream URL and opens it, standing in for the
// browser. A nil response with a nil error means the connection itself failed
// and has already been logged; the caller answers 502.
//
// refresh discards ingest's cached URL first, which is what makes retrying
// worth anything — asking again for the same dead URL would fail identically.
func (g *Gateway) fetchInstant(r *http.Request, videoID string, refresh bool) (*http.Response, error) {
	resolved, err := g.ingest.ResolveStream(r.Context(), connect.NewRequest(&ingestv1.ResolveStreamRequest{
		VideoId: videoID,
		Refresh: refresh,
	}))
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, resolved.Msg.GetUrl(), nil)
	if err != nil {
		return nil, err
	}
	// The player seeks this tier with real HTTP range requests, so the range the
	// browser asks for has to reach the upstream — but never open-ended.
	//
	// An open-ended request ("bytes=0-", or none at all) asks googlevideo for
	// the rest of the file in one response, and it answers that with a redirect
	// to a host which then refuses: 403, measured at up to 9 of 12 attempts on
	// one video and varying by the minute. The identical URL asked for a bounded
	// range answers 206 every time — 10 of 10, including URLs whose open-ended
	// request had just been refused.
	//
	// `bytes=0-` is exactly what Chrome sends to open a video, which is why a
	// video with no local copy would not start while everything already on disk
	// played normally.
	//
	// Answering 206 with fewer bytes than were asked for is what a range request
	// permits, and the player already knows how to ask for the next piece.
	req.Header.Set("Range", boundedRange(r.Header.Get("Range")))
	// A signed URL is only half of what googlevideo checks: a request with no
	// User-Agent at all — Go's http.Client sends none by default when one
	// isn't set — is refused with a 403 the same as a bad signature is. This
	// URL was only ever meant to be requested by an actual browser, which
	// supplies this automatically; standing in for the browser means doing
	// the same.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.youtube.com/")
	req.Header.Set("Origin", "https://www.youtube.com")

	// A dedicated client with no timeout, for the same reason the remux proxy
	// uses one: the response lasts as long as the video does.
	resp, err := g.streamClient.Do(req)
	if err != nil {
		g.logger.Warn("instant proxy", "video", videoID, "error", err, "refresh", refresh)
		return nil, nil
	}
	return resp, nil
}

// unavailableReasonOf asks ingest why a video it has already given up on cannot
// be fetched.
//
// The catalogue records the state, not the reason: the reason is what upstream
// said, which is ingest's to keep. One resolve is enough — it answers from the
// record without touching YouTube.
func unavailableReasonOf(ctx context.Context, g *Gateway, videoID string) string {
	_, err := g.ingest.ResolveStream(ctx, connect.NewRequest(&ingestv1.ResolveStreamRequest{
		VideoId: videoID,
	}))
	if err == nil {
		return "unavailable"
	}
	return unavailableReason(err.Error())
}
