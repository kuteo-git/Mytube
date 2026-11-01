package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// The most asked of the audio track in one request.
//
// CLAUDE.md §4: googlevideo's audio track answers 206 at ≤1 MiB and 403 at
// ≥2 MiB, measured 8 of 8. The same number as ffmpeg's -request_size, for the
// same reason; never raise it without measuring the audio track again.
const audioChunkBytes = 1 << 20

// handleAudio serves the sound of a video nobody has downloaded, for a speaker
// to fetch: the AAC track the HLS ladder already resolves, from the same cache,
// so a video opened on a phone and then cast costs one resolve.
//
// A speaker asks for the whole file, or for an open-ended range. Both are what
// googlevideo refuses, so the answer is assembled from bounded pieces.
func (h *Handler) handleAudio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	videoID := r.PathValue("videoId")

	resolver, ok := h.remux.remux.(TrackResolver)
	if !ok {
		http.Error(w, "audio not available", http.StatusNotImplemented)
		return
	}
	sourceURL, err := h.sources.SourceURLFor(ctx, videoID)
	if err != nil || sourceURL == "" {
		http.Error(w, "unknown video", http.StatusNotFound)
		return
	}
	if h.refuseKnown(w, ctx, sourceURL) {
		return
	}

	start, end, ranged, ok := clientRange(r.Header.Get("Range"))
	if !ok {
		http.Error(w, "unsupported range", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	key := sourceURL + "|hls|" + strconv.Itoa(int(hlsMaxHeight))
	tracks, cached := h.hls.get(key)
	if !cached {
		if tracks, err = h.resolveOnce(ctx, videoID, key, resolver, sourceURL); err != nil {
			h.logger.Warn("resolve audio track", "video", videoID, "error", err)
			if h.refuse(w, ctx, sourceURL, videoID, err) {
				return
			}
			http.Error(w, "cannot resolve media", http.StatusBadGateway)
			return
		}
	}
	track := &audioTrack{h: h, videoID: videoID, key: key, resolver: resolver, sourceURL: sourceURL, url: tracks.Audio.URL}

	firstEnd := start + audioChunkBytes - 1
	if end >= 0 {
		firstEnd = min(firstEnd, end)
	}
	piece, total, err := track.read(ctx, start, firstEnd)
	if err != nil {
		var status upstreamStatus
		if errors.As(err, &status) && status.code == http.StatusRequestedRangeNotSatisfiable {
			http.Error(w, "past the end", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if ctx.Err() == nil {
			h.logger.Warn("audio track", "video", videoID, "error", err)
		}
		http.Error(w, "cannot read media", http.StatusBadGateway)
		return
	}
	if end < 0 || end >= total {
		end = total - 1
	}

	w.Header().Set("Content-Type", "audio/mp4")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	if ranged {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	if _, err := w.Write(piece); err != nil {
		return
	}

	for pos := start + int64(len(piece)); pos <= end; {
		piece, _, err := track.read(ctx, pos, min(pos+audioChunkBytes-1, end))
		if err != nil {
			// The status is already sent; a short body is all that is left to
			// say, and the speaker treats it as a failed fetch.
			if ctx.Err() == nil {
				h.logger.Warn("audio track cut short", "video", videoID, "at", pos, "error", err)
			}
			return
		}
		if _, err := w.Write(piece); err != nil {
			return
		}
		pos += int64(len(piece))
	}
}

// audioTrack reads bounded pieces of one track, resolving it again once when
// upstream refuses its URL.
type audioTrack struct {
	h         *Handler
	videoID   string
	key       string
	resolver  TrackResolver
	sourceURL string
	url       string
	retried   bool
}

func (a *audioTrack) read(ctx context.Context, start, end int64) ([]byte, int64, error) {
	piece, total, err := readPiece(ctx, a.url, start, end)
	if refusedUpstream(err) && !a.retried && a.h.hls.mayResolve(a.key, resolveCooldown) {
		a.retried = true
		a.h.logger.Warn("audio track refused, resolving again", "video", a.videoID, "error", err)
		a.h.hls.forget(a.key)
		fresh, resolveErr := a.resolver.ResolveTracks(ctx, a.sourceURL, hlsMaxHeight)
		if resolveErr != nil {
			return nil, 0, resolveErr
		}
		a.h.hls.put(a.key, fresh)
		a.url = fresh.Audio.URL
		piece, total, err = readPiece(ctx, a.url, start, end)
	}
	return piece, total, err
}

func readPiece(ctx context.Context, url string, start, end int64) ([]byte, int64, error) {
	body, _, header, err := openRange(ctx, url, start, end)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = body.Close() }()
	piece, err := io.ReadAll(body)
	if err != nil {
		return nil, 0, err
	}
	total, ok := totalOf(header.Get("Content-Range"))
	if !ok {
		return nil, 0, fmt.Errorf("upstream answered without a usable Content-Range: %q", header.Get("Content-Range"))
	}
	return piece, total, nil
}

// totalOf reads the size from "bytes 0-1048575/3456789".
func totalOf(contentRange string) (int64, bool) {
	_, size, found := strings.Cut(contentRange, "/")
	if !found {
		return 0, false
	}
	total, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	return total, err == nil && total > 0
}

// clientRange reads what a speaker asked for: nothing (the whole file),
// "bytes=N-" or "bytes=N-M". end is -1 for "to the end". Suffix and multiple
// ranges are not something a speaker sends, and are refused.
func clientRange(header string) (start, end int64, ranged, ok bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0, -1, false, true
	}
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	from, to, found := strings.Cut(spec, "-")
	if !found || strings.TrimSpace(from) == "" {
		return 0, 0, false, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(from), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false, false
	}
	if strings.TrimSpace(to) == "" {
		return start, -1, true, true
	}
	end, err = strconv.ParseInt(strings.TrimSpace(to), 10, 64)
	if err != nil || end < start {
		return 0, 0, false, false
	}
	return start, end, true, true
}
