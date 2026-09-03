package api

import (
	"context"
	"io"
	"strings"
	"time"
)

// Narrating a broadcast, which is a different problem from narrating a file.
//
// The pass in narration_manifest.go reads a caption file, indexes it, and works
// from one end to the other. None of that survives contact with a live stream,
// and the reasons are measured rather than assumed — see the app charter's
// "A live broadcast has captions and no end".
//
//   - **No zero.** A broadcast's cue times count from when this listener
//     started, not from when the broadcast did, so every offset the recorded
//     pass computes is from a beginning that does not exist. What the feed does
//     carry is `EXT-X-PROGRAM-DATE-TIME`, an absolute wall clock, and so does
//     the video playlist — so the two are matched on the clock instead.
//   - **No next cue, so no slot.** The line after this one has not been spoken
//     yet, and the slot is what fits the speech into the gap it belongs in. So
//     a clause waits here until the clause after it arrives, and is spoken only
//     then. That is one clause of latency, bought deliberately.
//   - **No end.** The pass runs until it is cancelled, which is what the close
//     button on a phone now does.
//
// ## What the feed actually looks like
//
// Measured against Al Jazeera English while on air: a rolling HLS playlist,
// `TARGETDURATION:5`, six segments — a thirty-second window, so a poller slower
// than that loses lines. Each segment is its own small WebVTT with cue times
// running 0..5s *relative to that segment*.
//
// Consecutive segments do not revise each other. A cue that spans a segment
// boundary is **split**, appearing at the end of one and again at the start of
// the next with the same text — `"shows the aftermath of"` at 3→5s and then at
// 0→1s. So the join is a de-duplication, not a reconciliation of two versions.

const (
	// How long a line may wait before it is dropped rather than spoken.
	//
	// Translation and speech can fall behind the broadcast — the model is on
	// the LAN and a batch is not instant — and a queue that only drains slower
	// than it fills never recovers. Past this, the picture has moved on and a
	// voice describing it is worse than a voice saying nothing.
	//
	// The same judgement `tempoFor` already makes with `errTooFast`: one line
	// lost beats two spoiled.
	liveStaleAfter = 25 * time.Second

	// How long the feed is left alone when a poll finds nothing new.
	//
	// Half the segment length, so a five-second window is never missed by a
	// poll that happened to land just before it was published.
	livePollEvery = 2500 * time.Millisecond

	// A broadcast's caption playlist URL carries `expire/`, hours out, and the
	// broadcast can also simply end. Repeated failures stop the pass rather
	// than hammering a dead address.
	liveFailuresBeforeStopping = 5
)

// runLiveNarration follows a broadcast's caption feed until it is cancelled.
func (g *Gateway) runLiveNarration(
	ctx context.Context,
	videoID string,
	gen int,
	sourceURL string,
) {
	cfg := loadTranslateConfig(g.translateConfigPath())
	if strings.TrimSpace(cfg.Model) == "" {
		g.failNarration(videoID, gen, "no translation model configured")
		return
	}
	partition := "omniroute:" + cfg.Model

	// The cache is read once and then held: a broadcast's lines are new, so
	// almost nothing will hit — but a rerun of the same words, which live
	// captions produce often, costs nothing the second time.
	translations, err := readNarrationCache(g.mediaRoot, videoID, partition)
	if err != nil || translations == nil {
		translations = map[string]string{}
	}
	voice := loadTTSConfig(g.ttsConfigPath()).Voice

	feed := &liveCaptionFeed{}
	// Clauses that have been closed and are waiting for the next one, which is
	// what tells them how much time they have.
	var pending []liveClause
	failures := 0

	for {
		if ctx.Err() != nil {
			g.finishNarration(videoID, gen, narrationIdle)
			return
		}

		captionsURL, err := g.liveCaptionsURL(ctx, sourceURL)
		if err != nil || captionsURL == "" {
			// Resolving can fail for a moment and a broadcast can end. Both
			// look the same from here, so both are counted rather than
			// distinguished.
			failures++
			if failures >= liveFailuresBeforeStopping {
				g.finishNarration(videoID, gen, narrationDone)
				return
			}
			if !sleepCtx(ctx, livePollEvery) {
				g.finishNarration(videoID, gen, narrationIdle)
				return
			}
			continue
		}

		fresh, err := g.readLiveCaptions(ctx, captionsURL, feed)
		if err != nil {
			failures++
			if failures >= liveFailuresBeforeStopping {
				g.finishNarration(videoID, gen, narrationDone)
				return
			}
		} else {
			failures = 0
			pending = append(pending, fresh...)
		}
		if err != nil {
			g.logger.Warn("live captions poll", "video", videoID, "error", err)
		}
		g.logger.Info("live captions", "video", videoID,
			"seq", feed.lastSequence, "fresh", len(fresh), "pending", len(pending))

		pending = g.speakSettled(ctx, videoID, gen, pending, translations, voice, partition)

		if !sleepCtx(ctx, livePollEvery) {
			g.finishNarration(videoID, gen, narrationIdle)
			return
		}
	}
}

// speakSettled translates and speaks every clause whose successor has arrived,
// and answers what is still waiting.
//
// A clause is spoken only once the next one exists, because the gap between
// their starts *is* its slot. The last clause therefore always stays behind —
// it is the one still being spoken on air.
func (g *Gateway) speakSettled(
	ctx context.Context,
	videoID string,
	gen int,
	pending []liveClause,
	translations map[string]string,
	voice, partition string,
) []liveClause {
	// Everything already too late goes first, in one sweep and without a
	// request. Reaching a stale line only after translating the ones before it
	// is how a pass that has fallen behind stays behind.
	kept := pending[:0]
	for _, clause := range pending {
		if time.Since(clause.at) > liveStaleAfter {
			g.advanceNarration(videoID, gen)
			continue
		}
		kept = append(kept, clause)
	}
	pending = kept

	// At most one line per poll. Translation and speech are the slow half of
	// this, and doing all of a backlog before looking at the feed again lets
	// the feed run away — the poll is what keeps the two in step.
	for spoken := 0; spoken < 1 && len(pending) >= 2; spoken++ {
		if ctx.Err() != nil {
			return pending
		}
		cur, next := pending[0], pending[1]
		pending = pending[1:]

		if time.Since(cur.at) > liveStaleAfter {
			// Behind the picture past the point of being worth hearing. Dropped
			// rather than queued, or the delay only ever grows.
			g.logger.Info("live narration dropped", "video", videoID,
				"behind", time.Since(cur.at).Round(time.Second).String())
			g.advanceNarration(videoID, gen)
			continue
		}

		slot := next.at.Sub(cur.at).Seconds()
		if slot <= 0 {
			continue
		}

		if _, have := translations[cur.text]; !have {
			g.translateLines(ctx, []string{cur.text}, []float64{slot},
				cur.context, translations, videoID, partition)
		}
		line := translations[cur.text]
		if line == "" {
			g.advanceNarration(videoID, gen)
			continue
		}

		clipURL, ok := g.spokenClip(ctx, videoID, line, slot, voice)
		g.advanceNarration(videoID, gen)
		if !ok {
			continue
		}

		g.addNarrationClip(videoID, gen, narrationClip{
			DurationSeconds:    slot,
			ClipURL:            clipURL,
			Text:               line,
			StartsAtUnixMillis: cur.at.UnixMilli(),
		})
	}
	return pending
}

// liveCaptionsURL asks where this broadcast's captions are, through the same
// one-minute cache the playlist routes use.
func (g *Gateway) liveCaptionsURL(ctx context.Context, sourceURL string) (string, error) {
	live, err := g.resolveLive(ctx, sourceURL)
	if err != nil {
		return "", err
	}
	if !live.GetIsLive() {
		return "", nil
	}
	return live.GetCaptionsUrl(), nil
}

// sleepCtx waits, and reports false if the wait was cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// readLiveCaptions fetches the playlist, reads whatever segments are new, and
// returns the clauses they completed.
func (g *Gateway) readLiveCaptions(
	ctx context.Context,
	captionsURL string,
	feed *liveCaptionFeed,
) ([]liveClause, error) {
	body, status, _, err := openRangeless(ctx, captionsURL)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(body)
	_ = body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if status >= 400 {
		return nil, errLivePlaylist
	}

	segments := parseLivePlaylist(string(raw))

	// The first playlist places the feed at the live edge rather than being
	// read. See [liveCaptionFeed.started]: a caption playlist can carry hours.
	if !feed.started {
		feed.started = true
		if n := len(segments); n > 0 {
			at := max(0, n-1-liveEdgeLookback)
			feed.lastSequence = segments[at].sequence
		}
		return nil, nil
	}

	var out []liveClause
	for _, seg := range segments {
		if seg.sequence <= feed.lastSequence {
			continue
		}
		segBody, segStatus, _, err := openRangeless(ctx, seg.url)
		if err != nil {
			// One segment missed is a few seconds of speech, not a reason to
			// stop: the sequence is not advanced, so the next poll tries again
			// while it is still in the window.
			continue
		}
		text, readErr := io.ReadAll(segBody)
		_ = segBody.Close()
		if readErr != nil || segStatus >= 400 {
			continue
		}
		feed.lastSequence = seg.sequence
		out = append(out, feed.absorb(seg.at, string(text))...)
	}
	return out, nil
}
