package api

// How the stream route should treat a row's claim to be on air.
//
// The two facts it weighs are not interchangeable and the difference is the
// whole of this file. `live_status` is yt-dlp's own word, written whenever
// anybody last asked; `is_live_now` is that word weighed against *when* it was
// said, computed in SQL so no reader invents a second definition of "on air".
//
// Measured on VsQWkHo_E4o, a 24/7 station with 35 concurrent viewers: the row
// said `is_live` with `live_checked_at` **13h55m** old, so `is_live_now` was
// false and the route took the file path — which cannot work, because every one
// of a broadcast's formats is behind a manifest. Both tiers answered 502
// "cannot resolve media" while `/api/live/…/master.m3u8` served a full
// 144p–1080p ladder the whole time.
//
// The row went stale because ScanLive reads each subscribed channel's /streams
// tab, and this channel's tab lists nothing at all — so no pass was ever going
// to correct it. A freshness window cannot be the last word when nothing is
// guaranteed to refresh it.
type liveDecision int

const (
	// Not a broadcast, or one that has finished. Take the file path.
	liveNo liveDecision = iota
	// On air, and the row is recent enough to say so on its own.
	liveYes
	// The row says `is_live` and can no longer vouch for it. Ask upstream.
	//
	// Kept apart from liveYes rather than folded into it, because the two have
	// opposite answers when the resolve fails: a fresh row is believed and its
	// video plays, a stale one is not and falls through to the file path. There
	// are 589 `is_live` rows in this library and 21 of them fresh — most of the
	// rest are broadcasts that ended, and serving a live tier for those would be
	// the fault above in the other direction.
	liveAsk
)

func decideLive(liveStatus string, isLiveNow bool) liveDecision {
	switch {
	case isLiveNow:
		return liveYes
	case liveStatus == "is_live":
		return liveAsk
	default:
		return liveNo
	}
}
