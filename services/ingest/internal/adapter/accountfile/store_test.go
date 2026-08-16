package accountfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

const goodCookies = "# Netscape HTTP Cookie File\n" +
	".youtube.com\tTRUE\t/\tTRUE\t1799999999\tSID\tabc123\n" +
	".youtube.com\tTRUE\t/\tTRUE\t1799999999\tHSID\tdef456\n"

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "cookies"))
}

func TestSavingWritesAFileOnlyTheOwnerCanRead(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, "u_luc", "Luc", goodCookies); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The file is a live Google session. Anything readable by another account
	// on this machine is that session handed over.
	info, err := os.Stat(filepath.Join(s.dir, "u_luc.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("cookie file mode = %o, want 600", mode)
	}
	dir, err := os.Stat(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := dir.Mode().Perm(); mode != 0o700 {
		t.Errorf("directory mode = %o, want 700 — a readable directory lists whose sessions exist", mode)
	}
}

// Nothing is written until the paste is known to be usable.
//
// session.go's lesson, one step further on: a cookies file that is not there is
// worse than none, and one that is there but is nonsense is worse still,
// because it also looks configured.
func TestABadPasteNeverReplacesAWorkingSession(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, "u_luc", "Luc", goodCookies); err != nil {
		t.Fatal(err)
	}

	if err := s.Save(ctx, "u_luc", "Luc", "{\"cookies\": []}"); err == nil {
		t.Fatal("a JSON export was accepted")
	}

	raw, err := os.ReadFile(filepath.Join(s.dir, "u_luc.txt"))
	if err != nil || !strings.Contains(string(raw), "SID") {
		t.Error("the working session was overwritten by a rejected paste")
	}
}

func TestValidationRejectsWhatCannotBeUsed(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"json export":      `{"cookies":[{"domain":".youtube.com"}]}`,
		"no header line":   ".youtube.com\tTRUE\t/\tTRUE\t1799999999\tSID\tabc\n",
		"header only":      "# Netscape HTTP Cookie File\n",
		"nothing youtube":  "# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t1\tA\tb\n",
		"not tab separate": "# Netscape HTTP Cookie File\n.youtube.com TRUE / TRUE 1 SID abc\n",
	}
	for name, raw := range cases {
		if err := domain.ValidateCookies(raw); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := domain.ValidateCookies(goodCookies); err != nil {
		t.Errorf("a real export was rejected: %v", err)
	}
	// The other accepted header, which the extensions emit interchangeably.
	if err := domain.ValidateCookies("# HTTP Cookie File\n" +
		".youtube.com\tTRUE\t/\tTRUE\t1\tSID\tabc\n"); err != nil {
		t.Errorf("# HTTP Cookie File was rejected: %v", err)
	}
}

// The list is metadata. The cookies are not in it, and must never be.
func TestListingNeverExposesTheSession(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, "u_luc", "Luc", goodCookies); err != nil {
		t.Fatal(err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].UserID != "u_luc" || list[0].Label != "Luc" {
		t.Fatalf("list = %+v", list)
	}
	for _, a := range list {
		if strings.Contains(a.LastResult, "SID") || strings.Contains(a.Label, "abc123") {
			t.Fatal("cookie content reached the metadata")
		}
	}

	// And the registry file itself holds none of it either — it sits beside the
	// cookies and is the thing most likely to be opened while debugging.
	raw, _ := os.ReadFile(filepath.Join(s.dir, "accounts.json"))
	if strings.Contains(string(raw), "abc123") {
		t.Fatal("the registry recorded the session")
	}
}

func TestTwoAccountsAreKeptApart(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, "u_luc", "Luc", goodCookies); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, "u_vo", "Vợ", goodCookies); err != nil {
		t.Fatal(err)
	}

	luc, err := s.CookiePath(ctx, "u_luc")
	if err != nil {
		t.Fatal(err)
	}
	vo, err := s.CookiePath(ctx, "u_vo")
	if err != nil {
		t.Fatal(err)
	}
	if luc == vo {
		t.Fatal("two people share one session file")
	}
}

// A session that has actually ended stops being replayed.
//
// One refusal is ordinary — this library has measured the same URL answering
// 206, then 403, then 206 within an hour. Two in a row is a dead session, and
// asking again every hour is how a blocked address becomes a banned account.
func TestATwiceRefusedAccountIsLeftAlone(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, "u_luc", "Luc", goodCookies); err != nil {
		t.Fatal(err)
	}

	if err := s.Record(ctx, "u_luc", "sign in required", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CookiePath(ctx, "u_luc"); err != nil {
		t.Fatal("one failure took the account out of service; waves of refusals are normal here")
	}

	if err := s.Record(ctx, "u_luc", "sign in required", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CookiePath(ctx, "u_luc"); !errors.Is(err, domain.ErrNoAccount) {
		t.Errorf("err = %v, want the expired account to be skipped", err)
	}

	list, _ := s.List(ctx)
	if list[0].State != domain.AccountExpired {
		t.Errorf("state = %q, want EXPIRED", list[0].State)
	}
}

// One good pass clears the count. The limit is about failures in a row.
func TestAPassThatWorksForgivesTheOneBefore(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Save(ctx, "u_luc", "Luc", goodCookies); err != nil {
		t.Fatal(err)
	}
	_ = s.Record(ctx, "u_luc", "refused", true)
	_ = s.Record(ctx, "u_luc", "42 subscriptions", false)
	_ = s.Record(ctx, "u_luc", "refused", true)

	if _, err := s.CookiePath(ctx, "u_luc"); err != nil {
		t.Error("a single failure after a good pass expired the account")
	}
}

// Re-pasting is the answer to an expired session, so it must actually revive it.
func TestPastingAgainRevivesAnExpiredAccount(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_ = s.Save(ctx, "u_luc", "Luc", goodCookies)
	_ = s.Record(ctx, "u_luc", "refused", true)
	_ = s.Record(ctx, "u_luc", "refused", true)

	if err := s.Save(ctx, "u_luc", "Luc", goodCookies); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CookiePath(ctx, "u_luc"); err != nil {
		t.Errorf("a freshly pasted session was still treated as expired: %v", err)
	}
}

// Deleting the file by hand is a supported way to revoke access.
func TestARemovedFileIsReportedRatherThanAssumedWorking(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_ = s.Save(ctx, "u_luc", "Luc", goodCookies)
	if err := os.Remove(filepath.Join(s.dir, "u_luc.txt")); err != nil {
		t.Fatal(err)
	}

	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].State != domain.AccountNeverSet {
		t.Errorf("list = %+v, want the account reported as having no session", list)
	}
	if _, err := s.CookiePath(ctx, "u_luc"); !errors.Is(err, domain.ErrNoAccount) {
		t.Errorf("err = %v, want ErrNoAccount", err)
	}
}

func TestRemovingTakesBothTheFileAndTheEntry(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	_ = s.Save(ctx, "u_luc", "Luc", goodCookies)

	if err := s.Remove(ctx, "u_luc"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "u_luc.txt")); !os.IsNotExist(err) {
		t.Error("the session file survived removal")
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Errorf("list = %+v, want empty", list)
	}
}

// A profile id is never a directory.
func TestAnIdCannotEscapeTheDirectory(t *testing.T) {
	s := newStore(t)
	if err := s.Save(context.Background(), "../../escape", "x", goodCookies); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "escape.txt")); err != nil {
		t.Errorf("the id was not reduced to a base name: %v", err)
	}
}
