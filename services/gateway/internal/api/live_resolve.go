package api

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"

	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
)

// How long a resolved broadcast is reused.
//
// Resolving runs yt-dlp, which is the expensive thing in this system, and two
// requests want the same answer within a second of each other: `/stream` asks
// whether the broadcast has captions, and `master.m3u8` asks for its playlists
// immediately afterwards. Without this, opening one live video ran yt-dlp
// twice.
//
// Short, because the URLs inside carry `expire/` about six hours out and a
// broadcast can end at any moment. A minute is long enough to cover one
// viewer's opening sequence and short enough that "is this still on air" is
// never answered from memory for long.
const liveResolveTTL = time.Minute

type liveResolveEntry struct {
	res *ingestv1.ResolveLiveResponse
	at  time.Time
}

type liveResolveCache struct {
	mu      sync.Mutex
	entries map[string]liveResolveEntry
}

func newLiveResolveCache() *liveResolveCache {
	return &liveResolveCache{entries: map[string]liveResolveEntry{}}
}

// resolveLive answers from memory when it can, and from ingest when it cannot.
//
// Keyed by the source URL rather than the video id, because that is what the
// RPC takes and two catalogue rows for one broadcast would otherwise resolve
// separately.
//
// Errors are not cached. A failure here is usually a network moment or yt-dlp
// being turned away, and remembering it would make one bad second into a minute
// of a video refusing to play.
func (g *Gateway) resolveLive(ctx context.Context, sourceURL string) (*ingestv1.ResolveLiveResponse, error) {
	now := time.Now()

	g.liveResolves.mu.Lock()
	if e, ok := g.liveResolves.entries[sourceURL]; ok && now.Sub(e.at) < liveResolveTTL {
		g.liveResolves.mu.Unlock()
		return e.res, nil
	}
	g.liveResolves.mu.Unlock()

	live, err := g.ingest.ResolveLive(ctx, connect.NewRequest(&ingestv1.ResolveLiveRequest{
		Url: sourceURL,
	}))
	if err != nil {
		return nil, err
	}

	g.liveResolves.mu.Lock()
	g.liveResolves.entries[sourceURL] = liveResolveEntry{res: live.Msg, at: now}
	// Swept here rather than on a timer: this map holds one entry per broadcast
	// anybody has opened, which is a handful, and a goroutine to tidy a handful
	// of entries is more machinery than the thing it tidies.
	for url, e := range g.liveResolves.entries {
		if now.Sub(e.at) >= liveResolveTTL {
			delete(g.liveResolves.entries, url)
		}
	}
	g.liveResolves.mu.Unlock()

	return live.Msg, nil
}
