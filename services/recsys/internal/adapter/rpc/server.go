// Package rpc adapts the ranking use cases to ConnectRPC.
package rpc

import (
	"context"

	"connectrpc.com/connect"

	recsysv1 "github.com/lucnguyen/local-youtube/gen/go/recsys/v1"
	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
	"github.com/lucnguyen/local-youtube/services/recsys/internal/usecase"
)

type Server struct {
	ranker *usecase.Ranker
}

func NewServer(ranker *usecase.Ranker) *Server {
	return &Server{ranker: ranker}
}

var reasons = map[domain.Reason]recsysv1.RecommendationReason{
	domain.ReasonContinueWatching:  recsysv1.RecommendationReason_RECOMMENDATION_REASON_CONTINUE_WATCHING,
	domain.ReasonRecentlyAdded:     recsysv1.RecommendationReason_RECOMMENDATION_REASON_RECENTLY_ADDED,
	domain.ReasonNeverWatched:      recsysv1.RecommendationReason_RECOMMENDATION_REASON_NEVER_WATCHED,
	domain.ReasonSubscribedChannel: recsysv1.RecommendationReason_RECOMMENDATION_REASON_SUBSCRIBED_CHANNEL,
	domain.ReasonRewatch:           recsysv1.RecommendationReason_RECOMMENDATION_REASON_REWATCH,
	domain.ReasonSameChannel:       recsysv1.RecommendationReason_RECOMMENDATION_REASON_SAME_CHANNEL,
	domain.ReasonSharedTags:        recsysv1.RecommendationReason_RECOMMENDATION_REASON_SHARED_TAGS,
	domain.ReasonBounced:           recsysv1.RecommendationReason_RECOMMENDATION_REASON_BOUNCED,
	domain.ReasonDiscovery:         recsysv1.RecommendationReason_RECOMMENDATION_REASON_DISCOVERY,
}

var signalTypes = map[recsysv1.SignalType]domain.SignalType{
	recsysv1.SignalType_SIGNAL_TYPE_WATCH:       domain.SignalWatch,
	recsysv1.SignalType_SIGNAL_TYPE_LIKE:        domain.SignalLike,
	recsysv1.SignalType_SIGNAL_TYPE_DISLIKE:     domain.SignalDislike,
	recsysv1.SignalType_SIGNAL_TYPE_SUBSCRIBE:   domain.SignalSubscribe,
	recsysv1.SignalType_SIGNAL_TYPE_UNSUBSCRIBE: domain.SignalUnsubscribe,
	recsysv1.SignalType_SIGNAL_TYPE_SEARCH:      domain.SignalSearch,
	recsysv1.SignalType_SIGNAL_TYPE_SKIP:        domain.SignalSkip,
}

func toProto(ranked []domain.RankedVideo) []*recsysv1.RankedVideo {
	out := make([]*recsysv1.RankedVideo, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, &recsysv1.RankedVideo{
			VideoId: r.VideoID,
			Score:   float32(r.Score),
			Reason:  reasons[r.Reason],
		})
	}
	return out
}

func (s *Server) GetFeed(ctx context.Context, req *connect.Request[recsysv1.GetFeedRequest]) (*connect.Response[recsysv1.GetFeedResponse], error) {
	snapshotID, offset := decodeToken(req.Msg.GetPageToken())

	// An absent mix leaves every field zero, which normalised() reads as
	// "unset" and answers with the defaults — so a caller that has never heard
	// of the setting gets exactly the feed it always got.
	mix := usecase.FeedMix{
		Subscribed: int(req.Msg.GetMix().GetSubscribedPercent()),
		Affinity:   int(req.Msg.GetMix().GetAffinityPercent()),
		Discovery:  int(req.Msg.GetMix().GetDiscoveryPercent()),
	}

	page, err := s.ranker.GetFeedPage(ctx, req.Msg.GetUserId(), req.Msg.GetCategory(),
		snapshotID, req.Msg.GetPageSize(), offset, mix, req.Msg.GetLanguages())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	next := ""
	if page.Remaining > 0 {
		next = encodeToken(page.SnapshotID, offset+int32(len(page.Videos)))
	}
	return connect.NewResponse(&recsysv1.GetFeedResponse{
		Videos:         toProto(page.Videos),
		NextPageToken:  next,
		RemainingCount: int32(page.Remaining),
	}), nil
}

func (s *Server) GetUpNext(ctx context.Context, req *connect.Request[recsysv1.GetUpNextRequest]) (*connect.Response[recsysv1.GetUpNextResponse], error) {
	ranked, nextToken, err := s.ranker.GetUpNext(ctx, req.Msg.GetUserId(), req.Msg.GetCurrentVideoId(),
		req.Msg.GetChannelFilter(), req.Msg.GetPageSize(), req.Msg.GetPageToken())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&recsysv1.GetUpNextResponse{
		Videos:        toProto(ranked),
		NextPageToken: nextToken,
	}), nil
}

func (s *Server) GetMostWatched(ctx context.Context, req *connect.Request[recsysv1.GetMostWatchedRequest]) (*connect.Response[recsysv1.GetMostWatchedResponse], error) {
	ranked, err := s.ranker.MostWatched(ctx, req.Msg.GetUserId(), req.Msg.GetLimit())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&recsysv1.GetMostWatchedResponse{Videos: toProto(ranked)}), nil
}

func (s *Server) RecordSignal(ctx context.Context, req *connect.Request[recsysv1.RecordSignalRequest]) (*connect.Response[recsysv1.RecordSignalResponse], error) {
	signal := domain.Signal{
		UserID:          req.Msg.GetUserId(),
		Type:            signalTypes[req.Msg.GetType()],
		VideoID:         req.Msg.GetVideoId(),
		Query:           req.Msg.GetQuery(),
		WatchedFraction: req.Msg.GetWatchedFraction(),
	}
	if ts := req.Msg.GetOccurredAt(); ts != nil {
		signal.OccurredAt = ts.AsTime()
	}
	if signal.Type == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errUnknownSignal)
	}

	if err := s.ranker.RecordSignal(ctx, signal); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&recsysv1.RecordSignalResponse{}), nil
}

func (s *Server) RecordImpressions(ctx context.Context, req *connect.Request[recsysv1.RecordImpressionsRequest]) (*connect.Response[recsysv1.RecordImpressionsResponse], error) {
	if err := s.ranker.RecordImpressions(ctx, req.Msg.GetUserId(), req.Msg.GetVideoIds()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&recsysv1.RecordImpressionsResponse{}), nil
}
