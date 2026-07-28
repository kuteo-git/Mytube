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

// Search annotates results with whether each video is already in the library,
// so the UI can show "Add" or "Play" rather than making the user guess.
func (i *Ingest) Search(ctx context.Context, query string, limit int32) ([]domain.ExternalVideo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	videos, err := i.downloader.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	return i.annotate(ctx, videos), nil
}

func (i *Ingest) Preview(ctx context.Context, url string) (domain.ExternalVideo, error) {
	if strings.TrimSpace(url) == "" {
		return domain.ExternalVideo{}, fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}

	v, err := i.downloader.Preview(ctx, url)
	if err != nil {
		return domain.ExternalVideo{}, err
	}
	return i.annotate(ctx, []domain.ExternalVideo{v})[0], nil
}

func (i *Ingest) ListPlaylist(ctx context.Context, url string, limit int32) (string, []domain.ExternalVideo, error) {
	if strings.TrimSpace(url) == "" {
		return "", nil, fmt.Errorf("%w: url is required", domain.ErrInvalid)
	}

	title, videos, err := i.downloader.ListPlaylist(ctx, url, limit)
	if err != nil {
		return "", nil, err
	}
	return title, i.annotate(ctx, videos), nil
}

func (i *Ingest) annotate(ctx context.Context, videos []domain.ExternalVideo) []domain.ExternalVideo {
	for idx := range videos {
		if _, found, err := i.library.FindBySourceURL(ctx, videos[idx].SourceURL); err == nil {
			videos[idx].InLibrary = found
		}
	}
	return videos
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
