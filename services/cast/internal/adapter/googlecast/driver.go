// Package googlecast drives Google Nest speakers through castbridge, the
// pychromecast sidecar on 127.0.0.1:8187 (services/castbridge). The Cast
// protocol has no Go library worth trusting, and pychromecast is the one the
// Home Assistant world keeps working.
package googlecast

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// The Default Media Receiver, the Cast app that plays a URL it is handed. Any
// other app on the speaker is somebody else's.
const defaultReceiver = "CC1AD845"

// Apps a speaker shows while doing nothing, which are not a takeover.
var idleApps = map[string]bool{"E8C28D3C": true}

type Driver struct {
	bridge    string
	mediaBase string
	client    *http.Client
}

func New(bridge, mediaBase string, timeout time.Duration) *Driver {
	return &Driver{
		bridge: strings.TrimRight(bridge, "/"), mediaBase: strings.TrimRight(mediaBase, "/"),
		client: &http.Client{Timeout: timeout},
	}
}

func (d *Driver) Kind() domain.Kind { return domain.KindGoogle }

type bridgeDevice struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	Model    string `json:"model"`
	CastType string `json:"cast_type"`
}

// Devices are what castbridge has discovered and still sees; it drops one
// gone for a minute, so everything listed is online.
func (d *Driver) Devices(ctx context.Context) ([]domain.Device, error) {
	var listed []bridgeDevice
	if err := d.call(ctx, http.MethodGet, "/devices", nil, &listed); err != nil {
		return nil, err
	}
	out := make([]domain.Device, 0, len(listed))
	for _, b := range listed {
		// A television is a Chromecast with a screen: castbridge leaves those
		// out, and so does this, in case one slips through.
		if b.CastType == "cast" {
			continue
		}
		out = append(out, domain.Device{ID: b.UUID, Name: b.Name, Kind: domain.KindGoogle, Model: b.Model, Online: true})
	}
	return out, nil
}

func (d *Driver) audioURL(videoID string) string {
	return d.mediaBase + "/api/videos/" + url.PathEscape(videoID) + "/audio"
}

func (d *Driver) Load(ctx context.Context, deviceID string, it domain.Item, startS int, _ bool) (string, error) {
	media := d.audioURL(it.VideoID)
	thumb := it.ThumbnailURL
	if thumb != "" && !strings.HasPrefix(thumb, "http") {
		thumb = d.mediaBase + thumb
	}
	body := map[string]any{
		"url": media, "content_type": "audio/mp4", "title": it.Title, "thumbnail": thumb, "start_s": startS,
	}
	if err := d.call(ctx, http.MethodPost, d.devicePath(deviceID, "load"), body, nil); err != nil {
		return "", err
	}
	return media, nil
}

func (d *Driver) Pause(ctx context.Context, deviceID string) error {
	return d.call(ctx, http.MethodPost, d.devicePath(deviceID, "pause"), nil, nil)
}

func (d *Driver) Resume(ctx context.Context, deviceID string) error {
	return d.call(ctx, http.MethodPost, d.devicePath(deviceID, "play"), nil, nil)
}

func (d *Driver) Stop(ctx context.Context, deviceID string) error {
	return d.call(ctx, http.MethodPost, d.devicePath(deviceID, "stop"), nil, nil)
}

func (d *Driver) Seek(ctx context.Context, deviceID string, s int) error {
	return d.call(ctx, http.MethodPost, d.devicePath(deviceID, "seek"), map[string]any{"s": s}, nil)
}

func (d *Driver) SetVolume(ctx context.Context, deviceID string, pct int) error {
	return d.call(ctx, http.MethodPost, d.devicePath(deviceID, "volume"), map[string]any{"level": float64(pct) / 100}, nil)
}

type bridgeStatus struct {
	PlayerState string  `json:"player_state"`
	ContentID   string  `json:"content_id"`
	AppID       string  `json:"app_id"`
	CurrentTime float64 `json:"current_time"`
	Duration    float64 `json:"duration"`
	IdleReason  string  `json:"idle_reason"`
	VolumeLevel float64 `json:"volume_level"`
}

func (d *Driver) Status(ctx context.Context, deviceID string) (domain.DeviceStatus, error) {
	var b bridgeStatus
	if err := d.call(ctx, http.MethodGet, d.devicePath(deviceID, "status"), nil, &b); err != nil {
		return domain.DeviceStatus{}, err
	}
	return statusOf(b), nil
}

func statusOf(b bridgeStatus) domain.DeviceStatus {
	st := domain.DeviceStatus{
		PositionS:  int(b.CurrentTime),
		DurationS:  int(math.Round(b.Duration)),
		Volume:     int(math.Round(b.VolumeLevel * 100)),
		IdleReason: domain.IdleReason(b.IdleReason),
	}
	switch {
	case b.AppID == defaultReceiver:
		st.MediaKey = b.ContentID
	case b.AppID != "" && !idleApps[b.AppID]:
		st.MediaKey = "app:" + b.AppID
	}
	switch b.PlayerState {
	case "PLAYING":
		st.State = domain.DevicePlaying
	case "BUFFERING":
		st.State = domain.DeviceLoading
	case "PAUSED":
		st.State = domain.DevicePaused
	default:
		st.State = domain.DeviceIdle
	}
	// Another app playing reports its own player state, which is not about
	// our song; the key alone is what the session needs from it.
	return st
}

func (d *Driver) devicePath(deviceID, action string) string {
	return "/devices/" + url.PathEscape(deviceID) + "/" + action
}

func (d *Driver) call(ctx context.Context, method, path string, body, out any) error {
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.bridge+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("castbridge %s: %w", path, domain.ErrNotFound)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("castbridge %s answered %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
