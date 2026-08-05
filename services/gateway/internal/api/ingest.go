package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
const remuxHeight = 1080

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
	// 360p by what YouTube still publishes muxed. Absent when the video offers
	// no progressive format at all.
	Instant *sourceDTO `json:"instant,omitempty"`
	// Full resolution, muxed on the fly from the adaptive tracks. No index, so
	// not seekable. Absent once the local file exists.
	Remux *sourceDTO `json:"remux,omitempty"`
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
func (g *Gateway) ensureDownload(sourceURL, userID string) {
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

	// Whether the row is telling the truth about the disk.
	//
	// Repaired here rather than by a sweep because this is the one place that
	// holds both answers at once — the catalog says READY, the disk says no —
	// and it sits on the path somebody has just pressed play on, so the lie can
	// be corrected in the request that found it.
	repaired := false
	if v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_READY && v.GetMediaPath() != "" {
		switch checkMedia(g.mediaRoot, v.GetMediaPath()) {
		case mediaPresent:
			// On disk: nothing upstream is worth offering beside it. Touching
			// last_accessed_at happens through watch progress, so the eviction
			// sweep sees actual viewing rather than mere resolution.
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

	// Pressing play is what schedules a download. Enqueue is idempotent per
	// source URL, so repeated resolves attach to the running job.
	if !prefetch && v.GetSourceUrl() != "" {
		go g.ensureDownload(v.GetSourceUrl(), g.userID(r))
	}

	out := streamDTO{
		// Set when this request found the catalog claiming a file the disk does
		// not have, and corrected it. The player has already been handed a video
		// row saying READY, and it stops asking for a new one once the state
		// looks settled — so without being told, it would go on showing the
		// video as downloaded for as long as the page stayed open.
		Repaired: repaired,
		// Full resolution before the copy lands means muxing YouTube's separate
		// video and audio tracks ourselves. No index, so no seeking — which is
		// why it is the fallback rather than the opening move.
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

	// The instant tier: a progressive upstream file the browser can range-request
	// on its own. Resolution is capped at 360p, and it is offered first anyway —
	// it starts in milliseconds and seeks properly, and the download that
	// replaces it usually lands within seconds.
	if resolved, resolveErr := g.ingest.ResolveStream(ctx, connect.NewRequest(&ingestv1.ResolveStreamRequest{
		VideoId: videoID,
	})); resolveErr != nil {
		// Not every video publishes a progressive format. That is not an error
		// worth failing the request over — it just means starting at the remux.
		g.logger.Info("no instant source", "video", videoID, "error", resolveErr)
		// When no local copy exists and the instant URL is gone, the
		// remux is the only path. If ResolveStream failed because YouTube
		// blocked access, the remux fails too. Tell the player why.
		if out.Local == nil {
			// Strip the gRPC framing ("internal: ") from the yt-dlp message
			// so the player shows a readable reason instead of a stack trace.
			msg := resolveErr.Error()
			if code := connect.CodeOf(resolveErr).String(); code != "" {
				msg = strings.TrimPrefix(msg, code+": ")
			}
			out.StreamError = msg
		}
	} else {
		out.Instant = &sourceDTO{
			URL:       resolved.Msg.GetUrl(),
			Height:    resolved.Msg.GetHeight(),
			MimeType:  resolved.Msg.GetMimeType(),
			Seekable:  true,
			ExpiresAt: resolved.Msg.GetExpiresAt().AsTime().UTC().Format("2006-01-02T15:04:05Z"),
		}
	}

	writeJSON(w, http.StatusOK, out)
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
