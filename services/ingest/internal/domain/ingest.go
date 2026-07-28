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
	// Topic names assigned by the scanner from the source this video was found
	// in. Never taken from YouTube's own categories, which are too coarse to be
	// useful — there are about fifteen of them globally.
	Topics    []string
	Hashtags  []string
	InLibrary bool
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
}

// JobStore is the ingest-owned persistence port.
type JobStore interface {
	Enqueue(ctx context.Context, job Job) (Job, error)
	Get(ctx context.Context, jobID string) (Job, error)
	List(ctx context.Context, activeOnly bool, limit int32) ([]Job, error)
	Cancel(ctx context.Context, jobID string) error
	// Claim atomically takes the oldest queued job and marks it running.
	// Returns ErrNotFound when the queue is empty.
	Claim(ctx context.Context, lease time.Duration) (Job, error)
	Heartbeat(ctx context.Context, jobID string, lease time.Duration, p Progress) error
	MarkResolved(ctx context.Context, jobID, videoID, title string) error
	Finish(ctx context.Context, jobID string, state JobState, errorMessage string) error
	// ReleaseExpired returns jobs whose worker died back to the queue.
	ReleaseExpired(ctx context.Context) (int, error)
}

// Library is the port over the catalog service. Ingest never writes to
// catalog's database; it calls the service.
type Library interface {
	FindBySourceURL(ctx context.Context, sourceURL string) (videoID string, found bool, err error)
	UpsertChannel(ctx context.Context, v ExternalVideo) error
	UpsertVideo(ctx context.Context, v ExternalVideo, state string) error
	SetMediaState(ctx context.Context, videoID, state, mediaPath string, sizeBytes int64, subtitles []SubtitleTrack) error
	SourceURLFor(ctx context.Context, videoID string) (string, error)
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
