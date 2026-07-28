package usecase

import (
	"strings"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// What a like is taken to mean, in descending order of confidence.
//
// A like says "more like this", and a video carries three claims about what
// "this" is. The topic is the strongest: it is the label the library was
// organised by, and it is what a person means by "I like this kind of thing".
// The channel is nearly as strong but overlaps with Subscribe, which says the
// same thing more explicitly. Hashtags are the weakest — they are whatever the
// uploader typed.
const (
	likeWeightTopic   = 1.0
	likeWeightChannel = 0.8
	likeWeightHashtag = 0.5
)

// LikeAffinity is how much a user's likes point at each topic, channel and
// hashtag. Recomputed per request from the raw signals: there is no model here
// and nothing is stored, so a like changes the next grid immediately and the
// reason can always be explained.
type LikeAffinity struct {
	Topics   map[string]float64
	Channels map[string]float64
	Hashtags map[string]float64
}

func buildLikeAffinity(features []domain.VideoFeatures, liked map[string]bool) LikeAffinity {
	affinity := LikeAffinity{
		Topics:   map[string]float64{},
		Channels: map[string]float64{},
		Hashtags: map[string]float64{},
	}
	if len(liked) == 0 {
		return affinity
	}

	for _, f := range features {
		if !liked[f.VideoID] {
			continue
		}
		affinity.Channels[f.ChannelID] += likeWeightChannel
		for _, topic := range f.Topics {
			affinity.Topics[strings.ToLower(topic)] += likeWeightTopic
		}
		for _, tag := range f.Hashtags {
			affinity.Hashtags[strings.ToLower(tag)] += likeWeightHashtag
		}
	}
	return affinity
}

// Score is how much this user's likes argue for showing a given video. A video
// matching on several axes scores the sum, which is the intended behaviour:
// the same channel *and* the same topic is a stronger argument than either.
func (a LikeAffinity) Score(f domain.VideoFeatures) float64 {
	score := a.Channels[f.ChannelID]
	for _, topic := range f.Topics {
		score += a.Topics[strings.ToLower(topic)]
	}
	for _, tag := range f.Hashtags {
		score += a.Hashtags[strings.ToLower(tag)]
	}
	return score
}
