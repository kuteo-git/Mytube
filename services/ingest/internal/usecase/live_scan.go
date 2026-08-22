package usecase

import (
	"context"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Finding the broadcasts that are on air right now.
//
// This is a separate pass from both the hourly scan and the five-minute RSS
// one, and it had to be, for a reason that is worth stating before the code:
// **neither of them can see a live broadcast at all.**
//
// Measured on ABC News, one request each, the same minute:
//
//	/videos  tab — live_status on 0 of 40 entries, and no broadcast listed
//	/streams tab — live_status on 40 of 40: 1 is_live, 39 was_live
//
// The hourly scan walks /videos, so a channel could be broadcasting to a
// hundred thousand people and nothing here would know. RSS is worse than
// useless for it: no live marker of any kind, and it answered 404 for this
// channel outright.
//
// What makes the pass affordable is that the answer rides on a *flat* listing —
// the cheap kind, the one §8 risk 6 explicitly does not count. Measured at
// 0.6s per channel across ABC News, NASA, LofiGirl and MKBHD, which over the
// household's 351 subscribed channels is about three and a half minutes.
const (
	// How deep into /streams to look.
	//
	// The tab is ordered newest first and a broadcast in progress sorts to the
	// top, so five is generous rather than tight — ABC News' whole tab is 5,077
	// entries and asking for all of them to find one would be absurd. NASA was
	// measured with an is_upcoming *above* two is_live, which is why it is five
	// and not one.
	liveProbeDepth = 5

	// How long the scan waits between channels.
	//
	// Nothing: a flat listing is the cheap request, and the pass already takes
	// three and a half minutes of wall clock at 0.6s each. The metadata
	// backfill's 4s gap exists because a *full* fetch is the expensive kind;
	// copying it here would turn one pass into twenty-three minutes and buy
	// protection against a cost that is not being incurred.
	liveProbeGap = 0
)

// ScanLive asks every subscribed channel whether it is broadcasting.
//
// It writes two kinds of answer and both matter. A channel that is on air gets
// an upsert carrying "is_live" — which also *creates the row* when the
// broadcast is not in the library yet, and it usually is not, since nothing
// else ever lists it. A channel whose top entries are all finished gets nothing
// written at all; the broadcast that ended is corrected on the pass that first
// sees it as "was_live", and by the thirty-minute staleness cut in between.
//
// One channel's failure is logged and skipped. A channel with no /streams tab
// is the ordinary case, not an error worth stopping 350 others for — the same
// view ScanSubscribed takes of a malformed feed, for the same reason.
func (s *Scanner) ScanLive(ctx context.Context) error {
	channels, err := s.library.ListSubscribedChannels(ctx)
	if err != nil {
		return err
	}

	var live int
	for _, channel := range channels {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		live += s.scanChannelLive(ctx, channel)
		if liveProbeGap > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(liveProbeGap):
			}
		}
	}
	s.logger.Info("live scan", "channels", len(channels), "live", live)
	return nil
}

// scanChannelLive reads one channel's /streams tab and returns how many
// broadcasts it found on air.
func (s *Scanner) scanChannelLive(ctx context.Context, channel domain.SubscribedChannel) int {
	// Deliberately fetch.ListPlaylist rather than listSource.
	//
	// listSource prefers the browse API for channels, and browse carries view
	// counts and upload dates but *not* live_status. Going through it here
	// would produce a pass that runs, costs the requests, logs cheerfully and
	// finds nothing — the worst possible failure, because it looks like an
	// answer.
	_, videos, err := s.fetch.ListPlaylist(ctx, channelStreamsURL(channel), 0, liveProbeDepth)
	if err != nil {
		// A channel with no Videos tab is already an ordinary, expected failure
		// in this codebase; a channel that has never broadcast has no Streams
		// tab for exactly the same reason, and there are far more of them.
		s.logger.Debug("live scan: no streams listing",
			"channel", channel.ID, "error", err)
		return 0
	}

	found := 0
	for _, video := range videos {
		// Both are recorded; only one is counted, and only one is ever listed
		// under the Live chip.
		//
		// "is_upcoming" is not a live — a scheduled broadcast plays nothing
		// when pressed, and an item that does nothing when pressed is the dead
		// control §5 forbids, here wearing a red dot. But *recording* it is not
		// the same as listing it, and not recording it was its own fault: a
		// scheduled broadcast still appears in Home like any other video, and
		// with nothing stored the stream route had no idea and offered tiers
		// built from adaptive tracks that do not exist yet. Measured on
		// mYPF7KARk5Q, a subscribed channel's stream: yt-dlp answers "This live
		// event will begin in a few moments" and the player got a generic
		// failure.
		if video.LiveStatus != "is_live" && video.LiveStatus != "is_upcoming" {
			continue
		}
		if video.LiveStatus == "is_live" {
			found++
		}

		// The channel comes from the subscription record rather than the
		// listing. A flat listing of a channel tab reports the owner once, on
		// the playlist, not per entry — the same gap upsertFromFeed fills, and
		// without it the broadcast would be filed under an empty channel and
		// disappear from a feed that joins on channels.
		video.ChannelID = channel.ID
		if video.ChannelName == "" {
			video.ChannelName = channel.Name
		}

		// ABSENT, not DOWNLOADING or anything else: there is no file and none
		// is coming. §4 refuses a live broadcast as a download job — it has no
		// end to download to, and one held the single worker slot for hours
		// while every later job sat queued at 0%.
		if err := s.library.UpsertVideo(ctx, video, "ABSENT"); err != nil {
			s.logger.Warn("live scan: upsert",
				"channel", channel.ID, "video", video.ID, "error", err)
			continue
		}
	}
	return found
}

// RunLive scans for broadcasts on its own timer.
//
// Ten minutes, and the interval is a rest rather than a rate: a pass takes
// about three and a half minutes, so five would leave it running almost
// continuously against an address §8 risk 6 is already about, while an hour
// would let most of a broadcast finish before anyone was told it started.
//
// Its own goroutine rather than a step inside the hourly scan, for the same
// reason RunSubscribed has one: liveness is the most time-sensitive fact in
// this library and the least expensive to establish, and tying it to the pass
// that walks every source would give it the latency of the slowest thing here.
func (s *Scanner) RunLive(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.ScanLive(ctx); err != nil {
				s.logger.Warn("live scan", "error", err)
			}
		}
	}
}

// channelStreamsURL is channelUploadsURL's sibling, pointing at the tab that
// actually carries live_status.
//
// Kept beside it rather than parameterised: they answer different questions and
// a shared helper taking a tab name reads as though either tab would do for
// either purpose, which is the exact mistake this pass exists to correct.
func channelStreamsURL(channel domain.SubscribedChannel) string {
	if handle := strings.TrimSpace(channel.Handle); strings.HasPrefix(handle, "@") {
		return "https://www.youtube.com/" + handle + "/streams"
	}
	return "https://www.youtube.com/channel/" + channel.ID + "/streams"
}
