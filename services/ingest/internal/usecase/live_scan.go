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
			// After the channel walk, not instead of it. The walk is the cheap
			// half and finds broadcasts this library has never heard of; this is
			// the expensive half and only ever confirms rows it already holds.
			// A failing walk must not stop it — the rows needing confirmation
			// are the ones the walk cannot see.
			s.recheckKnownLive(ctx)
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

// Confirming the broadcasts this library already believes in.
//
// ScanLive above finds what a channel's /streams tab lists, and that is the
// only thing that has ever wound the thirty-minute clock `is_live_now` is cut
// at. Measured on VsQWkHo_E4o, a station on air with 34 concurrent viewers: its
// channel's /streams tab lists **nothing at all**, and `/live` answers "The
// channel is not currently live" — YouTube does not surface that broadcast on
// the channel at all. The row was written once, by the app opening the video,
// and then aged out of every list that reads the cut.
//
// The other half of the same gap is that nothing ever said a broadcast had
// ended either. 568 rows still said `is_live`, the oldest last checked on
// 22 August, and each one keeps the ranker's exemption from the 365-day age
// filter for as long as it says so.
//
// So this asks upstream directly, one video at a time, and writes back the word
// it is given. The pass is a backlog that drains: a finished broadcast settles
// into `was_live` and is gone from the set for good, so every pass shortens the
// set by its whole quota.
//
// `ListStaleLive` hands over the *most recent* claims first, and that direction
// was measured rather than chosen — see its own comment. A row confirmed on air
// leaves the set for thirty minutes, so its newest end is the broadcasts most
// likely to still be running; ordering the other way put the reported video 544
// rows back and seven hours away.
const (
	// Videos per pass.
	//
	// Chosen against the set this drains to rather than the backlog it starts
	// with. Around twenty broadcasts are on air across this household's
	// channels at any time, so once the dead rows are settled the whole
	// remaining set fits in one pass — every genuinely live row is re-confirmed
	// every ten minutes, and the thirty-minute cut never drops one.
	//
	// It does not have to cover the backlog, because the ordering puts the rows
	// that might be live at the front: a broadcast on air is asked about on the
	// first pass whether there are fifty forgotten rows behind it or five
	// hundred. Those then settle at a quota a pass, which takes hours and costs
	// nobody anything — being impatient with them is what §8 risk 6 is about.
	liveRecheckPerPass = 20

	// Gap between them. The metadata backfill's number, for its reason, which
	// applies here word for word: this is the *expensive* kind of request, and
	// an earlier version of that pass running eight at once had YouTube
	// answering every full metadata request on this address with "Sign in to
	// confirm you're not a bot" — which took stream resolution down with it.
	liveRecheckGap = 4 * time.Second

	// Consecutive failures that end the recheck for this pass. A rate-limit
	// block presents as every request failing in a row, and pushing through it
	// only lengthens the block. The backfill's rule, at a third of its number
	// because this pass is a tenth of its size.
	liveRecheckFailureCutoff = 5

	// How many candidates to ask for, against how many are probed.
	//
	// Wider than the quota because some rows can never be settled: a
	// members-only or deleted video answers nothing, so its word never changes
	// and `ListStaleLive` hands it back at the head of the queue for ever.
	// Measured on the eight oldest rows, one was members-only. Without the slack
	// the pass would spend itself on the same few rows and everything behind
	// them would never be reached.
	liveRecheckCandidates = liveRecheckPerPass * 4
)

// recheckKnownLive asks upstream about the rows whose claim to be on air has
// expired, and writes back what it is told.
//
// Failures are remembered for the lifetime of the process rather than written
// down. A video upstream will not describe is not a fact about the library —
// `live_status` is yt-dlp's own word and inventing one it did not say is how a
// column stops meaning anything — so the row is left alone and simply not asked
// again this run. A restart asks once more, which is the right frequency for
// "has this become describable again".
func (s *Scanner) recheckKnownLive(ctx context.Context) {
	candidates, err := s.library.ListStaleLive(ctx, liveRecheckCandidates)
	if err != nil {
		s.logger.Warn("live recheck: list", "error", err)
		return
	}

	var probed, stillLive, settled, failures int
	for _, videoID := range candidates {
		if ctx.Err() != nil {
			return
		}
		if probed >= liveRecheckPerPass || failures >= liveRecheckFailureCutoff {
			break
		}
		if s.liveRecheckSkipped(videoID) {
			continue
		}

		if probed > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.liveRecheckPause()):
			}
		}
		probed++

		sourceURL, err := s.library.SourceURLFor(ctx, videoID)
		if err != nil || sourceURL == "" {
			// Read from the row rather than built from the id, the rule
			// RefreshVideoMetadata states: pointing a fetch at one address and
			// writing the answer onto another id is the one way this corrupts a
			// row.
			sourceURL = "https://www.youtube.com/watch?v=" + videoID
		}

		video, err := s.fetch.Preview(ctx, sourceURL)
		if err != nil {
			failures++
			s.skipLiveRecheck(videoID)
			s.logger.Debug("live recheck: preview failed",
				"video", videoID, "error", err)
			continue
		}
		failures = 0

		// Silence is not an answer, and the upsert reads it as one: an
		// ExternalVideo carrying no live_status leaves both columns exactly as
		// they were, so a row that would otherwise have been settled stays in
		// this set for ever. Nothing to write means nothing to ask again about.
		if video.LiveStatus == "" {
			s.skipLiveRecheck(videoID)
			s.logger.Debug("live recheck: upstream named no live status", "video", videoID)
			continue
		}

		video.ID = videoID
		if err := s.library.UpsertVideo(ctx, video, ""); err != nil {
			s.logger.Warn("live recheck: upsert", "video", videoID, "error", err)
			continue
		}
		if video.LiveStatus == "is_live" {
			stillLive++
		} else {
			settled++
		}
	}

	if probed > 0 {
		s.logger.Info("live recheck", "probed", probed,
			"still_live", stillLive, "settled", settled, "remaining", len(candidates))
	}
}

func (s *Scanner) liveRecheckSkipped(videoID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, skipped := s.liveRecheckFailed[videoID]
	return skipped
}

func (s *Scanner) skipLiveRecheck(videoID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.liveRecheckFailed == nil {
		s.liveRecheckFailed = map[string]struct{}{}
	}
	s.liveRecheckFailed[videoID] = struct{}{}
}

// liveRecheckPause is the gap between probes, overridable so a test does not
// have to wait the real one out.
func (s *Scanner) liveRecheckPause() time.Duration {
	if s.liveRecheckDelay > 0 {
		return s.liveRecheckDelay
	}
	return liveRecheckGap
}
