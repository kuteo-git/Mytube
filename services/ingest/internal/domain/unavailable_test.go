package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// The message that started this, copied from the job row it failed in.
const membersOnly = `preview "https://www.youtube.com/watch?v=6Jw56FSSpg4": exit code 1: exit status 1

ERROR: [youtube] 6Jw56FSSpg4: Join this channel to get access to members-only content like this video, and other exclusive perks.`

func TestClassifyPermanent(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    domain.UnavailableReason
	}{
		{"members only", membersOnly, domain.ReasonMembersOnly},
		{
			"private",
			"ERROR: [youtube] abc: Private video. Sign in if you've been granted access to this video",
			domain.ReasonPrivate,
		},
		{
			"removed by uploader",
			"ERROR: [youtube] abc: Video unavailable. This video has been removed by the uploader",
			domain.ReasonRemoved,
		},
		{
			"terminated account",
			"ERROR: [youtube] abc: This video is no longer available because the YouTube account associated with this video has been terminated.",
			domain.ReasonRemoved,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, permanent := domain.ClassifyUnavailable(c.message)
			if !permanent {
				t.Fatalf("classified as temporary: %s", c.message)
			}
			if got != c.want {
				t.Fatalf("reason = %q, want %q", got, c.want)
			}
		})
	}
}

// The failure this feature must never cause. A rate limit is YouTube saying
// "not now"; reading it as "not ever" would bury every video a bad afternoon
// touched, and each one would need a person to press Retry to come back.
func TestClassifyLeavesTemporaryFailuresAlone(t *testing.T) {
	messages := []string{
		"ERROR: [youtube] abc: HTTP Error 429: Too Many Requests",
		"ERROR: unable to download video data: HTTP Error 403: Forbidden",
		"ERROR: [youtube] abc: Sign in to confirm you're not a bot",
		"ERROR: [youtube] abc: The uploader has not made this video available in your country",
		"ERROR: [youtube] abc: Video unavailable. Please try again later",
		"ERROR: unable to connect to proxy: connection refused",
		"ERROR: [youtube] abc: This video is temporarily unavailable",
		"context deadline exceeded",
		"",
	}
	for _, m := range messages {
		if reason, permanent := domain.ClassifyUnavailable(m); permanent {
			t.Fatalf("classified %q as permanently %q", m, reason)
		}
	}
}

// Age-gating and geo-blocking are deliberately temporary: both can be answered
// with cookies or a different route, so neither is the library's final word.
//
// The geo case used to be tested with a message ending "Please try again
// later", which passed for the wrong reason — that phrase is on the temporary
// list, so the test proved nothing about geo-blocking at all. The message
// below is the one YouTube actually sends, measured on 4H857SWaTHQ.
func TestAgeAndCountryBlocksAreNotPermanent(t *testing.T) {
	for _, m := range []string{
		"ERROR: [youtube] abc: Sign in to confirm your age. This video may be inappropriate for some users",
		"ERROR: [youtube] abc: The uploader has not made this video available in your country. This video is available in United States.",
		"ERROR: [youtube] abc: Video unavailable. This video is not available in your country",
	} {
		if _, permanent := domain.ClassifyUnavailable(m); permanent {
			t.Fatalf("classified %q as permanent", m)
		}
	}
}

// A refusal YouTube declines to explain is not evidence of anything.
//
// "Video unavailable", alone, used to be recorded as permanent — and permanent
// means the video disappears from the library until somebody presses Retry.
// Measured on 4H857SWaTHQ: YouTube gave exactly that at 06:23, and hours later
// gave "The uploader has not made this video available in your country" for the
// same URL. The bare form was the same server declining to say why.
//
// Erring the other way costs a few retries and ends at FAILED with a Retry
// button, which is what the classifier's own note says to prefer.
func TestABareUnavailableIsNotPermanent(t *testing.T) {
	for _, m := range []string{
		"ERROR: [youtube] abc: Video unavailable",
		"ERROR: [youtube] abc: This video is unavailable",
	} {
		if reason, permanent := domain.ClassifyUnavailable(m); permanent {
			t.Fatalf("classified %q as permanently %s", m, reason)
		}
	}
}

func TestAsUnavailableWraps(t *testing.T) {
	err := domain.AsUnavailable(errors.New(membersOnly))

	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("errors.Is(ErrUnavailable) = false for %v", err)
	}
	reason, ok := domain.ReasonOf(err)
	if !ok || reason != domain.ReasonMembersOnly {
		t.Fatalf("ReasonOf = %q, %v", reason, ok)
	}
}

// The detail kept is yt-dlp's own line, not the command that ran it: the
// Activity page shows this, and a classification with no evidence behind it
// cannot be argued with.
func TestAsUnavailableKeepsTheUpstreamLine(t *testing.T) {
	err := domain.AsUnavailable(errors.New(membersOnly))

	var u *domain.Unavailable
	if !errors.As(err, &u) {
		t.Fatal("not an *Unavailable")
	}
	want := "ERROR: [youtube] 6Jw56FSSpg4: Join this channel to get access to members-only content like this video, and other exclusive perks."
	if u.Detail != want {
		t.Fatalf("detail = %q, want %q", u.Detail, want)
	}
}

func TestAsUnavailableLeavesOtherErrorsUntouched(t *testing.T) {
	original := errors.New("HTTP Error 429: Too Many Requests")

	if got := domain.AsUnavailable(original); got != original {
		t.Fatalf("wrapped a temporary failure: %v", got)
	}
	if domain.AsUnavailable(nil) != nil {
		t.Fatal("wrapped nil")
	}
}

// Wrapping twice must not bury the reason behind a second classification pass.
func TestAsUnavailableIsIdempotent(t *testing.T) {
	once := domain.AsUnavailable(errors.New(membersOnly))
	twice := domain.AsUnavailable(fmt.Errorf("fetch comments: %w", once))

	reason, ok := domain.ReasonOf(twice)
	if !ok || reason != domain.ReasonMembersOnly {
		t.Fatalf("ReasonOf = %q, %v", reason, ok)
	}
}
