package timedtext

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeYouTube answers the three requests this package makes: the watch page it
// reads the API key out of, the player call that lists the caption tracks, and
// the file itself.
type fakeYouTube struct {
	tracks     []map[string]any
	captionRes int
	playerRes  int
	body       string
	hits       int
}

func (f *fakeYouTube) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/watch", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<html>"INNERTUBE_API_KEY":"test-key"</html>`)
	})

	mux.HandleFunc("/youtubei/v1/player", func(w http.ResponseWriter, _ *http.Request) {
		if f.playerRes != 0 {
			w.WriteHeader(f.playerRes)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"captions": map[string]any{
				"playerCaptionsTracklistRenderer": map[string]any{
					"captionTracks": f.tracks,
				},
			},
		})
	})

	mux.HandleFunc("/timedtext", func(w http.ResponseWriter, _ *http.Request) {
		f.hits++
		if f.captionRes != 0 {
			w.WriteHeader(f.captionRes)
			return
		}
		_, _ = io.WriteString(w, f.body)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, f *fakeYouTube) (*Client, string) {
	t.Helper()
	srv := f.server(t)
	root := t.TempDir()
	c := New(root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.watch = srv.URL + "/watch?v=%s"
	c.player = srv.URL + "/youtubei/v1/player?key=%s"
	return c, root
}

func track(lang, kind, base string) map[string]any {
	return map[string]any{"languageCode": lang, "kind": kind, "baseUrl": base}
}

const vtt = "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nxin chào\n"

func TestOneListingThenOneDownload(t *testing.T) {
	f := &fakeYouTube{body: vtt}
	c, root := newTestClient(t, f)
	f.tracks = []map[string]any{
		track("en", "asr", c.watchHost()+"/timedtext?lang=en"),
		track("vi", "asr", c.watchHost()+"/timedtext?lang=vi"),
	}

	tracks, refused := c.FetchSubtitles(context.Background(), "", "abc123", 1080)

	if refused {
		t.Fatal("refused on a good answer")
	}
	// The whole claim of this package: one file, not four. Vietnamese over
	// English, because YouTube's own translation beats the one this app makes.
	if len(tracks) != 1 || tracks[0].Language != "vi" {
		t.Fatalf("got %+v, want one Vietnamese track", tracks)
	}
	if f.hits != 1 {
		t.Errorf("hit the caption endpoint %d times, want 1", f.hits)
	}
	if !tracks[0].Generated {
		t.Error("a track YouTube calls asr is machine-made")
	}

	// Named the way the rest of the app expects to find it.
	written := filepath.Join(root, "abc123", "1080p.mp4.vi.vtt")
	body, err := os.ReadFile(written)
	if err != nil {
		t.Fatalf("no file at %s: %v", written, err)
	}
	if !strings.HasPrefix(string(body), "WEBVTT") {
		t.Errorf("wrote %q, want the VTT upstream sent", string(body)[:10])
	}
}

// A regional tag is the same language. The web app matches subtitle codes
// exactly, so `en-US` would be fetched, written, published — and then offered
// to nobody, because it is not one of the codes the menu looks for.
func TestARegionalTagIsWrittenAsItsPrimarySubtag(t *testing.T) {
	f := &fakeYouTube{body: vtt}
	c, root := newTestClient(t, f)
	f.tracks = []map[string]any{track("en-US", "", c.watchHost()+"/timedtext?lang=en")}

	tracks, _ := c.FetchSubtitles(context.Background(), "", "abc123", 720)

	if len(tracks) != 1 || tracks[0].Language != "en" {
		t.Fatalf("got %+v, want language en", tracks)
	}
	if _, err := os.Stat(filepath.Join(root, "abc123", "720p.mp4.en.vtt")); err != nil {
		t.Errorf("expected 720p.mp4.en.vtt: %v", err)
	}
}

// 429 is the whole reason the retry table exists, and it has to be told apart
// from a video that simply has no captions: one is worth asking about again,
// the other is finished.
func TestARefusalIsReportedAsSuch(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusForbidden} {
		f := &fakeYouTube{captionRes: status}
		c, _ := newTestClient(t, f)
		f.tracks = []map[string]any{track("en", "asr", c.watchHost()+"/timedtext?lang=en")}

		tracks, refused := c.FetchSubtitles(context.Background(), "", "abc123", 1080)
		if !refused {
			t.Errorf("status %d: refused=false, want true", status)
		}
		if len(tracks) != 0 {
			t.Errorf("status %d: returned %+v, want nothing", status, tracks)
		}
	}
}

func TestNoCaptionsIsNotARefusal(t *testing.T) {
	f := &fakeYouTube{}
	c, _ := newTestClient(t, f)

	tracks, refused := c.FetchSubtitles(context.Background(), "", "abc123", 1080)

	if refused {
		t.Error("refused=true on a video that simply has no captions")
	}
	if len(tracks) != 0 {
		t.Errorf("got %+v, want nothing", tracks)
	}
	// And nothing was spent finding that out beyond the listing.
	if f.hits != 0 {
		t.Errorf("hit the caption endpoint %d times, want 0", f.hits)
	}
}

// Languages nobody in this household reads are left alone. Fetching one anyway
// spends the request that is scarce on a file nobody can use.
func TestATrackInAnUnreadLanguageIsNotFetched(t *testing.T) {
	f := &fakeYouTube{body: vtt}
	c, _ := newTestClient(t, f)
	f.tracks = []map[string]any{track("ja", "asr", c.watchHost()+"/timedtext?lang=ja")}

	tracks, refused := c.FetchSubtitles(context.Background(), "", "abc123", 1080)

	if refused || len(tracks) != 0 || f.hits != 0 {
		t.Errorf("tracks=%+v refused=%v hits=%d, want nothing fetched", tracks, refused, f.hits)
	}
}

// A player call that fails is not a refusal unless upstream said so. Reporting
// it as one would have the queue retrying a fault of ours.
func TestAPlayerErrorIsNotARefusal(t *testing.T) {
	f := &fakeYouTube{playerRes: http.StatusInternalServerError}
	c, _ := newTestClient(t, f)

	_, refused := c.FetchSubtitles(context.Background(), "", "abc123", 1080)
	if refused {
		t.Error("refused=true on a 500 from the player call")
	}
}
