package usecase

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Captions, fetched the moment playback is asked for.
//
// They used to arrive only when the download worker got round to the job, which
// put four separate waits in front of them: the worker's poll, the full metadata
// fetch the job does first, and then two caption passes run one after the other.
// Five to twelve seconds, all of it landing in exactly the window the captions
// are wanted most — while the viewer is watching the low-quality upstream stream
// and the good copy is still transferring.
//
// Starting here removes the first two of those waits, and the passes themselves
// now run at the same time. The work is the same work; it simply no longer
// queues behind a job that has other things to do first.
//
// This does not run for `?prefetch=1`. That request means a card was hovered,
// not played, and hovering a feed is dozens of cards: a caption pass is a full
// yt-dlp extract, and doing dozens of them for videos nobody chose is the shape
// of the incident that had this address blocked by YouTube once already — an
// incident that took `ResolveStream` down with it, so nothing would play at all.
// Only pressing play reaches Submit, which is why the trigger lives here.
const subtitleFetchTimeout = 3 * time.Minute

type subtitleFetches struct {
	mu      sync.Mutex
	running map[string]struct{}
}

func newSubtitleFetches() *subtitleFetches {
	return &subtitleFetches{running: map[string]struct{}{}}
}

// begin claims a video, or reports that someone else already has it. Pressing
// play twice, or two people opening the same video, must not become two sets of
// requests upstream.
func (s *subtitleFetches) begin(videoID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, running := s.running[videoID]; running {
		return false
	}
	s.running[videoID] = struct{}{}
	return true
}

func (s *subtitleFetches) done(videoID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, videoID)
}

// startSubtitleFetch runs the caption passes in the background and returns at
// once. Nothing waits on captions: a video without them is a working video, so
// a failure here is logged and forgotten rather than surfaced.
func (i *Ingest) startSubtitleFetch(sourceURL string, height int32) {
	videoID := videoIDFromURL(sourceURL)
	if videoID == "" || !i.subtitles.begin(videoID) {
		return
	}

	go func() {
		defer i.subtitles.done(videoID)

		// Its own context: the request that started this is long gone, and
		// inheriting its cancellation would abandon the fetch the moment the
		// browser had its answer.
		ctx, cancel := context.WithTimeout(context.Background(), subtitleFetchTimeout)
		defer cancel()

		i.fetchAndPublishSubtitles(ctx, sourceURL, videoID, height)
	}()
}

// fetchSubtitlesOnce is the download worker's way in. It runs the same fetch as
// the play-time path, under the same claim, and does nothing when the claim
// fails.
//
// Sharing the claim is the point. Both callers used to test "does the video
// folder hold a .vtt yet" and skip on that alone — but a running fetch keeps its
// files in .subs-authored/.subs-auto until both passes finish, so the folder
// looks empty for the whole duration and both callers could run a full fetch.
// The one that finished second found its files already moved, published the
// subset it managed to rename, and SetMediaState replaces the list wholesale —
// so a complete en+vi list could be overwritten by an en-only one, which is
// exactly what made the translator run on a video that has Vietnamese.
func (i *Ingest) fetchSubtitlesOnce(ctx context.Context, sourceURL, videoID string, height int32) {
	if videoID == "" || !i.subtitles.begin(videoID) {
		// Someone else holds it and will publish what they find.
		return
	}
	defer i.subtitles.done(videoID)
	i.fetchAndPublishSubtitles(ctx, sourceURL, videoID, height)
}

// fetchAndPublishSubtitles assumes the claim is already held.
func (i *Ingest) fetchAndPublishSubtitles(ctx context.Context, sourceURL, videoID string, height int32) {
	started := time.Now()
	tracks, refused := i.captions.FetchSubtitles(ctx, sourceURL, videoID, height)

	// One line per attempt, whatever happened.
	//
	// Every failure had a line already and success had none, so the log could
	// say why captions did not arrive and could not say that they had — which
	// makes "is this working" a question about the disk rather than about the
	// log. It is also the only place the three outcomes are named side by side:
	// landed, refused, and the video simply having none.
	outcome := "none"
	switch {
	case refused:
		outcome = "refused"
	case len(tracks) > 0:
		outcome = "landed"
	}
	i.logger.Info("captions",
		"video", videoID,
		"outcome", outcome,
		"langs", trackLanguages(tracks),
		"took", time.Since(started).Round(time.Millisecond),
	)

	// Refused is not empty, and the two want opposite responses.
	//
	// The attempts made here all happen while somebody is watching — four of
	// them over about ninety seconds — and upstream refuses the caption endpoint
	// in waves that outlast that. Asking the viewer's window to coincide with
	// upstream's is what left a video with no captions for as long as the page
	// stayed open, and with them the translation and the read-aloud, which are
	// both built on the .vtt. Written down here; the sweep asks again later.
	if refused {
		if err := i.store.RecordSubtitleRefusal(ctx, domain.SubtitleRetry{
			SourceURL: sourceURL,
			VideoID:   videoID,
			Height:    height,
			LastError: "upstream refused the caption endpoint",
		}); err != nil {
			i.logger.Warn("record subtitle refusal", "video", videoID, "error", err)
		}
		return
	}

	if len(tracks) == 0 {
		// Either there are no captions, or they are already on disk from an
		// earlier run. Neither is worth publishing, and neither is worth asking
		// about again: a video with no captions is finished.
		i.forgetSubtitleRetry(ctx, sourceURL)
		return
	}
	i.forgetSubtitleRetry(ctx, sourceURL)

	// Downloading is what pressing play means: Submit has already queued the
	// job that will set exactly this state itself. Saying it a few seconds
	// earlier alongside the captions introduces no state the job was not
	// about to introduce anyway.
	if err := i.library.SetMediaState(ctx, videoID, "DOWNLOADING", "", 0, tracks); err != nil {
		i.logger.Warn("publish subtitles", "video", videoID, "error", err)
	}
}

// videoIDFromURL reads the id back out of a watch URL.
//
// Everything in this system stores videos under their YouTube id and builds the
// source URL from it, so this is that construction read backwards rather than a
// general parser. An id that cannot be recovered simply means no early fetch —
// the worker still does it.
func videoIDFromURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if id := parsed.Query().Get("v"); id != "" {
		return id
	}
	// youtu.be/<id>
	if strings.EqualFold(parsed.Host, "youtu.be") {
		return strings.Trim(parsed.Path, "/")
	}
	return ""
}

// forgetSubtitleRetry drops a video from the retry table once the question has
// been answered — captions landed, or there are none to land.
func (i *Ingest) forgetSubtitleRetry(ctx context.Context, sourceURL string) {
	if err := i.store.ClearSubtitleRetry(ctx, sourceURL); err != nil {
		i.logger.Warn("clear subtitle retry", "url", sourceURL, "error", err)
	}
}

// RetrySubtitlesOnce asks again for one video whose captions upstream refused.
//
// Called from the worker's sweep, on the same ticker that returns abandoned
// jobs and requeues failed transfers — the three are the same kind of chore and
// a fourth ticker would be a fourth thing to reason about.
//
// It goes through fetchSubtitlesOnce, so it shares the claim with the play-time
// path: a viewer opening the video at the same moment does not become a second
// set of requests upstream.
func (i *Ingest) RetrySubtitlesOnce(ctx context.Context, backoff []time.Duration) {
	r, due, err := i.store.DueSubtitleRetry(ctx, backoff)
	if err != nil {
		i.logger.Warn("due subtitle retries", "error", err)
		return
	}
	if !due {
		return
	}

	height := r.Height
	if height <= 0 {
		height = i.defaultHeight
	}
	i.logger.Info("asking for captions again",
		"video", r.VideoID, "url", r.SourceURL, "attempt", r.Attempts+1)
	i.fetchSubtitlesOnce(ctx, r.SourceURL, r.VideoID, height)
}

// trackLanguages names what landed, for the log line above. Empty is honest:
// a refusal and a video with no captions both produce nothing, and the outcome
// beside it is what tells them apart.
func trackLanguages(tracks []domain.SubtitleTrack) string {
	out := make([]string, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, t.Language)
	}
	return strings.Join(out, ",")
}
