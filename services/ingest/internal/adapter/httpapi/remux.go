// Package httpapi serves the one thing that cannot go over ConnectRPC: a
// continuous stream of media bytes.
package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// Remuxer is the part of the downloader that assembles a playable stream from
// YouTube's separate video and audio files.
type Remuxer interface {
	ResolveRemuxURLs(ctx context.Context, videoURL string, height int32) ([]string, error)
	OpenRemux(ctx context.Context, urls []string, startSeconds float64) (io.ReadCloser, error)
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
	// Wrapped so that seeking — which reopens the mux, and may do so several
	// times a minute — does not re-run yt-dlp each time.
	return &Handler{
		remux:         newCachedRemuxURLs(remux),
		sources:       sources,
		defaultHeight: defaultHeight,
		logger:        logger,
	}
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /stream/{videoId}", h.handleRemux)
}

// handleRemux streams a video at full resolution, muxed on the fly.
//
// Deliberately not a range server. The stream has no known length and no index,
// so it is served as one continuous body and the browser treats it as
// unseekable — which is correct, because it is.
//
// Seeking is done by asking again. `?t=` opens a fresh mux from that offset, so
// the player performs a seek by replacing the stream rather than by moving
// within it. That costs a couple of seconds and a new ffmpeg, which is why the
// player only asks once the viewer has let go of the scrub bar rather than
// while they are dragging it.
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

	// Where to start. Anything unparseable is treated as the beginning: a
	// mangled timestamp should play the video, not fail the request.
	var startSeconds float64
	if raw := r.URL.Query().Get("t"); raw != "" {
		if v, convErr := strconv.ParseFloat(raw, 64); convErr == nil && v > 0 {
			startSeconds = v
		}
	}

	resolveStart := time.Now()
	urls, err := h.remux.ResolveRemuxURLs(ctx, sourceURL, height)
	resolveTook := time.Since(resolveStart)
	if err != nil {
		h.logger.Warn("resolve remux urls", "video", videoID, "error", err)
		http.Error(w, "cannot resolve media", http.StatusBadGateway)
		return
	}

	stream, err := h.remux.OpenRemux(ctx, urls, startSeconds)
	if err != nil {
		h.logger.Warn("open remux", "video", videoID, "error", err)
		http.Error(w, "cannot open stream", http.StatusBadGateway)
		return
	}
	// Closing kills ffmpeg. Without it a viewer who navigates away would leave
	// a process pulling the rest of the video for nothing.
	defer func() { _ = stream.Close() }()

	// Logged because this is the one request whose cost is a process rather
	// than a query, and because "did the player even ask for it?" turned out to
	// be the question that could not be answered from the outside.
	h.logger.Info("live mux opened",
		"video", videoID, "height", height, "from", startSeconds,
		"resolve", resolveTook.Truncate(time.Millisecond))

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	// Saying so explicitly stops the browser from attempting a range request
	// this stream cannot answer.
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)

	written, err := io.Copy(w, stream)
	if err != nil && ctx.Err() == nil {
		h.logger.Warn("remux stream ended early", "video", videoID, "error", err)
	}
	h.logger.Info("live mux closed", "video", videoID, "bytes", written)
}
