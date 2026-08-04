package httpapi

import (
	"context"
	"io"
	"strconv"
	"sync"
	"time"
)

// How long resolved media URLs are reused.
//
// YouTube signs them for around six hours; a fraction of that leaves plenty of
// room for a stream opened at the end of the window to run to completion.
const remuxURLTTL = 90 * time.Minute

// cachedRemuxURLs reuses resolved media URLs across the life of one viewing.
//
// Seeking in a muxed stream means opening a new mux, and without this every
// seek would also mean a fresh yt-dlp process — measured at about 1.4 seconds
// on top of the 2.2 the mux itself takes. That is the difference between a seek
// that feels slow and one that feels broken.
//
// Keyed by video and height because those are what the resolution depends on;
// the offset is applied by ffmpeg to the same URLs.
type cachedRemuxURLs struct {
	remux Remuxer

	mu      sync.Mutex
	entries map[string]remuxURLEntry
}

type remuxURLEntry struct {
	urls      []string
	expiresAt time.Time
}

func newCachedRemuxURLs(remux Remuxer) *cachedRemuxURLs {
	return &cachedRemuxURLs{remux: remux, entries: map[string]remuxURLEntry{}}
}

// ResolveRemuxURLs returns cached URLs when they are still good, and resolves
// otherwise.
func (c *cachedRemuxURLs) ResolveRemuxURLs(
	ctx context.Context, videoURL string, height int32,
) ([]string, error) {
	key := videoURL + "|" + strconv.Itoa(int(height))

	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.urls, nil
	}

	urls, err := c.remux.ResolveRemuxURLs(ctx, videoURL, height)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.entries[key] = remuxURLEntry{urls: urls, expiresAt: time.Now().Add(remuxURLTTL)}
	// Kept bounded rather than swept on a timer: a household watches a handful
	// of videos at a time, and a map that only grows while the process lives is
	// a leak however slow.
	if len(c.entries) > 256 {
		for k, e := range c.entries {
			if time.Now().After(e.expiresAt) {
				delete(c.entries, k)
			}
		}
	}
	c.mu.Unlock()

	return urls, nil
}

// OpenRemux passes straight through; only resolution is worth caching.
func (c *cachedRemuxURLs) OpenRemux(
	ctx context.Context, urls []string, startSeconds, audioStartSeconds float64,
) (io.ReadCloser, error) {
	return c.remux.OpenRemux(ctx, urls, startSeconds, audioStartSeconds)
}

// ProbeKeyframe passes straight through. The player asks /start once and hands
// the answer back with the stream request, so the same mark is not probed twice
// and there is nothing here worth remembering.
func (c *cachedRemuxURLs) ProbeKeyframe(
	ctx context.Context, videoURL string, at float64,
) (float64, error) {
	return c.remux.ProbeKeyframe(ctx, videoURL, at)
}
