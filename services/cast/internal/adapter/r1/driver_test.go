package r1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// fakeR1 answers the routes of the R1's ControlServer.kt that the driver uses.
type fakeR1 struct {
	mu       sync.Mutex
	paths    []string
	playBody map[string]any
	media    string
	volume   int
	delay    time.Duration
}

func (f *fakeR1) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.RequestURI())
	switch r.URL.Path {
	case "/api/media/play":
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &f.playBody)
		_, _ = w.Write([]byte(`{"ok":true}`))
	case "/api/media/state":
		_, _ = w.Write([]byte(f.media))
	case "/api/state":
		_ = json.NewEncoder(w).Encode(map[string]any{"volume": f.volume, "volume_steps": 15})
	default:
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

func setup(t *testing.T, fake *fakeR1) (*Driver, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	file := filepath.Join(t.TempDir(), "cast-devices.json")
	_ = os.WriteFile(file, []byte(`[{"id":"r1-living","name":"Loa R1","url":"`+srv.URL+`"}]`), 0o644)
	return New(file, "http://10.0.0.2:8180", time.Second), srv
}

func TestLoadSendsTheSongTheServerExpects(t *testing.T) {
	fake := &fakeR1{media: `{"state":"idle"}`}
	d, _ := setup(t, fake)
	item := domain.Item{VideoID: "abc", Title: "Bài", Channel: "Kênh", ThumbnailURL: "/media/abc/thumb.jpg", DurationS: 245}

	key, err := d.Load(context.Background(), "r1-living", item, 90, false)
	if err != nil || key != "abc" {
		t.Fatalf("load = %q %v", key, err)
	}
	items := fake.playBody["items"].([]any)
	got := items[0].(map[string]any)
	want := map[string]any{
		"video_id": "abc", "title": "Bài", "artist": "Kênh",
		"thumbnail": "http://10.0.0.2:8180/media/abc/thumb.jpg", "duration": "4:05",
		"audio_url": "http://10.0.0.2:8180/api/videos/abc/audio",
		"source":    "mytube", "_seek": float64(90), "_silent": true,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, want %#v", k, got[k], v)
		}
	}
	if fake.playBody["start_index"] != float64(0) {
		t.Errorf("start_index = %v", fake.playBody["start_index"])
	}
}

func TestOnlyTheFirstSongIsAnnounced(t *testing.T) {
	fake := &fakeR1{media: `{"state":"idle"}`}
	d, _ := setup(t, fake)
	if _, err := d.Load(context.Background(), "r1-living", domain.Item{VideoID: "a"}, 0, true); err != nil {
		t.Fatal(err)
	}
	if got := fake.playBody["items"].([]any)[0].(map[string]any)["_silent"]; got != false {
		t.Fatalf("_silent = %v on the first song", got)
	}
}

func TestStatusMapsTheR1States(t *testing.T) {
	cases := map[string]domain.DeviceState{
		`{"state":"idle"}`:                                  domain.DeviceIdle,
		`{"state":"stopped"}`:                               domain.DeviceIdle,
		`{"state":"downloading","video_id":"a"}`:            domain.DeviceLoading,
		`{"state":"playing","video_id":"a","position_s":7}`: domain.DevicePlaying,
		`{"state":"paused","video_id":"a"}`:                 domain.DevicePaused,
	}
	for body, want := range cases {
		fake := &fakeR1{media: body, volume: 53}
		d, _ := setup(t, fake)
		st, err := d.Status(context.Background(), "r1-living")
		if err != nil {
			t.Fatal(err)
		}
		if st.State != want || st.Volume != 53 {
			t.Errorf("%s -> state %s volume %d", body, st.State, st.Volume)
		}
	}
}

func TestStatusCarriesPositionAndKey(t *testing.T) {
	fake := &fakeR1{media: `{"state":"playing","video_id":"a","duration_s":245,"position_s":120}`}
	d, _ := setup(t, fake)
	st, _ := d.Status(context.Background(), "r1-living")
	if st.MediaKey != "a" || st.PositionS != 120 || st.DurationS != 245 {
		t.Fatalf("status = %+v", st)
	}
}

func TestCommandsUseTheR1Routes(t *testing.T) {
	fake := &fakeR1{media: `{"state":"idle"}`}
	d, _ := setup(t, fake)
	ctx := context.Background()
	_ = d.Pause(ctx, "r1-living")
	_ = d.Resume(ctx, "r1-living")
	_ = d.Stop(ctx, "r1-living")
	_ = d.Seek(ctx, "r1-living", 180)
	_ = d.SetVolume(ctx, "r1-living", 0)
	want := []string{"/api/media/pause", "/api/media/resume", "/api/media/stop",
		"/api/media/seek?position_s=180", "/api/set?key=volume&value=0"}
	for i, p := range want {
		if i >= len(fake.paths) || fake.paths[i] != p {
			t.Fatalf("paths = %v, want %v", fake.paths, want)
		}
	}
}

func TestASlowR1IsOffline(t *testing.T) {
	fake := &fakeR1{media: `{"state":"idle"}`, delay: 1500 * time.Millisecond}
	d, _ := setup(t, fake)
	devices, err := d.Devices(context.Background())
	if err != nil || len(devices) != 1 || devices[0].Online {
		t.Fatalf("devices = %+v %v", devices, err)
	}
}

func TestAnAnsweringR1IsOnline(t *testing.T) {
	d, _ := setup(t, &fakeR1{media: `{"state":"idle"}`})
	devices, _ := d.Devices(context.Background())
	if len(devices) != 1 || !devices[0].Online || devices[0].Kind != domain.KindR1 || devices[0].Name != "Loa R1" {
		t.Fatalf("devices = %+v", devices)
	}
}

func TestNoDevicesFileIsNoR1(t *testing.T) {
	d := New(filepath.Join(t.TempDir(), "missing.json"), "http://x", time.Second)
	devices, err := d.Devices(context.Background())
	if err != nil || len(devices) != 0 {
		t.Fatalf("devices = %+v %v", devices, err)
	}
}

func TestARefusedPlayIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"empty items"}`))
	}))
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "d.json")
	_ = os.WriteFile(file, []byte(`[{"id":"r","name":"R","url":"`+srv.URL+`"}]`), 0o644)
	d := New(file, "http://x", time.Second)
	if _, err := d.Load(context.Background(), "r", domain.Item{VideoID: "a"}, 0, true); err == nil {
		t.Fatal("a refused play was taken as loaded")
	}
}
