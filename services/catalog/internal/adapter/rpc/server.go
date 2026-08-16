// Package rpc adapts the catalog use cases to the ConnectRPC surface.
// It only translates between protobuf messages and domain types; no business
// rule may live here.
package rpc

import (
	"context"
	"errors"
	"strconv"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
	"github.com/lucnguyen/local-youtube/services/catalog/internal/usecase"
)

type Server struct {
	catalog *usecase.Catalog
}

func NewServer(catalog *usecase.Catalog) *Server {
	return &Server{catalog: catalog}
}

// toConnectErr maps domain errors onto Connect codes so HTTP status codes are
// meaningful without the transport leaking into the use cases.
func toConnectErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, domain.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

// Offset-based paging behind an opaque token: the client never depends on the
// encoding, so it can become a keyset cursor later without an API change.
func decodePageToken(token string) int32 {
	offset, err := strconv.Atoi(token)
	if err != nil || offset < 0 {
		return 0
	}
	return int32(offset)
}

func nextPageToken(offset, pageSize int32, returned int) string {
	if returned < int(pageSize) {
		return ""
	}
	return strconv.Itoa(int(offset) + returned)
}

// ---------------------------------------------------------------------------
// Mapping
// ---------------------------------------------------------------------------

var mediaStates = map[domain.MediaState]catalogv1.MediaState{
	domain.MediaQueued:      catalogv1.MediaState_MEDIA_STATE_QUEUED,
	domain.MediaDownloading: catalogv1.MediaState_MEDIA_STATE_DOWNLOADING,
	domain.MediaReady:       catalogv1.MediaState_MEDIA_STATE_READY,
	domain.MediaEvicted:     catalogv1.MediaState_MEDIA_STATE_EVICTED,
	domain.MediaFailed:      catalogv1.MediaState_MEDIA_STATE_FAILED,
	domain.MediaUnavailable: catalogv1.MediaState_MEDIA_STATE_UNAVAILABLE,
}

var reactionsToProto = map[domain.Reaction]catalogv1.Reaction{
	domain.ReactionNone:    catalogv1.Reaction_REACTION_NONE,
	domain.ReactionLike:    catalogv1.Reaction_REACTION_LIKE,
	domain.ReactionDislike: catalogv1.Reaction_REACTION_DISLIKE,
}

func reactionFromProto(r catalogv1.Reaction) domain.Reaction {
	switch r {
	case catalogv1.Reaction_REACTION_LIKE:
		return domain.ReactionLike
	case catalogv1.Reaction_REACTION_DISLIKE:
		return domain.ReactionDislike
	default:
		return domain.ReactionNone
	}
}

func channelToProto(c domain.Channel) *catalogv1.Channel {
	return &catalogv1.Channel{
		Id:              c.ID,
		Name:            c.Name,
		Handle:          c.Handle,
		AvatarPath:      c.AvatarPath,
		BannerPath:      c.BannerPath,
		SubscriberCount: c.SubscriberCount,
		Verified:        c.Verified,
		Subscribed:      c.Subscribed,
	}
}

func videoToProto(v domain.Video) *catalogv1.Video {
	out := &catalogv1.Video{
		Id:              v.ID,
		Title:           v.Title,
		Channel:         channelToProto(v.Channel),
		DurationSeconds: v.DurationSeconds,
		ViewCount:       v.ViewCount,
		AddedAt:         timestamppb.New(v.AddedAt),
		ThumbnailPath:   v.ThumbnailPath,
		Description:     v.Description,
		Hashtags:        v.Hashtags,
		Topics:          v.Topics,
		MediaState:      mediaStates[v.MediaState],
		MediaPath:       v.MediaPath,
		SizeBytes:       v.SizeBytes,
		Pinned:          v.Pinned,
		SourceUrl:       v.SourceURL,
		LikeCount:       v.LikeCount,
		Language:        v.Language,
		DiscoveredVia:   v.DiscoveredVia,
	}

	for _, t := range v.Subtitles {
		out.Subtitles = append(out.Subtitles, &catalogv1.SubtitleTrack{
			Language:  t.Language,
			Label:     t.Label,
			Path:      t.Path,
			Generated: t.Generated,
		})
	}

	if !v.PublishedAt.IsZero() {
		out.PublishedAt = timestamppb.New(v.PublishedAt)
	}

	if v.UserState != nil {
		out.UserState = &catalogv1.VideoUserState{
			WatchProgress:        v.UserState.WatchProgress,
			WatchPositionSeconds: v.UserState.WatchPositionSeconds,
			LastWatchedAt:        timestamppb.New(v.UserState.LastWatchedAt),
			Reaction:             reactionsToProto[v.UserState.Reaction],
			InWatchLater:         v.UserState.InWatchLater,
		}
	}
	return out
}

func videosToProto(vs []domain.Video) []*catalogv1.Video {
	out := make([]*catalogv1.Video, 0, len(vs))
	for _, v := range vs {
		out = append(out, videoToProto(v))
	}
	return out
}

func commentToProto(c domain.Comment) *catalogv1.Comment {
	var userID *string
	if c.Author.UserID != nil {
		uid := *c.Author.UserID
		userID = &uid
	}
	return &catalogv1.Comment{
		Id:      c.ID,
		VideoId: c.VideoID,
		Author: &catalogv1.CommentAuthor{
			UserId:     userID,
			Handle:     c.Author.Handle,
			AvatarPath: c.Author.AvatarPath,
		},
		Text:        c.Body,
		PublishedAt: timestamppb.New(c.PublishedAt),
		LikeCount:   c.LikeCount,
		PinnedBy:    c.PinnedBy,
		Replies:     commentsToProto(c.Replies),
		ReplyCount:  int32(len(c.Replies)),
	}
}

func commentsToProto(cs []domain.Comment) []*catalogv1.Comment {
	out := make([]*catalogv1.Comment, 0, len(cs))
	for _, c := range cs {
		out = append(out, commentToProto(c))
	}
	return out
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

func (s *Server) GetVideo(ctx context.Context, req *connect.Request[catalogv1.GetVideoRequest]) (*connect.Response[catalogv1.GetVideoResponse], error) {
	v, err := s.catalog.GetVideo(ctx, req.Msg.GetVideoId(), req.Msg.GetUserId())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.GetVideoResponse{Video: videoToProto(v)}), nil
}

func (s *Server) BatchGetVideos(ctx context.Context, req *connect.Request[catalogv1.BatchGetVideosRequest]) (*connect.Response[catalogv1.BatchGetVideosResponse], error) {
	vs, err := s.catalog.BatchGetVideos(ctx, req.Msg.GetVideoIds(), req.Msg.GetUserId())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.BatchGetVideosResponse{Videos: videosToProto(vs)}), nil
}

func (s *Server) SearchVideos(ctx context.Context, req *connect.Request[catalogv1.SearchVideosRequest]) (*connect.Response[catalogv1.SearchVideosResponse], error) {
	offset := decodePageToken(req.Msg.GetPageToken())
	vs, err := s.catalog.SearchVideos(ctx, req.Msg.GetQuery(), req.Msg.GetUserId(), req.Msg.GetPageSize(), offset)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.SearchVideosResponse{
		Videos:        videosToProto(vs),
		NextPageToken: nextPageToken(offset, req.Msg.GetPageSize(), len(vs)),
	}), nil
}

var suggestionKinds = map[domain.SuggestionKind]catalogv1.SuggestionKind{
	domain.SuggestTitle:   catalogv1.SuggestionKind_SUGGESTION_KIND_TITLE,
	domain.SuggestTopic:   catalogv1.SuggestionKind_SUGGESTION_KIND_TOPIC,
	domain.SuggestChannel: catalogv1.SuggestionKind_SUGGESTION_KIND_CHANNEL,
}

func (s *Server) Suggest(ctx context.Context, req *connect.Request[catalogv1.SuggestRequest]) (*connect.Response[catalogv1.SuggestResponse], error) {
	suggestions, err := s.catalog.Suggest(ctx, req.Msg.GetQuery(), req.Msg.GetLimit())
	if err != nil {
		return nil, toConnectErr(err)
	}

	out := make([]*catalogv1.Suggestion, 0, len(suggestions))
	for _, sg := range suggestions {
		out = append(out, &catalogv1.Suggestion{
			Text:       sg.Text,
			Kind:       suggestionKinds[sg.Kind],
			VideoCount: sg.VideoCount,
		})
	}
	return connect.NewResponse(&catalogv1.SuggestResponse{Suggestions: out}), nil
}

func (s *Server) ListChannelVideos(ctx context.Context, req *connect.Request[catalogv1.ListChannelVideosRequest]) (*connect.Response[catalogv1.ListChannelVideosResponse], error) {
	offset := decodePageToken(req.Msg.GetPageToken())
	vs, err := s.catalog.ListChannelVideos(ctx, req.Msg.GetChannelId(), req.Msg.GetUserId(), req.Msg.GetPageSize(), offset)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.ListChannelVideosResponse{
		Videos:        videosToProto(vs),
		NextPageToken: nextPageToken(offset, req.Msg.GetPageSize(), len(vs)),
	}), nil
}

func (s *Server) GetChannel(ctx context.Context, req *connect.Request[catalogv1.GetChannelRequest]) (*connect.Response[catalogv1.GetChannelResponse], error) {
	c, count, err := s.catalog.GetChannel(ctx, req.Msg.GetChannelId(), req.Msg.GetUserId())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.GetChannelResponse{
		Channel:    channelToProto(c),
		VideoCount: count,
	}), nil
}

func (s *Server) ListTopics(ctx context.Context, req *connect.Request[catalogv1.ListTopicsRequest]) (*connect.Response[catalogv1.ListTopicsResponse], error) {
	ts, err := s.catalog.ListTopics(ctx, req.Msg.GetMinVideoCount())
	if err != nil {
		return nil, toConnectErr(err)
	}
	out := make([]*catalogv1.Topic, 0, len(ts))
	for _, t := range ts {
		out = append(out, &catalogv1.Topic{Name: t.Name, VideoCount: t.VideoCount})
	}
	return connect.NewResponse(&catalogv1.ListTopicsResponse{Topics: out}), nil
}

func (s *Server) ListVideoFeatures(ctx context.Context, req *connect.Request[catalogv1.ListVideoFeaturesRequest]) (*connect.Response[catalogv1.ListVideoFeaturesResponse], error) {
	offset := decodePageToken(req.Msg.GetPageToken())
	fs, err := s.catalog.ListVideoFeatures(ctx, req.Msg.GetPageSize(), offset)
	if err != nil {
		return nil, toConnectErr(err)
	}

	return connect.NewResponse(&catalogv1.ListVideoFeaturesResponse{
		Videos:        featuresToProto(fs),
		NextPageToken: nextPageToken(offset, req.Msg.GetPageSize(), len(fs)),
	}), nil
}

// featuresToProto is everything the ranker is told about a video.
//
// A named function rather than a loop inside the handler, so that a field can
// be asserted to survive the crossing. `is_short` did not: the column existed,
// the query selected it, the repository filled the struct and recsys read it
// off the wire — and this mapping never copied it, so every video crossed as
// "not a Short" and the feed kept showing them. Nothing failed anywhere. A
// false bool is absent from proto3 JSON, so the wire looked exactly the same as
// a field nobody had added yet.
func featuresToProto(fs []domain.VideoFeatures) []*catalogv1.VideoFeatures {
	out := make([]*catalogv1.VideoFeatures, 0, len(fs))
	for _, f := range fs {
		v := &catalogv1.VideoFeatures{
			VideoId:         f.VideoID,
			ChannelId:       f.ChannelID,
			Topics:          f.Topics,
			Hashtags:        f.Hashtags,
			AddedAt:         timestamppb.New(f.AddedAt),
			DurationSeconds: f.DurationSeconds,
			MediaState:      mediaStates[f.MediaState],
			Language:        f.Language,
			ViewCount:       f.ViewCount,
			IsShort:         f.IsShort,
			DiscoveredVia:   f.DiscoveredVia,
		}
		// Left unset rather than sent as the zero instant: the ranker excludes
		// an undated video outright, and epoch zero is a date.
		if !f.PublishedAt.IsZero() {
			v.PublishedAt = timestamppb.New(f.PublishedAt)
		}
		out = append(out, v)
	}
	return out
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

var mediaStatesFromProto = map[catalogv1.MediaState]domain.MediaState{
	catalogv1.MediaState_MEDIA_STATE_QUEUED:      domain.MediaQueued,
	catalogv1.MediaState_MEDIA_STATE_DOWNLOADING: domain.MediaDownloading,
	catalogv1.MediaState_MEDIA_STATE_READY:       domain.MediaReady,
	catalogv1.MediaState_MEDIA_STATE_EVICTED:     domain.MediaEvicted,
	catalogv1.MediaState_MEDIA_STATE_FAILED:      domain.MediaFailed,
	catalogv1.MediaState_MEDIA_STATE_UNAVAILABLE: domain.MediaUnavailable,
}

func (s *Server) UpsertChannel(ctx context.Context, req *connect.Request[catalogv1.UpsertChannelRequest]) (*connect.Response[catalogv1.UpsertChannelResponse], error) {
	in := req.Msg.GetChannel()
	c, err := s.catalog.UpsertChannel(ctx, domain.Channel{
		ID:              in.GetId(),
		Name:            in.GetName(),
		Handle:          in.GetHandle(),
		AvatarPath:      in.GetAvatarPath(),
		BannerPath:      in.GetBannerPath(),
		SubscriberCount: in.GetSubscriberCount(),
		Verified:        in.GetVerified(),
	})
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.UpsertChannelResponse{Channel: channelToProto(c)}), nil
}

func (s *Server) UpsertVideo(ctx context.Context, req *connect.Request[catalogv1.UpsertVideoRequest]) (*connect.Response[catalogv1.UpsertVideoResponse], error) {
	in := req.Msg.GetVideo()

	v := domain.Video{
		ID:              in.GetId(),
		Title:           in.GetTitle(),
		Channel:         domain.Channel{ID: in.GetChannel().GetId()},
		DurationSeconds: in.GetDurationSeconds(),
		ViewCount:       in.GetViewCount(),
		ThumbnailPath:   in.GetThumbnailPath(),
		Description:     in.GetDescription(),
		Hashtags:        in.GetHashtags(),
		Topics:          in.GetTopics(),
		MediaState:      mediaStatesFromProto[in.GetMediaState()],
		MediaPath:       in.GetMediaPath(),
		SizeBytes:       in.GetSizeBytes(),
		SourceURL:       in.GetSourceUrl(),
		Language:        in.GetLanguage(),
		DiscoveredVia:   in.GetDiscoveredVia(),
	}
	if ts := in.GetPublishedAt(); ts != nil {
		v.PublishedAt = ts.AsTime()
	}

	saved, err := s.catalog.UpsertVideo(ctx, v)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.UpsertVideoResponse{Video: videoToProto(saved)}), nil
}

func (s *Server) SetShort(ctx context.Context, req *connect.Request[catalogv1.SetShortRequest]) (*connect.Response[catalogv1.SetShortResponse], error) {
	if err := s.catalog.SetShort(ctx, req.Msg.GetVideoId(), req.Msg.GetIsShort()); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.SetShortResponse{}), nil
}

func (s *Server) ListUncheckedShorts(ctx context.Context, req *connect.Request[catalogv1.ListUncheckedShortsRequest]) (*connect.Response[catalogv1.ListUncheckedShortsResponse], error) {
	ids, err := s.catalog.ListUncheckedShorts(ctx, req.Msg.GetLimit())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.ListUncheckedShortsResponse{VideoIds: ids}), nil
}

func (s *Server) SetMediaState(ctx context.Context, req *connect.Request[catalogv1.SetMediaStateRequest]) (*connect.Response[catalogv1.SetMediaStateResponse], error) {
	tracks := make([]domain.SubtitleTrack, 0, len(req.Msg.GetSubtitles()))
	for _, t := range req.Msg.GetSubtitles() {
		tracks = append(tracks, domain.SubtitleTrack{
			Language:  t.GetLanguage(),
			Label:     t.GetLabel(),
			Path:      t.GetPath(),
			Generated: t.GetGenerated(),
		})
	}

	err := s.catalog.SetMediaState(ctx,
		req.Msg.GetVideoId(),
		mediaStatesFromProto[req.Msg.GetMediaState()],
		req.Msg.GetMediaPath(),
		req.Msg.GetSizeBytes(),
		tracks)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.SetMediaStateResponse{}), nil
}

func (s *Server) FindBySourceURL(ctx context.Context, req *connect.Request[catalogv1.FindBySourceURLRequest]) (*connect.Response[catalogv1.FindBySourceURLResponse], error) {
	v, err := s.catalog.FindBySourceURL(ctx, req.Msg.GetSourceUrl(), "")
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// A miss is a normal answer here, not an error: ingest asks this
			// question precisely to find out whether it must do any work.
			return connect.NewResponse(&catalogv1.FindBySourceURLResponse{}), nil
		}
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.FindBySourceURLResponse{Video: videoToProto(v)}), nil
}

// ---------------------------------------------------------------------------
// Comments
// ---------------------------------------------------------------------------

func (s *Server) ListComments(ctx context.Context, req *connect.Request[catalogv1.ListCommentsRequest]) (*connect.Response[catalogv1.ListCommentsResponse], error) {
	sort := domain.SortTop
	if req.Msg.GetSort() == catalogv1.CommentSort_COMMENT_SORT_NEWEST {
		sort = domain.SortNewest
	}
	offset := decodePageToken(req.Msg.GetPageToken())

	cs, total, err := s.catalog.ListComments(ctx, req.Msg.GetVideoId(), sort, req.Msg.GetPageSize(), offset)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.ListCommentsResponse{
		Comments:      commentsToProto(cs),
		TotalCount:    total,
		NextPageToken: nextPageToken(offset, req.Msg.GetPageSize(), len(cs)),
	}), nil
}

func (s *Server) CreateComment(ctx context.Context, req *connect.Request[catalogv1.CreateCommentRequest]) (*connect.Response[catalogv1.CreateCommentResponse], error) {
	c, err := s.catalog.CreateComment(ctx,
		req.Msg.GetVideoId(), req.Msg.GetUserId(), req.Msg.GetUserId(),
		req.Msg.GetText(), req.Msg.ParentCommentId)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.CreateCommentResponse{Comment: commentToProto(c)}), nil
}

// ---------------------------------------------------------------------------
// Interaction
// ---------------------------------------------------------------------------

func (s *Server) RecordWatchProgress(ctx context.Context, req *connect.Request[catalogv1.RecordWatchProgressRequest]) (*connect.Response[catalogv1.RecordWatchProgressResponse], error) {
	err := s.catalog.RecordWatchProgress(ctx, req.Msg.GetUserId(), req.Msg.GetVideoId(),
		req.Msg.GetPositionSeconds(), req.Msg.GetWatchedFraction())
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.RecordWatchProgressResponse{}), nil
}

func (s *Server) SetReaction(ctx context.Context, req *connect.Request[catalogv1.SetReactionRequest]) (*connect.Response[catalogv1.SetReactionResponse], error) {
	likes, err := s.catalog.SetReaction(ctx, req.Msg.GetUserId(), req.Msg.GetVideoId(),
		reactionFromProto(req.Msg.GetReaction()))
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.SetReactionResponse{LikeCount: likes}), nil
}

func (s *Server) SetSubscription(ctx context.Context, req *connect.Request[catalogv1.SetSubscriptionRequest]) (*connect.Response[catalogv1.SetSubscriptionResponse], error) {
	if err := s.catalog.SetSubscription(ctx, req.Msg.GetUserId(), req.Msg.GetChannelId(), req.Msg.GetSubscribed()); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.SetSubscriptionResponse{}), nil
}

func (s *Server) ListSubscriptions(ctx context.Context, req *connect.Request[catalogv1.ListSubscriptionsRequest]) (*connect.Response[catalogv1.ListSubscriptionsResponse], error) {
	list := func() ([]domain.Channel, error) {
		if req.Msg.GetAllMembers() {
			return s.catalog.ListAllSubscribedChannels(ctx)
		}
		return s.catalog.ListSubscriptions(ctx, req.Msg.GetUserId())
	}
	channels, err := list()
	if err != nil {
		return nil, toConnectErr(err)
	}
	out := make([]*catalogv1.Channel, 0, len(channels))
	for _, c := range channels {
		out = append(out, channelToProto(c))
	}
	return connect.NewResponse(&catalogv1.ListSubscriptionsResponse{Channels: out}), nil
}

func (s *Server) SetWatchLater(ctx context.Context, req *connect.Request[catalogv1.SetWatchLaterRequest]) (*connect.Response[catalogv1.SetWatchLaterResponse], error) {
	if err := s.catalog.SetWatchLater(ctx,
		req.Msg.GetUserId(), req.Msg.GetVideoId(), req.Msg.GetInWatchLater()); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.SetWatchLaterResponse{}), nil
}

func (s *Server) ListWatchLater(ctx context.Context, req *connect.Request[catalogv1.ListWatchLaterRequest]) (*connect.Response[catalogv1.ListWatchLaterResponse], error) {
	offset := decodePageToken(req.Msg.GetPageToken())
	vs, err := s.catalog.ListWatchLater(ctx, req.Msg.GetUserId(), req.Msg.GetPageSize(), offset)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.ListWatchLaterResponse{
		Videos:        videosToProto(vs),
		NextPageToken: nextPageToken(offset, req.Msg.GetPageSize(), len(vs)),
	}), nil
}

func (s *Server) ListHistory(ctx context.Context, req *connect.Request[catalogv1.ListHistoryRequest]) (*connect.Response[catalogv1.ListHistoryResponse], error) {
	offset := decodePageToken(req.Msg.GetPageToken())
	vs, err := s.catalog.ListHistory(ctx, req.Msg.GetUserId(), req.Msg.GetPageSize(), offset)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.ListHistoryResponse{
		Videos:        videosToProto(vs),
		NextPageToken: nextPageToken(offset, req.Msg.GetPageSize(), len(vs)),
	}), nil
}

// ---------------------------------------------------------------------------
// Storage
// ---------------------------------------------------------------------------

func (s *Server) GetStorageUsage(ctx context.Context, _ *connect.Request[catalogv1.GetStorageUsageRequest]) (*connect.Response[catalogv1.GetStorageUsageResponse], error) {
	u, err := s.catalog.GetStorageUsage(ctx)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.GetStorageUsageResponse{
		UsedBytes:          u.UsedBytes,
		BudgetBytes:        u.BudgetBytes,
		DiskFreeBytes:      u.DiskFreeBytes,
		VideoCount:         u.VideoCount,
		EvictedCount:       u.EvictedCount,
		KeptCount:          u.KeptCount,
		EvictionCandidates: videosToProto(u.EvictionCandidates),
	}), nil
}

func (s *Server) SetPinned(ctx context.Context, req *connect.Request[catalogv1.SetPinnedRequest]) (*connect.Response[catalogv1.SetPinnedResponse], error) {
	if err := s.catalog.SetPinned(ctx,
		req.Msg.GetUserId(), req.Msg.GetVideoId(), req.Msg.GetPinned()); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.SetPinnedResponse{}), nil
}

func (s *Server) ListPinnedVideos(ctx context.Context, req *connect.Request[catalogv1.ListPinnedVideosRequest]) (*connect.Response[catalogv1.ListPinnedVideosResponse], error) {
	offset := decodePageToken(req.Msg.GetPageToken())
	vs, err := s.catalog.ListPinnedVideos(ctx, req.Msg.GetUserId(), req.Msg.GetPageSize(), offset)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.ListPinnedVideosResponse{
		Videos:        videosToProto(vs),
		NextPageToken: nextPageToken(offset, req.Msg.GetPageSize(), len(vs)),
	}), nil
}

func (s *Server) ImportComments(ctx context.Context, req *connect.Request[catalogv1.ImportCommentsRequest]) (*connect.Response[catalogv1.ImportCommentsResponse], error) {
	in := req.Msg.GetComments()
	comments := make([]domain.ImportComment, len(in))
	for i, c := range in {
		comments[i] = domain.ImportComment{
			ID:              c.GetId(),
			ParentID:        c.GetParentId(),
			AuthorHandle:    c.GetAuthorHandle(),
			Text:            c.GetText(),
			PublishedAtUnix: c.GetPublishedAtUnix(),
			LikeCount:       c.GetLikeCount(),
			PinnedBy:        c.PinnedBy,
		}
	}
	imported, err := s.catalog.ImportComments(ctx, req.Msg.GetVideoId(), comments)
	if err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&catalogv1.ImportCommentsResponse{Imported: imported}), nil
}
