// Package httpapi serves the one thing that cannot go over ConnectRPC: a
// continuous stream of media bytes.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
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

// Refusals is what this handler knows about upstream saying no for good.
//
// It is here because this handler is often the first to be told: a video nobody
// has downloaded is met by the player asking for a stream, not by the queue.
// Whichever request hears the refusal is the one that records it.
type Refusals interface {
	NoteUpstreamFailure(ctx context.Context, sourceURL, videoID string, cause error)
	Refusal(ctx context.Context, sourceURL string) (domain.UnavailableSource, bool)
}

type Handler struct {
	remux      *cachedRemuxURLs
	sources    SourceLookup
	refusals   Refusals
	liveHeight int32
	logger     *slog.Logger
	// Resolved track pairs for the HLS routes, kept apart from the muxer's cache
	// because they answer a different question and are dropped for different
	// reasons.
	hls hlsCache
}

// liveHeight is the rendition muxed on the fly, which is deliberately not the
// rendition kept on disk — see the note in cmd/ingest.
func NewHandler(remux Remuxer, sources SourceLookup, refusals Refusals, liveHeight int32, logger *slog.Logger) *Handler {
	// Wrapped so that seeking — which reopens the mux, and may do so several
	// times a minute — does not re-run yt-dlp each time.
	return &Handler{
		remux:      newCachedRemuxURLs(remux),
		sources:    sources,
		refusals:   refusals,
		liveHeight: liveHeight,
		logger:     logger,
	}
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /stream/{videoId}", h.handleRemux)
	mux.HandleFunc("GET /stream/{videoId}/start", h.handleStreamStart)
	// The browser combines the two adaptive tracks itself, so these three serve
	// a description and some bytes rather than a muxed stream. See hls.go.
	mux.HandleFunc("GET /hls/{videoId}/{name}", h.handleHLS)
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

	urls, err := h.remux.ResolveRemuxURLs(ctx, sourceURL, h.liveHeight)
	if err != nil || len(urls) == 0 {
		h.logger.Warn("resolve remux urls for start", "video", videoID, "error", err)
		if h.refuse(w, ctx, sourceURL, videoID, err) {
			return
		}
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

	height := h.liveHeight
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

	// Asking at all is pointless when upstream has already refused for good,
	// and each ask is another extract counted against this address.
	if h.refuseKnown(w, ctx, sourceURL) {
		return
	}

	resolveStart := time.Now()
	urls, err := h.remux.ResolveRemuxURLs(ctx, sourceURL, height)
	resolveTook := time.Since(resolveStart)
	if err != nil {
		h.logger.Warn("resolve remux urls", "video", videoID, "error", err)
		if h.refuse(w, ctx, sourceURL, videoID, err) {
			return
		}
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

	// OpenRemux returns the moment ffmpeg starts, long before it has tried to
	// read anything, so a URL upstream refuses cannot be noticed here — the
	// status was already committed as 200 and the browser met an empty body,
	// which it reports as DEMUXER_ERROR_COULD_NOT_OPEN with nothing else said.
	//
	// Waiting for the first bytes is what turns that into something answerable:
	// no bytes means ffmpeg died, which for a stream that resolved cleanly means
	// the URLs are dead. Dropping them and resolving once more is nearly always
	// enough, and the header has not been written yet, so the retry is invisible
	// to the player.
	stream, head, err := h.openRemuxWithHead(ctx, urls, startSeconds, audioStart)
	if err != nil {
		h.logger.Warn("open remux", "video", videoID, "error", err)

		h.remux.forget(sourceURL, height)
		urls, resolveErr := h.remux.ResolveRemuxURLs(ctx, sourceURL, height)
		if resolveErr != nil {
			h.logger.Warn("resolve remux urls again", "video", videoID, "error", resolveErr)
			http.Error(w, "cannot open stream", http.StatusBadGateway)
			return
		}
		stream, head, err = h.openRemuxWithHead(ctx, urls, startSeconds, audioStart)
		if err != nil {
			h.logger.Warn("open remux after re-resolve", "video", videoID, "error", err)
			http.Error(w, "cannot open stream", http.StatusBadGateway)
			return
		}
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

	// The bytes already read to prove the stream opened are part of the file and
	// must go out ahead of the rest, or the fMP4 loses its header.
	written, err := io.Copy(w, io.MultiReader(bytes.NewReader(head), stream))
	if err != nil && ctx.Err() == nil {
		h.logger.Warn("remux stream ended early", "video", videoID, "error", err)
	}
	// Whatever ffmpeg complained about, even when it went on to produce a
	// stream. One input can die while the other keeps muxing — an audio URL
	// refused after a second of sound leaves a file whose picture runs for a
	// minute and whose sound stops at 0.81s, which reaches the viewer as
	// PIPELINE_ERROR_DECODE and reaches the log as nothing at all, because
	// bytes flowed and this was called a success.
	if complaint := stderrOf(stream); complaint != "" {
		h.logger.Warn("live mux complained", "video", videoID, "bytes", written, "stderr", complaint)
		// And do not hand the same pair of URLs to the next viewer. They are
		// kept for 90 minutes, and `forget` was only reached when a mux failed
		// to open at all — so a pair that opens and then loses an input stays in
		// the cache, and every attempt at that video for the next hour and a
		// half reproduces the same half-broken stream. Costs one resolve; the
		// alternative costs the video.
		h.remux.forget(sourceURL, height)
	}
	h.logger.Info("live mux closed", "video", videoID, "bytes", written)
}

// How much of the mux to read before answering the request.
//
// It was 32 KiB, which only ever proved the stream *started*: enough for the
// initialisation segment and a fraction of a second of content. That left the
// case this is now sized for — a mux that opens perfectly and then loses one of
// its two inputs — indistinguishable from a mux that works, because bytes had
// flowed and the status was already committed as 200.
//
// About a second and a half of the 1080p stream at the bitrates seen here, so
// ffmpeg has been reading both inputs for a second by the time the question is
// asked. The delay is paid by a climb that happens out of sight while the viewer
// watches the low rendition, which is why it is affordable at all.
const remuxHeadBytes = 1536 * 1024

// openRemuxWithHead starts a mux and reads its first bytes, so that a stream
// which never produces any — or which produces some and then breaks — is
// reported as a failure to open rather than as a successful response the browser
// has to discover the truth about.
//
// The bytes are handed back rather than discarded: they are the start of the
// fMP4, and the initialisation segment is exactly the part a player cannot do
// without.
func (h *Handler) openRemuxWithHead(
	ctx context.Context, urls []string, startSeconds, audioStart float64,
) (io.ReadCloser, []byte, error) {
	stream, err := h.remux.OpenRemux(ctx, urls, startSeconds, audioStart)
	if err != nil {
		return nil, nil, err
	}

	head := make([]byte, remuxHeadBytes)
	n, err := io.ReadFull(stream, head)
	if n > 0 {
		// ffmpeg runs at `-loglevel error`, so anything it has written is a
		// fault rather than chatter — and by now it has been reading both inputs
		// for about a second, which is where the faults worth catching appear.
		// An input that dies here leaves a stream whose picture runs for a minute
		// and whose sound stops after 0.8s; the browser can only report that as
		// PIPELINE_ERROR_DECODE, long after this handler said 200.
		//
		// Returned as an error so it takes the path that already exists for a
		// mux that would not open: drop the cached URLs, resolve again, open
		// again — all before a single byte has reached the browser.
		if said := stderrOf(stream); said != "" {
			_ = stream.Close()
			return nil, nil, fmt.Errorf("mux complained while producing its first bytes: %s", said)
		}
		return stream, head[:n], nil
	}
	_ = stream.Close()
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	// "EOF" describes the pipe, not the fault. ffmpeg has just written the real
	// reason — a 403, a dead URL, a codec it will not copy — to its stderr, and
	// without this the log carried only the word EOF for every one of them.
	if reporter, ok := stream.(interface{ Stderr() string }); ok {
		if said := reporter.Stderr(); said != "" {
			return nil, nil, fmt.Errorf("%w: %s", err, said)
		}
	}
	return nil, nil, err
}

// stderrOf reads whatever ffmpeg wrote, from a stream that carries it. Empty
// when the stream does not, which is every implementation but the real one.
func stderrOf(stream io.ReadCloser) string {
	if reporter, ok := stream.(interface{ Stderr() string }); ok {
		return reporter.Stderr()
	}
	return ""
}

// refuse answers a permanent upstream refusal, and records it on the way.
//
// 409 rather than 502: nothing here is broken. The gateway is working, yt-dlp
// is working, and YouTube gave a clear answer — this video is not ours to
// fetch. A 502 says "try again", which is exactly what must not happen.
func (h *Handler) refuse(w http.ResponseWriter, ctx context.Context, sourceURL, videoID string, cause error) bool {
	if h.refusals == nil || cause == nil {
		return false
	}
	reason, permanent := domain.ReasonOf(domain.AsUnavailable(cause))
	if !permanent {
		return false
	}
	h.refusals.NoteUpstreamFailure(ctx, sourceURL, videoID, cause)
	writeUnavailable(w, reason)
	return true
}

// refuseKnown answers from what has already been recorded, without asking
// upstream at all.
func (h *Handler) refuseKnown(w http.ResponseWriter, ctx context.Context, sourceURL string) bool {
	if h.refusals == nil {
		return false
	}
	refusal, found := h.refusals.Refusal(ctx, sourceURL)
	if !found {
		return false
	}
	writeUnavailable(w, refusal.Reason)
	return true
}

func writeUnavailable(w http.ResponseWriter, reason domain.UnavailableReason) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code":   "video_unavailable",
		"reason": string(reason),
	})
}
