// Package youtube asks YouTube questions that have no answer in any listing.
package youtube

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ShortChecker asks whether an id is served as a Short.
//
// The whole test is what YouTube does with https://www.youtube.com/shorts/<id>:
// a Short is served at that address, and anything else is redirected to
// /watch?v=<id>. Measured on four videos from this library —
//
//	CpvaxO7Ec30  40s  200  Short
//	AgkeJCAJl3Y  59s  200  Short
//	tpjJeH1pPws  14s  303  ordinary clip
//	0i78zdYFQGI 828s  303  ordinary video
//
// — which is also the measurement that rules out doing this by duration. The
// 14-second video would have been thrown out of the feed by any length rule
// short enough to catch the other two.
type ShortChecker struct {
	client  *http.Client
	baseURL string
}

func NewShortChecker(timeout time.Duration) *ShortChecker {
	return &ShortChecker{
		client: &http.Client{
			Timeout: timeout,
			// The redirect *is* the answer, so it must not be followed. Left to
			// itself the client would chase it to /watch and report 200, which
			// is the same status a Short returns — and every video would look
			// like a Short.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		baseURL: "https://www.youtube.com/shorts/",
	}
}

func (c *ShortChecker) IsShort(ctx context.Context, videoID string) (bool, error) {
	// GET rather than HEAD: the body is discarded either way, and what is read
	// is the status line.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+videoID, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "+
			"(KHTML, like Gecko) Chrome/126.0 Safari/537.36")

	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusOK:
		return true, nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return false, nil
	default:
		// A 404 is a video that has gone, and a 429 is this address being asked
		// too much. Neither answers the question, and recording either as "not a
		// Short" would close the question for good on a wrong answer.
		return false, fmt.Errorf("shorts probe %s: unexpected status %d", videoID, resp.StatusCode)
	}
}
