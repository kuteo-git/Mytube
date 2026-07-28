// Package domain holds the recommendation entities and ports.
package domain

import (
	"context"
	"time"
)

type SignalType string

const (
	SignalWatch       SignalType = "WATCH"
	SignalLike        SignalType = "LIKE"
	SignalDislike     SignalType = "DISLIKE"
	SignalSubscribe   SignalType = "SUBSCRIBE"
	SignalUnsubscribe SignalType = "UNSUBSCRIBE"
	SignalSearch      SignalType = "SEARCH"
	SignalSkip        SignalType = "SKIP"
)

type Reason string

const (
	ReasonContinueWatching  Reason = "CONTINUE_WATCHING"
	ReasonRecentlyAdded     Reason = "RECENTLY_ADDED"
	ReasonNeverWatched      Reason = "NEVER_WATCHED"
	ReasonSubscribedChannel Reason = "SUBSCRIBED_CHANNEL"
	ReasonRewatch           Reason = "REWATCH"
	ReasonSameChannel       Reason = "SAME_CHANNEL"
	ReasonSharedTags        Reason = "SHARED_TAGS"
)

type Signal struct {
	UserID          string
	Type            SignalType
	VideoID         string
	Query           string
	WatchedFraction float32
	OccurredAt      time.Time
}

// VideoFeatures mirrors the projection catalog exposes. Ranking never sees
// titles or descriptions, which keeps the two services genuinely decoupled.
type VideoFeatures struct {
	VideoID         string
	ChannelID       string
	Topics          []string
	Hashtags        []string
	PublishedAt     time.Time
	AddedAt         time.Time
	DurationSeconds int32
	MediaState      string
}

// UserProfile is derived from signals, never stored. Recomputing it per request
// is what makes recommendations react immediately to the last thing watched,
// with no batch job in the loop.
type UserProfile struct {
	// Highest watched fraction per video. Channel affinity is derived from this
	// at ranking time, because only the catalog projection knows which channel
	// a video belongs to.
	WatchedFraction map[string]float32
	Liked           map[string]bool
	Disliked        map[string]bool
	Subscribed      map[string]bool
	// Videos shown recently, suppressed to keep the feed from repeating.
	RecentImpressions map[string]bool
}

type RankedVideo struct {
	VideoID string
	Score   float64
	Reason  Reason
}

// SignalStore is the recsys-owned persistence port.
type SignalStore interface {
	AppendSignal(ctx context.Context, s Signal) error
	RecordImpressions(ctx context.Context, userID string, videoIDs []string) error
	BuildProfile(ctx context.Context, userID string, impressionWindow time.Duration) (UserProfile, error)
}

// FeatureSource pulls the video projection from the catalog service over RPC.
type FeatureSource interface {
	ListVideoFeatures(ctx context.Context) ([]VideoFeatures, error)
}
