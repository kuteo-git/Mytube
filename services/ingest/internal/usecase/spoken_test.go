package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeLister struct {
	lang    string
	refused bool
	asked   int
}

func (f *fakeLister) SpokenLanguage(context.Context, string) (string, bool, error) {
	f.asked++
	if f.refused {
		return "", true, errors.New("429")
	}
	return f.lang, false, nil
}

type fakeSpokenStore struct{ set map[string]string }

func (f *fakeSpokenStore) SetSpokenLanguage(_ context.Context, id, lang string) error {
	f.set[id] = lang
	return nil
}

func spokenRig() (*SpokenLanguages, *fakeLister, *fakeSpokenStore, *time.Time) {
	l := &fakeLister{}
	st := &fakeSpokenStore{set: map[string]string{}}
	s := NewSpokenLanguages(l, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, l, st, &now
}

func TestASpokenLanguageHeardIsRecorded(t *testing.T) {
	s, l, st, _ := spokenRig()
	l.lang = "vi"
	if got := s.Ask(context.Background(), "v"); got != "vi" || st.set["v"] != "vi" {
		t.Fatalf("answered %q, recorded %q", got, st.set["v"])
	}
}

// A refusal is not asked again through the wave, and "none" not on every play.
func TestNoAnswerRestsBeforeAskingAgain(t *testing.T) {
	s, l, st, now := spokenRig()
	l.refused = true
	s.Ask(context.Background(), "v")
	s.Ask(context.Background(), "v")
	if l.asked != 1 {
		t.Fatalf("asked YouTube %d times through a refusal", l.asked)
	}
	*now = now.Add(spokenRefusedRest + time.Second)
	l.refused = false
	s.Ask(context.Background(), "v")
	s.Ask(context.Background(), "v")
	if l.asked != 2 {
		t.Fatalf("asked %d times, want once more after the rest and then a rest for none", l.asked)
	}
	if len(st.set) != 0 {
		t.Fatalf("recorded %v for no answer", st.set)
	}
	*now = now.Add(spokenNoneRest + time.Second)
	l.lang = "en"
	if got := s.Ask(context.Background(), "v"); got != "en" {
		t.Fatalf("after the rest: %q", got)
	}
}

// A caption fetch has just listed the video and found no automatic captions,
// or was refused: the play that follows does not list it again.
func TestACaptionFetchsListingSparesThePlayItsOwn(t *testing.T) {
	s, l, _, _ := spokenRig()
	l.lang = "en"
	s.Listed(context.Background(), "v", "", false)
	s.Listed(context.Background(), "w", "", true)
	s.Ask(context.Background(), "v")
	s.Ask(context.Background(), "w")
	if l.asked != 0 {
		t.Fatalf("listed YouTube %d times straight after a caption fetch had", l.asked)
	}
}

// slowLister answers after a while, unless its context is done first.
type slowLister struct{ after time.Duration }

func (l slowLister) SpokenLanguage(ctx context.Context, _ string) (string, bool, error) {
	select {
	case <-time.After(l.after):
		return "vi", false, nil
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
}

// A listing slower than its asker's patience is finished and recorded all
// the same: the asker comes back for it, and finds it in the catalogue
// instead of listing YouTube again.
func TestAListingOutlivesItsImpatientAsker(t *testing.T) {
	st := &fakeSpokenStore{set: map[string]string{}}
	s := NewSpokenLanguages(slowLister{after: 50 * time.Millisecond}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	s.Ask(ctx, "v")
	if st.set["v"] != "vi" {
		t.Fatalf("recorded %q: the listing died with the asker", st.set["v"])
	}
}
