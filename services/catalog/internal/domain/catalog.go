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
	// MediaUnavailable is upstream's final answer: members-only, private or
	// removed. Kept apart from MediaFailed because the next action differs —
	// there is nothing to retry, and the UI must stop offering it.
	MediaUnavailable MediaState = "UNAVAILABLE"
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
	BannerPath      string
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
	Subtitles       []SubtitleTrack
	UserState       *VideoUserState
	Language        string
}

// SubtitleTrack is a caption file on disk, fetched with the media.
type SubtitleTrack struct {
	Language  string
	Label     string
	Path      string
	Generated bool
}

type VideoUserState struct {
	WatchProgress        float32
	WatchPositionSeconds int32
	LastWatchedAt        time.Time
	Reaction             Reaction
	InWatchLater         bool
}

type CommentAuthor struct {
	UserID     *string // nil for imported YouTube comments
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

	// ImportComment is a YouTube comment ready to be batch-inserted into the
	// catalog. Its own author is the source platform — not a local user — so the
	// author is a handle rather than a user_id.
	type ImportComment struct {
		ID              string
		ParentID        string
		AuthorHandle    string
		Text            string
		PublishedAtUnix int64
		LikeCount       int64
		PinnedBy        *string
	}

type CommentSort string

const (
	SortTop    CommentSort = "TOP"
	SortNewest CommentSort = "NEWEST"
)

type SuggestionKind string

const (
	SuggestTitle   SuggestionKind = "TITLE"
	SuggestTopic   SuggestionKind = "TOPIC"
	SuggestChannel SuggestionKind = "CHANNEL"
)

type Suggestion struct {
	Text       string
	Kind       SuggestionKind
	VideoCount int32
}

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
	Language        string
	ViewCount       int64
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

// EvictionCandidate is a downloaded, unpinned video, offered oldest-accessed
// first. Only the media file is ever removed: metadata, thumbnail and watch
// history stay, so the grid can offer a one-click re-download rather than
// pretending the video never existed.
type EvictionCandidate struct {
	VideoID   string
	MediaPath string
	SizeBytes int64
}

// EvictionRepository is the slice of the repository the sweep needs. Kept
// narrow so the sweep can be tested without a database.
type EvictionRepository interface {
	UsedBytes(ctx context.Context) (int64, error)
	ListEvictionCandidates(ctx context.Context, downToBytes int64) ([]EvictionCandidate, error)
	MarkEvicted(ctx context.Context, videoID string) error
}

// Repository is the port the use cases depend on. The Postgres adapter is one
// implementation; tests can supply another without touching business logic.
type Repository interface {
	GetVideo(ctx context.Context, videoID, userID string) (Video, error)
	// BatchGetVideos must return videos in the order the ids were given, so a
	// ranking produced elsewhere survives hydration. Missing ids are skipped.
	BatchGetVideos(ctx context.Context, videoIDs []string, userID string) ([]Video, error)
	SearchVideos(ctx context.Context, query, userID string, page Page) ([]Video, error)
	Suggest(ctx context.Context, query string, limit int32) ([]Suggestion, error)
	ListChannelVideos(ctx context.Context, channelID, userID string, page Page) ([]Video, error)
	GetChannel(ctx context.Context, channelID, userID string) (Channel, int32, error)
	ListTopics(ctx context.Context, minVideoCount int32) ([]Topic, error)
	ListVideoFeatures(ctx context.Context, page Page) ([]VideoFeatures, error)

	UpsertChannel(ctx context.Context, c Channel) (Channel, error)
	UpsertVideo(ctx context.Context, v Video) (Video, error)
	SetMediaState(ctx context.Context, videoID string, state MediaState, mediaPath string, sizeBytes int64, subtitles []SubtitleTrack) error
	FindBySourceURL(ctx context.Context, sourceURL, userID string) (Video, error)

	ListComments(ctx context.Context, videoID string, sort CommentSort, page Page) ([]Comment, int32, error)
	CreateComment(ctx context.Context, c Comment, parentID *string) (Comment, error)
	// ImportComments batch-inserts YouTube comments. Idempotent: rows whose id
	// already exists are skipped (ON CONFLICT DO NOTHING). Returns the number
	// of rows actually inserted.
	ImportComments(ctx context.Context, videoID string, comments []ImportComment) (int32, error)

	RecordWatchProgress(ctx context.Context, userID, videoID string, positionSeconds int32, watchedFraction float32) error
	SetReaction(ctx context.Context, userID, videoID string, reaction Reaction) (int64, error)
	SetSubscription(ctx context.Context, userID, channelID string, subscribed bool) error
	ListSubscriptions(ctx context.Context, userID string) ([]Channel, error)
	ListHistory(ctx context.Context, userID string, page Page) ([]Video, error)

	GetStorageUsage(ctx context.Context, budgetBytes int64) (StorageUsage, error)
	SetPinned(ctx context.Context, videoID string, pinned bool) error
	ListPinnedVideos(ctx context.Context, userID string, page Page) ([]Video, error)
}
