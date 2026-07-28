package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	recsysv1 "github.com/lucnguyen/local-youtube/gen/go/recsys/v1"
)

// Ingest-facing endpoints. These are what make the hybrid playback model
// visible to the client: search finds videos that are not in the library yet,
// submit queues the local copy, and stream returns something playable in the
// meantime.

type externalVideoDTO struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	ChannelID       string `json:"channelId"`
	ChannelName     string `json:"channelName"`
	DurationSeconds int32  `json:"durationSeconds"`
	ViewCount       int64  `json:"viewCount"`
	ThumbnailURL    string `json:"thumbnailUrl"`
	SourceURL       string `json:"sourceUrl"`
	PublishedAt     string `json:"publishedAt"`
	InLibrary       bool   `json:"inLibrary"`
}

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

func toExternalDTO(v *ingestv1.ExternalVideo) externalVideoDTO {
	return externalVideoDTO{
		ID:              v.GetId(),
		Title:           v.GetTitle(),
		ChannelID:       v.GetChannelId(),
		ChannelName:     v.GetChannelName(),
		DurationSeconds: v.GetDurationSeconds(),
		ViewCount:       v.GetViewCount(),
		ThumbnailURL:    v.GetThumbnailUrl(),
		SourceURL:       v.GetSourceUrl(),
		PublishedAt:     v.GetPublishedAt().AsTime().UTC().Format("2006-01-02T15:04:05Z"),
		InLibrary:       v.GetInLibrary(),
	}
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

// handleDiscover searches upstream. Kept separate from /api/search, which only
// looks at the local library: mixing them would make every keystroke in the
// search box hit yt-dlp.
func (g *Gateway) handleDiscover(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"videos": []externalVideoDTO{}})
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
		out = append(out, toExternalDTO(v))
	}

	go g.recordSignal(g.userID(r), recsysv1.SignalType_SIGNAL_TYPE_SEARCH, "", query, 0)
	writeJSON(w, http.StatusOK, map[string]any{"videos": out})
}

func (g *Gateway) handlePreview(w http.ResponseWriter, r *http.Request) {
	resp, err := g.ingest.Preview(r.Context(), connect.NewRequest(&ingestv1.PreviewRequest{
		Url: r.URL.Query().Get("url"),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toExternalDTO(resp.Msg.GetVideo()))
}

func (g *Gateway) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	resp, err := g.ingest.ListPlaylist(r.Context(), connect.NewRequest(&ingestv1.ListPlaylistRequest{
		Url:   r.URL.Query().Get("url"),
		Limit: intParam(r, "limit", 50),
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	out := make([]externalVideoDTO, 0, len(resp.Msg.GetVideos()))
	for _, v := range resp.Msg.GetVideos() {
		out = append(out, toExternalDTO(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"playlistTitle": resp.Msg.GetPlaylistTitle(),
		"videos":        out,
	})
}

type submitRequest struct {
	URL             string `json:"url"`
	PreferredHeight int32  `json:"preferredHeight,omitempty"`
}

func (g *Gateway) handleSubmitIngest(w http.ResponseWriter, r *http.Request) {
	var body submitRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	resp, err := g.ingest.Submit(r.Context(), connect.NewRequest(&ingestv1.SubmitRequest{
		Url:             body.URL,
		RequestedBy:     g.userID(r),
		PreferredHeight: body.PreferredHeight,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toJobDTO(resp.Msg.GetJob()))
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

	if v := video.Msg.GetVideo(); v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_READY && v.GetMediaPath() != "" {
		writeJSON(w, http.StatusOK, streamDTO{
			Source:   "local",
			URL:      "/media/" + v.GetMediaPath(),
			MimeType: "video/mp4",
		})
		return
	}

	// Not on disk yet: fall back to a resolved upstream stream so playback can
	// start now. This is the half of the hybrid model that hides the download.
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
