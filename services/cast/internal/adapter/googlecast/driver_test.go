package googlecast

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

type seen struct {
	method, path string
	body         map[string]any
}

func bridge(t *testing.T, answers map[string]string) (*Driver, *[]seen) {
	t.Helper()
	var calls []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, seen{r.Method, r.URL.Path, body})
		if a, ok := answers[r.URL.Path]; ok {
			_, _ = w.Write([]byte(a))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "http://10.0.0.2:8180", time.Second), &calls
}

func TestATelevisionIsNotASpeaker(t *testing.T) {
	d, _ := bridge(t, map[string]string{"/devices": `[
		{"uuid":"u1","name":"Nest Mini","model":"Google Nest Mini","cast_type":"audio"},
		{"uuid":"u2","name":"Cả nhà","model":"Google Cast Group","cast_type":"group"},
		{"uuid":"u3","name":"TV","model":"Chromecast","cast_type":"cast"}]`})
	devices, err := d.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[0].ID != "u1" || devices[1].ID != "u2" || !devices[0].Online {
		t.Fatalf("devices = %+v", devices)
	}
}

func TestLoadHandsTheSpeakerTheAudioURL(t *testing.T) {
	d, calls := bridge(t, nil)
	key, err := d.Load(context.Background(), "u1",
		domain.Item{VideoID: "abc", Title: "Bài", ThumbnailURL: "https://i.ytimg.com/a.jpg"}, 90, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if key != "http://10.0.0.2:8180/api/videos/abc/audio" {
		t.Fatalf("key = %q", key)
	}
	c := (*calls)[0]
	if c.method != http.MethodPost || c.path != "/devices/u1/load" || c.body["url"] != key ||
		c.body["content_type"] != "audio/mp4" || c.body["start_s"] != float64(90) || c.body["thumbnail"] != "https://i.ytimg.com/a.jpg" {
		t.Fatalf("call = %+v", c)
	}
}

func TestVolumeIsAFraction(t *testing.T) {
	d, calls := bridge(t, nil)
	_ = d.SetVolume(context.Background(), "u1", 40)
	if (*calls)[0].body["level"] != 0.4 {
		t.Fatalf("body = %+v", (*calls)[0].body)
	}
}

func TestStatusOf(t *testing.T) {
	cases := []struct {
		name string
		in   bridgeStatus
		want domain.DeviceStatus
	}{
		{"our song playing",
			bridgeStatus{PlayerState: "PLAYING", AppID: defaultReceiver, ContentID: "http://m/a", CurrentTime: 12.7, Duration: 244.6, VolumeLevel: 0.3},
			domain.DeviceStatus{State: domain.DevicePlaying, MediaKey: "http://m/a", PositionS: 12, DurationS: 245, Volume: 30}},
		{"finished",
			bridgeStatus{PlayerState: "IDLE", AppID: defaultReceiver, ContentID: "http://m/a", IdleReason: "FINISHED"},
			domain.DeviceStatus{State: domain.DeviceIdle, MediaKey: "http://m/a", IdleReason: domain.IdleFinished}},
		{"another app",
			bridgeStatus{PlayerState: "PLAYING", AppID: "CC32E753"},
			domain.DeviceStatus{State: domain.DevicePlaying, MediaKey: "app:CC32E753"}},
		{"nothing running",
			bridgeStatus{PlayerState: "UNKNOWN"},
			domain.DeviceStatus{State: domain.DeviceIdle}},
		{"buffering",
			bridgeStatus{PlayerState: "BUFFERING", AppID: defaultReceiver, ContentID: "http://m/a"},
			domain.DeviceStatus{State: domain.DeviceLoading, MediaKey: "http://m/a"}},
	}
	for _, c := range cases {
		if got := statusOf(c.in); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestABridgeThatIsDownIsAnError(t *testing.T) {
	d := New("http://127.0.0.1:1", "http://x", 200*time.Millisecond)
	if _, err := d.Devices(context.Background()); err == nil {
		t.Fatal("no error from a bridge that is not there")
	}
}

func TestTheNarratedSoundHasItsOwnAddress(t *testing.T) {
	d, calls := bridge(t, nil)
	key, err := d.Load(context.Background(), "u1", domain.Item{VideoID: "abc"}, 47, false, true)
	if err != nil {
		t.Fatal(err)
	}
	const want = "http://10.0.0.2:8180/api/videos/abc/audio/narrated.wav"
	if key != want || (*calls)[0].body["url"] != want || (*calls)[0].body["content_type"] != "audio/wav" {
		t.Fatalf("key = %q, body = %v", key, (*calls)[0].body)
	}
}
