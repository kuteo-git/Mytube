// Package domain holds the ingest entities and ports.
package domain

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid argument")
	// ErrNoProgressiveFormat is returned when upstream only offers adaptive
	// streams. A bare <video> element cannot play those, so instant playback is
	// simply unavailable for that video and the caller must wait for the copy.
	ErrNoProgressiveFormat = errors.New("no directly playable format available")
	// ErrJobNotRunning is what a heartbeat reports when the job it belongs to is
	// no longer running — cancelled, most often, by a viewer leaving the page.
	// The worker runs in its own process, so the job row is the only place the
	// two can speak, and the heartbeat is the only moment a transfer in flight
	// listens.
	ErrJobNotRunning = errors.New("job is no longer running")
)

type JobState string

const (
	JobQueued    JobState = "QUEUED"
	JobRunning   JobState = "RUNNING"
	JobSucceeded JobState = "SUCCEEDED"
	JobFailed    JobState = "FAILED"
	JobCancelled JobState = "CANCELLED"
)

type Job struct {
	ID              string
	SourceURL       string
	VideoID         string
	Title           string
	State           JobState
	PreferredHeight int32
	Progress        float32
	DownloadedBytes int64
	TotalBytes      int64
	ErrorMessage    string
	Attempts        int32
	RequestedBy     string
	CreatedAt       time.Time
	FinishedAt      *time.Time
}

// ExternalVideo is metadata resolved from upstream, before anything is stored.
type ExternalVideo struct {
	ID              string
	Title           string
	ChannelID       string
	ChannelName     string
	ChannelHandle   string
	DurationSeconds int32
	ViewCount       int64
	ThumbnailURL    string
	SourceURL       string
	PublishedAt     time.Time
	Description     string
	// Topic names a new video is filed under, taken from YouTube's own category
	// (e.g. "Science & Technology", "Gaming") — see CLAUDE.md §7 for the
	// reversal of the earlier "categories are too coarse" decision. Only
	// populated for videos the scanner has not seen before; an already-known
	// video keeps whatever topic it was first given, and is never re-fetched.
	Topics    []string
	Hashtags  []string
	InLibrary bool
	// How this video was reached: SOURCE, RELATED or SEARCH. Set by whichever
	// path found it, and empty where nobody said.
	DiscoveredVia string
	// Category is the first entry of YouTube's own category list for this
	// video. Only present when the caller did a full metadata fetch (Preview);
	// a flat playlist listing never carries it.
	Category string
	// Language is the video's primary language, as reported by yt-dlp on a full
	// metadata fetch or detected from the title when yt-dlp did not carry one.
	// Empty when neither source could determine it.
	Language string
	// IsLive marks a broadcast still in progress. Only a full metadata fetch
	// reports it; a flat listing never does.
	IsLive bool
}

// ChannelMetadata is everything a channel page needs that a flat video listing
// does not carry. Fetched once per source per scan rather than per video.
type ChannelMetadata struct {
	ID              string
	Name            string
	Handle          string
	AvatarURL       string
	BannerURL       string
	SubscriberCount int64
	Verified        bool
}

// StreamLocation is a short-lived, directly playable upstream URL.
type StreamLocation struct {
	URL       string
	Height    int32
	MimeType  string
	ExpiresAt time.Time
}

// DownloadResult describes what landed on disk.
type DownloadResult struct {
	// Relative to the media root, e.g. "dQw4w9WgXcQ/720p.mp4".
	MediaPath string
	SizeBytes int64
	Subtitles []SubtitleTrack
}

// SubtitleTrack is a caption file written next to the media file.
type SubtitleTrack struct {
	Language  string
	Label     string
	Path      string
	Generated bool
}

// Progress is reported by the downloader while a job runs.
type Progress struct {
	Fraction        float32
	DownloadedBytes int64
	TotalBytes      int64
}

// RSSEntry is one video from a channel's RSS feed. It carries just the fields
// a flat playlist listing cannot supply: exact publish dates and view counts.
// Only the 15 most recent uploads are present — older videos need Preview.
type RSSEntry struct {
	VideoID     string
	PublishedAt time.Time
	ViewCount   int64
	// Enough to build a catalog row without any other call.
	//
	// The scan pass only ever used this to fill in dates and view counts on rows
	// a listing had already produced, so these went unread. The fast pass over
	// subscribed channels has no listing behind it — the feed is the whole of
	// what it knows — and a video with no title is not a row anybody can be shown.
	Title        string
	ChannelID    string
	ChannelName  string
	ThumbnailURL string
}

// YouTubeComment is a single comment fetched from YouTube via yt-dlp's
// --write-comments. Every comment is returned at once — yt-dlp has no
// pagination — so the gateway batches them into catalog and the frontend
// paginates from there.
type YouTubeComment struct {
	ID              string
	ParentID        string
	Author          string
	AuthorID        string
	Text            string
	PublishedAtUnix int64
	LikeCount       int64
	PinnedBy        *string
}

// Downloader is the port over the external tool. Keeping it an interface is
// what lets the use cases be exercised without touching the network.
type Downloader interface {
	Search(ctx context.Context, query string, limit int32) ([]ExternalVideo, error)
	Preview(ctx context.Context, url string) (ExternalVideo, error)
	// offset skips entries already scanned, which is how the library is
	// deepened past the most recent few dozen uploads.
	ListPlaylist(ctx context.Context, url string, offset, limit int32) (string, []ExternalVideo, error)
	ResolveStream(ctx context.Context, videoURL string) (StreamLocation, error)
	// FetchSubtitles runs ahead of the media transfer so captions are usable
	// while the viewer is still watching the upstream stream. It never returns
	// an error: a video without captions is a working video.
	FetchSubtitles(ctx context.Context, videoURL, videoID string, height int32) []SubtitleTrack
	Download(ctx context.Context, videoURL, videoID string, height int32, onProgress func(Progress)) (DownloadResult, error)
	// ChannelInfo reads a channel's own metadata — artwork, handle, subscriber
	// count — none of which appears in a flat playlist listing.
	ChannelInfo(ctx context.Context, channelURL string) (ChannelMetadata, error)
	// FetchChannelArtwork downloads the avatar and banner and returns their
	// paths under the media root. A failure to fetch either is decoration lost,
	// never an error: the returned path is simply empty.
	FetchChannelArtwork(ctx context.Context, m ChannelMetadata) (avatarPath, bannerPath string)
	// SaveThumbnail downloads a video thumbnail into the media root and
	// returns its relative path, or "" on failure.
	SaveThumbnail(ctx context.Context, url, videoID string) string
	// FetchChannelFeed reads a channel's RSS feed and returns up to 15 entries
	// with exact publish dates and view counts — neither of which a flat
	// playlist listing carries. An error is a missed opportunity, not a failed
	// scan: the feed is supplementary.
	FetchChannelFeed(ctx context.Context, channelID string) ([]RSSEntry, error)
	// FetchComments reads YouTube comments for a video via yt-dlp's
	// --write-comments. Every comment is returned at once.
	FetchComments(ctx context.Context, videoURL string) ([]YouTubeComment, error)
}

// JobStore is the ingest-owned persistence port.
type JobStore interface {
	Enqueue(ctx context.Context, job Job) (Job, error)
	Get(ctx context.Context, jobID string) (Job, error)
	// hideDismissed drops jobs somebody has cleared off the Activity page.
	//
	// A parameter rather than a rule, because two callers read this list for
	// different reasons. The Activity page is being tidied and wants them gone;
	// the player is watching for its own download to land and must be shown
	// every job there is. Filtering for both would mean dismissing a completed
	// job could leave a player waiting for a copy that had already arrived —
	// the same fault, from a new direction, as a job falling off the end of
	// this list (CLAUDE.md §8b).
	List(ctx context.Context, activeOnly, hideDismissed bool, limit int32) ([]Job, error)
	Cancel(ctx context.Context, jobID string) error
	// Dismiss hides a finished job. Only terminal states can be dismissed:
	// hiding something still running would leave work with no way to see it.
	Dismiss(ctx context.Context, jobID string) error
	// DismissByState hides every job with a given state. Returns how many were
	// dismissed so the caller can tell whether there was anything to clear.
	DismissByState(ctx context.Context, state string) (int64, error)
	// CancelForVideo stops any queued or running transfer for a video and
	// reports how many it stopped. Zero is not an error.
	CancelForVideo(ctx context.Context, videoID string) (int, error)
	// Claim atomically takes the oldest queued job and marks it running.
	// Returns ErrNotFound when the queue is empty.
	Claim(ctx context.Context, lease time.Duration) (Job, error)
	Heartbeat(ctx context.Context, jobID string, lease time.Duration, p Progress) error
	MarkResolved(ctx context.Context, jobID, videoID, title string) error
	Finish(ctx context.Context, jobID string, state JobState, errorMessage string) error
	// ReleaseExpired returns jobs whose worker died back to the queue.
	ReleaseExpired(ctx context.Context) (int, error)
	// RequeueFailed puts one failed transfer back on the queue if any is due,
	// waiting backoff[attempts] since it failed. False means none was.
	//
	// One at a time: there is a single worker slot, so requeueing several at
	// once only produces a burst of requests to an address that has just been
	// refusing them.
	RequeueFailed(ctx context.Context, backoff []time.Duration) (Job, bool, error)

	// LastFailureFor reports when the most recent failed transfer of a URL
	// finished. Found is false when none ever has.
	//
	// It answers "was this just tried?", which is a different question from
	// "has this been refused for good" (UnavailableSourceFor) and needs a
	// different answer: a 403 that lasts ten minutes is not a members-only
	// video, and must not be recorded as one.
	LastFailureFor(ctx context.Context, sourceURL string) (time.Time, bool, error)

	// MarkUnavailable records that upstream has permanently refused a URL.
	// Idempotent: the first refusal is the one kept, because the first is when
	// it started being true.
	MarkUnavailable(ctx context.Context, u UnavailableSource) error
	// UnavailableSourceFor reports a recorded refusal, if there is one.
	UnavailableSourceFor(ctx context.Context, sourceURL string) (UnavailableSource, bool, error)
	// ClearUnavailable forgets a refusal, so the URL can be tried again. Only a
	// person asking for it gets here — see Ingest.RetryJob.
	ClearUnavailable(ctx context.Context, sourceURL string) error
	// UnreportedUnavailable lists refusals the catalogue has not been told
	// about yet, so a restart can finish what a catalog outage interrupted.
	UnreportedUnavailable(ctx context.Context, limit int32) ([]UnavailableSource, error)
	// MarkUnavailableReported records that catalog now knows.
	MarkUnavailableReported(ctx context.Context, sourceURL string) error
}

// UnavailableSource is a URL upstream will not hand over, and why.
type UnavailableSource struct {
	SourceURL   string
	VideoID     string
	Reason      UnavailableReason
	Detail      string
	FirstSeenAt time.Time
}

// Library is the port over the catalog service. Ingest never writes to
// catalog's database; it calls the service.
type Library interface {
	FindBySourceURL(ctx context.Context, sourceURL string) (videoID string, found bool, err error)
	UpsertChannel(ctx context.Context, v ExternalVideo) error
	UpsertVideo(ctx context.Context, v ExternalVideo, state string) error
	SetMediaState(ctx context.Context, videoID, state, mediaPath string, sizeBytes int64, subtitles []SubtitleTrack) error
	SourceURLFor(ctx context.Context, videoID string) (string, error)
	// UpsertChannelArtwork records artwork already downloaded to the media root.
	UpsertChannelArtwork(ctx context.Context, m ChannelMetadata, avatarPath, bannerPath string) error
	ListSubscribedChannels(ctx context.Context) ([]SubscribedChannel, error)
	// ListVideosNeedingBackfill returns videos the catalogue holds with either no
	// topic assigned or no published_at. A scan cannot supply either — flat listings
	// carry no category or date — so this is how the backfill finds its work.
	ListVideosNeedingBackfill(ctx context.Context, limit int32) ([]VideoRef, error)
	// ListUncheckedShorts returns videos nobody has asked YouTube about yet.
	ListUncheckedShorts(ctx context.Context, limit int32) ([]string, error)
	// SetShort records the answer for one video.
	SetShort(ctx context.Context, videoID string, isShort bool) error
	// SetSubscription records that a household member follows a channel.
	//
	// Per user by nature: importing one person's subscriptions must not
	// subscribe anybody else. The videos are shared — that is what a household
	// library is — and the relationships are not.
	SetSubscription(ctx context.Context, userID, channelID string, subscribed bool) error
	// SetLiked records a like a member already gave the video on YouTube.
	SetLiked(ctx context.Context, userID, videoID string) error
}

// ShortChecker answers the one question duration cannot.
//
// Asked of YouTube directly, because nothing in a listing marks a Short apart —
// not the URL a listing hands back, which is an ordinary /watch link, and not
// the length. Measured: 14- and 9-second videos are ordinary clips and 40- and
// 59-second ones are Shorts.
type ShortChecker interface {
	// IsShort reports whether YouTube serves this id as a Short.
	IsShort(ctx context.Context, videoID string) (bool, error)
}

// VideoRef is the little a backfill needs to know about a video: which one,
// where to fetch it from, and which field it is missing.
type VideoRef struct {
	VideoID   string
	SourceURL string
	// True when the video has topics but no published_at. Set separately from
	// MissingDuration because it changes what the backfill may conclude: a video
	// that only lacks metadata is finished by whatever Preview returns, while
	// one that lacks a topic needs a category to exist upstream.
	MissingPublishedAt bool
	// True when the stored duration is zero, which is what a card showing 0:00
	// is reading. RSS carries no duration, and for a while the upsert let it
	// write that absence over a real one.
	MissingDuration bool
}

// SubscribedChannel is a channel a user chose to follow. Subscriptions are a
// content source alongside topics.yaml: the file is what the owner curated
// ahead of time, a subscription is what someone chose while using the system.
// Both feed the same scanner.
type SubscribedChannel struct {
	ID     string
	Handle string
	Name   string
}

// ChannelUploads is one page of a channel's videos, plus the ordering choices
// YouTube itself offers for that channel. The sort names are whatever YouTube
// returned ("Latest", "Popular", "Oldest") rather than a fixed list, so a
// channel that offers fewer of them cannot produce a control that does nothing.
type ChannelUploads struct {
	Videos        []ExternalVideo
	SortOptions   []SortOption
	NextPageToken string
	// AvatarURL is the channel's picture, taken from the same response rather
	// than fetched. Empty when the listing came from the flat-playlist
	// fallback, which carries no header at all.
	AvatarURL string
}

// SortOption is one ordering, carrying the opaque token that selects it.
type SortOption struct {
	Label string
	Token string
}

// ChannelSource lists a channel's uploads with real view counts and upload
// dates — neither of which a flat playlist listing carries. Implemented over
// YouTube's internal browse API, so callers must tolerate it failing and fall
// back to the flat listing.
type ChannelSource interface {
	ResolveChannelID(ctx context.Context, channel string) (string, error)
	ChannelUploads(ctx context.Context, browseID, pageToken string) (ChannelUploads, error)
}

// RelatedSource is the port over YouTube's watch-next panel. It is deliberately
// separate from Downloader: it speaks to a different, undocumented API, and
// callers are required to treat its failure as "no results" rather than as an
// error worth surfacing.
type RelatedSource interface {
	Related(ctx context.Context, videoID string) ([]ExternalVideo, error)
}

// CursorStore remembers how far into each source the library has been filled,
// so deepening resumes rather than re-reading the same first page forever.
type CursorStore interface {
	NextOffset(ctx context.Context, sourceURL string) (int32, error)
	AdvanceOffset(ctx context.Context, sourceURL string, by int32) error
}
