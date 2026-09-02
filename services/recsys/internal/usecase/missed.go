package usecase

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// defaultMissedWindow is how far back "did I miss anything" looks when the
// caller does not say.
//
// A day, because that is the period a person asks the question about — "what
// went up since yesterday" — and because the answer has to run out. A week's
// worth of uploads from twenty channels is a second feed, and a second feed is
// the thing this exists instead of.
const defaultMissedWindow = 24 * time.Hour

// missedWatchedThreshold is how much of a video counts as having seen it.
//
// Deliberately low, and deliberately not the ranker's own isWatched (0.95) or
// watchedEnoughThreshold (0.85). Those answer "did they finish it", which is a
// question about satisfaction; this answers "have they already dealt with
// this", which is a question about a list. A tenth is past an accidental tap
// and past the opening titles.
//
// The error it can make is showing something already seen, which costs a row,
// against hiding something never seen, which is the exact failure this list was
// built to prevent.
const missedWatchedThreshold = 0.10

// MissedPage is one slice of the answer, plus how much of it is left.
type MissedPage struct {
	Videos    []domain.RankedVideo
	Remaining int
}

// GetMissed lists what the followed channels published recently and this viewer
// has not watched.
//
// **No snapshot, unlike the feed.** `GetFeedPage` freezes an ordering because it
// ranks the whole library and a re-rank between pages would shuffle what
// somebody is scrolling through. Here the candidate set is a day of uploads from
// the channels one household follows — tens of videos, not thousands — and the
// ordering is a plain sort over a stable score. Two pages asked a second apart
// agree, so freezing would be machinery guarding against nothing.
//
// The order is **affinity first, views as the tie-break**. That is the request
// stated plainly: the channels this viewer actually watches come first, and
// among channels they watch equally the video the world is watching leads. Views
// alone would hand the list to whichever followed channel is biggest.
func (r *Ranker) GetMissed(
	ctx context.Context,
	userID string,
	within time.Duration,
	pageSize, offset int32,
	languages []string,
	// Which channel opens the list. See oneChannelAtATime.
	rotation int64,
) (MissedPage, error) {
	if within <= 0 {
		within = defaultMissedWindow
	}
	if pageSize <= 0 {
		pageSize = 24
	}
	if offset < 0 {
		offset = 0
	}

	features, err := r.features.ListVideoFeatures(ctx)
	if err != nil {
		return MissedPage{}, err
	}
	profile, err := r.store.BuildProfile(ctx, userID, impressionWindow)
	if err != nil {
		return MissedPage{}, err
	}

	now := r.now()
	cutoff := now.Add(-within)

	// The same notion of "channels I watch" the feed ranks with, rather than a
	// second one written here. Two definitions of a viewer's taste is one too
	// many, and this one would be the copy that quietly stops matching.
	affinity := buildWatchAffinity(features, profile.WatchedFraction, profile.WatchedAt, now)
	wanted := languageFilter(languages)

	ranked := make([]domain.RankedVideo, 0, 64)
	channelOf := make(map[string]string, 64)
	for _, f := range features {
		if !profile.Subscribed[f.ChannelID] {
			continue
		}
		// Publication, not ingestion. A video the library imported today and
		// YouTube published last year was not missed — it was never there to
		// miss, and putting it here would make an old back catalogue look like
		// this morning's uploads.
		if f.PublishedAt.IsZero() || f.PublishedAt.Before(cutoff) {
			continue
		}
		if f.PublishedAt.After(now) {
			continue
		}
		if profile.WatchedFraction[f.VideoID] > missedWatchedThreshold {
			continue
		}
		// Shorts are not something anybody misses.
		//
		// The feed excludes them outright (see explain.go) and this list is
		// stricter, not looser: a channel that posts six Shorts a day would
		// otherwise take six of its rounds here, and the uploads somebody
		// followed the channel for would be behind them.
		//
		// **Never inferred from duration.** The flag is catalog's answer from
		// YouTube, and the measurement behind that rule is recorded beside the
		// feed's own check: 14- and 9-second videos are ordinary clips, while
		// 40- and 59-second ones are Shorts. Length is what a Short usually
		// has, not what it is.
		if f.IsShort {
			continue
		}
		if len(wanted) > 0 && f.Language != "" && !wanted[f.Language] {
			continue
		}

		channelOf[f.VideoID] = f.ChannelID
		ranked = append(ranked, domain.RankedVideo{
			VideoID: f.VideoID,
			Score:   missedScore(affinity.Channels[f.ChannelID], f.ViewCount),
			Reason:  domain.ReasonSubscribedChannel,
		})
	}

	// Ties broken by id so two requests never disagree about the order. Without
	// it Go's sort is free to swap equal elements, and a page boundary landing
	// between two of them would drop one video and repeat another.
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		return ranked[i].VideoID < ranked[j].VideoID
	})

	ranked = oneChannelAtATime(ranked, channelOf, rotation)

	if int(offset) >= len(ranked) {
		return MissedPage{Videos: []domain.RankedVideo{}}, nil
	}
	end := int(offset) + int(pageSize)
	if end > len(ranked) {
		end = len(ranked)
	}
	return MissedPage{
		Videos:    ranked[offset:end],
		Remaining: len(ranked) - end,
	}, nil
}

// oneChannelAtATime deals the ordering out round by round, one video per
// channel.
//
// Measured against the running library before this existed: a hundred results
// held six channels, and three of them — two news channels and a games site —
// took ninety-seven of the hundred rows. A single upload from a channel the
// household follows for one thing sat below forty news clips, which is the exact
// failure this list was written to prevent. Scoring cannot fix it, because the
// channels drowning the page are the ones genuinely watched most; what is wrong
// is not the order of the channels but that one of them is allowed to run.
//
// So: channels keep the order their best video earned, and each gives up one
// video per round. Every followed channel that posted today appears before any
// channel's second video does, which is the promise the chip makes.
//
// **`rotation` decides which channel opens the list**, and it is what makes
// pulling to refresh worth doing. Without it every refresh answered with the
// identical list in the identical order: correct, and useless as a gesture —
// somebody pulls a list down precisely because they want it to be different.
//
// A shuffle was the obvious answer and is the wrong one twice over. It throws
// away the ordering that decides what is worth seeing first, and it breaks
// paging: the page token is an offset into this ordering, so re-shuffling
// between page one and page two would repeat some videos and skip others. This
// only moves the starting point, so the ordering stays a fixed permutation and
// an offset still means what it says. The one-per-channel promise is untouched:
// which channel leads changes, that every channel appears before any second
// does not.
//
// The value is carried in the page token rather than asked for, so it is chosen
// once per pull and every page of that pull agrees about it. See the rpc layer.
//
// The input must already be sorted, and the output holds exactly the same set —
// this reorders, it never drops.
func oneChannelAtATime(
	ranked []domain.RankedVideo, channelOf map[string]string, rotation int64,
) []domain.RankedVideo {
	if len(ranked) < 2 {
		return ranked
	}

	order := make([]string, 0, 8)
	byChannel := make(map[string][]domain.RankedVideo, 8)
	for _, v := range ranked {
		channel := channelOf[v.VideoID]
		if _, seen := byChannel[channel]; !seen {
			order = append(order, channel)
		}
		byChannel[channel] = append(byChannel[channel], v)
	}

	// Rotated, not sorted differently: the channels keep the order their best
	// video earned, and the list simply starts further along it.
	if rotation > 0 && len(order) > 1 {
		at := int(rotation % int64(len(order)))
		order = append(order[at:], order[:at]...)
	}

	out := make([]domain.RankedVideo, 0, len(ranked))
	for round := 0; len(out) < len(ranked); round++ {
		for _, channel := range order {
			if videos := byChannel[channel]; round < len(videos) {
				out = append(out, videos[round])
			}
		}
	}
	return out
}

// missedScore puts channel affinity first and views second.
//
// Affinity dominates by construction: it is scaled by a whole order of
// magnitude over the view term, so a channel this viewer watches always leads a
// channel they merely follow. The view term is logarithmic for the reason every
// other count in this service is — the difference between 100 and 1,000 views
// says more than the difference between 100,000 and 101,000.
//
// A followed channel never watched scores from views alone, which is right: the
// household followed it on purpose, and never having watched it yet is the
// state this list exists to interrupt.
func missedScore(channelAffinity float64, viewCount int64) float64 {
	views := 0.0
	if viewCount > 0 {
		views = math.Log1p(float64(viewCount)) / math.Log1p(maxFreshnessViewCount)
	}
	return channelAffinity*10 + views
}

// languageFilter is the set form of the caller's language list, or nil for "no
// filter". A video with no known language is never filtered out — an unfilled
// column is not a statement that it is in the wrong language.
func languageFilter(languages []string) map[string]bool {
	if len(languages) == 0 {
		return nil
	}
	wanted := make(map[string]bool, len(languages))
	for _, code := range languages {
		if code != "" {
			wanted[code] = true
		}
	}
	return wanted
}
