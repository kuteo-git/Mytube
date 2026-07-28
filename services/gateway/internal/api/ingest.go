package api

import (
	"net/http"
	"strconv"
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

type streamDTO struct {
	// "local" when the file is on disk, "upstream" while the copy downloads.
	Source    string `json:"source"`
	URL       string `json:"url"`
	Height    int32  `json:"height,omitempty"`
	MimeType  string `json:"mimeType,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
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

func (g *Gateway) handleListJobs(w http.ResponseWriter, r *http.Request) {
	activeOnly, _ := strconv.ParseBool(r.URL.Query().Get("activeOnly"))

	resp, err := g.ingest.ListJobs(r.Context(), connect.NewRequest(&ingestv1.ListJobsRequest{
		ActiveOnly: activeOnly,
		Limit:      intParam(r, "limit", 50),
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

// handleStream is the single place the client asks "how do I play this?".
// Answering here rather than in the browser means the switch from upstream to
// local copy is invisible to the UI: it just asks again and gets a local path.
func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	videoID := r.PathValue("id")

	video, err := g.catalog.GetVideo(ctx, connect.NewRequest(&catalogv1.GetVideoRequest{
		VideoId: videoID,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	v := video.Msg.GetVideo()

	if v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_READY && v.GetMediaPath() != "" {
		// Cached. Touching last_accessed_at happens through watch progress, so
		// the eviction sweep sees actual viewing rather than mere resolution.
		writeJSON(w, http.StatusOK, streamDTO{
			Source:   "local",
			URL:      "/media/" + v.GetMediaPath(),
			MimeType: "video/mp4",
		})
		return
	}

	// Pressing play is what schedules a download. Enqueue is idempotent per
	// source URL, so repeated resolves attach to the running job.
	if v.GetSourceUrl() != "" {
		go g.ensureDownload(v.GetSourceUrl(), g.userID(r))
	}

	// Until the copy is usable, play from upstream so the video starts now.
	resolved, err := g.ingest.ResolveStream(ctx, connect.NewRequest(&ingestv1.ResolveStreamRequest{
		VideoId: videoID,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, streamDTO{
		Source:    "upstream",
		URL:       resolved.Msg.GetUrl(),
		Height:    resolved.Msg.GetHeight(),
		MimeType:  resolved.Msg.GetMimeType(),
		ExpiresAt: resolved.Msg.GetExpiresAt().AsTime().UTC().Format("2006-01-02T15:04:05Z"),
	})
}
