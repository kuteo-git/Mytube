package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A household member's YouTube account, as far as ingest is concerned.
//
// Which is: a cookie file, and what happened last time it was used. The account
// itself belongs to Google; this holds only the session somebody pasted and a
// note of whether it still works.
type Account struct {
	// UserID is the profile this account belongs to. Everything imported for it
	// is written under this id: subscriptions, likes, watch later.
	UserID string
	// Label is what the viewer called it, for the settings screen.
	Label      string
	AddedAt    time.Time
	LastScanAt time.Time
	// LastResult is a short human sentence, shown on the settings screen. It
	// must never contain anything read out of the cookie file.
	LastResult string
	State      AccountState
	// Failures in a row. Two is enough to stop using the account — see
	// accountFailureLimit.
	Failures int
}

type AccountState string

const (
	AccountOK       AccountState = "OK"
	AccountExpired  AccountState = "EXPIRED"
	AccountNeverSet AccountState = "NEVER_SET"
)

// ErrNoAccount is returned when a profile has no cookies stored.
var ErrNoAccount = errors.New("no account")

// AccountStore keeps the cookie files and the little that is known about them.
//
// Deliberately narrow, and deliberately without a "read the cookies" method
// that anything but the yt-dlp call site can reach: the only thing that should
// ever hold this content is the command line being built for it. There is no
// path from the API to the bytes, so there is none to leak.
type AccountStore interface {
	// Save validates and writes. An invalid paste writes nothing.
	Save(ctx context.Context, userID, label, cookies string) error
	// Remove deletes the cookie file and forgets the account.
	Remove(ctx context.Context, userID string) error
	// List is the metadata only. Never the cookies.
	List(ctx context.Context) ([]Account, error)
	// CookiePath is where yt-dlp should read from, or ErrNoAccount.
	CookiePath(ctx context.Context, userID string) (string, error)
	// Record what a pass found, including whether the session still works.
	Record(ctx context.Context, userID string, result string, authFailed bool) error
}

// How many authentication failures in a row before an account is left alone.
//
// Two. One is ordinary — YouTube refuses things in waves, and this library has
// measured the same URL answering 206, then 403, then 206 within the hour. Two
// in a row is a session that has actually ended, and replaying a dead session
// hourly is how an address stops being merely blocked (§8, risk 6) and an
// account starts being banned.
const AccountFailureLimit = 2

// ValidateCookies checks a paste before any of it is written.
//
// Nothing is stored until this passes, and the reason is written in
// session.go: "a cookies file that is not there is worse than none" — yt-dlp
// fails the request outright rather than carrying on without it. A file that is
// there but is nonsense is worse still, because it also looks configured.
func ValidateCookies(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("%w: nothing pasted", ErrInvalid)
	}

	lines := strings.Split(trimmed, "\n")
	first := strings.TrimSpace(lines[0])
	// yt-dlp's own requirement, and the one thing that tells a Netscape export
	// apart from the JSON the same extensions also offer.
	if !strings.HasPrefix(first, "# HTTP Cookie File") &&
		!strings.HasPrefix(first, "# Netscape HTTP Cookie File") {
		return fmt.Errorf("%w: this does not look like a cookies.txt — the first line must say "+
			"\"# HTTP Cookie File\" or \"# Netscape HTTP Cookie File\"", ErrInvalid)
	}

	var youtube, rows int
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// domain, includeSubdomains, path, secure, expiry, name, value
		if fields := strings.Split(line, "\t"); len(fields) >= 6 {
			rows++
			if strings.Contains(fields[0], "youtube.com") {
				youtube++
			}
		}
	}

	if rows == 0 {
		return fmt.Errorf("%w: no cookies in the file", ErrInvalid)
	}
	if youtube == 0 {
		return fmt.Errorf("%w: no youtube.com cookies — export them from a tab that is "+
			"signed in to YouTube", ErrInvalid)
	}
	return nil
}
