// Package remotetranscript asks a local helper for a video's captions.
//
// YouTube rate-limits the caption endpoint by address, and measured on this one
// it does so for hours — thirteen straight on 2026-08-27, while videos played
// normally throughout. No amount of care with the request gets past that: the
// refusal is about the house.
//
// This package used to be about asking *another machine*, on the reasoning that
// another machine is another address. That was measured and it is false: the
// household's Home Assistant box was refused with exactly the same 429, in the
// same minute. A second door in the same wall is not a second door.
//
// So the helper moved to loopback and stopped being configurable, and what is
// configured instead is the **proxy** it goes out through (see the proxycfg
// package). Measured 2026-08-28 across four videos in one minute: direct was
// `IpBlocked` 4 of 4, and through a rotating residential proxy 4 of 4 answered.
//
// The contract is this project's own, because there is no standard for "give me
// this video's captions" — OpenAI's `/v1/audio/transcriptions` is an audio
// upload, not a lookup by video id, and every commercial service that does this
// has invented its own shape. So one was defined, in the smallest form that
// answers the question:
//
//	GET {base}/transcript?video_id=<id>&langs=vi,en
//	X-Transcript-Proxy: http://user:pass@host:port   (optional)
//	→ {"language":"vi","generated":true,"vtt":"WEBVTT\n\n..."}
//	→ {"error":"...","kind":"proxy|upstream"} for anything that went wrong
//
// A GET so it can be typed into a browser, which is the difference between "the
// server is down" and "I cannot tell what is wrong" at nine in the evening. VTT
// rather than cues, because that is what goes on disk — anything else means a
// second parser here, and Python's youtube_transcript_api already ships a
// WebVTTFormatter that writes it.
//
// `langs` is sent rather than left to the other end: "Vietnamese if there is
// any, else English" is one rule, and two servers holding it separately is two
// rules that agree until one of them is changed. The proxy travels the same way
// and for a stronger version of the same reason — see Client.
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

	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/proxycfg"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// The languages this app can use, in the order it wants them. The same order
// the local path applies, and for the same reason: YouTube's own Vietnamese is
// better than the translation this app would make from English.
const languages = "vi,en"

// The helper, which is not a setting any more.
//
// Loopback and a fixed port: it holds no credential, has no address worth
// choosing, and `scripts/dev.sh` starts it beside the translation and speech
// servers. What used to be two fields on a settings screen — which machine, and
// the shared secret to reach it — are gone with the premise that put them there.
//
// **8185, in this app's own 818x block, and it was 8009 for one release.** A
// port is not an identity: another project on this machine took 8009 while the
// stack was stopped, dev.sh saw something listening and reported the helper as
// up, and every caption fetch came back 404 from a stranger. dev.sh now asks
// what is there rather than assuming; this constant moved so it is unlikely to
// have to.
const serverURL = "http://127.0.0.1:8185/transcript"

// How the proxy reaches the Python side.
//
// A header rather than a query parameter: it carries a password, and a query
// string lands in access logs.
//
// Sent per request rather than read from that process's environment, which is
// the same rule this file already followed for its own settings and for the same
// reason: a caption fetch can be started by the retry sweep, on a timer, with no
// request to carry anything on. A proxy read once at start-up on the Python side
// would mean saving the settings form did nothing until somebody restarted a
// different process — the trap `internal/mediaroot` exists to document.
const proxyHeader = "X-Transcript-Proxy"

// proxySource is the part of proxycfg this needs, named here so the dependency
// points inward: this package asks a question, it does not depend on how the
// answer is stored.
type proxySource interface {
	URLFor(proxycfg.Use) string
}

type Client struct {
	http    *http.Client
	root    string
	proxies proxySource
	logger  *slog.Logger
}

func New(mediaRoot string, proxies proxySource, logger *slog.Logger) *Client {
	return &Client{
		http:    &http.Client{Timeout: 30 * time.Second},
		root:    mediaRoot,
		proxies: proxies,
		logger:  logger,
	}
}

// Configured reports whether asking is worth the request.
//
// The question moved with the setting. It used to be "has somebody named a
// machine to ask"; it is now "is there a proxy to ask through", because asking
// from this house's own address is measured to be refused — `IpBlocked` 4 of 4
// on 2026-08-28. Going anyway would spend a request per video to be told no.
func (c *Client) Configured() bool {
	return c.proxyURL() != ""
}

func (c *Client) proxyURL() string {
	if c.proxies == nil {
		return ""
	}
	return c.proxies.URLFor(proxycfg.Captions)
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
	proxy := c.proxyURL()
	if proxy == "" {
		return nil, false
	}
	if height <= 0 {
		height = 1080
	}

	got, err := c.ask(ctx, proxy, videoID)
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

func (c *Client) ask(ctx context.Context, proxy, videoID string) (answer, error) {
	q := url.Values{"video_id": {videoID}, "langs": {languages}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"?"+q.Encode(), nil)
	if err != nil {
		return answer{}, err
	}
	req.Header.Set(proxyHeader, proxy)

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
		// A 404 here almost always means the port is answering and the helper is
		// not — some other process holding it. Said plainly, because the last
		// time it happened the log read "server said 404 Not Found" and it took
		// a look at `lsof` to learn that a different project's uvicorn app had
		// taken the port while this stack was stopped.
		if res.StatusCode == http.StatusNotFound {
			return answer{}, fmt.Errorf(
				"nothing answered at %s — is something else holding that port?", serverURL)
		}
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
