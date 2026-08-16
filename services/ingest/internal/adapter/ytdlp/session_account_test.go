package ytdlp

import (
	"context"
	"strings"
	"testing"

	"github.com/lrstanley/go-ytdlp"
)

// The rule that protects the household's accounts. Do not delete this test.
//
// The scanner walks 93 sources every hour. Attaching a signed-in session to
// that traffic is the fastest way to lose the account it belongs to, and the
// whole `purpose` split exists to make it impossible — so the split is worth
// exactly as much as this assertion and nothing more.
//
// It is checked with a cookie file explicitly handed in, because that is the
// mistake worth catching: somebody wiring the account store into a listing call
// and finding it works.
func TestListingsNeverCarryASession(t *testing.T) {
	cmd := newCommandWithCookies(purposeListing, "/tmp/whoever.txt")

	if args := commandArgs(t, cmd); containsAny(args, "--cookies") {
		t.Fatalf("a listing request carried cookies: %v", args)
	}
}

func TestMediaCarriesTheHouseholdSession(t *testing.T) {
	cmd := newCommandWithCookies(purposeMedia, "/tmp/household.txt")

	args := commandArgs(t, cmd)
	if !containsAny(args, "--cookies") {
		t.Fatalf("a media request went out without the cookies it was given: %v", args)
	}
	if !containsAny(args, "/tmp/household.txt") {
		t.Errorf("wrong cookie file: %v", args)
	}
}

// One pass reads two people's feeds with two different sessions.
func TestAnAccountRequestCarriesThatPersonsSession(t *testing.T) {
	first := commandArgs(t, newAccountCommand("/tmp/luc.txt"))
	second := commandArgs(t, newAccountCommand("/tmp/vo.txt"))

	if !containsAny(first, "/tmp/luc.txt") {
		t.Errorf("first account used the wrong file: %v", first)
	}
	if !containsAny(second, "/tmp/vo.txt") {
		t.Errorf("second account used the wrong file: %v", second)
	}
	if containsAny(second, "/tmp/luc.txt") {
		t.Error("one person's session leaked into another's request")
	}
}

// No file means no --cookies, rather than an empty one.
//
// session.go already learned this for the missing-file case: yt-dlp fails the
// request outright rather than carrying on, so an empty path would take down a
// pass instead of doing without the session it never had.
func TestNoSessionMeansNoFlag(t *testing.T) {
	if args := commandArgs(t, newAccountCommand("")); containsAny(args, "--cookies") {
		t.Errorf("passed --cookies with no file: %v", args)
	}
}

// commandArgs is the command line yt-dlp would actually be run with.
//
// Built rather than inspected through the builder's own fields: what matters is
// what reaches the process, and that is the only place the answer is certain.
func commandArgs(t *testing.T, cmd *ytdlp.Command) []string {
	t.Helper()
	return cmd.BuildCommand(context.Background()).Args
}

func containsAny(args []string, want string) bool {
	for _, a := range args {
		if strings.Contains(a, want) {
			return true
		}
	}
	return false
}
