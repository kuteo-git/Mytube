package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnavailable means upstream will not hand this video over, and asking again
// will not change that.
//
// Told apart from an ordinary failure because the answer is different. A 429, a
// dropped network or a yt-dlp that has fallen behind YouTube (CLAUDE.md §8) all
// want retrying; a members-only video wants recording and leaving alone. Before
// this they were the same thing, and one members-only video collected thirteen
// identical jobs in two minutes — each of them a fresh extract against an
// upstream that had already said no.
var ErrUnavailable = errors.New("video unavailable upstream")

// UnavailableReason is why, in a word the UI can act on. A viewer who learns a
// video is members-only can go and join the channel; "could not fetch" sends
// them nowhere.
type UnavailableReason string

const (
	ReasonMembersOnly UnavailableReason = "members_only"
	ReasonPrivate     UnavailableReason = "private"
	ReasonRemoved     UnavailableReason = "removed"
	ReasonUnavailable UnavailableReason = "unavailable"
)

// Unavailable carries the reason alongside the sentinel, so callers can match
// on the kind with errors.Is and still report which kind it was.
type Unavailable struct {
	Reason UnavailableReason
	// Detail is the upstream line this was read from, kept for the Activity
	// page: a classification with no evidence behind it cannot be argued with.
	Detail string
}

func (u *Unavailable) Error() string {
	return fmt.Sprintf("%s: %s", u.Reason, u.Detail)
}

func (u *Unavailable) Is(target error) bool { return target == ErrUnavailable }

// NewUnavailable builds the error for a reason and the line that proved it.
func NewUnavailable(reason UnavailableReason, detail string) error {
	return &Unavailable{Reason: reason, Detail: detail}
}

// ReasonOf reports the reason behind an error, if it is an unavailability at
// all. The bool is the question "is this permanent", asked once and answered in
// one place.
func ReasonOf(err error) (UnavailableReason, bool) {
	var u *Unavailable
	if errors.As(err, &u) {
		return u.Reason, true
	}
	return "", false
}

// permanentSignatures maps a phrase yt-dlp prints to what it means.
//
// Matched on yt-dlp's own wording rather than on loose keywords, and this is
// the whole risk of the feature: a temporary refusal classified as permanent
// takes a video out of the library until somebody presses Retry by hand. A rate
// limit is the case that matters — a bad afternoon against YouTube would
// otherwise bury hundreds of videos at once — so nothing here may match a 429,
// a timeout, a network error, or a yt-dlp that needs updating.
//
// Lowercased before comparison because yt-dlp's casing is not a contract.
var permanentSignatures = []struct {
	phrase string
	reason UnavailableReason
}{
	// "Join this channel to get access to members-only content like this video"
	{"join this channel to get access to members-only content", ReasonMembersOnly},
	{"this video is available to this channel's members", ReasonMembersOnly},
	{"members-only content", ReasonMembersOnly},

	{"this video is private", ReasonPrivate},
	{"private video", ReasonPrivate},
	{"sign in if you've been granted access to this video", ReasonPrivate},

	{"this video has been removed by the uploader", ReasonRemoved},
	{"this video has been removed for violating", ReasonRemoved},
	{"this video is no longer available because the youtube account associated with this video has been terminated", ReasonRemoved},
	{"video unavailable. this video has been removed", ReasonRemoved},

	// **No bare form.** "Video unavailable" on its own used to be here, last,
	// on the reasoning that the phrases above are more specific and match
	// first. It is a catch-all, and this file's own rule below says there is
	// not supposed to be one.
	//
	// Measured on 4H857SWaTHQ: YouTube answered a bare "Video unavailable",
	// which was recorded as permanent — and answering the same URL hours later
	// gave "The uploader has not made this video available in your country.
	// This video is available in United States." A geo-block, which §4 calls
	// temporary because a route or a session answers it. One video, two
	// messages, and the vaguer one is not evidence of anything.
	//
	// A removed video that YouTube declines to explain now costs a few retries
	// and ends up FAILED with a Retry button, which is the direction this file
	// says to err in.
}

// ClassifyUnavailable reads an upstream failure and says whether it is
// permanent, and why.
//
// Deliberately not a catch-all: anything it does not recognise is treated as
// temporary, which is the safe direction. A video wrongly called temporary
// costs one more failed attempt; a video wrongly called permanent disappears
// until a person intervenes.
func ClassifyUnavailable(message string) (UnavailableReason, bool) {
	lower := strings.ToLower(message)
	// A refusal that names a rate limit is a refusal for today, whatever else
	// the message happens to contain. Checked before the signatures because
	// YouTube's throttling page can carry an "unavailable" wording of its own.
	for _, temporary := range []string{
		"429",
		"too many requests",
		"rate limit",
		"sign in to confirm you're not a bot",
		"sign in to confirm your age",
		"temporarily",
		"try again later",
		// Geo-blocking, in YouTube's own words. §4 calls it temporary — a
		// route or a session answers it — and it was covered only by accident:
		// nothing matched it, so it fell through to "not recognised". Named
		// here so it is a decision rather than a gap, and so the test for it
		// tests the message YouTube actually sends.
		"available in your country",
		"not available in your country",
	} {
		if strings.Contains(lower, temporary) {
			return "", false
		}
	}
	for _, sig := range permanentSignatures {
		if strings.Contains(lower, sig.phrase) {
			return sig.reason, true
		}
	}
	return "", false
}

// AsUnavailable returns a permanent-unavailability error when err reads like
// one, and err untouched when it does not. The single place an upstream failure
// changes kind.
func AsUnavailable(err error) error {
	if err == nil {
		return nil
	}
	if _, already := ReasonOf(err); already {
		return err
	}
	reason, permanent := ClassifyUnavailable(err.Error())
	if !permanent {
		return err
	}
	return NewUnavailable(reason, firstUpstreamLine(err.Error()))
}

// firstUpstreamLine picks the ERROR: line out of yt-dlp's output, which is the
// part worth showing. The rest is the command that was run.
func firstUpstreamLine(message string) string {
	for _, line := range strings.Split(message, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ERROR:") {
			return trimmed
		}
	}
	return strings.TrimSpace(message)
}
