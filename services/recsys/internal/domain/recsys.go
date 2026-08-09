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
	// ReasonBounced marks a video this viewer opened and left almost at once.
	// Distinct from never having watched it: the viewer has already answered
	// the question this video asks, and the answer was no.
	ReasonBounced Reason = "BOUNCED"
	// ReasonDiscovery is for videos outside the viewer's affinity — unwatched
	// channels, unfamiliar topics. Reserving a quota for them keeps the feed
	// from converging entirely on what is already known.
	ReasonDiscovery Reason = "DISCOVERY"
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
	Language        string
	ViewCount       int64
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
	// When each disliked video was turned down, not merely that it was.
	//
	// Presence is still the whole of the rule for the video itself — it is
	// skipped, whenever it was pressed. The time is for what a dislike is taken
	// to say about topics and channels in general, which ages like every other
	// signal here.
	Disliked   map[string]time.Time
	Subscribed map[string]bool
	// Videos shown recently, suppressed to keep the feed from repeating.
	RecentImpressions map[string]bool
	// Videos watched in the last few hours. Distinct from WatchedFraction,
	// which records whether something was ever watched and not when: deciding
	// what follows a video needs "just now" specifically.
	RecentlyWatched map[string]bool
	// The handful of videos this viewer has actually watched in the current
	// sitting, with how far they got.
	//
	// Watch history says what someone likes; this says what they came for today.
	// The two disagree constantly and the disagreement is the point — a viewer
	// with two years of music in their history who has spent the last hour on
	// cooking videos is asking for cooking videos, and a feed built only from the
	// accumulated total cannot tell. It is deliberately small: a handful of
	// recent videos, not a second history.
	SessionWatched map[string]float32
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
	// MostWatched ranks videos by how much time this user has actually spent on
	// them. Watch signals are appended on a timer during playback, so counting
	// them measures time watched rather than times opened — which is the honest
	// reading of "the ones I play most".
	MostWatched(ctx context.Context, userID string, limit int32) ([]RankedVideo, error)
	// VideoRetention reports how far the average viewer gets through each
	// video, across everyone. Unlike the rest of the profile this says nothing
	// about who is asking — it is a property of the video, and it is what lets
	// ranking prefer material that holds an audience over material that merely
	// gets opened.
	VideoRetention(ctx context.Context) (map[string]float32, error)
	// ImpressionCoverage reports how many times each video has been shown to
	// anyone in the household. Like VideoRetention it is a property of the video
	// rather than of the viewer asking, and it is what lets the discovery share
	// work through the library instead of re-offering the same few videos every
	// time somebody asks for something new.
	ImpressionCoverage(ctx context.Context) (map[string]int, error)
}

// FeatureSource pulls the video projection from the catalog service over RPC.
type FeatureSource interface {
	ListVideoFeatures(ctx context.Context) ([]VideoFeatures, error)
}
