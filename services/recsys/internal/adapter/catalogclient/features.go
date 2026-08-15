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

type FeatureSource struct {
	client catalogv1connect.CatalogServiceClient
	ttl    time.Duration

	mu        sync.Mutex
	cached    []domain.VideoFeatures
	cachedAt  time.Time
	inFlight  bool
	refreshed chan struct{}
}

func New(httpClient *http.Client, baseURL string, ttl time.Duration) *FeatureSource {
	return &FeatureSource{
		client: catalogv1connect.NewCatalogServiceClient(httpClient, baseURL),
		ttl:    ttl,
	}
}

func (f *FeatureSource) ListVideoFeatures(ctx context.Context) ([]domain.VideoFeatures, error) {
	f.mu.Lock()
	if time.Since(f.cachedAt) < f.ttl && f.cached != nil {
		cached := f.cached
		f.mu.Unlock()
		return cached, nil
	}
	f.mu.Unlock()

	features, err := f.fetchAll(ctx)
	if err != nil {
		// Serve a stale projection rather than an empty feed if catalog blips.
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.cached != nil {
			return f.cached, nil
		}
		return nil, err
	}

	f.mu.Lock()
	f.cached, f.cachedAt = features, time.Now()
	f.mu.Unlock()
	return features, nil
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
