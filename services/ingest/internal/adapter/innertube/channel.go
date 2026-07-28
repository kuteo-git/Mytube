package innertube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

const (
	browseEndpoint  = "https://www.youtube.com/youtubei/v1/browse"
	resolveEndpoint = "https://www.youtube.com/youtubei/v1/navigation/resolve_url"
	// Selects a channel's Videos tab. Everything else about ordering and paging
	// comes from tokens YouTube hands back, so this is the only encoded blob the
	// code has to know.
	videosTabParams = "EgZ2aWRlb3PyBgQKAjoA"
)

// channelPage is the raw shape collected while walking the response, before it
// is turned into the domain type.
//
// Sort tokens are read out of the response rather than constructed: YouTube
// ships the Latest/Popular/Oldest chips with their own continuation tokens, so
// sorting never depends on guessing an encoded parameter that could change.
type channelPage struct {
	videos        []domain.ExternalVideo
	sortOrder     []string
	sortTokens    map[string]string
	nextPageToken string
}

type browseRequest struct {
	BrowseID     string      `json:"browseId,omitempty"`
	Params       string      `json:"params,omitempty"`
	Continuation string      `json:"continuation,omitempty"`
	Context      nextContext `json:"context"`
}

type resolveRequest struct {
	URL     string      `json:"url"`
	Context nextContext `json:"context"`
}

func webContext() nextContext {
	return nextContext{Client: nextClient{
		ClientName:    clientName,
		ClientVersion: clientVersion,
		Hl:            "en",
		Gl:            "US",
	}}
}

func (c *Client) post(ctx context.Context, endpoint string, payload any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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
		return nil, fmt.Errorf("innertube %s: status %d", endpoint, resp.StatusCode)
	}

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// ResolveChannelID turns a handle ("@mkbhd") into the UC… id that browse needs.
// A value that is already a channel id is returned untouched.
func (c *Client) ResolveChannelID(ctx context.Context, channel string) (string, error) {
	if strings.HasPrefix(channel, "UC") {
		return channel, nil
	}

	handle := channel
	if !strings.HasPrefix(handle, "@") {
		handle = "@" + handle
	}

	decoded, err := c.post(ctx, c.resolveURL, resolveRequest{
		URL:     "https://www.youtube.com/" + handle,
		Context: webContext(),
	})
	if err != nil {
		return "", err
	}

	if id, ok := findString(decoded, "browseId"); ok && strings.HasPrefix(id, "UC") {
		return id, nil
	}
	return "", fmt.Errorf("innertube: could not resolve %q to a channel id", channel)
}

// ChannelUploads lists a channel's videos. An empty pageToken starts at the
// Videos tab in YouTube's default order; any token — a sort chip's or the next
// page's — continues from there.
func (c *Client) ChannelUploads(ctx context.Context, browseID, pageToken string) (domain.ChannelUploads, error) {
	payload := browseRequest{Context: webContext()}
	if pageToken == "" {
		payload.BrowseID = browseID
		payload.Params = videosTabParams
	} else {
		payload.Continuation = pageToken
	}

	decoded, err := c.post(ctx, c.browseURL, payload)
	if err != nil {
		return domain.ChannelUploads{}, err
	}

	page := channelPage{sortTokens: map[string]string{}}
	collectChannelPage(decoded, &page)

	out := domain.ChannelUploads{
		Videos:        page.videos,
		NextPageToken: page.nextPageToken,
	}
	// Preserve YouTube's own chip order rather than a map's iteration order, so
	// the control does not reshuffle itself between requests.
	for _, label := range page.sortOrder {
		out.SortOptions = append(out.SortOptions, domain.SortOption{
			Label: label,
			Token: page.sortTokens[label],
		})
	}
	return out, nil
}

// collectChannelPage walks the response once, picking up the three things worth
// having: the videos, the sort chips, and the token for the next page. The shape
// is undocumented and deeply nested, so this searches by key rather than
// asserting a path that a redesign would silently break.
func collectChannelPage(node any, page *channelPage) {
	switch v := node.(type) {
	case map[string]any:
		if lockup, ok := v["lockupViewModel"].(map[string]any); ok {
			if video, ok := parseLockup(lockup); ok {
				page.videos = append(page.videos, video)
			}
		}
		if chip, ok := v["chipViewModel"].(map[string]any); ok {
			label, _ := chip["text"].(string)
			token, hasToken := stringAt(chip,
				"tapCommand", "innertubeCommand", "continuationCommand", "token")
			if hasToken && label != "" {
				if _, seen := page.sortTokens[label]; !seen {
					page.sortOrder = append(page.sortOrder, label)
				}
				page.sortTokens[label] = token
			}
		}
		if cont, ok := v["continuationItemRenderer"].(map[string]any); ok {
			if token, ok := stringAt(cont, "continuationEndpoint", "continuationCommand", "token"); ok {
				page.nextPageToken = token
			}
		}
		for _, child := range v {
			collectChannelPage(child, page)
		}
	case []any:
		for _, child := range v {
			collectChannelPage(child, page)
		}
	}
}

func parseLockup(lockup map[string]any) (domain.ExternalVideo, bool) {
	if t, _ := lockup["contentType"].(string); t != "LOCKUP_CONTENT_TYPE_VIDEO" {
		return domain.ExternalVideo{}, false
	}
	id, _ := lockup["contentId"].(string)
	if id == "" {
		return domain.ExternalVideo{}, false
	}

	video := domain.ExternalVideo{
		ID:        id,
		SourceURL: "https://www.youtube.com/watch?v=" + id,
	}

	// Walked by an exact path, not searched for. Each lockup embeds a whole
	// context menu whose entries also have "content" fields ("Add to queue",
	// "Save to Watch later"), and Go randomises map iteration order — so a
	// search finds one of those instead of the title, at random.
	if title, ok := stringAt(lockup, "metadata", "lockupMetadataViewModel", "title", "content"); ok {
		video.Title = title
	}

	// The metadata row is a list of free-text parts — "1.5M views" and
	// "21 hours ago" — in whichever order and quantity the client decided on.
	// Classifying by content rather than position is what keeps this working
	// when a channel omits one of them.
	for _, part := range metadataParts(lockup) {
		switch {
		case strings.Contains(part, "view"):
			video.ViewCount = parseCompactCount(part)
		case strings.Contains(part, "ago"):
			video.PublishedAt = parseRelativeTime(part, time.Now())
		}
	}

	if url, ok := largestThumbnail(lockup); ok {
		video.ThumbnailURL = url
	}
	if d, ok := badgeDuration(lockup); ok {
		video.DurationSeconds = d
	}
	return video, true
}

// metadataParts reads the "1.5M views • 21 hours ago" row.
//
// Reached by an exact path for the same reason the title is: the surrounding
// context menu carries its own text fields, and a search would return those
// instead on some runs and not others.
func metadataParts(lockup map[string]any) []string {
	node, ok := valueAt(lockup,
		"metadata", "lockupMetadataViewModel", "metadata", "contentMetadataViewModel", "metadataRows")
	if !ok {
		return nil
	}
	rows, ok := node.([]any)
	if !ok {
		return nil
	}

	var out []string
	for _, row := range rows {
		parts, ok := valueAt(row, "metadataParts")
		if !ok {
			continue
		}
		list, ok := parts.([]any)
		if !ok {
			continue
		}
		for _, p := range list {
			if text, ok := stringAt(p, "text", "content"); ok {
				out = append(out, text)
			}
		}
	}
	return out
}

func largestThumbnail(lockup map[string]any) (string, bool) {
	image, ok := lockup["contentImage"].(map[string]any)
	if !ok {
		return "", false
	}

	best, bestWidth := "", -1.0
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			url, hasURL := v["url"].(string)
			width, hasWidth := v["width"].(float64)
			if hasURL && hasWidth && width > bestWidth {
				best, bestWidth = url, width
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(image)
	return best, best != ""
}

func badgeDuration(lockup map[string]any) (int32, bool) {
	var found int32
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if badge, ok := v["thumbnailBadgeViewModel"].(map[string]any); ok {
				if text, ok := badge["text"].(string); ok {
					if d := parseDuration(text); d > 0 && found == 0 {
						found = d
					}
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(lockup["contentImage"])
	return found, found > 0
}

// valueAt walks an exact path and returns whatever is there.
func valueAt(node any, path ...string) (any, bool) {
	current := node
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// stringAt reads a string down an exact path. Use this wherever the value has a
// known home: a search would pick an arbitrary same-named field out of the
// surrounding menus and overlays, and pick a different one on the next run.
func stringAt(node any, path ...string) (string, bool) {
	value, ok := valueAt(node, path...)
	if !ok {
		return "", false
	}
	s, ok := value.(string)
	return s, ok && s != ""
}

// findString returns the first string value stored under key anywhere in node.
func findString(node any, key string) (string, bool) {
	switch v := node.(type) {
	case map[string]any:
		if s, ok := v[key].(string); ok && s != "" {
			return s, true
		}
		for _, child := range v {
			if s, ok := findString(child, key); ok {
				return s, true
			}
		}
	case []any:
		for _, child := range v {
			if s, ok := findString(child, key); ok {
				return s, true
			}
		}
	}
	return "", false
}

var compactCount = regexp.MustCompile(`([\d.,]+)\s*([KMB]?)`)

// parseCompactCount reads "1.5M views" and "12,345 views". A view count that
// cannot be read becomes zero, which the UI renders as "no count" rather than
// as the lie that the video has never been watched.
func parseCompactCount(text string) int64 {
	m := compactCount.FindStringSubmatch(strings.ToUpper(text))
	if m == nil {
		return 0
	}

	digits := strings.ReplaceAll(m[1], ",", "")
	value, err := strconv.ParseFloat(digits, 64)
	if err != nil {
		return 0
	}

	switch m[2] {
	case "K":
		value *= 1_000
	case "M":
		value *= 1_000_000
	case "B":
		value *= 1_000_000_000
	}
	return int64(value)
}

var relativeTime = regexp.MustCompile(`(\d+)\s+(second|minute|hour|day|week|month|year)s?\s+ago`)

// parseRelativeTime turns "2 years ago" into an absolute instant.
//
// The result is approximate by nature — that is all YouTube discloses here —
// but storing an instant rather than the phrase is what keeps the display
// honest over time: "21 hours ago" saved today would still claim 21 hours
// tomorrow, whereas a timestamp re-renders itself as "2 days ago" on its own.
func parseRelativeTime(text string, now time.Time) time.Time {
	m := relativeTime.FindStringSubmatch(strings.ToLower(text))
	if m == nil {
		return time.Time{}
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return time.Time{}
	}

	switch m[2] {
	case "second":
		return now.Add(-time.Duration(n) * time.Second)
	case "minute":
		return now.Add(-time.Duration(n) * time.Minute)
	case "hour":
		return now.Add(-time.Duration(n) * time.Hour)
	case "day":
		return now.AddDate(0, 0, -n)
	case "week":
		return now.AddDate(0, 0, -7*n)
	case "month":
		return now.AddDate(0, -n, 0)
	case "year":
		return now.AddDate(-n, 0, 0)
	}
	return time.Time{}
}
