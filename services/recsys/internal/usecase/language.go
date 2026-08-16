package usecase

import (
	"strings"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// Which languages this household actually watches.
//
// Measured on this library, and the measurement is the argument: the feed was
// offering Hindi, Malayalam, Indonesian and Nepali to a household whose entire
// watch history is 406 English videos, 200 of unknown language, 27 Vietnamese
// and 18 en-US — with not one view in any of the four. Meanwhile Vietnamese was
// 11 of the first 1000 slots, fewer than Hindi's 21.
//
// It is not a ranking fault. Those videos sit in the affinity and discovery
// buckets, which are for channels the viewer has *not* subscribed to and are
// working exactly as specified. The fault is what those buckets draw from: 621
// of the library's 708 channels arrived through ExpandLibrary reaching
// InnerTube search, and a search by topic name returns whatever YouTube returns.
//
// So the rule is about provenance rather than about language as such. Something
// the viewer chose — a subscription — is theirs to choose in any language. What
// the feed pushes at them uninvited should at least be in a language they read.

// How many watched videos a language needs before it counts as one this
// household reads.
//
// Three, so a single accidental open does not admit a language for good. Below
// that a language is indistinguishable from a mis-tagged title, which is a real
// possibility here: the column is filled from the title on flat listings.
const languageWatchFloor = 3

// How much watch history is needed before the rule applies at all.
//
// A fresh library has no way to know what anybody reads, and guessing from four
// videos would leave a new installation showing almost nothing. Below this the
// feed stays open and lets the household teach it.
const languageHistoryFloor = 20

// WatchedLanguages is the set this household has demonstrably watched.
//
// Empty means the rule is off — either too little history to judge, or nothing
// clearing the floor.
func buildWatchedLanguages(
	features []domain.VideoFeatures, watched map[string]float32,
) map[string]bool {
	if len(watched) < languageHistoryFloor {
		return nil
	}

	counts := map[string]int{}
	for _, f := range features {
		if _, seen := watched[f.VideoID]; !seen {
			continue
		}
		if lang := normaliseLanguage(f.Language); lang != "" {
			counts[lang]++
		}
	}

	out := map[string]bool{}
	for lang, n := range counts {
		if n >= languageWatchFloor {
			out[lang] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normaliseLanguage reduces a tag to its primary subtag.
//
// "en-US" and "en" are one language to a reader and two strings to a map, and
// this library holds both — 6045 rows tagged en against 91 tagged en-US, from
// the same channels.
func normaliseLanguage(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		tag = tag[:i]
	}
	return tag
}

// unreadable reports whether an uninvited video is in a language nobody here
// reads.
//
// Unknown language is always allowed. 1961 of this library's videos carry no
// language at all and 200 of them have been watched; excluding them would take
// out a quarter of the library to catch a handful of Bollywood.
func unreadable(f domain.VideoFeatures, watchedLanguages map[string]bool) bool {
	if len(watchedLanguages) == 0 {
		return false
	}
	lang := normaliseLanguage(f.Language)
	if lang == "" {
		return false
	}
	return !watchedLanguages[lang]
}
