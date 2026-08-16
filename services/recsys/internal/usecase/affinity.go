package usecase

import (
	"math"
	"strings"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// What a reaction is taken to mean, in descending order of confidence.
//
// A like says "more like this", and a video carries three claims about what
// "this" is. The topic is the strongest: it is the label the library was
// organised by, and it is what a person means by "I like this kind of thing".
// The channel is nearly as strong but overlaps with Subscribe, which says the
// same thing more explicitly. Hashtags are the weakest — they are whatever the
// uploader typed.
//
// A dislike reads the same three claims. What differs is how far it is trusted,
// which is a weight at the call site rather than a difference here: the axes
// mean the same thing whichever direction the viewer pressed.
const (
	likeWeightTopic   = 1.0
	likeWeightChannel = 0.8
	likeWeightHashtag = 0.5
)

// How long a dislike keeps its full say over what a viewer is shown.
//
// Every other signal in the ranker ages — recently-added decays over a
// fortnight, publication over a year — and this one did not, because it was
// only ever a boolean. Ninety days is a judgement, put here to be argued with:
// long enough that a season of rejections still counts, short enough that what
// somebody was tired of last year is not still filtering this year's feed.
//
// The video pressed on is unaffected. That is a decision, not a preference, and
// it stays out of the feed for good; this only ages what the decision is taken
// to say about topics and channels in general.
const dislikeHalfLifeDays = 90.0

// ReactionAffinity is how much a user's likes — or dislikes — point at each
// topic, channel and hashtag. Recomputed per request from the raw signals:
// there is no model here and nothing is stored, so a reaction changes the next
// grid immediately and the reason can always be explained.
type ReactionAffinity struct {
	Topics   map[string]float64
	Channels map[string]float64
	Hashtags map[string]float64
}

func newReactionAffinity() ReactionAffinity {
	return ReactionAffinity{
		Topics:   map[string]float64{},
		Channels: map[string]float64{},
		Hashtags: map[string]float64{},
	}
}

func buildLikeAffinity(features []domain.VideoFeatures, liked map[string]bool) ReactionAffinity {
	affinity := newReactionAffinity()
	if len(liked) == 0 {
		return affinity
	}

	for _, f := range features {
		if !liked[f.VideoID] {
			continue
		}
		affinity.add(f, 1)
	}

	return affinity
}

// buildDislikeAffinity is the same reading of the same three axes, aged.
//
// It exists because rejecting something used to teach the feed nothing at all.
// A dislike removed exactly the video it was pressed on, so turning down ten
// videos of a kind removed those ten and left the eleventh where it was — the
// viewer saying "fewer things like this" and the system hearing "not this one".
func buildDislikeAffinity(
	features []domain.VideoFeatures,
	disliked map[string]time.Time,
	now time.Time,
) ReactionAffinity {
	affinity := newReactionAffinity()
	if len(disliked) == 0 {
		return affinity
	}

	for _, f := range features {
		at, ok := disliked[f.VideoID]
		if !ok {
			continue
		}
		affinity.add(f, dislikeAgeFactor(at, now))
	}

	return affinity
}

// dislikeAgeFactor is 1.0 the day it was pressed and halves every half-life.
//
// A missing timestamp counts in full rather than not at all: the signal is
// known to exist, and reading "no date" as "infinitely old" would silently
// discard every dislike recorded before the date was carried through.
func dislikeAgeFactor(at, now time.Time) float64 {
	if at.IsZero() || at.Unix() <= 0 {
		return 1
	}
	days := now.Sub(at).Hours() / 24
	if days <= 0 {
		return 1
	}
	return math.Exp2(-days / dislikeHalfLifeDays)
}

// squashReaction bounds an accumulated reaction score to below 1.
//
// The totals it is given grow by one for every video reacted to, with no
// ceiling — a topic liked thirty times scores thirty. Measured on this library
// that reached 17.6 for likes and -12.4 for dislikes on a single video, against
// weightSubscribed's 2.5 and the 1.5 for never having watched something. Two
// terms an order of magnitude above everything else were deciding the feed on
// their own, and no adjustment anywhere else could have been felt underneath
// them.
//
// Bounded here rather than inside the builders, and that placement is the
// point. The accumulation is real information — two likes do mean more than
// one, and a dislike really does fade — and normalising the maps would have
// erased exactly that by making every reaction set peak at 1.0 whatever it
// contained. What was wrong was never the ratios, it was letting an unbounded
// count reach the score.
//
// x/(1+x): one reaction is worth a half, four are worth four fifths, and thirty
// are worth what thirty-one would be. Ordering is kept everywhere and the
// contribution can no longer outgrow the rest of the ranker.
func squashReaction(score float64) float64 {
	if score <= 0 {
		return 0
	}
	return score / (1 + score)
}

func (a ReactionAffinity) add(f domain.VideoFeatures, weight float64) {
	if weight <= 0 {
		return
	}
	a.Channels[f.ChannelID] += likeWeightChannel * weight
	for _, topic := range f.Topics {
		a.Topics[strings.ToLower(topic)] += likeWeightTopic * weight
	}
	for _, tag := range f.Hashtags {
		a.Hashtags[strings.ToLower(tag)] += likeWeightHashtag * weight
	}
}

// Score is how much this user's reactions argue about a given video. A video
// matching on several axes scores the sum, which is the intended behaviour:
// the same channel *and* the same topic is a stronger argument than either.
func (a ReactionAffinity) Score(f domain.VideoFeatures) float64 {
	score := a.Channels[f.ChannelID]
	for _, topic := range f.Topics {
		score += a.Topics[strings.ToLower(topic)]
	}
	for _, tag := range f.Hashtags {
		score += a.Hashtags[strings.ToLower(tag)]
	}
	return score
}
