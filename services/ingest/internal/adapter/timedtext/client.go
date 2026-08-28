// Package timedtext fetches captions the cheap way: ask once what exists, then
// download exactly one file.
//
// Why this exists beside the yt-dlp path, which already fetched captions: cost,
// measured on the endpoint that is actually refusing us. yt-dlp runs two passes
// (authored and automatic, because the two live in different places in
// YouTube's data and it cannot ask for both at once) and each pass downloads
// every language asked for. That is a full metadata extract twice over and
// **four hits on `timedtext`** per video — for `en,vi`, where one file is what
// the player ends up using.
//
// `timedtext` is rate-limited by address, hard, and separately from everything
// else: video bytes come from googlevideo on signed URLs and were never
// affected, which is why videos kept playing while captions did not. Measured
// on one evening: 70 refusals across 20 videos, and a plain yt-dlp run outside
// this app refusing identically.
//
// So: one player call lists the tracks, and one download takes the best of
// them. Vietnamese if YouTube has it — its own translation is better than ours
// — English otherwise, which the app translates itself.
package timedtext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// The client YouTube's own Android app identifies as.
//
// Not WEB: asked as WEB without the key and version from a real page load, the
// player answers with an empty caption list — measured, zero tracks on a video
// that plainly has them. This pair is what the widely-used Python client sends,
// and it lists the tracks reliably.
const (
	clientName    = "ANDROID"
	clientVersion = "20.10.38"
	watchURL      = "https://www.youtube.com/watch?v=%s"
	playerURL     = "https://www.youtube.com/youtubei/v1/player?key=%s"
)

// apiKey is stamped into every watch page. It is not a secret and not ours; it
// is read rather than hard-coded because YouTube rotates it.
var apiKey = regexp.MustCompile(`"INNERTUBE_API_KEY":"([^"]+)"`)

// Preference order. Vietnamese first because this household reads it and
// YouTube's own translation beats the one this app would make; English second
// because everything downstream — the translation pass, the read-aloud — is
// built to start from it.
var wanted = []string{"vi", "en"}

type Client struct {
	http   *http.Client
	root   string
	logger *slog.Logger
	// The two addresses, as fields rather than constants so a test can point
	// them at itself. Nothing else sets them: a caption fetcher that can be
	// aimed anywhere by configuration is a way to leak the library's requests
	// to somebody else's server.
	watch  string
	player string
}

func New(root string, logger *slog.Logger) *Client {
	// A cookie jar, because the watch page sets six of them and the player call
	// is expected to carry them. Without one every request arrives as a stranger.
	// Shared for the life of the process, like a browser's.
	jar, _ := cookiejar.New(nil)
	return &Client{
		http:   &http.Client{Timeout: 30 * time.Second, Jar: jar},
		root:   root,
		logger: logger,
		watch:  watchURL,
		player: playerURL,
	}
}

// errRefused is upstream turning this address away, as distinct from a video
// having no captions. The two look identical from the outside and want opposite
// responses: one is worth asking about again later, the other is finished.
var errRefused = errors.New("upstream refused the caption endpoint")

type captionTrack struct {
	BaseURL string `json:"baseUrl"`
	Lang    string `json:"languageCode"`
	Kind    string `json:"kind"`
	Name    struct {
		SimpleText string `json:"simpleText"`
		Runs       []struct {
			Text string `json:"text"`
		} `json:"runs"`
	} `json:"name"`
}

func (t captionTrack) label() string {
	if t.Name.SimpleText != "" {
		return t.Name.SimpleText
	}
	if len(t.Name.Runs) > 0 {
		return t.Name.Runs[0].Text
	}
	return strings.ToUpper(t.Lang)
}

type playerResponse struct {
	Captions struct {
		Renderer struct {
			Tracks []captionTrack `json:"captionTracks"`
		} `json:"playerCaptionsTracklistRenderer"`
	} `json:"captions"`
}

// FetchSubtitles has the same shape as the yt-dlp downloader's, so the two are
// interchangeable and a caller can try one and fall back to the other.
func (c *Client) FetchSubtitles(ctx context.Context, videoURL, videoID string, height int32) ([]domain.SubtitleTrack, bool) {
	if height <= 0 {
		height = 1080
	}
	dir := filepath.Join(c.root, videoID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false
	}

	tracks, err := c.list(ctx, videoID)
	if err != nil {
		c.logger.Warn("caption list", "video", videoID, "error", err)
		return nil, errors.Is(err, errRefused)
	}
	if len(tracks) == 0 {
		// Nothing to fetch, and nothing refused us. A video with no captions is
		// a finished question, not one to come back to.
		return nil, false
	}

	best, ok := pick(tracks)
	if !ok {
		c.logger.Info("no caption track in a language we read",
			"video", videoID, "offered", languages(tracks))
		return nil, false
	}

	// What the one listing request bought, before the one download is spent.
	// This is the whole claim of this package — ask once, take one — and it is
	// the line that shows it happening rather than yt-dlp's four.
	c.logger.Info("caption tracks",
		"video", videoID, "offered", languages(tracks), "taking", best.Lang)

	vtt, err := c.download(ctx, best.BaseURL)
	if err != nil {
		c.logger.Warn("caption download", "video", videoID, "lang", best.Lang, "error", err)
		return nil, errors.Is(err, errRefused)
	}

	// Named by the primary subtag, not by whatever YouTube called the track.
	//
	// It answers `en-US` as readily as `en`, and the web app matches subtitle
	// languages exactly — `/^(en|eng|vi|vie|vi-x-mt)$/` decides whether a track
	// becomes a row in the subtitle menu, and `desiredTrackMode` compares
	// against the literal 'vi'. A file called `1080p.mp4.en-US.vtt` would be
	// fetched, written, published, and then offered to nobody. yt-dlp
	// normalised this on the way past and the app never had to know.
	lang := primary(best.Lang)
	name := fmt.Sprintf("%dp.mp4.%s.vtt", height, lang)
	if err := os.WriteFile(filepath.Join(dir, name), vtt, 0o644); err != nil {
		c.logger.Warn("write captions", "video", videoID, "error", err)
		return nil, false
	}

	return []domain.SubtitleTrack{{
		Language: lang,
		Label:    best.label(),
		Path:     filepath.Join(videoID, name),
		// "asr" is YouTube's own word for a machine transcription.
		Generated: best.Kind == "asr",
	}}, false
}

// pick takes the first language this household reads, in preference order.
//
// One track, not all of them. Every extra language is another hit on the
// endpoint that is refusing us, and the player shows one caption at a time.
func pick(tracks []captionTrack) (captionTrack, bool) {
	for _, want := range wanted {
		for _, t := range tracks {
			// "en-US" and "en" are one language; the primary subtag decides,
			// the same rule the feed's language filter uses.
			if strings.EqualFold(primary(t.Lang), want) {
				return t, true
			}
		}
	}
	return captionTrack{}, false
}

func primary(lang string) string {
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		return lang[:i]
	}
	return lang
}

func languages(tracks []captionTrack) string {
	out := make([]string, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, t.Lang)
	}
	return strings.Join(out, ",")
}

// list asks once what captions exist.
func (c *Client) list(ctx context.Context, videoID string) ([]captionTrack, error) {
	html, err := c.get(ctx, fmt.Sprintf(c.watch, videoID))
	if err != nil {
		return nil, fmt.Errorf("watch page: %w", err)
	}
	m := apiKey.FindSubmatch(html)
	if m == nil {
		return nil, errors.New("no innertube key in the watch page")
	}

	body, _ := json.Marshal(map[string]any{
		"context": map[string]any{"client": map[string]any{
			"clientName": clientName, "clientVersion": clientVersion,
		}},
		"videoId": videoID,
	})
	raw, err := c.post(ctx, fmt.Sprintf(c.player, m[1]), body)
	if err != nil {
		return nil, fmt.Errorf("player: %w", err)
	}

	var p playerResponse
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("player response: %w", err)
	}
	return p.Captions.Renderer.Tracks, nil
}

// download takes the one file, asking for WebVTT directly.
//
// `fmt=vtt` is what makes this a drop-in for the yt-dlp path: without it the
// endpoint answers in YouTube's own XML and something here would have to
// convert it, which is a second parser to keep correct for no gain.
func (c *Client) download(ctx context.Context, baseURL string) ([]byte, error) {
	return c.get(ctx, baseURL+"&fmt=vtt")
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *Client) post(ctx context.Context, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	req.Header.Set("Accept-Language", "en-US")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; local-mytube/1.0)")
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()

	// 429 is the one that matters, and it is reported as a refusal rather than
	// as an error so the retry table can hold it. 403 travels with it: on this
	// endpoint it is the same weather under a different name.
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: %s", errRefused, res.Status)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream said %s", res.Status)
	}
	// Bounded: a caption file is tens of kilobytes and a watch page a couple of
	// megabytes. Without a limit a redirect to something enormous is this
	// process's memory.
	return io.ReadAll(io.LimitReader(res.Body, 8<<20))
}

// watchHost is the scheme and host the client is pointed at, for tests that
// need to hand it a caption URL on the same server.
func (c *Client) watchHost() string {
	if i := strings.Index(c.watch, "/watch"); i > 0 {
		return c.watch[:i]
	}
	return "https://www.youtube.com"
}
