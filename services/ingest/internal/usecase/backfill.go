package usecase

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Videos fetched at once. YouTube tolerates this comfortably from one address;
// the ceiling here is politeness rather than throughput.
//
// Measured on this machine: sequentially a full metadata fetch costs ~2.5s per
// video, which is why CLAUDE.md §8b removed it from the scan path — 40 new
// videos turned an 8-second scan into 101 seconds. Eight at a time brings the
// effective cost to 0.23s, which is what makes a one-off pass over a few
// thousand videos a coffee break rather than an afternoon.
const backfillConcurrency = 8

// How long the whole pass may run before giving up. A backfill is a background
// convenience; it must not pin a worker forever if YouTube starts refusing.
const backfillTimeout = 60 * time.Minute

// BackfillResult reports what a pass did.
type BackfillResult struct {
	Examined int32
	Updated  int32
	// Videos whose metadata could not be fetched: private, removed, or
	// region-blocked. Counted rather than retried — they will not become
	// fetchable by asking again.
	Failed int32
}

// BackfillTopics assigns YouTube's own category to videos that have no topic.
//
// Why this is needed at all: a scan uses --flat-playlist, which does not return
// categories, so a video only acquires a topic when something already fetches
// it in full — opening it from search, or downloading it. Everything brought in
// by a scan or by feed expansion therefore arrives with no topic, and the
// library ends up mostly invisible to topic filters and to the topic half of
// taste matching. Measured here: 2,337 of 3,092 videos, three quarters of the
// library.
//
// Deliberately not folded into the scan. That trade was already made and
// measured, and it made scanning unusable. This is a separate pass that runs
// when asked, so the cost lands where someone is expecting it.
//
// The pass is resumable by construction: it selects on "has no topic", so
// running it again picks up whatever the last run did not finish, and running
// it when there is nothing to do costs one listing.
func (i *Ingest) BackfillTopics(ctx context.Context, limit int32) (BackfillResult, error) {
	ctx, cancel := context.WithTimeout(ctx, backfillTimeout)
	defer cancel()

	pending, err := i.library.ListVideosMissingTopics(ctx, limit)
	if err != nil {
		return BackfillResult{}, fmt.Errorf("backfill: list videos: %w", err)
	}
	if len(pending) == 0 {
		i.logger.Info("topic backfill: nothing to do")
		return BackfillResult{}, nil
	}

	i.logger.Info("topic backfill starting", "videos", len(pending), "workers", backfillConcurrency)

	var result BackfillResult
	result.Examined = int32(len(pending))

	work := make(chan domain.VideoRef)
	var waitGroup sync.WaitGroup

	for worker := 0; worker < backfillConcurrency; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for ref := range work {
				if ctx.Err() != nil {
					return
				}
				if i.backfillOne(ctx, ref) {
					atomic.AddInt32(&result.Updated, 1)
				} else {
					atomic.AddInt32(&result.Failed, 1)
				}
			}
		}()
	}

	for _, ref := range pending {
		select {
		case work <- ref:
		case <-ctx.Done():
		}
	}
	close(work)
	waitGroup.Wait()

	i.logger.Info("topic backfill finished",
		"examined", result.Examined, "updated", result.Updated, "failed", result.Failed)
	return result, nil
}

// backfillOne fetches one video's metadata and writes its category back.
//
// Returns false for anything that could not be updated. A video that is private
// or removed is not an error worth failing the pass over — it is simply a video
// that will never have a category.
func (i *Ingest) backfillOne(ctx context.Context, ref domain.VideoRef) bool {
	sourceURL := ref.SourceURL
	if sourceURL == "" {
		sourceURL = "https://www.youtube.com/watch?v=" + ref.VideoID
	}

	preview, err := i.downloader.Preview(ctx, sourceURL)
	if err != nil {
		i.logger.Debug("topic backfill: preview failed", "video", ref.VideoID, "error", err)
		return false
	}
	if preview.Category == "" {
		// Fetched fine, but YouTube publishes no category for it. Writing an
		// empty topic list would be indistinguishable from not having tried.
		return false
	}

	// The id from the catalogue wins. Preview resolves the URL itself and a
	// redirect could return a different one, which would create a second row
	// rather than updating the one that needed a topic.
	preview.ID = ref.VideoID
	preview.Topics = categoryTopics(preview)

	// Upsert preserves media_state, media_path and added_at, and unions topics
	// rather than replacing them — so this cannot demote a downloaded video or
	// discard a topic assigned from topics.yaml.
	if err := i.library.UpsertVideo(ctx, preview, ""); err != nil {
		i.logger.Debug("topic backfill: upsert failed", "video", ref.VideoID, "error", err)
		return false
	}
	return true
}
