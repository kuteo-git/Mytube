// Package domain holds the catalog entities and the ports the use cases need.
// It knows nothing about Postgres, Connect, or protobuf.
package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid argument")
)

type MediaState string

const (
	MediaQueued      MediaState = "QUEUED"
	MediaDownloading MediaState = "DOWNLOADING"
	MediaReady       MediaState = "READY"
	MediaEvicted     MediaState = "EVICTED"
	MediaFailed      MediaState = "FAILED"
)

type Reaction string

const (
	ReactionNone    Reaction = ""
	ReactionLike    Reaction = "LIKE"
	ReactionDislike Reaction = "DISLIKE"
)

type Channel struct {
	ID              string
	Name            string
	Handle          string
	AvatarPath      string
	SubscriberCount int64
	Verified        bool
	Subscribed      bool
}

type Video struct {
	ID              string
	Title           string
	Channel         Channel
	DurationSeconds int32
	ViewCount       int64
	PublishedAt     time.Time
	AddedAt         time.Time
	ThumbnailPath   string
	Description     string
	Hashtags        []string
	Topics          []string
	MediaState      MediaState
	MediaPath       string
	SizeBytes       int64
	Pinned          bool
	SourceURL       string
	LikeCount       int64
	UserState       *VideoUserState
}

type VideoUserState struct {
	WatchProgress        float32
	WatchPositionSeconds int32
	LastWatchedAt        time.Time
	Reaction             Reaction
	InWatchLater         bool
}

type CommentAuthor struct {
	UserID     string
	Handle     string
	AvatarPath string
}

type Comment struct {
	ID          string
	VideoID     string
	Author      CommentAuthor
	Body        string
	PublishedAt time.Time
	LikeCount   int64
	PinnedBy    *string
	Replies     []Comment
}

type CommentSort string

const (
	SortTop    CommentSort = "TOP"
	SortNewest CommentSort = "NEWEST"
)

type Topic struct {
	Name       string
	VideoCount int32
}

// VideoFeatures is the projection handed to the recommendation service. It
// carries only what ranking needs — no titles, no descriptions, no user state.
type VideoFeatures struct {
	VideoID         string
	ChannelID       string
	Topics          []string
	Hashtags        []string
	PublishedAt     time.Time
	AddedAt         time.Time
	DurationSeconds int32
	MediaState      MediaState
}

type StorageUsage struct {
	UsedBytes          int64
	BudgetBytes        int64
	DiskFreeBytes      int64
	VideoCount         int32
	EvictedCount       int32
	EvictionCandidates []Video
}

type Page struct {
	Size   int32
	Offset int32
}

// Repository is the port the use cases depend on. The Postgres adapter is one
// implementation; tests can supply another without touching business logic.
type Repository interface {
	GetVideo(ctx context.Context, videoID, userID string) (Video, error)
	// BatchGetVideos must return videos in the order the ids were given, so a
	// ranking produced elsewhere survives hydration. Missing ids are skipped.
	BatchGetVideos(ctx context.Context, videoIDs []string, userID string) ([]Video, error)
	SearchVideos(ctx context.Context, query, userID string, page Page) ([]Video, error)
	ListChannelVideos(ctx context.Context, channelID, userID string, page Page) ([]Video, error)
	GetChannel(ctx context.Context, channelID, userID string) (Channel, int32, error)
	ListTopics(ctx context.Context, minVideoCount int32) ([]Topic, error)
	ListVideoFeatures(ctx context.Context, page Page) ([]VideoFeatures, error)

	UpsertChannel(ctx context.Context, c Channel) (Channel, error)
	UpsertVideo(ctx context.Context, v Video) (Video, error)
	SetMediaState(ctx context.Context, videoID string, state MediaState, mediaPath string, sizeBytes int64) error
	FindBySourceURL(ctx context.Context, sourceURL, userID string) (Video, error)

	ListComments(ctx context.Context, videoID string, sort CommentSort, page Page) ([]Comment, int32, error)
	CreateComment(ctx context.Context, c Comment, parentID *string) (Comment, error)

	RecordWatchProgress(ctx context.Context, userID, videoID string, positionSeconds int32, watchedFraction float32) error
	SetReaction(ctx context.Context, userID, videoID string, reaction Reaction) (int64, error)
	SetSubscription(ctx context.Context, userID, channelID string, subscribed bool) error
	ListHistory(ctx context.Context, userID string, page Page) ([]Video, error)

	GetStorageUsage(ctx context.Context, budgetBytes int64) (StorageUsage, error)
	SetPinned(ctx context.Context, videoID string, pinned bool) error
}
