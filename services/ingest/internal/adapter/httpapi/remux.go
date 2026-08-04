// Package httpapi serves the one thing that cannot go over ConnectRPC: a
// continuous stream of media bytes.
package httpapi

import (
	"context"
	"encoding/json"
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
	OpenRemux(ctx context.Context, urls []string, startSeconds, audioStartSeconds float64) (io.ReadCloser, error)
	ProbeKeyframe(ctx context.Context, videoURL string, at float64) (float64, error)
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
	mux.HandleFunc("GET /stream/{videoId}/start", h.handleStreamStart)
}

// handleStreamStart answers "if I ask for the stream at t, where will it really
// begin?" — the keyframe an input seek lands on, which is up to a group of
// pictures earlier than asked.
//
// It is a separate request because the player needs the number and cannot have
// it: the stream itself is consumed by a <video> element, and script cannot read
// the headers of a response it never sees. Asking here and passing the answer
// back as `audioAt` also means the mux itself does not have to probe, so the
// 1.29s is paid once rather than twice.
func (h *Handler) handleStreamStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	videoID := r.PathValue("videoId")

	at := queryFloat(r, "t")
	if at <= 0 {
		// Opening at the beginning needs no probe: both inputs start at zero and
		// there is nothing to align.
		writeJSON(w, streamStartDTO{Start: 0})
		return
	}

	sourceURL, err := h.sources.SourceURLFor(ctx, videoID)
	if err != nil || sourceURL == "" {
		http.Error(w, "unknown video", http.StatusNotFound)
		return
	}

	urls, err := h.remux.ResolveRemuxURLs(ctx, sourceURL, h.defaultHeight)
	if err != nil || len(urls) == 0 {
		h.logger.Warn("resolve remux urls for start", "video", videoID, "error", err)
		http.Error(w, "cannot resolve media", http.StatusBadGateway)
		return
	}

	start, err := h.remux.ProbeKeyframe(ctx, urls[0], at)
	if err != nil {
		// Not an error the player should act on: without an answer it opens the
		// stream as before, sound slightly adrift, rather than not at all.
		h.logger.Warn("probe keyframe", "video", videoID, "at", at, "error", err)
		writeJSON(w, streamStartDTO{Start: 0})
		return
	}
	writeJSON(w, streamStartDTO{Start: start})
}

type streamStartDTO struct {
	// Start is where the stream will really begin, in absolute video seconds.
	// Zero means unknown, not the beginning of the video.
	Start float64 `json:"start"`
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(body)
}

// queryFloat reads a positive number from the query string. Anything
// unparseable reads as absent: a mangled timestamp should play the video from
// the start, not fail the request.
func queryFloat(r *http.Request, name string) float64 {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return v
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

	startSeconds := queryFloat(r, "t")
	// Where the audio should start, which is not the same place: see OpenRemux.
	// The player learns it from /start and passes it here so the probe is not
	// repeated; a caller that omits it gets one done on its behalf.
	audioStart := queryFloat(r, "audioAt")

	resolveStart := time.Now()
	urls, err := h.remux.ResolveRemuxURLs(ctx, sourceURL, height)
	resolveTook := time.Since(resolveStart)
	if err != nil {
		h.logger.Warn("resolve remux urls", "video", videoID, "error", err)
		http.Error(w, "cannot resolve media", http.StatusBadGateway)
		return
	}

	if startSeconds > 0 && audioStart <= 0 && len(urls) > 1 {
		if probed, probeErr := h.remux.ProbeKeyframe(ctx, urls[0], startSeconds); probeErr == nil {
			audioStart = probed
		} else {
			h.logger.Warn("probe keyframe", "video", videoID, "at", startSeconds, "error", probeErr)
		}
	}

	stream, err := h.remux.OpenRemux(ctx, urls, startSeconds, audioStart)
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
		"video", videoID, "height", height, "from", startSeconds, "audioFrom", audioStart,
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
