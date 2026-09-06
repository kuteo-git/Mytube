// Package catalogclient pulls the video projection from the catalog service.
//
// This is the only channel between the two services. Recsys never touches the
// catalog database; it asks over RPC and caches the answer briefly, because a
// library of a few hundred videos changes far more slowly than the feed is
// requested.
package catalogclient

import (
	"context"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

const pageSize = 500

// How long a background refresh may take before it is abandoned. Generous,
// because nothing is waiting on it; bounded, because a hung catalog must not
// leave inFlight set for ever and block every refresh after it.
const refreshTimeout = 2 * time.Minute

type FeatureSource struct {
	client catalogv1connect.CatalogServiceClient
	ttl    time.Duration

	mu       sync.Mutex
	cached   []domain.VideoFeatures
	cachedAt time.Time
	loaded   bool
	inFlight bool
}

func New(httpClient *http.Client, baseURL string, ttl time.Duration) *FeatureSource {
	return &FeatureSource{
		client: catalogv1connect.NewCatalogServiceClient(httpClient, baseURL),
		ttl:    ttl,
	}
}

// ListVideoFeatures answers from the projection, refreshing it in the
// background rather than in front of the caller.
//
// The refresh reads the whole library — measured at 43,079 videos, about 1.3s —
// and blocking on it once per TTL put that second and a third onto one feed
// request in every thirty. A viewer pulling the grid down cannot tell a slow
// server from a broken one, and the projection they would have been served
// instead is a few seconds out of date on a library that changes hourly. So a
// stale answer is served and the refresh runs behind it.
//
// The one caller that still waits is the first after a restart, which has
// nothing stale to be served.
func (f *FeatureSource) ListVideoFeatures(ctx context.Context) ([]domain.VideoFeatures, error) {
	f.mu.Lock()
	fresh := time.Since(f.cachedAt) < f.ttl
	if f.loaded && fresh {
		cached := f.cached
		f.mu.Unlock()
		return cached, nil
	}
	if f.loaded {
		// Stale, and that is good enough to answer with. Start one refresh
		// behind it — one, however many requests arrive while it runs.
		cached := f.cached
		if !f.inFlight {
			f.inFlight = true
			go f.refresh()
		}
		f.mu.Unlock()
		return cached, nil
	}
	f.mu.Unlock()

	// Nothing has ever been loaded, so there is nothing to serve but the wait.
	features, err := f.fetchAll(ctx)
	if err != nil {
		// Serve a stale projection rather than an empty feed if catalog blips.
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.loaded {
			return f.cached, nil
		}
		return nil, err
	}

	f.mu.Lock()
	f.cached, f.cachedAt, f.loaded = features, time.Now(), true
	f.mu.Unlock()
	return features, nil
}

// refresh replaces the projection out of band. Its context is its own: the
// request that noticed the staleness has long been answered, and hanging the
// refresh off that request's context would cancel it the moment the viewer got
// their page.
func (f *FeatureSource) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()

	features, err := f.fetchAll(ctx)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.inFlight = false
	if err != nil {
		// Keep serving what is held. Leaving cachedAt where it is means the
		// next request tries again rather than waiting out another TTL.
		return
	}
	f.cached, f.cachedAt, f.loaded = features, time.Now(), true
}

func (f *FeatureSource) fetchAll(ctx context.Context) ([]domain.VideoFeatures, error) {
	var (
		out   []domain.VideoFeatures
		token string
	)

	for {
		resp, err := f.client.ListVideoFeatures(ctx, connect.NewRequest(&catalogv1.ListVideoFeaturesRequest{
			PageSize:  pageSize,
			PageToken: token,
		}))
		if err != nil {
			return nil, err
		}

		for _, v := range resp.Msg.GetVideos() {
			out = append(out, domain.VideoFeatures{
				VideoID:         v.GetVideoId(),
				ChannelID:       v.GetChannelId(),
				Topics:          v.GetTopics(),
				Hashtags:        v.GetHashtags(),
				PublishedAt:     v.GetPublishedAt().AsTime(),
				AddedAt:         v.GetAddedAt().AsTime(),
				DurationSeconds: v.GetDurationSeconds(),
				IsShort:         v.GetIsShort(),
				IsLive:          v.GetIsLiveNow(),
				DiscoveredVia:   v.GetDiscoveredVia(),
				MediaState:      v.GetMediaState().String(),
				Language:        v.GetLanguage(),
				ViewCount:       v.GetViewCount(),
			})
		}

		token = resp.Msg.GetNextPageToken()
		if token == "" {
			return out, nil
		}
	}
}
