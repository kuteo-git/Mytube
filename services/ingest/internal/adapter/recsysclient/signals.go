// Package recsysclient tells the ranker about behaviour ingest imported.
//
// It exists because there are two records of "who follows what", and they can
// disagree. `catalog.subscriptions` is the authoritative list — it is what the
// Subscriptions page shows — and `recsys.signals` is what ranking reads, built
// from the events it has been told about. The gateway keeps both in step on
// every click; when the account importer wrote only the first, the ranker went
// on believing that member followed nobody.
//
// The symptom was exact and silent: a household member with nineteen imported
// subscriptions and eight hundred videos from them saw a page of twenty-four
// cards all labelled "Suggested video", because every one of their channels
// looked unsubscribed to the thing doing the ranking.
package recsysclient

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	recsysv1 "github.com/lucnguyen/local-youtube/gen/go/recsys/v1"
	"github.com/lucnguyen/local-youtube/gen/go/recsys/v1/recsysv1connect"
)

type Signals struct {
	client recsysv1connect.RecommendationServiceClient
}

func New(httpClient *http.Client, baseURL string) *Signals {
	return &Signals{client: recsysv1connect.NewRecommendationServiceClient(httpClient, baseURL)}
}

// Subscribed records that a member follows a channel, as the gateway does when
// somebody presses the button.
//
// The channel id travels in video_id, which is how SUBSCRIBE signals have
// always been written — see the gateway's own handler and BuildProfile, which
// reads it back out of the same column.
func (s *Signals) Subscribed(ctx context.Context, userID, channelID string, occurredAt time.Time) error {
	return s.record(ctx, userID, recsysv1.SignalType_SIGNAL_TYPE_SUBSCRIBE, channelID, occurredAt)
}

// Liked records a like the member gave the video on YouTube.
func (s *Signals) Liked(ctx context.Context, userID, videoID string, occurredAt time.Time) error {
	return s.record(ctx, userID, recsysv1.SignalType_SIGNAL_TYPE_LIKE, videoID, occurredAt)
}

func (s *Signals) record(
	ctx context.Context, userID string, kind recsysv1.SignalType, target string, at time.Time,
) error {
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.client.RecordSignal(ctx, connect.NewRequest(&recsysv1.RecordSignalRequest{
		UserId:     userID,
		Type:       kind,
		VideoId:    target,
		OccurredAt: timestamppb.New(at),
	}))
	return err
}
