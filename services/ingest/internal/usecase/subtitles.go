package usecase

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
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

		tracks := i.downloader.FetchSubtitles(ctx, sourceURL, videoID, height)
		if len(tracks) == 0 {
			// Either there are no captions, or they are already on disk from an
			// earlier run. Neither is worth publishing.
			return
		}

		// Downloading is what pressing play means: Submit has already queued the
		// job that will set exactly this state itself. Saying it a few seconds
		// earlier alongside the captions introduces no state the job was not
		// about to introduce anyway.
		if err := i.library.SetMediaState(ctx, videoID, "DOWNLOADING", "", 0, tracks); err != nil {
			i.logger.Warn("publish subtitles", "video", videoID, "error", err)
		}
	}()
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
