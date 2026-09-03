package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
)

// Watching a broadcast that is still on air.
//
// Every other tier here is built from a finished file: the adaptive resolve
// wants an https URL with a segment index inside it, and a live video publishes
// no such thing — measured on a real broadcast, all seven formats were
// `m3u8_native` and none was a plain file. What YouTube does publish for live is
// HLS, which is what the player already speaks.
//
// So nothing is indexed and nothing is built from byte ranges. The playlists are
// YouTube's own, passed through. Three things still have to happen on the way:
//
//   - **The URLs are signed to the address that resolved them** (§4), so every
//     byte comes back through here, exactly as the recorded HLS routes do.
//   - **googlevideo sends no CORS header.** Measured: the manifest answers 200
//     to curl and 200 to a request carrying an Origin, with no
//     `access-control-allow-origin` in the reply — so hls.js, which fetches over
//     XHR, is refused by the browser before the bytes are ever in question. That
//     alone makes a proxy mandatory rather than merely tidy.
//   - **There is no master playlist.** YouTube offers the sound and the pictures
//     as separate media playlists and nothing that names both, so the master the
//     browser reads is written here.

// liveSegmentHost is the only host this will fetch on a caller's behalf.
//
// Segment addresses arrive as opaque signed URLs and are handed back to the
// browser rewritten, which means the browser hands them back here. Without this
// check that is an open proxy: anything on the LAN could route any request
// through this machine and wear its address.
const liveSegmentHost = ".googlevideo.com"

func (g *Gateway) handleLiveMaster(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	videoID := r.PathValue("id")

	video, err := g.catalog.GetVideo(ctx, connect.NewRequest(&catalogv1.GetVideoRequest{
		VideoId: videoID,
	}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}

	live, err := g.resolveLive(ctx, video.Msg.GetVideo().GetSourceUrl())
	if err != nil {
		g.logger.Warn("resolve live", "video", videoID, "error", err)
		http.Error(w, "cannot resolve broadcast", http.StatusBadGateway)
		return
	}
	if !live.GetIsLive() {
		// Not an error, and not this route's business: a finished broadcast is
		// an ordinary video and /stream already knows what to do with one.
		http.Error(w, "not broadcasting", http.StatusNotFound)
		return
	}

	var audio *ingestv1.LiveRendition
	video480 := make([]*ingestv1.LiveRendition, 0, 4)
	for _, rend := range live.GetRenditions() {
		if rend.GetAudioOnly() {
			if audio == nil {
				audio = rend
			}
			continue
		}
		video480 = append(video480, rend)
	}
	if audio == nil || len(video480) == 0 {
		http.Error(w, "broadcast offers no usable rendition", http.StatusBadGateway)
		return
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	fmt.Fprintf(&b,
		"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"Audio\",DEFAULT=YES,AUTOSELECT=YES,URI=%q\n",
		livePlaylistURL(videoID, audio.GetUrl()))
	for _, rend := range video480 {
		// CODECS carries the picture only. yt-dlp leaves the sound's codec
		// blank on YouTube's HLS audio playlists, and a guessed value in this
		// attribute is worse than a missing one — a player reads it to decide
		// whether it can play the stream before fetching a byte.
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,CODECS=%q,RESOLUTION=%dx%d,AUDIO=\"audio\"\n",
			rend.GetBitrate()+audio.GetBitrate(), rend.GetCodec(),
			rend.GetWidth(), rend.GetHeight())
		b.WriteString(livePlaylistURL(videoID, rend.GetUrl()) + "\n")
	}

	writePlaylist(w, b.String())
}

// handleLivePlaylist passes one of YouTube's media playlists through, with every
// segment address rewritten to come back here.
//
// Never cached. A live playlist is a window that moves every few seconds — the
// one measured had `#EXT-X-TARGETDURATION:5` — so a cached copy is a viewer
// stuck a minute behind, then stalled when the segments it names age out.
func (g *Gateway) handleLivePlaylist(w http.ResponseWriter, r *http.Request) {
	target, ok := decodeLiveURL(r.URL.Query().Get("u"))
	if !ok {
		http.Error(w, "bad playlist reference", http.StatusBadRequest)
		return
	}

	body, status, _, err := openRangeless(r.Context(), target)
	if err != nil {
		g.logger.Warn("live playlist", "video", r.PathValue("id"), "error", err)
		http.Error(w, "cannot read broadcast", http.StatusBadGateway)
		return
	}
	defer func() { _ = body.Close() }()
	if status >= http.StatusBadRequest {
		http.Error(w, "upstream refused the broadcast", http.StatusBadGateway)
		return
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, "cannot read broadcast", http.StatusBadGateway)
		return
	}

	videoID := r.PathValue("id")
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Everything that is not a tag or blank is an address, and every address
		// in here is absolute and signed.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines[i] = liveSegmentURL(videoID, trimmed)
	}
	writePlaylist(w, strings.Join(lines, "\n"))
}

// handleLiveSegment fetches one segment on the browser's behalf.
func (g *Gateway) handleLiveSegment(w http.ResponseWriter, r *http.Request) {
	target, ok := decodeLiveURL(r.URL.Query().Get("u"))
	if !ok {
		http.Error(w, "bad segment reference", http.StatusBadRequest)
		return
	}

	body, status, header, err := openRangeless(r.Context(), target)
	if err != nil {
		// A segment the client walked away from is not a fault; hls.js abandons
		// them routinely when it catches up to the live edge.
		if r.Context().Err() != nil {
			return
		}
		g.logger.Warn("live segment", "video", r.PathValue("id"), "error", err)
		http.Error(w, "cannot read broadcast", http.StatusBadGateway)
		return
	}
	defer func() { _ = body.Close() }()

	for _, name := range []string{"Content-Type", "Content-Length"} {
		if v := header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	copyStream(w, body)
}

// The playlist media type, and no caching. Both matter: a live playlist is a
// window that moves every few seconds, and a cached one leaves a viewer stuck
// behind it and then stalled when the segments it names age out.
func writePlaylist(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, body)
}

// Its own client with a modest timeout: a live segment is five seconds of video
// and a playlist is a few kilobytes, so anything slow here is broken rather
// than large.
var liveClient = &http.Client{Timeout: 30 * time.Second}

func livePlaylistURL(videoID, target string) string {
	return "/api/live/" + url.PathEscape(videoID) + "/playlist.m3u8?u=" + encodeLiveURL(target)
}

func liveSegmentURL(videoID, target string) string {
	return "/api/live/" + url.PathEscape(videoID) + "/segment?u=" + encodeLiveURL(target)
}

// The address travels in the request rather than in a table on this side.
//
// A live playlist is rewritten every few seconds and names segments by an
// ever-advancing sequence number; a server-side map of them would be a cache
// with an eviction policy, for no gain — the URL is already opaque, already
// signed, and already short-lived. The host check on the way back is what keeps
// this from being an open proxy.
func encodeLiveURL(target string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(target))
}

func decodeLiveURL(encoded string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}
	parsed, err := url.Parse(string(raw))
	if err != nil || parsed.Scheme != "https" {
		return "", false
	}
	if !strings.HasSuffix(strings.ToLower(parsed.Hostname()), liveSegmentHost) {
		return "", false
	}
	return string(raw), true
}

// openRangeless is the plain GET the live path needs: no Range header, because
// a live segment is a whole small file and a playlist is text.
//
// The rule against open-ended requests (§4) is about `videoplayback` on a
// finished file, where asking for the rest of it draws a redirect to a host
// that then refuses. These are already bounded — five seconds each, measured at
// `#EXTINF:5.138`.
func openRangeless(ctx context.Context, target string) (io.ReadCloser, int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	resp, err := liveClient.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	return resp.Body, resp.StatusCode, resp.Header, nil
}
