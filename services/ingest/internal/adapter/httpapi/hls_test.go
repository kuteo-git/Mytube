package httpapi

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// A fragmented MP4 as YouTube serves one: ftyp, moov, the segment index, then
// the segments themselves. Built here rather than fetched, so this exercises
// the whole route — resolve, index, playlist, byte range — with no network in
// it and no dependence on an upstream that refuses on its own schedule.
func syntheticTrack(segmentSizes []uint32) []byte {
	mp4box := func(typ string, payload []byte) []byte {
		b := make([]byte, 8+len(payload))
		binary.BigEndian.PutUint32(b, uint32(8+len(payload)))
		copy(b[4:], typ)
		copy(b[8:], payload)
		return b
	}
	be32 := func(v uint32) []byte {
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, v)
		return b
	}

	var sidx []byte
	sidx = append(sidx, be32(0)...)    // version 0
	sidx = append(sidx, be32(1)...)    // reference_ID
	sidx = append(sidx, be32(1000)...) // timescale
	sidx = append(sidx, be32(0)...)    // earliest_presentation_time
	sidx = append(sidx, be32(0)...)    // first_offset
	sidx = append(sidx, 0, 0)          // reserved
	sidx = append(sidx, byte(len(segmentSizes)>>8), byte(len(segmentSizes)))
	for _, size := range segmentSizes {
		sidx = append(sidx, be32(size)...) // media, top bit clear
		sidx = append(sidx, be32(2000)...) // two seconds at this timescale
		sidx = append(sidx, be32(0)...)    // SAP fields
	}

	file := append(mp4box("ftyp", make([]byte, 8)), mp4box("moov", make([]byte, 64))...)
	file = append(file, mp4box("sidx", sidx)...)
	for _, size := range segmentSizes {
		file = append(file, make([]byte, size)...)
	}
	return file
}

// trackServer serves one synthetic track, honouring ranges the way googlevideo
// does when it is willing: 206 with a Content-Range.
func trackServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, end, ok := parseByteRange(r.Header.Get("Range"))
		if !ok {
			http.Error(w, "a range is required", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if start >= int64(len(body)) {
			http.Error(w, "past the end", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if end >= int64(len(body)) {
			end = int64(len(body)) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[start : end+1])
	}))
}

// hlsRemuxer stands in for the downloader: it can describe the two tracks, which
// is what the HLS routes need and what the muxer's own interface does not offer.
type hlsRemuxer struct {
	tracks   domain.MediaTracks
	resolves int
}

func (h *hlsRemuxer) ResolveRemuxURLs(context.Context, string, int32) ([]string, error) {
	return []string{h.tracks.Best().URL, h.tracks.Audio.URL}, nil
}

func (h *hlsRemuxer) OpenRemux(context.Context, []string, float64, float64) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (h *hlsRemuxer) ProbeKeyframe(context.Context, string, float64) (float64, error) { return 0, nil }

func (h *hlsRemuxer) ResolveTracks(context.Context, string, int32) (domain.MediaTracks, error) {
	h.resolves++
	return h.tracks, nil
}

func newHLSHandler(t *testing.T) (*Handler, *hlsRemuxer, []byte) {
	t.Helper()
	video := syntheticTrack([]uint32{1000, 2000, 3000})
	audio := syntheticTrack([]uint32{500, 500, 500})
	vs := trackServer(t, video)
	as := trackServer(t, audio)
	t.Cleanup(vs.Close)
	t.Cleanup(as.Close)

	remux := &hlsRemuxer{tracks: domain.MediaTracks{
		Videos: []domain.MediaTrack{domain.MediaTrack{URL: vs.URL, Codec: "avc1.4d401f", Width: 1280, Height: 720, Bitrate: 400_000}},
		Audio: domain.MediaTrack{URL: as.URL, Codec: "mp4a.40.2", Bitrate: 128_000},
	}}
	return NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 720, discardLogger()), remux, video
}

func serve(t *testing.T, h *Handler, path, rangeHeader string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Routes(mux)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestMasterPlaylistDescribesBothTracks(t *testing.T) {
	h, _, _ := newHLSHandler(t)

	rec := serve(t, h, "/hls/abc/master.m3u8", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type = %q", got)
	}
	for _, want := range []string{
		`CODECS="avc1.4d401f,mp4a.40.2"`,
		"RESOLUTION=1280x720",
		// Video and audio bitrates together: BANDWIDTH is what a player budgets
		// with, and it will be fetching both.
		"BANDWIDTH=528000",
		`URI="audio.m3u8"`,
		"video-720.m3u8",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("master playlist is missing %q:\n%s", want, rec.Body.String())
		}
	}
}

func TestMediaPlaylistListsEverySegmentAsAByteRange(t *testing.T) {
	h, _, _ := newHLSHandler(t)

	rec := serve(t, h, "/hls/abc/video.m3u8", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if n := strings.Count(body, "#EXT-X-BYTERANGE"); n != 3 {
		t.Errorf("got %d segments, want 3:\n%s", n, body)
	}
	// Relative, so the playlist keeps pointing at whatever path served it — the
	// gateway's, which is the only address these files may be fetched through.
	if !strings.Contains(body, `#EXT-X-MAP:URI="video"`) {
		t.Errorf("map is not relative:\n%s", body)
	}
	if strings.Contains(body, "http://") {
		t.Errorf("playlist leaks an absolute URL, which would bypass the proxy:\n%s", body)
	}
}

func TestSegmentBytesAreServedForTheRangeAsked(t *testing.T) {
	h, _, video := newHLSHandler(t)

	// The second segment, taken from the playlist's own arithmetic rather than
	// worked out again here.
	indexed, err := domain.IndexTrack(video)
	if err != nil {
		t.Fatalf("IndexTrack: %v", err)
	}
	second := indexed.Segments[1]

	rec := serve(t, h, "/hls/abc/video", fmt.Sprintf("bytes=%d-%d", second.Offset, second.Offset+second.Length-1))

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if int64(rec.Body.Len()) != second.Length {
		t.Errorf("served %d bytes, want %d", rec.Body.Len(), second.Length)
	}
}

// Every request to this route comes from a playlist that named an exact range,
// so anything else is a caller that has not read the playlist — and answering it
// with the whole file is how an open-ended request reaches googlevideo, which is
// the one thing CLAUDE.md §4 forbids outright.
func TestSegmentBytesRefuseAnUnboundedRange(t *testing.T) {
	h, _, _ := newHLSHandler(t)

	for _, header := range []string{"", "bytes=0-", "bytes=-500", "nonsense"} {
		rec := serve(t, h, "/hls/abc/video", header)
		if rec.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Errorf("Range %q gave %d, want 416", header, rec.Code)
		}
	}
}

// The playlist is fetched once and then every segment of the video is fetched
// against the same URLs. Resolving per request would mean a yt-dlp process per
// segment, which on a ten-minute video is a hundred of them.
func TestTracksAreResolvedOnceForTheWholeVideo(t *testing.T) {
	h, remux, _ := newHLSHandler(t)

	serve(t, h, "/hls/abc/master.m3u8", "")
	serve(t, h, "/hls/abc/video.m3u8", "")
	serve(t, h, "/hls/abc/audio.m3u8", "")
	serve(t, h, "/hls/abc/video", "bytes=0-10")

	if remux.resolves != 1 {
		t.Fatalf("resolved %d times, want 1", remux.resolves)
	}
}

// A signed URL that dies mid-video must not break the rest of it.
//
// The playlist is fetched once, at the start, and every segment afterwards is
// fetched against the URLs resolved then — which googlevideo signs and expires.
// Only the playlist path dropped the cache entry, so a URL refused an hour in
// broke every remaining segment for the rest of the 90-minute TTL, and nothing
// ever asked for the playlist again to trigger a re-resolve. The player could
// report only a stream that stopped.
func TestARefusedSegmentResolvesAgainAndServesTheBytes(t *testing.T) {
	video := syntheticTrack([]uint32{1000, 2000, 3000})
	audio := syntheticTrack([]uint32{500, 500, 500})

	// Refuses once, exactly as an expired URL does, then serves normally — the
	// stand-in for the fresh URL a second resolve hands back.
	refusals := 0
	dying := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refusals == 0 {
			refusals++
			http.Error(w, "expired", http.StatusForbidden)
			return
		}
		start, end, _ := parseByteRange(r.Header.Get("Range"))
		if end >= int64(len(video)) {
			end = int64(len(video)) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(video)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(video[start : end+1])
	}))
	defer dying.Close()
	as := trackServer(t, audio)
	defer as.Close()

	remux := &hlsRemuxer{tracks: domain.MediaTracks{
		Videos: []domain.MediaTrack{domain.MediaTrack{URL: dying.URL, Codec: "avc1.4d401f", Width: 1280, Height: 720, Bitrate: 400_000}},
		Audio: domain.MediaTrack{URL: as.URL, Codec: "mp4a.40.2", Bitrate: 128_000},
	}}
	h := NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 720, discardLogger())

	rec := serve(t, h, "/hls/abc/video", "bytes=0-99")

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 — a refused segment should be retried on a fresh URL", rec.Code)
	}
	if got := rec.Body.Len(); got != 100 {
		t.Errorf("body = %d bytes, want 100", got)
	}
	// Twice: once to fill the cache for this request, once because the segment
	// was refused. Never a third time — a second refusal is a real answer.
	if remux.resolves != 2 {
		t.Errorf("resolves = %d, want 2", remux.resolves)
	}
}

// A codec yt-dlp could not name properly is refused rather than written into a
// playlist that fails silently on the one device with nothing behind it.
func TestAPlaylistIsNotWrittenWithACodecNoPlayerCanRead(t *testing.T) {
	video := syntheticTrack([]uint32{1000})
	audio := syntheticTrack([]uint32{500})
	vs := trackServer(t, video)
	defer vs.Close()
	as := trackServer(t, audio)
	defer as.Close()

	remux := &hlsRemuxer{tracks: domain.MediaTracks{
		// What yt-dlp says when it knows the family and nothing else.
		Videos: []domain.MediaTrack{domain.MediaTrack{URL: vs.URL, Codec: "vp9", Width: 1280, Height: 720}},
		Audio: domain.MediaTrack{URL: as.URL, Codec: "mp4a.40.2"},
	}}
	h := NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 720, discardLogger())

	rec := serve(t, h, "/hls/abc/master.m3u8", "")

	if rec.Code == http.StatusOK {
		t.Fatalf("served a playlist with CODECS=\"vp9\": %s", rec.Body.String())
	}
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

// A 500 from one CDN host is worth a fresh URL, which usually names another.
//
// Observed while watching through the app: `upstream answered 500` on a
// segment, reaching the browser as a 502, stalling hls.js and pausing the
// picture mid-video. The cache held the same dead URL for the rest of its
// ninety minutes and nothing re-resolved, because only 401/403/410 counted as
// a refusal worth retrying.
func TestASegmentAnsweredWithAServerErrorIsRetriedOnAFreshURL(t *testing.T) {
	video := syntheticTrack([]uint32{1000, 2000})
	audio := syntheticTrack([]uint32{500})

	failures := 0
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failures == 0 {
			failures++
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		start, end, _ := parseByteRange(r.Header.Get("Range"))
		if end >= int64(len(video)) {
			end = int64(len(video)) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(video)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(video[start : end+1])
	}))
	defer flaky.Close()
	as := trackServer(t, audio)
	defer as.Close()

	remux := &hlsRemuxer{tracks: domain.MediaTracks{
		Videos: []domain.MediaTrack{domain.MediaTrack{URL: flaky.URL, Codec: "avc1.4d401f", Width: 1280, Height: 720}},
		Audio: domain.MediaTrack{URL: as.URL, Codec: "mp4a.40.2"},
	}}
	h := NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 720, discardLogger())

	rec := serve(t, h, "/hls/abc/video", "bytes=0-99")

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if rec.Body.Len() != 100 {
		t.Errorf("body = %d bytes, want 100", rec.Body.Len())
	}
}

// One re-resolve serves the whole bad minute, not one per segment.
//
// A player asks for a segment every few seconds and a refusal wave turns all of
// them away. Without a cooldown that is a yt-dlp process per segment, against
// the address §8 risk 6 counts, at the moment upstream is already refusing.
func TestARefusalWaveResolvesOnceRatherThanOncePerSegment(t *testing.T) {
	video := syntheticTrack([]uint32{1000, 2000})
	audio := syntheticTrack([]uint32{500})

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer dead.Close()
	as := trackServer(t, audio)
	defer as.Close()

	remux := &hlsRemuxer{tracks: domain.MediaTracks{
		Videos: []domain.MediaTrack{domain.MediaTrack{URL: dead.URL, Codec: "avc1.4d401f", Width: 1280, Height: 720}},
		Audio: domain.MediaTrack{URL: as.URL, Codec: "mp4a.40.2"},
	}}
	h := NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 720, discardLogger())

	for i := 0; i < 5; i++ {
		serve(t, h, "/hls/abc/video", "bytes=0-99")
	}

	// One to fill the cache for the first request, one for the refusal it met.
	// The four that followed read the cooldown and did not add to it.
	if remux.resolves > 2 {
		t.Errorf("resolves = %d after five refused segments, want at most 2", remux.resolves)
	}
	_ = video
}

// The ladder: several heights in the master playlist, each fetchable.
//
// Until now this emitted exactly one rendition at the muxed tier's height,
// which made the player's 1080p menu entry a dead button — CLAUDE.md §5's one
// prohibition, reached from the server side. The point of HLS was never the
// container; it was that the browser can change quality without asking, which
// needs more than one rung to change between.
func TestTheMasterPlaylistOffersEveryRenditionAndEachOneIsFetchable(t *testing.T) {
	tall := syntheticTrack([]uint32{4000, 4000})
	short := syntheticTrack([]uint32{1000, 1000})
	audio := syntheticTrack([]uint32{500})
	ts, ss, as := trackServer(t, tall), trackServer(t, short), trackServer(t, audio)
	defer ts.Close()
	defer ss.Close()
	defer as.Close()

	remux := &hlsRemuxer{tracks: domain.MediaTracks{
		Videos: []domain.MediaTrack{
			{URL: ts.URL, Codec: "avc1.640028", Width: 1920, Height: 1080, Bitrate: 2_500_000},
			{URL: ss.URL, Codec: "avc1.4d401f", Width: 1280, Height: 720, Bitrate: 900_000},
		},
		Audio: domain.MediaTrack{URL: as.URL, Codec: "mp4a.40.2", Bitrate: 128_000},
	}}
	h := NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 720, discardLogger())

	master := serve(t, h, "/hls/abc/master.m3u8", "").Body.String()
	for _, want := range []string{
		"RESOLUTION=1920x1080", "video-1080.m3u8",
		"RESOLUTION=1280x720", "video-720.m3u8",
	} {
		if !strings.Contains(master, want) {
			t.Errorf("master playlist is missing %q:\n%s", want, master)
		}
	}

	// Each rung has to be reachable by the name the master playlist used, or the
	// ladder is decorative — and the player only discovers that on the switch,
	// which happens when the network is already struggling.
	for _, name := range []string{"video-1080", "video-720"} {
		if rec := serve(t, h, "/hls/abc/"+name+".m3u8", ""); rec.Code != http.StatusOK {
			t.Errorf("%s.m3u8 = %d, want 200", name, rec.Code)
		}
		if rec := serve(t, h, "/hls/abc/"+name, "bytes=0-49"); rec.Code != http.StatusPartialContent {
			t.Errorf("%s segment = %d, want 206", name, rec.Code)
		}
	}

	// The two rungs are different files, so their media playlists must describe
	// different byte ranges. Comparing the first bytes of each would not show
	// it: every fragmented MP4 opens with the same `ftyp`, which is exactly the
	// sort of sameness that makes a wrong mapping look right.
	tallList := serve(t, h, "/hls/abc/video-1080.m3u8", "").Body.String()
	shortList := serve(t, h, "/hls/abc/video-720.m3u8", "").Body.String()
	if tallList == shortList {
		t.Errorf("both rungs described the same bytes:\n%s", tallList)
	}
	if !strings.Contains(tallList, "video-1080") || !strings.Contains(shortList, "video-720") {
		t.Errorf("a media playlist points at the wrong track:\n%s\n%s", tallList, shortList)
	}
}

// A rung whose codec cannot be described is dropped; the playlist is not.
func TestARenditionWithAnUnusableCodecIsDroppedRatherThanTakingThePlaylistDown(t *testing.T) {
	tall := syntheticTrack([]uint32{4000})
	short := syntheticTrack([]uint32{1000})
	audio := syntheticTrack([]uint32{500})
	ts, ss, as := trackServer(t, tall), trackServer(t, short), trackServer(t, audio)
	defer ts.Close()
	defer ss.Close()
	defer as.Close()

	remux := &hlsRemuxer{tracks: domain.MediaTracks{
		Videos: []domain.MediaTrack{
			// yt-dlp knew the family and nothing else.
			{URL: ts.URL, Codec: "vp9", Width: 1920, Height: 1080, Bitrate: 2_500_000},
			{URL: ss.URL, Codec: "avc1.4d401f", Width: 1280, Height: 720, Bitrate: 900_000},
		},
		Audio: domain.MediaTrack{URL: as.URL, Codec: "mp4a.40.2", Bitrate: 128_000},
	}}
	h := NewHandler(remux, fixedSource{url: "https://youtu.be/abc"}, nil, 720, discardLogger())

	rec := serve(t, h, "/hls/abc/master.m3u8", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — one bad rung must not lose the ladder", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "video-1080") {
		t.Errorf("offered a rung a player cannot read:\n%s", body)
	}
	if !strings.Contains(body, "video-720") {
		t.Errorf("dropped the good rung too:\n%s", body)
	}
}
