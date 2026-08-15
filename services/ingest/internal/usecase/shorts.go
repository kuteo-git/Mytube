package usecase

import (
	"context"
	"time"
)

// Finding the Shorts already in the library, and the ones still arriving.
//
// The charter drops Shorts from the interface, and the sources in topics.yaml
// all point at a channel's Videos tab, which YouTube keeps them out of. They
// arrive by the other two doors: ExpandLibrary reaches InnerTube related and
// search, and a subscribed channel's RSS feed carries them with nothing to mark
// them apart. Neither door can be closed without closing what it is for.
//
// So the question is asked per video, once, and the answer kept. It is a
// property of the video and it never changes — nothing stops being a Short —
// which is what makes a single pass over the backlog enough.
//
// Deliberately not derived from duration. Measured against YouTube: a
// 14-second and a 9-second video are ordinary clips, while 40- and 59-second
// ones are Shorts. Any length rule catching the second pair throws away the
// first.

// Pause between probes. The same four seconds the metadata backfill uses, and
// for the same reason — §8 of the charter is about this address being blocked
// for asking too much, and that block took out stream resolution along with the
// pass that caused it. A page request is cheaper than a metadata fetch, but not
// so much cheaper that it is worth finding out where the line is.
const shortProbeDelay = 4 * time.Second

// Videos per pass. Bounded like every other pass here: one that finishes is
// worth more than one that gets throttled halfway, and the next pass continues
// where this stopped because it selects on "never asked".
const defaultShortProbeLimit = 200

// Consecutive failures that end a pass. A rate-limit block presents as every
// request failing in a row, and pushing through only lengthens it.
const shortProbeFailureCutoff = 15

// ProbeShorts asks about videos nobody has asked about yet.
//
// Returns how many were answered. A failure is left unrecorded rather than
// written down as "not a Short": the column is a tri-state precisely so that a
// question which could not be answered stays open.
func (i *Ingest) ProbeShorts(ctx context.Context, limit int32) (int32, error) {
	if i.shorts == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = defaultShortProbeLimit
	}

	ids, err := i.library.ListUncheckedShorts(ctx, limit)
	if err != nil {
		return 0, err
	}

	var (
		answered int32
		failures int
	)
	for n, id := range ids {
		if n > 0 {
			select {
			case <-ctx.Done():
				return answered, ctx.Err()
			case <-time.After(i.shortPause()):
			}
		}

		isShort, err := i.shorts.IsShort(ctx, id)
		if err != nil {
			failures++
			i.logger.Warn("shorts probe", "video_id", id, "error", err)
			if failures >= shortProbeFailureCutoff {
				i.logger.Warn("shorts probe stopping early",
					"consecutive_failures", failures, "answered", answered)
				return answered, nil
			}
			continue
		}
		failures = 0

		if err := i.library.SetShort(ctx, id, isShort); err != nil {
			i.logger.Warn("recording shorts answer", "video_id", id, "error", err)
			continue
		}
		answered++
	}
	return answered, nil
}

// RunShortProbe runs the pass on a timer. A zero interval disables it.
//
// On a schedule for the same reason the metadata backfill is: a pass that only
// runs when somebody remembers it does not run. The backlog here is thousands
// of rows and the newest are the ones that reach the feed, which is why the
// query is ordered by publish date rather than taken in whatever order the
// table offers.
func (i *Ingest) RunShortProbe(ctx context.Context, initialDelay, interval time.Duration) {
	if interval <= 0 || i.shorts == nil {
		return
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	for {
		if _, err := i.ProbeShorts(ctx, 0); err != nil {
			i.logger.Warn("scheduled shorts probe", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (i *Ingest) shortPause() time.Duration {
	if i.shortDelay > 0 {
		return i.shortDelay
	}
	return shortProbeDelay
}
