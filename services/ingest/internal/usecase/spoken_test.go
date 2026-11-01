package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
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

type fakeSpokenStore struct {
	mu  sync.Mutex
	set map[string]string
}

func (f *fakeSpokenStore) SetSpokenLanguage(_ context.Context, id, lang string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.set[id] = lang
	return nil
}

func (f *fakeSpokenStore) get(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.set[id]
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
	*now = now.Add(spokenRest + time.Second)
	l.refused = false
	s.Ask(context.Background(), "v")
	s.Ask(context.Background(), "v")
	if l.asked != 2 {
		t.Fatalf("asked %d times, want once more after the rest and then a rest for none", l.asked)
	}
	if len(st.set) != 0 {
		t.Fatalf("recorded %v for no answer", st.set)
	}
	*now = now.Add(spokenRest + time.Second)
	l.lang = "en"
	if got := s.Ask(context.Background(), "v"); got != "en" {
		t.Fatalf("after the rest: %q", got)
	}
}

// A caption fetch has just listed the video and found no automatic captions,
// or failed: the play that follows does not list it again, and nothing is
// recorded for it.
func TestACaptionFetchsListingSparesThePlayItsOwn(t *testing.T) {
	s, l, st, _ := spokenRig()
	l.lang = "en"
	s.Listed(context.Background(), "v", "")
	s.Ask(context.Background(), "v")
	if l.asked != 0 || len(st.set) != 0 {
		t.Fatalf("listed YouTube %d times straight after a caption fetch had; recorded %v", l.asked, st.set)
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
	if got := s.Ask(ctx, "v"); got != "" {
		t.Fatalf("answered %q after the asker gave up", got)
	}
	for deadline := time.Now().Add(2 * time.Second); st.get("v") == "" && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if st.get("v") != "vi" {
		t.Fatal("the listing died with the asker")
	}
}

// Two asks at once share one listing.
func TestAsksAtOnceShareOneListing(t *testing.T) {
	l := &countingSlowLister{after: 30 * time.Millisecond}
	s := NewSpokenLanguages(l, &fakeSpokenStore{set: map[string]string{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.Ask(context.Background(), "v") }()
	}
	wg.Wait()
	if n := l.count(); n != 1 {
		t.Fatalf("listed YouTube %d times for three asks at once", n)
	}
}

type countingSlowLister struct {
	after time.Duration
	mu    sync.Mutex
	n     int
}

func (l *countingSlowLister) SpokenLanguage(context.Context, string) (string, bool, error) {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
	time.Sleep(l.after)
	return "vi", false, nil
}

func (l *countingSlowLister) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}
