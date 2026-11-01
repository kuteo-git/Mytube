package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
)

type oneVideo struct {
	catalogv1connect.CatalogServiceClient
	video   *catalogv1.Video
	evicted atomic.Int32
}

func (c *oneVideo) GetVideo(context.Context, *connect.Request[catalogv1.GetVideoRequest]) (*connect.Response[catalogv1.GetVideoResponse], error) {
	return connect.NewResponse(&catalogv1.GetVideoResponse{Video: c.video}), nil
}

func (c *oneVideo) SetMediaState(_ context.Context, req *connect.Request[catalogv1.SetMediaStateRequest]) (*connect.Response[catalogv1.SetMediaStateResponse], error) {
	if req.Msg.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_EVICTED {
		c.evicted.Add(1)
	}
	return connect.NewResponse(&catalogv1.SetMediaStateResponse{}), nil
}

type submitCounter struct {
	ingestv1connect.IngestServiceClient
	submits atomic.Int32
}

func (s *submitCounter) Submit(context.Context, *connect.Request[ingestv1.SubmitRequest]) (*connect.Response[ingestv1.SubmitResponse], error) {
	s.submits.Add(1)
	return connect.NewResponse(&ingestv1.SubmitResponse{}), nil
}

const audioBytes = "0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnopqrstuvwxyz"

type audioRig struct {
	g       *Gateway
	catalog *oneVideo
	ingest  *submitCounter
	root    string
	cuts    *atomic.Int32
	ingestR *[]string
}

func newAudioRig(t *testing.T, v *catalogv1.Video) audioRig {
	t.Helper()
	root := t.TempDir()
	var ranges []string
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.URL.Path+" "+r.Header.Get("Range"))
		mu.Unlock()
		w.Header().Set("Content-Type", "audio/mp4")
		_, _ = w.Write([]byte("from ingest"))
	}))
	t.Cleanup(upstream.Close)

	cuts := &atomic.Int32{}
	cat := &oneVideo{video: v}
	ing := &submitCounter{}
	g := &Gateway{
		logger: discardLogger(), catalog: cat, ingest: ing, mediaRoot: root, configDir: t.TempDir(),
		ingestBaseURL: upstream.URL, streamClient: &http.Client{},
		extractAudio: func(_ context.Context, src, dst string) error {
			cuts.Add(1)
			time.Sleep(20 * time.Millisecond)
			return os.WriteFile(dst, []byte(audioBytes), 0o644)
		},
	}
	return audioRig{g: g, catalog: cat, ingest: ing, root: root, cuts: cuts, ingestR: &ranges}
}

func (r audioRig) get(t *testing.T, rangeHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/videos/abc/audio", nil)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	rec := httptest.NewRecorder()
	r.g.Routes().ServeHTTP(rec, req)
	return rec
}

func readyVideo() *catalogv1.Video {
	return &catalogv1.Video{
		Id: "abc", SourceUrl: "https://www.youtube.com/watch?v=abc",
		MediaState: catalogv1.MediaState_MEDIA_STATE_READY, MediaPath: "abc/1080p.mp4",
	}
}

func putMedia(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "abc", "1080p.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestADownloadedVideoIsCutOnceForEveryoneAsking(t *testing.T) {
	r := newAudioRig(t, readyVideo())
	putMedia(t, r.root)

	var wg sync.WaitGroup
	codes := make([]int, 10)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := r.get(t, "")
			codes[i] = rec.Code
			if rec.Body.String() != audioBytes {
				t.Errorf("request %d body = %q", i, rec.Body.String())
			}
		}(i)
	}
	wg.Wait()
	if n := r.cuts.Load(); n != 1 {
		t.Fatalf("ffmpeg ran %d times for ten requests, want 1", n)
	}
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("request %d status = %d", i, c)
		}
	}
	if _, err := os.Stat(filepath.Join(r.root, "abc", "audio.m4a")); err != nil {
		t.Fatalf("audio.m4a not kept: %v", err)
	}
	if r.get(t, "").Code != http.StatusOK || r.cuts.Load() != 1 {
		t.Fatal("a kept audio.m4a was cut again")
	}
}

func TestADownloadedVideoAnswersARange(t *testing.T) {
	r := newAudioRig(t, readyVideo())
	putMedia(t, r.root)
	rec := r.get(t, "bytes=100-199")
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 100 {
		t.Fatalf("status = %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if rec.Body.String() != audioBytes[100:200] {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/mp4" {
		t.Fatalf("content type = %q", got)
	}
}

func TestAVideoNotOnDiskIsProxiedAndFetched(t *testing.T) {
	v := readyVideo()
	v.MediaState, v.MediaPath = catalogv1.MediaState_MEDIA_STATE_ABSENT, ""
	r := newAudioRig(t, v)
	rec := r.get(t, "bytes=0-")
	if rec.Code != http.StatusOK || rec.Body.String() != "from ingest" {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	if got := strings.Join(*r.ingestR, ","); got != "/audio/abc bytes=0-" {
		t.Fatalf("ingest was asked %q", got)
	}
	waitFor(t, func() bool { return r.ingest.submits.Load() == 1 }, "the download was not scheduled")
	if r.cuts.Load() != 0 {
		t.Fatal("ffmpeg ran for a video with no file")
	}
}

func TestAFileGoneByHandIsMarkedAndProxied(t *testing.T) {
	r := newAudioRig(t, readyVideo())
	// The root is there; the file is not.
	rec := r.get(t, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "from ingest" {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	if r.catalog.evicted.Load() != 1 {
		t.Fatal("a missing file was not marked evicted")
	}
}

func TestAnUnmountedLibraryWritesNothing(t *testing.T) {
	r := newAudioRig(t, readyVideo())
	r.g.mediaRoot = filepath.Join(r.root, "not-mounted")
	rec := r.get(t, "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "media_root_unavailable") {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
	if r.catalog.evicted.Load() != 0 || len(*r.ingestR) != 0 {
		t.Fatal("an unmounted library was written to or proxied")
	}
}

func TestABroadcastHasNoAudioToCast(t *testing.T) {
	for _, v := range []*catalogv1.Video{
		{Id: "abc", LiveStatus: "is_live", IsLiveNow: true},
		{Id: "abc", LiveStatus: "is_upcoming"},
	} {
		r := newAudioRig(t, v)
		if rec := r.get(t, ""); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d, want 422", v.GetLiveStatus(), rec.Code)
		}
	}
}

func TestARefusedVideoIsAConflict(t *testing.T) {
	r := newAudioRig(t, &catalogv1.Video{Id: "abc", MediaState: catalogv1.MediaState_MEDIA_STATE_UNAVAILABLE})
	if rec := r.get(t, ""); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

// The real cut, through the real ffmpeg: the sound copied out of an mp4 is
// AAC, not re-encoded, and nothing but it.
func TestFFmpegCutsTheSoundWithoutReencoding(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "1080p.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=d=2:s=64x64",
		"-f", "lavfi", "-i", "sine=d=2", "-c:v", "mpeg4", "-c:a", "aac", "-shortest", src).CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v %s", err, out)
	}
	dst := filepath.Join(dir, "audio.m4a")
	if err := ffmpegExtractAudio(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name",
		"-of", "csv=p=0", dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "aac,audio" {
		t.Fatalf("streams = %q, want only aac audio", got)
	}
}

func waitFor(t *testing.T, ok func() bool, msg string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
