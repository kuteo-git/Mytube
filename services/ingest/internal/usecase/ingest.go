// Package usecase holds the ingest application logic.
package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type Ingest struct {
	downloader    domain.Downloader
	store         domain.JobStore
	library       domain.Library
	defaultHeight int32
	newID         func() string
}

func New(downloader domain.Downloader, store domain.JobStore, library domain.Library, defaultHeight int32) *Ingest {
	return &Ingest{
		downloader:    downloader,
		store:         store,
		library:       library,
		defaultHeight: defaultHeight,
		newID:         uuid.NewString,
	}
}

// Search looks upstream, always — the library is what the topics chose to
// bring in, and a person searching is by definition looking past that. Results
// are annotated with whether each video is already local, because that is the
// difference between playing in two seconds and waiting for a download.
func (i *Ingest) Search(ctx context.Context, query string, limit int32) ([]domain.ExternalVideo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	videos, err := i.downloader.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	for idx := range videos {
		if _, found, err := i.library.FindBySourceURL(ctx, videos[idx].SourceURL); err == nil {
			videos[idx].InLibrary = found
		}
	}
	return videos, nil
}

// EnsureVideo writes the catalog row for a search result so the watch page can
// open it. Deliberately no topic: the feed stays what topics.yaml chose, and a
// video someone went looking for once should not start shaping it.
func (i *Ingest) EnsureVideo(ctx context.Context, url string) (string, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}

	if videoID, found, err := i.library.FindBySourceURL(ctx, url); err == nil && found {
		return videoID, nil
	}

	meta, err := i.downloader.Preview(ctx, url)
	if err != nil {
		return "", err
	}
	if meta.ID == "" {
		return "", fmt.Errorf("%w: upstream returned no video id", domain.ErrNotFound)
	}

	// Preview already paid for full metadata, so YouTube's own category is
	// here for free — that is where topics come from now (CLAUDE.md §7).
	// Catalog merges topics on conflict, so this adds to whatever a scan may
	// have already filed the video under rather than replacing it.
	meta.Topics = categoryTopics(meta)

	if err := i.library.UpsertChannel(ctx, meta); err != nil {
		return "", err
	}
	if err := i.library.UpsertVideo(ctx, meta, "QUEUED"); err != nil {
		return "", err
	}
	return meta.ID, nil
}

// ListChannelUploads reads a channel's uploads straight from YouTube.
//
// The channel page uses this instead of the local catalog: a scan only ever
// brings in the most recent few dozen uploads, so browsing a channel through
// the catalog would hit a wall that has nothing to do with the channel. Results
// are annotated with whether each video is already local, which is the
// difference between opening instantly and waiting for a fetch.
func (i *Ingest) ListChannelUploads(ctx context.Context, channel string, offset, limit int32) ([]domain.ExternalVideo, error) {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return nil, fmt.Errorf("%w: channel is required", domain.ErrInvalid)
	}

	_, videos, err := i.downloader.ListPlaylist(ctx, channelUploadsURL(channel), offset, limit)
	if err != nil {
		return nil, err
	}

	for idx := range videos {
		if _, found, err := i.library.FindBySourceURL(ctx, videos[idx].SourceURL); err == nil {
			videos[idx].InLibrary = found
		}
	}
	return videos, nil
}

// channelUploadsURL accepts either a handle or a bare channel id, since the
// catalog stores both shapes depending on what the source listing carried.
func channelUploadsURL(channel string) string {
	if strings.HasPrefix(channel, "http://") || strings.HasPrefix(channel, "https://") {
		return channel
	}
	if strings.HasPrefix(channel, "@") {
		return "https://www.youtube.com/" + channel + "/videos"
	}
	return "https://www.youtube.com/channel/" + channel + "/videos"
}

// Preview resolves full metadata for one video. Used by the download worker,
// because flat listings omit fields the catalog row needs.
func (i *Ingest) Preview(ctx context.Context, url string) (domain.ExternalVideo, error) {
	if strings.TrimSpace(url) == "" {
		return domain.ExternalVideo{}, fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}
	return i.downloader.Preview(ctx, url)
}

// ResolveStream returns a directly playable upstream URL for a video that is
// not on disk yet. This is the half of the hybrid model that makes clicking a
// result feel immediate; the other half is the background download.
func (i *Ingest) ResolveStream(ctx context.Context, videoID string) (domain.StreamLocation, error) {
	if videoID == "" {
		return domain.StreamLocation{}, fmt.Errorf("%w: video_id is required", domain.ErrInvalid)
	}

	sourceURL, err := i.library.SourceURLFor(ctx, videoID)
	if err != nil {
		return domain.StreamLocation{}, err
	}
	if sourceURL == "" {
		return domain.StreamLocation{}, fmt.Errorf("video %s has no source url: %w", videoID, domain.ErrNotFound)
	}
	return i.downloader.ResolveStream(ctx, sourceURL)
}

// Submit resolves metadata immediately so the video appears in the library
// straight away — marked as downloading rather than absent — and only then
// queues the transfer.
func (i *Ingest) Submit(ctx context.Context, url, requestedBy string, preferredHeight int32) (domain.Job, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return domain.Job{}, fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}
	if preferredHeight <= 0 {
		preferredHeight = i.defaultHeight
	}

	job, err := i.store.Enqueue(ctx, domain.Job{
		ID:              i.newID(),
		SourceURL:       url,
		PreferredHeight: preferredHeight,
		RequestedBy:     requestedBy,
		State:           domain.JobQueued,
	})
	if err != nil {
		return domain.Job{}, err
	}
	return job, nil
}

func (i *Ingest) GetJob(ctx context.Context, jobID string) (domain.Job, error) {
	if jobID == "" {
		return domain.Job{}, fmt.Errorf("%w: job_id is required", domain.ErrInvalid)
	}
	return i.store.Get(ctx, jobID)
}

func (i *Ingest) ListJobs(ctx context.Context, activeOnly bool, limit int32) ([]domain.Job, error) {
	return i.store.List(ctx, activeOnly, limit)
}

func (i *Ingest) CancelJob(ctx context.Context, jobID string) error {
	if jobID == "" {
		return fmt.Errorf("%w: job_id is required", domain.ErrInvalid)
	}
	return i.store.Cancel(ctx, jobID)
}

// categoryTopics turns YouTube's own category into the video's topic list.
// Empty when the category is unknown, which leaves the video's existing topics
// untouched rather than clearing them.
func categoryTopics(v domain.ExternalVideo) []string {
	if v.Category == "" {
		return v.Topics
	}
	return []string{v.Category}
}
