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
	// recorded, when set, is told of every write.
	recorded chan string
}

func (f *fakeSpokenStore) SetSpokenLanguage(_ context.Context, id, lang string) error {
	f.mu.Lock()
	f.set[id] = lang
	f.mu.Unlock()
	if f.recorded != nil {
		f.recorded <- id
	}
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
// or failed: the play that follows does not list it again, and nothing is
// recorded for it.
func TestACaptionFetchsListingSparesThePlayItsOwn(t *testing.T) {
	s, l, st, _ := spokenRig()
	l.lang = "en"
	s.Listed(context.Background(), "v", "", false)
	s.Ask(context.Background(), "v")
	if l.asked != 0 || len(st.set) != 0 {
		t.Fatalf("listed YouTube %d times straight after a caption fetch had; recorded %v", l.asked, st.set)
	}
}

// heldLister answers when released, and says when it has been asked.
type heldLister struct {
	asked   chan struct{}
	release chan struct{}
	mu      sync.Mutex
	n       int
}

func newHeldLister() *heldLister {
	return &heldLister{asked: make(chan struct{}, 8), release: make(chan struct{})}
}

func (l *heldLister) SpokenLanguage(context.Context, string) (string, bool, error) {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
	l.asked <- struct{}{}
	<-l.release
	return "vi", false, nil
}

func (l *heldLister) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// A listing slower than its asker's patience is finished and recorded all
// the same: the asker comes back for it, and finds it in the catalogue
// instead of listing YouTube again.
func TestAListingOutlivesItsImpatientAsker(t *testing.T) {
	l := newHeldLister()
	st := &fakeSpokenStore{set: map[string]string{}, recorded: make(chan string, 1)}
	s := NewSpokenLanguages(l, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-l.asked; cancel() }()
	if got := s.Ask(ctx, "v"); got != "" {
		t.Fatalf("answered %q after the asker gave up", got)
	}
	close(l.release)
	select {
	case id := <-st.recorded:
		if id != "v" {
			t.Fatalf("recorded %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the listing died with the asker")
	}
}

// Asks while a listing is under way join it.
func TestAsksDuringAListingJoinIt(t *testing.T) {
	l := newHeldLister()
	s := NewSpokenLanguages(l, &fakeSpokenStore{set: map[string]string{}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	answers := make(chan string, 3)
	go func() { answers <- s.Ask(context.Background(), "v") }()
	<-l.asked // the first is listing, and held
	for i := 0; i < 2; i++ {
		go func() { answers <- s.Ask(context.Background(), "v") }()
	}
	// The two joiners are in DoChan once they wait; give them the chance
	// before the listing ends, then release it.
	time.Sleep(20 * time.Millisecond)
	close(l.release)
	for i := 0; i < 3; i++ {
		if got := <-answers; got != "vi" {
			t.Fatalf("answer %q", got)
		}
	}
	if n := l.count(); n != 1 {
		t.Fatalf("listed YouTube %d times for three asks during one listing", n)
	}
}
