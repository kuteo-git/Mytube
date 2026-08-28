// Package remotetranscript asks another machine for a video's captions.
//
// YouTube rate-limits the caption endpoint by address, and measured on this one
// it does so for hours — thirteen straight on 2026-08-27, while videos played
// normally throughout. No amount of care with the request gets past that: the
// refusal is about the house. Asking from somewhere else does, and this
// household already runs a machine that can.
//
// The contract is this project's own, because there is no standard for "give me
// this video's captions" — OpenAI's `/v1/audio/transcriptions` is an audio
// upload, not a lookup by video id, and every commercial service that does this
// has invented its own shape. So one was defined, in the smallest form that
// answers the question:
//
//	GET {base}/transcript?video_id=<id>&langs=vi,en
//	→ {"language":"vi","generated":true,"vtt":"WEBVTT\n\n..."}
//	→ {"error":"..."} for anything that went wrong
//
// A GET so it can be typed into a browser, which is the difference between "the
// server is down" and "I cannot tell what is wrong" for somebody setting this
// up on another machine. VTT rather than cues, because that is what goes on
// disk — anything else means a second parser here, and Python's
// youtube_transcript_api already ships a WebVTTFormatter that writes it.
//
// `langs` is sent rather than left to the other end: "Vietnamese if there is
// any, else English" is one rule, and two servers holding it separately is two
// rules that agree until one of them is changed.
package remotetranscript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// The languages this app can use, in the order it wants them. The same order
// the local path applies, and for the same reason: YouTube's own Vietnamese is
// better than the translation this app would make from English.
const languages = "vi,en"

// Config is read from disk per request rather than held.
//
// The gateway owns the settings screen and writes the file; this reads it. Per
// request because a caption fetch can be started by a timer nobody is waiting
// on — the retry sweep — so there is no request to carry the setting on, and a
// value read once at start-up would mean saving the form did nothing until the
// next restart. That is the trap `internal/mediaroot` exists to document.
type Config struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

type Client struct {
	http       *http.Client
	root       string
	configPath string
	logger     *slog.Logger
}

func New(mediaRoot, configDir string, logger *slog.Logger) *Client {
	return &Client{
		http:       &http.Client{Timeout: 30 * time.Second},
		root:       mediaRoot,
		configPath: filepath.Join(configDir, "transcript-config.json"),
		logger:     logger,
	}
}

// Configured reports whether anybody has filled the form in. Empty is the
// ordinary state and means "do not ask anybody else".
func (c *Client) Configured() bool {
	return endpointFor(c.load().BaseURL) != ""
}

func (c *Client) load() Config {
	cfg := Config{
		BaseURL: os.Getenv("TRANSCRIPT_BASE_URL"),
		APIKey:  os.Getenv("TRANSCRIPT_API_KEY"),
	}
	raw, err := os.ReadFile(c.configPath)
	if err != nil {
		return cfg
	}
	var saved Config
	if err := json.Unmarshal(raw, &saved); err != nil {
		return cfg
	}
	if saved.BaseURL != "" {
		cfg.BaseURL = saved.BaseURL
	}
	if saved.APIKey != "" {
		cfg.APIKey = saved.APIKey
	}
	return cfg
}

// endpointFor tolerates a base with or without the path, the same way the
// gateway's speech field does, because the settings screen holds several of
// these and somebody will paste one into the other.
func endpointFor(base string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(base), "/")
	if trimmed == "" {
		return ""
	}
	if strings.HasSuffix(trimmed, "/transcript") {
		return trimmed
	}
	return trimmed + "/transcript"
}

type answer struct {
	Language  string `json:"language"`
	Generated bool   `json:"generated"`
	VTT       string `json:"vtt"`
	Error     string `json:"error"`
}

// FetchSubtitles has the same shape as the other two caption sources, so the
// three are interchangeable and the chain can try them in order.
//
// `refused` is always false. It means "YouTube turned this address away", which
// is a statement about the house and its purpose is to put the video in the
// retry queue; a helper on another machine failing says nothing about that and
// must not stand in for it. The chain goes on to yt-dlp instead.
func (c *Client) FetchSubtitles(ctx context.Context, _, videoID string, height int32) ([]domain.SubtitleTrack, bool) {
	cfg := c.load()
	endpoint := endpointFor(cfg.BaseURL)
	if endpoint == "" {
		return nil, false
	}
	if height <= 0 {
		height = 1080
	}

	got, err := c.ask(ctx, cfg, endpoint, videoID)
	if err != nil {
		c.logger.Warn("remote transcript", "video", videoID, "error", err)
		return nil, false
	}

	lang := primary(got.Language)
	if lang == "" {
		c.logger.Warn("remote transcript", "video", videoID, "error", "no language named")
		return nil, false
	}

	dir := filepath.Join(c.root, videoID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false
	}
	name := fmt.Sprintf("%dp.mp4.%s.vtt", height, lang)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(got.VTT), 0o644); err != nil {
		c.logger.Warn("remote transcript write", "video", videoID, "error", err)
		return nil, false
	}

	c.logger.Info("remote transcript",
		"video", videoID, "lang", lang, "generated", got.Generated, "bytes", len(got.VTT))

	return []domain.SubtitleTrack{{
		Language:  lang,
		Label:     labelFor(lang),
		Path:      filepath.Join(videoID, name),
		Generated: got.Generated,
	}}, false
}

func (c *Client) ask(ctx context.Context, cfg Config, endpoint, videoID string) (answer, error) {
	q := url.Values{"video_id": {videoID}, "langs": {languages}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return answer{}, err
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return answer{}, err
	}
	defer func() { _ = res.Body.Close() }()

	// Bounded: a caption file is tens of kilobytes, and without a limit a
	// misconfigured URL pointing at something enormous is this process's memory.
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return answer{}, err
	}
	if res.StatusCode != http.StatusOK {
		return answer{}, fmt.Errorf("server said %s", res.Status)
	}

	var got answer
	if err := json.Unmarshal(body, &got); err != nil {
		return answer{}, fmt.Errorf("not the answer shape: %w", err)
	}
	if got.Error != "" {
		return answer{}, errors.New(got.Error)
	}
	if !strings.HasPrefix(strings.TrimSpace(got.VTT), "WEBVTT") {
		// Refused early rather than written to disk. A file that does not begin
		// WEBVTT is not a subtitle file, and the player reports that as a track
		// that exists and shows nothing — which is worse than no track at all.
		return answer{}, errors.New("answered with something that is not WebVTT")
	}
	return got, nil
}

// primary keeps the language tag the rest of the app matches on. The web app
// compares subtitle codes exactly, so `en-US` would be written, published, and
// then offered to nobody.
func primary(lang string) string {
	lang = strings.TrimSpace(lang)
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		return lang[:i]
	}
	return lang
}

func labelFor(lang string) string {
	switch lang {
	case "vi":
		return "Tiếng Việt"
	case "en":
		return "English"
	default:
		return strings.ToUpper(lang)
	}
}
