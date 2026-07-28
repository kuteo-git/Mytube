// Package innertube reads YouTube's internal watch-next API.
//
// This exists because nothing else offers related videos: yt-dlp does not
// expose them and the .NET YoutubeExplode has no such call. Every library that
// does offer them reads the same endpoint this does.
//
// It is an undocumented API with no compatibility contract, so every function
// here is written to degrade to "no results" rather than to an error. A caller
// must be able to treat a total failure as an empty list — the feed has other
// ways to refill, and losing variety is a far better outcome than a broken page.
package innertube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

const defaultEndpoint = "https://www.youtube.com/youtubei/v1/next"

// The web client identity YouTube's own page sends. Version drift here is the
// most likely cause of a sudden empty result, and is the first thing to check.
const (
	clientName    = "WEB"
	clientVersion = "2.20240101.00.00"
)

type Client struct {
	http *http.Client
	// Endpoints are fields rather than constants so tests can point them at a
	// local server.
	endpoint   string
	browseURL  string
	resolveURL string
}

func New(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		http:       httpClient,
		endpoint:   defaultEndpoint,
		browseURL:  browseEndpoint,
		resolveURL: resolveEndpoint,
	}
}

type nextRequest struct {
	VideoID string      `json:"videoId"`
	Context nextContext `json:"context"`
}

type nextContext struct {
	Client nextClient `json:"client"`
}

type nextClient struct {
	ClientName    string `json:"clientName"`
	ClientVersion string `json:"clientVersion"`
	Hl            string `json:"hl"`
	Gl            string `json:"gl"`
}

// Only the fields that are actually read are declared. Everything else in the
// response — and there is a great deal of it — is discarded by the decoder.
type nextResponse struct {
	Contents struct {
		TwoColumnWatchNextResults struct {
			SecondaryResults struct {
				SecondaryResults struct {
					Results []struct {
						CompactVideoRenderer *compactVideoRenderer `json:"compactVideoRenderer"`
					} `json:"results"`
				} `json:"secondaryResults"`
			} `json:"secondaryResults"`
		} `json:"twoColumnWatchNextResults"`
	} `json:"contents"`
}

type compactVideoRenderer struct {
	VideoID string `json:"videoId"`
	Title   struct {
		SimpleText string `json:"simpleText"`
	} `json:"title"`
	LongBylineText struct {
		Runs []struct {
			Text string `json:"text"`
		} `json:"runs"`
	} `json:"longBylineText"`
	LengthText struct {
		SimpleText string `json:"simpleText"`
	} `json:"lengthText"`
	Thumbnail struct {
		Thumbnails []struct {
			URL string `json:"url"`
		} `json:"thumbnails"`
	} `json:"thumbnail"`
}

// Related returns the videos YouTube would show beside the given one.
//
// Anonymous requests get a thinner list than a signed-in browser would, because
// this panel is heavily personalised. That is accepted: the alternative is
// storing YouTube credentials, which this project does not do.
func (c *Client) Related(ctx context.Context, videoID string) ([]domain.ExternalVideo, error) {
	body, err := json.Marshal(nextRequest{
		VideoID: videoID,
		Context: nextContext{Client: nextClient{
			ClientName:    clientName,
			ClientVersion: clientVersion,
			Hl:            "en",
			Gl:            "US",
		}},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Youtube-Client-Name", "1")
	req.Header.Set("X-Youtube-Client-Version", clientVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("innertube next: status %d", resp.StatusCode)
	}

	var decoded nextResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		// A shape change is not an error the caller can act on, and treating it
		// as one would take the feed down with it.
		return nil, nil
	}

	results := decoded.Contents.TwoColumnWatchNextResults.SecondaryResults.SecondaryResults.Results
	out := make([]domain.ExternalVideo, 0, len(results))
	for _, entry := range results {
		r := entry.CompactVideoRenderer
		if r == nil || r.VideoID == "" {
			continue // continuation markers and ad slots live in this list too
		}

		channelName := ""
		if len(r.LongBylineText.Runs) > 0 {
			channelName = r.LongBylineText.Runs[0].Text
		}
		thumbnail := ""
		if n := len(r.Thumbnail.Thumbnails); n > 0 {
			thumbnail = r.Thumbnail.Thumbnails[n-1].URL // last is largest
		}

		out = append(out, domain.ExternalVideo{
			ID:              r.VideoID,
			Title:           r.Title.SimpleText,
			ChannelName:     channelName,
			DurationSeconds: parseDuration(r.LengthText.SimpleText),
			ThumbnailURL:    thumbnail,
			SourceURL:       "https://www.youtube.com/watch?v=" + r.VideoID,
			// Deliberately no Topics. Topics say "the curator chose this
			// source"; a related video was chosen by YouTube.
		})
	}
	return out, nil
}

// parseDuration reads "12:34" and "1:02:03". Live streams have no length and
// give an empty or non-numeric string, which becomes zero.
func parseDuration(text string) int32 {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	total := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return int32(total)
}
