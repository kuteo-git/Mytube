// Package catalog is the cast service's way into the catalogue: describing the
// videos being queued, and writing what was listened to into history.
package catalog

import (
	"context"
	"strings"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
	"github.com/lucnguyen/local-youtube/services/cast/internal/usecase"
)

type Client struct {
	client catalogv1connect.CatalogServiceClient
}

func New(client catalogv1connect.CatalogServiceClient) *Client { return &Client{client: client} }

// Videos answers in the order asked. A video the catalogue no longer has is
// answered as unavailable, so it is left out of the queue rather than queued
// with nothing to play.
func (c *Client) Videos(ctx context.Context, userID string, ids []string) ([]usecase.CatalogVideo, error) {
	resp, err := c.client.BatchGetVideos(ctx, connect.NewRequest(&catalogv1.BatchGetVideosRequest{
		VideoIds: ids, UserId: userID,
	}))
	if err != nil {
		return nil, err
	}
	byID := map[string]*catalogv1.Video{}
	for _, v := range resp.Msg.GetVideos() {
		byID[v.GetId()] = v
	}
	out := make([]usecase.CatalogVideo, 0, len(ids))
	for _, id := range ids {
		v, ok := byID[id]
		if !ok {
			out = append(out, usecase.CatalogVideo{Item: domain.Item{VideoID: id}, Unavailable: true})
			continue
		}
		out = append(out, toCatalogVideo(v))
	}
	return out, nil
}

func toCatalogVideo(v *catalogv1.Video) usecase.CatalogVideo {
	return usecase.CatalogVideo{
		Item: domain.Item{
			VideoID:      v.GetId(),
			Title:        v.GetTitle(),
			Channel:      v.GetChannel().GetName(),
			ThumbnailURL: mediaURL(v.GetThumbnailPath()),
			DurationS:    int(v.GetDurationSeconds()),
		},
		// Upcoming as well as on air: neither has an end to play to, and the
		// gateway refuses both as a file for the same reason.
		Live:           v.GetIsLiveNow() || v.GetLiveStatus() == "is_live" || v.GetLiveStatus() == "is_upcoming",
		Unavailable:    v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_UNAVAILABLE,
		SpokenLanguage: v.GetSpokenLanguage(),
	}
}

// mediaURL is web/src/shared/lib/media.ts's rule: thumbnails are usually
// hotlinked and absolute, and anything relative is under /media.
func mediaURL(ref string) string {
	switch {
	case ref == "", strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"), strings.HasPrefix(ref, "/"):
		return ref
	default:
		return "/media/" + ref
	}
}

func (c *Client) RecordProgress(ctx context.Context, userID, videoID string, positionS, durationS int) error {
	fraction := float32(0)
	if durationS > 0 {
		fraction = min(float32(positionS)/float32(durationS), 1)
	}
	_, err := c.client.RecordWatchProgress(ctx, connect.NewRequest(&catalogv1.RecordWatchProgressRequest{
		UserId: userID, VideoId: videoID, PositionSeconds: int32(positionS), WatchedFraction: fraction,
	}))
	return err
}
