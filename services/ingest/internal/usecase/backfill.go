package usecase

import (
	"context"
	"sync"
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

// BackfillResult reports what a pass did, and whether it is still doing it.
type BackfillResult struct {
	Running    bool
	StartedAt  time.Time
	FinishedAt time.Time
	Examined   int32
	Updated    int32
	// Videos whose metadata could not be fetched: private, removed, or
	// region-blocked. Counted rather than retried — they will not become
	// fetchable by asking again.
	Failed int32
}

// backfillState is the live progress of a pass, shared between the goroutine
// doing the work and whoever asks how it is going.
type backfillState struct {
	mu     sync.Mutex
	result BackfillResult
}

func (s *backfillState) snapshot() BackfillResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.result
}

// begin claims the right to run. Returns false when a pass is already going,
// which is what keeps a repeated button press from starting a second one
// competing with the first for the same rate limit.
func (s *backfillState) begin(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.result.Running {
		return false
	}
	s.result = BackfillResult{Running: true, StartedAt: now}
	return true
}

func (s *backfillState) setExamined(count int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result.Examined = count
}

func (s *backfillState) record(updated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if updated {
		s.result.Updated++
	} else {
		s.result.Failed++
	}
}

func (s *backfillState) finish(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result.Running = false
	s.result.FinishedAt = now
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
//
// It returns as soon as the pass has started, not when it has finished.
//
// Finishing takes hours. Measured on this library: a burst of two dozen videos
// runs at roughly four a second, but sustained the rate settles to about one
// every four seconds — YouTube throttles, and no amount of concurrency argues
// with that. Two thousand videos is therefore a couple of hours, and an HTTP
// request cannot be held open for it: the gateway's client gives ingest ten
// minutes, which is already generous for everything else it calls. An earlier
// version of this ran synchronously and every full pass died on that deadline,
// with the work continuing invisibly on the other side of a request that had
// already reported failure.
//
// So the pass runs on a background context and progress is polled instead. One
// at a time: a second pass would compete with the first for the same rate limit
// and finish neither any sooner.
func (i *Ingest) BackfillTopics(_ context.Context, limit int32) (BackfillResult, error) {
	if !i.backfill.begin(time.Now()) {
		// Already running. Reporting the live state rather than an error means
		// pressing the button twice is harmless and still answers the question
		// the presser was asking.
		return i.backfill.snapshot(), nil
	}

	go i.runBackfill(limit)
	return i.backfill.snapshot(), nil
}

// BackfillStatus reports the current or most recent pass.
func (i *Ingest) BackfillStatus() BackfillResult {
	return i.backfill.snapshot()
}

// runBackfill does the work. Deliberately takes no caller context: it outlives
// the request that started it by design.
func (i *Ingest) runBackfill(limit int32) {
	ctx, cancel := context.WithTimeout(context.Background(), backfillTimeout)
	defer cancel()
	defer func() { i.backfill.finish(time.Now()) }()

	pending, err := i.library.ListVideosMissingTopics(ctx, limit)
	if err != nil {
		i.logger.Error("topic backfill: list videos", "error", err)
		return
	}
	if len(pending) == 0 {
		i.logger.Info("topic backfill: nothing to do")
		return
	}

	i.backfill.setExamined(int32(len(pending)))
	i.logger.Info("topic backfill starting", "videos", len(pending), "workers", backfillConcurrency)

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
				i.backfill.record(i.backfillOne(ctx, ref))
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

	final := i.backfill.snapshot()
	i.logger.Info("topic backfill finished",
		"examined", final.Examined, "updated", final.Updated, "failed", final.Failed,
		"took", time.Since(final.StartedAt).Truncate(time.Second))
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
