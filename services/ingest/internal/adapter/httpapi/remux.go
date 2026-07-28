// Package httpapi serves the one thing that cannot go over ConnectRPC: a
// continuous stream of media bytes.
package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
)

// Remuxer is the part of the downloader that assembles a playable stream from
// YouTube's separate video and audio files.
type Remuxer interface {
	ResolveRemuxURLs(ctx context.Context, videoURL string, height int32) ([]string, error)
	OpenRemux(ctx context.Context, urls []string) (io.ReadCloser, error)
}

// SourceLookup turns a local video id back into its upstream URL.
type SourceLookup interface {
	SourceURLFor(ctx context.Context, videoID string) (string, error)
}

type Handler struct {
	remux         Remuxer
	sources       SourceLookup
	defaultHeight int32
	logger        *slog.Logger
}

func NewHandler(remux Remuxer, sources SourceLookup, defaultHeight int32, logger *slog.Logger) *Handler {
	return &Handler{remux: remux, sources: sources, defaultHeight: defaultHeight, logger: logger}
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /stream/{videoId}", h.handleRemux)
}

// handleRemux streams a video at full resolution, muxed on the fly.
//
// Deliberately not a range server. The stream has no known length and no index,
// so it is served as one continuous body: the browser treats it as unseekable
// and plays it from the start, which is exactly right for a first viewing while
// the real file downloads in the background. Advertising range support here
// would invite seeks that cannot be honoured.
func (h *Handler) handleRemux(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	videoID := r.PathValue("videoId")

	sourceURL, err := h.sources.SourceURLFor(ctx, videoID)
	if err != nil || sourceURL == "" {
		http.Error(w, "unknown video", http.StatusNotFound)
		return
	}

	height := h.defaultHeight
	if raw := r.URL.Query().Get("height"); raw != "" {
		if v, convErr := strconv.Atoi(raw); convErr == nil && v > 0 {
			height = int32(v)
		}
	}

	urls, err := h.remux.ResolveRemuxURLs(ctx, sourceURL, height)
	if err != nil {
		h.logger.Warn("resolve remux urls", "video", videoID, "error", err)
		http.Error(w, "cannot resolve media", http.StatusBadGateway)
		return
	}

	stream, err := h.remux.OpenRemux(ctx, urls)
	if err != nil {
		h.logger.Warn("open remux", "video", videoID, "error", err)
		http.Error(w, "cannot open stream", http.StatusBadGateway)
		return
	}
	// Closing kills ffmpeg. Without it a viewer who navigates away would leave
	// a process pulling the rest of the video for nothing.
	defer func() { _ = stream.Close() }()

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	// Saying so explicitly stops the browser from attempting a range request
	// this stream cannot answer.
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, stream); err != nil && ctx.Err() == nil {
		h.logger.Warn("remux stream ended early", "video", videoID, "error", err)
	}
}
