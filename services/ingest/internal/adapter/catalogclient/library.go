// Package catalogclient implements domain.Library over the catalog service.
//
// Ingest owns no video metadata of its own: the moment a video is resolved it
// belongs to catalog. This adapter is the only path between the two.
package catalogclient

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type Library struct {
	client catalogv1connect.CatalogServiceClient
	// userID is used until the identity service exists, same as the gateway's
	// devUserID — subscriptions are per-user, and Phase 1 has exactly one.
	userID string
}

func New(httpClient *http.Client, baseURL, userID string) *Library {
	return &Library{
		client: catalogv1connect.NewCatalogServiceClient(httpClient, baseURL),
		userID: userID,
	}
}

var mediaStates = map[string]catalogv1.MediaState{
	"QUEUED":      catalogv1.MediaState_MEDIA_STATE_QUEUED,
	"DOWNLOADING": catalogv1.MediaState_MEDIA_STATE_DOWNLOADING,
	"READY":       catalogv1.MediaState_MEDIA_STATE_READY,
	"EVICTED":     catalogv1.MediaState_MEDIA_STATE_EVICTED,
	"FAILED":      catalogv1.MediaState_MEDIA_STATE_FAILED,
}

func (l *Library) FindBySourceURL(ctx context.Context, sourceURL string) (string, bool, error) {
	resp, err := l.client.FindBySourceURL(ctx, connect.NewRequest(&catalogv1.FindBySourceURLRequest{
		SourceUrl: sourceURL,
	}))
	if err != nil {
		return "", false, err
	}
	if resp.Msg.Video == nil {
		return "", false, nil
	}
	return resp.Msg.GetVideo().GetId(), true, nil
}

func (l *Library) UpsertChannel(ctx context.Context, v domain.ExternalVideo) error {
	_, err := l.client.UpsertChannel(ctx, connect.NewRequest(&catalogv1.UpsertChannelRequest{
		Channel: &catalogv1.Channel{
			Id:     v.ChannelID,
			Name:   v.ChannelName,
			Handle: v.ChannelHandle,
		},
	}))
	return err
}

func (l *Library) UpsertVideo(ctx context.Context, v domain.ExternalVideo, state string) error {
	_, err := l.client.UpsertVideo(ctx, connect.NewRequest(&catalogv1.UpsertVideoRequest{
		Video: &catalogv1.Video{
			Id:              v.ID,
			Title:           v.Title,
			Channel:         &catalogv1.Channel{Id: v.ChannelID},
			DurationSeconds: v.DurationSeconds,
			ViewCount:       v.ViewCount,
			PublishedAt:     timestamppb.New(v.PublishedAt),
			// The remote thumbnail URL is stored as-is and hotlinked by the
			// client. Fetching 240 images for videos that mostly will not be
			// watched would cost more disk than the videos we actually keep.
			ThumbnailPath: v.ThumbnailURL,
			Description:   v.Description,
			Hashtags:      v.Hashtags,
			Topics:        v.Topics,
			MediaState:    mediaStates[state],
			SourceUrl:     v.SourceURL,
		},
	}))
	return err
}

func (l *Library) SetMediaState(ctx context.Context, videoID, state, mediaPath string, sizeBytes int64, subtitles []domain.SubtitleTrack) error {
	tracks := make([]*catalogv1.SubtitleTrack, 0, len(subtitles))
	for _, t := range subtitles {
		tracks = append(tracks, &catalogv1.SubtitleTrack{
			Language:  t.Language,
			Label:     t.Label,
			Path:      t.Path,
			Generated: t.Generated,
		})
	}

	_, err := l.client.SetMediaState(ctx, connect.NewRequest(&catalogv1.SetMediaStateRequest{
		VideoId:    videoID,
		MediaState: mediaStates[state],
		MediaPath:  mediaPath,
		SizeBytes:  sizeBytes,
		Subtitles:  tracks,
	}))
	return err
}

// SourceURLFor lets the resolver turn a local video id back into the upstream
// URL, which is what makes instant playback work for a video that is queued or
// still downloading.
func (l *Library) SourceURLFor(ctx context.Context, videoID string) (string, error) {
	resp, err := l.client.GetVideo(ctx, connect.NewRequest(&catalogv1.GetVideoRequest{
		VideoId: videoID,
	}))
	if err != nil {
		return "", err
	}
	return resp.Msg.GetVideo().GetSourceUrl(), nil
}

func (l *Library) UpsertChannelArtwork(ctx context.Context, m domain.ChannelMetadata, avatarPath, bannerPath string) error {
	_, err := l.client.UpsertChannel(ctx, connect.NewRequest(&catalogv1.UpsertChannelRequest{
		Channel: &catalogv1.Channel{
			Id:              m.ID,
			Name:            m.Name,
			Handle:          m.Handle,
			AvatarPath:      avatarPath,
			BannerPath:      bannerPath,
			SubscriberCount: m.SubscriberCount,
			Verified:        m.Verified,
		},
	}))
	return err
}

// ListSubscribedChannels lets the scanner treat subscriptions as a content
// source alongside topics.yaml.
func (l *Library) ListSubscribedChannels(ctx context.Context) ([]domain.SubscribedChannel, error) {
	resp, err := l.client.ListSubscriptions(ctx, connect.NewRequest(&catalogv1.ListSubscriptionsRequest{
		UserId: l.userID,
	}))
	if err != nil {
		return nil, err
	}

	out := make([]domain.SubscribedChannel, 0, len(resp.Msg.GetChannels()))
	for _, c := range resp.Msg.GetChannels() {
		out = append(out, domain.SubscribedChannel{
			ID:     c.GetId(),
			Handle: c.GetHandle(),
			Name:   c.GetName(),
		})
	}
	return out, nil
}
