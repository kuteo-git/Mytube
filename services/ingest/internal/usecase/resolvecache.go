package usecase

import (
	"sync"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// resolveCache remembers resolved upstream URLs for as long as they stay valid.
//
// Resolving costs a yt-dlp process — measured at ~1.4s — and that is the whole
// of the delay before a video starts. The URLs it returns are signed and stay
// good for hours, so paying that price once per video rather than once per
// request is the difference between pressing play and waiting.
//
// It also makes prefetching worth doing: hovering a card can pay the 1.4s in
// advance precisely because pressing play afterwards finds the answer already
// here.
//
// Entries are dropped a margin before their stated expiry, so a URL handed out
// is never one about to die mid-playback.
type resolveCache struct {
	mu      sync.Mutex
	entries map[string]domain.StreamLocation
}

// How early to treat a URL as expired. A stream handed to a player has to stay
// valid for the length of the video, not merely for the instant it is served.
const resolveExpiryMargin = 30 * time.Minute

func newResolveCache() *resolveCache {
	return &resolveCache{entries: make(map[string]domain.StreamLocation)}
}

func (c *resolveCache) get(key string) (domain.StreamLocation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return domain.StreamLocation{}, false
	}
	if time.Now().After(entry.ExpiresAt.Add(-resolveExpiryMargin)) {
		delete(c.entries, key)
		return domain.StreamLocation{}, false
	}
	return entry, true
}

func (c *resolveCache) put(key string, location domain.StreamLocation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = location
}

// forget drops an entry whose URL turned out to be dead before its stated
// expiry — the player reporting a load failure is the only reliable signal that
// a signed URL has been revoked early.
func (c *resolveCache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}
