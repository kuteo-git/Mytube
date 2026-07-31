package usecase

import (
	"context"
	"sync"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// One video at a time, with a pause between each.
//
// An earlier version ran eight concurrently and it worked beautifully for about
// eight hundred videos, at which point YouTube began answering every full
// metadata request on this address with "Sign in to confirm you're not a bot".
// That block is not scoped to the backfill: it takes out stream resolution too,
// so the cost of being impatient here was that nothing outside the already
// downloaded files could be played at all.
//
// Hence a trickle. A backfill has no deadline — it is filling in metadata for
// videos nobody is currently waiting on — so there is no reason for it to look
// like anything other than someone browsing. Serial, spaced out, and slow
// enough to run for days without being noticed.
const backfillConcurrency = 1

// Pause between fetches. Roughly a video every four seconds, which is about the
// rate a person clicking through a channel would produce.
const backfillDelay = 4 * time.Second

// Videos per pass. Small on purpose: a bounded pass that finishes is worth more
// than an unbounded one that gets throttled halfway, and the pass selects on
// "has no topic" so calling it again continues where it stopped.
const defaultBackfillLimit = 200

// Consecutive failures that end a pass. A rate-limit block presents as every
// request failing in a row, and pushing through it only lengthens the block.
// Well above the handful of genuinely dead videos any listing contains.
const backfillFailureCutoff = 15

// How long a pass may run before giving up.
const backfillTimeout = 2 * time.Hour

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
// It returns as soon as the pass has started, not when it has finished. A pass
// of the default size takes over ten minutes by design (see backfillDelay), and
// no HTTP request should be held open for that — an earlier synchronous version
// died on the gateway's own ten-minute client deadline every time, while the
// work carried on invisibly behind a request that had already reported failure.
// Progress is polled instead.
//
// One pass at a time: a second would double the request rate against a source
// that has already demonstrated it is counting.
func (i *Ingest) BackfillTopics(_ context.Context, limit int32) (BackfillResult, error) {
	if limit <= 0 {
		limit = defaultBackfillLimit
	}
	if !i.backfill.begin(time.Now()) {
		// Already running. Reporting the live state rather than an error means
		// pressing the button twice is harmless and still answers the question
		// the presser was asking.
		return i.backfill.snapshot(), nil
	}

	go i.runBackfill(limit)
	return i.backfill.snapshot(), nil
}

// backfillPause is the gap between fetches, overridable so tests do not have to
// wait out a rate limit that only exists for YouTube's benefit.
func (i *Ingest) backfillPause() time.Duration {
	if i.backfillDelay > 0 {
		return i.backfillDelay
	}
	return backfillDelay
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

	pending, err := i.library.ListVideosNeedingBackfill(ctx, limit)
	if err != nil {
		i.logger.Error("topic backfill: list videos", "error", err)
		return
	}
	if len(pending) == 0 {
		i.logger.Info("topic backfill: nothing to do")
		return
	}

	i.backfill.setExamined(int32(len(pending)))
	i.logger.Info("topic backfill starting", "videos", len(pending), "every", i.backfillPause())

	// Consecutive failures, used to stop early. A block announces itself as
	// every request failing at once, and continuing to hammer through it turns
	// a temporary throttle into a longer one.
	consecutiveFailures := 0

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
				ok := i.backfillOne(ctx, ref)
				i.backfill.record(ok)

				if ok {
					consecutiveFailures = 0
				} else {
					consecutiveFailures++
					if consecutiveFailures >= backfillFailureCutoff {
						i.logger.Warn("topic backfill: too many consecutive failures, stopping",
							"failures", consecutiveFailures)
						cancel()
						return
					}
				}

				select {
				case <-time.After(i.backfillPause()):
				case <-ctx.Done():
					return
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

	final := i.backfill.snapshot()
	i.logger.Info("topic backfill finished",
		"examined", final.Examined, "updated", final.Updated, "failed", final.Failed,
		"took", time.Since(final.StartedAt).Truncate(time.Second))
}

// backfillOne fetches one video's metadata and writes the missing fields back.
//
// Returns false for anything that could not be updated. A video that is private
// or removed is not an error worth failing the pass over — it is simply a video
// that will never have the missing data.
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

	preview.ID = ref.VideoID

	if ref.MissingPublishedAt {
		// Video has topics — we only need published_at. Even with no category
		// from YouTube, the preview already gave us the date and UpsertVideo
		// writes it via COALESCE.
	} else {
		if preview.Category == "" {
			// Fetched fine, but YouTube publishes no category for it. Writing an
			// empty topic list would be indistinguishable from not having tried.
			return false
		}
		preview.Topics = categoryTopics(preview)
	}

	// Upsert preserves media_state, media_path and added_at, and unions topics
	// rather than replacing them — so this cannot demote a downloaded video or
	// discard a topic assigned from topics.yaml.
	if err := i.library.UpsertVideo(ctx, preview, ""); err != nil {
		i.logger.Debug("topic backfill: upsert failed", "video", ref.VideoID, "error", err)
		return false
	}
	return true
}
