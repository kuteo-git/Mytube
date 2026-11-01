// Package r1 drives the R1 speaker through the HTTP control server of its own
// app (xiaozhi-android, ControlServer.kt). The R1 hands a song to
// xiaozhi-server, which fetches `audio_url` and streams it back down.
package r1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// configured is one entry of data/cast-devices.json. The R1 cannot be
// discovered: it announces nothing on the network, so it is written down.
type configured struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Driver struct {
	devicesFile string
	// mediaBase is the gateway as the speaker reaches it: an address on the
	// LAN, never localhost, which on the speaker would be the speaker.
	mediaBase string
	client    *http.Client
}

// New reads devicesFile on every call, so an R1 added to it is picked up
// without a restart. timeout bounds every request; it is what "online" means.
func New(devicesFile, mediaBase string, timeout time.Duration) *Driver {
	return &Driver{devicesFile: devicesFile, mediaBase: strings.TrimRight(mediaBase, "/"), client: &http.Client{Timeout: timeout}}
}

func (d *Driver) Kind() domain.Kind { return domain.KindR1 }

func (d *Driver) configured() ([]configured, error) {
	raw, err := os.ReadFile(d.devicesFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []configured
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", d.devicesFile, err)
	}
	return out, nil
}

func (d *Driver) base(deviceID string) (string, error) {
	devices, err := d.configured()
	if err != nil {
		return "", err
	}
	for _, c := range devices {
		if c.ID == deviceID {
			return strings.TrimRight(c.URL, "/"), nil
		}
	}
	return "", fmt.Errorf("r1 %q: %w", deviceID, domain.ErrNotFound)
}

func (d *Driver) Devices(ctx context.Context) ([]domain.Device, error) {
	devices, err := d.configured()
	if err != nil {
		return nil, err
	}
	out := make([]domain.Device, 0, len(devices))
	for _, c := range devices {
		_, err := d.mediaState(ctx, strings.TrimRight(c.URL, "/"))
		out = append(out, domain.Device{ID: c.ID, Name: c.Name, Kind: domain.KindR1, Model: "R1", Online: err == nil})
	}
	return out, nil
}

// song is one entry of the R1's play list, in the shape play_youtube.py reads.
type song struct {
	VideoID   string `json:"video_id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Thumbnail string `json:"thumbnail"`
	Duration  string `json:"duration"`
	AudioURL  string `json:"audio_url"`
	Source    string `json:"source"`
	Seek      int    `json:"_seek"`
	Silent    bool   `json:"_silent"`
}

func (d *Driver) Load(ctx context.Context, deviceID string, it domain.Item, startS int, first bool) (string, error) {
	base, err := d.base(deviceID)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"items": []song{{
			VideoID: it.VideoID, Title: it.Title, Artist: it.Channel,
			Thumbnail: d.absolute(it.ThumbnailURL), Duration: clock(it.DurationS),
			AudioURL: d.mediaBase + "/api/videos/" + url.PathEscape(it.VideoID) + "/audio",
			Source:   "mytube", Seek: startS,
			// Only the session's opening song says "Đang tải bài …".
			Silent: !first,
		}},
		"start_index": 0,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/media/play", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	var answer struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := d.do(req, &answer); err != nil {
		return "", err
	}
	if !answer.OK {
		return "", fmt.Errorf("r1 refused the song: %s", answer.Error)
	}
	return it.VideoID, nil
}

func (d *Driver) Pause(ctx context.Context, deviceID string) error {
	return d.get(ctx, deviceID, "/api/media/pause", nil)
}

func (d *Driver) Resume(ctx context.Context, deviceID string) error {
	return d.get(ctx, deviceID, "/api/media/resume", nil)
}

func (d *Driver) Stop(ctx context.Context, deviceID string) error {
	return d.get(ctx, deviceID, "/api/media/stop", nil)
}

func (d *Driver) Seek(ctx context.Context, deviceID string, s int) error {
	return d.get(ctx, deviceID, "/api/media/seek?position_s="+fmt.Sprint(s), nil)
}

func (d *Driver) SetVolume(ctx context.Context, deviceID string, pct int) error {
	return d.get(ctx, deviceID, "/api/set?key=volume&value="+fmt.Sprint(pct), nil)
}

type mediaState struct {
	State     string `json:"state"`
	VideoID   string `json:"video_id"`
	DurationS int    `json:"duration_s"`
	PositionS int    `json:"position_s"`
}

func (d *Driver) Status(ctx context.Context, deviceID string) (domain.DeviceStatus, error) {
	base, err := d.base(deviceID)
	if err != nil {
		return domain.DeviceStatus{}, err
	}
	m, err := d.mediaState(ctx, base)
	if err != nil {
		return domain.DeviceStatus{}, err
	}
	var state struct {
		Volume int `json:"volume"`
	}
	if err := d.getURL(ctx, base+"/api/state", &state); err != nil {
		return domain.DeviceStatus{}, err
	}
	st := domain.DeviceStatus{MediaKey: m.VideoID, PositionS: m.PositionS, DurationS: m.DurationS, Volume: state.Volume}
	switch m.State {
	case "downloading":
		st.State = domain.DeviceLoading
	case "playing":
		st.State = domain.DevicePlaying
	case "paused":
		st.State = domain.DevicePaused
	default:
		// idle and stopped. A stopped R1 reports no video_id at all.
		st.State, st.MediaKey = domain.DeviceIdle, ""
	}
	return st, nil
}

func (d *Driver) mediaState(ctx context.Context, base string) (mediaState, error) {
	var m mediaState
	err := d.getURL(ctx, base+"/api/media/state", &m)
	return m, err
}

func (d *Driver) get(ctx context.Context, deviceID, path string, out any) error {
	base, err := d.base(deviceID)
	if err != nil {
		return err
	}
	return d.getURL(ctx, base+path, out)
}

func (d *Driver) getURL(ctx context.Context, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	return d.do(req, out)
}

func (d *Driver) do(req *http.Request, out any) error {
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("r1 %s answered %d", req.URL.Path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (d *Driver) absolute(path string) string {
	if path == "" || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return d.mediaBase + path
}

// clock writes seconds the way the R1's panel shows durations, "4:05".
func clock(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}
