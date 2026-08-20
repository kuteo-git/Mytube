package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// HLS: the browser combines the two tracks, so nothing here has to.
//
// Three routes, and the shape of the playlist is what makes them few:
//
//	/hls/{id}/master.m3u8    the video rendition and the audio group
//	/hls/{id}/{kind}.m3u8    one track, as a list of byte ranges
//	/hls/{id}/{kind}         those bytes, proxied
//
// The URIs inside the playlists are relative, so a playlist served under the
// gateway's path keeps pointing at the gateway without knowing what that path
// is. That matters more than it sounds: these files are signed to the address
// that resolved them (CLAUDE.md §4), so every byte has to come back through us.

// TrackResolver is the part of the downloader that can describe the two
// adaptive files rather than merely locate them.
//
// Asserted rather than required, so the fakes in this package's other tests —
// which stand in for a muxer — do not have to grow a method they have nothing
// to do with. The same shape as the Stderr() assertion in remux.go.
type TrackResolver interface {
	ResolveTracks(ctx context.Context, videoURL string, height int32) (domain.MediaTracks, error)
}

// How much of a track's front to read before giving up on finding its index.
//
// Measured on this library: `ftyp`+`moov` ends at 740 bytes and the segment
// index at 1408, for a 9.8 MB video track. 64 KiB is generous by a factor of
// forty, and it is one bounded request — the only kind googlevideo answers.
const hlsHeadBytes = 64 * 1024

// The wider second attempt, for a file whose header is unusually large. Still
// bounded, and still one request.
const hlsHeadBytesWide = 512 * 1024

// How long a resolved pair of track URLs is reused.
//
// The same reasoning and the same window as the muxer's cache: the playlist is
// fetched once and then every segment of the video is fetched against those
// URLs, so resolving per request would mean a yt-dlp process per segment.
const hlsTrackTTL = 90 * time.Minute

type hlsEntry struct {
	tracks    domain.MediaTracks
	expiresAt time.Time
}

type hlsCache struct {
	mu      sync.Mutex
	entries map[string]hlsEntry
}

func (c *hlsCache) get(key string) (domain.MediaTracks, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return domain.MediaTracks{}, false
	}
	return e.tracks, true
}

func (c *hlsCache) put(key string, tracks domain.MediaTracks) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]hlsEntry{}
	}
	c.entries[key] = hlsEntry{tracks: tracks, expiresAt: time.Now().Add(hlsTrackTTL)}
	// Bounded rather than swept on a timer, as elsewhere here: a household
	// watches a handful of videos at a time, and a map that only grows while the
	// process lives is a leak however slow.
	if len(c.entries) > 256 {
		for k, e := range c.entries {
			if time.Now().After(e.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
}

func (c *hlsCache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// handleHLS answers all three routes, told apart by what was asked for.
func (h *Handler) handleHLS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	videoID := r.PathValue("videoId")
	name := r.PathValue("name")

	resolver, ok := h.remux.remux.(TrackResolver)
	if !ok {
		http.Error(w, "hls not available", http.StatusNotImplemented)
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

	key := sourceURL + "|" + strconv.Itoa(int(h.liveHeight))
	tracks, cached := h.hls.get(key)
	if !cached {
		tracks, err = resolver.ResolveTracks(ctx, sourceURL, h.liveHeight)
		if err != nil {
			h.logger.Warn("resolve hls tracks", "video", videoID, "error", err)
			if h.refuse(w, ctx, sourceURL, videoID, err) {
				return
			}
			http.Error(w, "cannot resolve media", http.StatusBadGateway)
			return
		}
		h.hls.put(key, tracks)
	}

	switch {
	case name == "master.m3u8":
		h.writeMasterPlaylist(w, videoID, tracks)
	case strings.HasSuffix(name, ".m3u8"):
		h.writeMediaPlaylist(w, r, videoID, key, tracks, strings.TrimSuffix(name, ".m3u8"))
	default:
		h.proxyTrackBytes(w, r, videoID, key, tracks, name, resolver, sourceURL)
	}
}

func (h *Handler) writeMasterPlaylist(w http.ResponseWriter, videoID string, tracks domain.MediaTracks) {
	// Refused rather than written wrong. A player reads CODECS to decide
	// whether it can play this at all, so a value it does not understand is a
	// silent refusal — no request, no log line, a generic element error — and
	// on iPhone there is no MediaSource behind it to catch the fall. yt-dlp
	// says "vp9" and "none" as readily as it says "avc1.4d401f", and neither of
	// those is an RFC 6381 value.
	for _, codec := range []string{tracks.Video.Codec, tracks.Audio.Codec} {
		if !domain.ValidCodec(codec) {
			h.logger.Warn("hls codec not usable in a playlist",
				"video", videoID, "video_codec", tracks.Video.Codec, "audio_codec", tracks.Audio.Codec)
			http.Error(w, "media codec cannot be described", http.StatusBadGateway)
			return
		}
	}

	body := domain.MasterPlaylist([]domain.Rendition{{
		URI:       "video.m3u8",
		Codecs:    tracks.Video.Codec,
		Bandwidth: tracks.Video.Bitrate + tracks.Audio.Bitrate,
		Width:     tracks.Video.Width,
		Height:    tracks.Video.Height,
	}}, "audio.m3u8", tracks.Audio.Codec)
	writePlaylist(w, body)
}

func (h *Handler) writeMediaPlaylist(
	w http.ResponseWriter, r *http.Request, videoID, key string, tracks domain.MediaTracks, kind string,
) {
	track, ok := trackOf(tracks, kind)
	if !ok {
		http.Error(w, "unknown track", http.StatusNotFound)
		return
	}

	indexed, err := h.indexTrack(r.Context(), track.URL)
	if err != nil {
		// The URLs may simply have died in the cache, which is the one thing a
		// second resolve fixes and nothing else does.
		h.hls.forget(key)
		h.logger.Warn("index hls track", "video", videoID, "kind", kind, "error", err)
		http.Error(w, "cannot read media", http.StatusBadGateway)
		return
	}
	// Relative, so the playlist points back through whatever path it was served
	// under without being told what that path is.
	writePlaylist(w, domain.MediaPlaylist(indexed, kind))
}

// indexTrack fetches the front of a track and reads its segment index, asking
// for more only when the first, small request did not reach it.
func (h *Handler) indexTrack(ctx context.Context, url string) (domain.Track, error) {
	for _, size := range []int64{hlsHeadBytes, hlsHeadBytesWide} {
		head, err := fetchRange(ctx, url, 0, size-1)
		if err != nil {
			return domain.Track{}, err
		}
		indexed, err := domain.IndexTrack(head)
		if err == nil {
			return indexed, nil
		}
		if err != domain.ErrNoSegmentIndex {
			return domain.Track{}, err
		}
	}
	return domain.Track{}, fmt.Errorf("no segment index in the first %d bytes", hlsHeadBytesWide)
}

// proxyTrackBytes serves the byte range the player asked for.
//
// The range is the player's, taken from the playlist this handler wrote, so it
// is already exact. It is bounded anyway: an open-ended request is what
// googlevideo answers with a redirect to a host that then refuses, and a
// playlist is not the only thing that can put a request here.
func (h *Handler) proxyTrackBytes(
	w http.ResponseWriter, r *http.Request, videoID, key string, tracks domain.MediaTracks, kind string,
	resolver TrackResolver, sourceURL string,
) {
	track, ok := trackOf(tracks, kind)
	if !ok {
		http.Error(w, "unknown track", http.StatusNotFound)
		return
	}

	start, end, ok := parseByteRange(r.Header.Get("Range"))
	if !ok {
		http.Error(w, "a range is required", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	body, status, header, err := openRange(r.Context(), track.URL, start, end)

	// A refused segment is almost always a signed URL that has expired, and the
	// cache holds it for ninety minutes.
	//
	// Only the playlist path used to drop the entry, and a playlist is fetched
	// once at the start — so a URL that died halfway through a video broke every
	// remaining segment until the cache aged out, with the player reporting
	// nothing but a stream that stopped. Nothing re-resolved, because nothing
	// asked for the playlist again.
	//
	// Once, deliberately. A second refusal is a real answer — the wave §4
	// documents refuses everything from this address for a few minutes — and
	// asking a third time only adds to whatever count upstream is keeping.
	if refusedUpstream(err) {
		h.logger.Warn("hls segment refused, resolving again",
			"video", videoID, "kind", kind, "error", err)
		h.hls.forget(key)

		fresh, resolveErr := resolver.ResolveTracks(r.Context(), sourceURL, h.liveHeight)
		if resolveErr != nil {
			h.logger.Warn("resolve hls tracks again", "video", videoID, "error", resolveErr)
			http.Error(w, "cannot read media", http.StatusBadGateway)
			return
		}
		h.hls.put(key, fresh)
		if track, ok = trackOf(fresh, kind); !ok {
			http.Error(w, "unknown track", http.StatusNotFound)
			return
		}
		body, status, header, err = openRange(r.Context(), track.URL, start, end)
	}

	if err != nil {
		h.logger.Warn("hls segment", "video", videoID, "kind", kind, "error", err)
		http.Error(w, "cannot read media", http.StatusBadGateway)
		return
	}
	defer func() { _ = body.Close() }()

	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range"} {
		if v := header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.WriteHeader(status)
	_, _ = io.Copy(w, body)
}

// One client for both, with redirects followed: googlevideo hands a reader to
// the CDN host that actually holds the bytes, and refusing to follow that is
// how a working URL gets mistaken for a dead one (see ytdlp/verify.go).
var hlsClient = &http.Client{Timeout: 60 * time.Second}

func openRange(ctx context.Context, url string, start, end int64) (io.ReadCloser, int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := hlsClient.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		_ = resp.Body.Close()
		// The status travels with the error rather than being flattened into
		// its text. A refusal and a genuine fault arrive the same way here — a
		// successful round trip carrying bad news — and only the caller knows
		// that one of them is worth a fresh URL.
		return nil, 0, nil, upstreamStatus{code: resp.StatusCode}
	}
	return resp.Body, resp.StatusCode, resp.Header, nil
}

func fetchRange(ctx context.Context, url string, start, end int64) ([]byte, error) {
	body, _, _, err := openRange(ctx, url, start, end)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return io.ReadAll(body)
}

func trackOf(tracks domain.MediaTracks, kind string) (domain.MediaTrack, bool) {
	switch kind {
	case "video":
		return tracks.Video, true
	case "audio":
		return tracks.Audio, true
	}
	return domain.MediaTrack{}, false
}

func writePlaylist(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, body)
}

// parseByteRange reads "bytes=start-end", and only that shape.
//
// An open-ended or absent range is refused rather than widened to the whole
// file: every request this route serves comes from a playlist that named an
// exact range, so anything else is a caller that has not read the playlist.
func parseByteRange(header string) (start, end int64, ok bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !found {
		return 0, 0, false
	}
	from, to, found := strings.Cut(spec, "-")
	if !found || from == "" || to == "" {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(from), 10, 64)
	if err != nil {
		return 0, 0, false
	}
	end, err = strconv.ParseInt(strings.TrimSpace(to), 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}
	return start, end, true
}

// upstreamStatus is a status googlevideo answered with, kept as a value so the
// caller can tell a refusal from a fault.
type upstreamStatus struct{ code int }

func (e upstreamStatus) Error() string { return fmt.Sprintf("upstream answered %d", e.code) }

// refusedUpstream reports whether googlevideo turned this request away in a way
// a freshly resolved URL might answer.
//
// 403 is the refusal §4 is about and by far the common one; 401 and 410 mean
// the same thing here — the URL is no longer good — and cost nothing to cover.
// Deliberately not 404 or 416: those are about what was asked for rather than
// about the credential, and re-resolving would ask again identically.
func refusedUpstream(err error) bool {
	var status upstreamStatus
	if !errors.As(err, &status) {
		return false
	}
	return status.code == http.StatusUnauthorized ||
		status.code == http.StatusForbidden ||
		status.code == http.StatusGone
}
