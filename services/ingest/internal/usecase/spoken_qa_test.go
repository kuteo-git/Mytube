package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// blockingLister is YouTube slower than the gateway's 5 s bound.
type blockingLister struct{ asked int }

func (b *blockingLister) SpokenLanguage(ctx context.Context, _ string) (string, bool, error) {
	b.asked++
	<-ctx.Done()
	return "", false, ctx.Err()
}

// QA I1 (was O1): a listing that outlives its asker's deadline runs to its
// own bound, and then rests the video: three impatient asks, one listing.
func TestQAAListingCutShortByTheDeadlineIsAskedOnce(t *testing.T) {
	l := &blockingLister{}
	st := &fakeSpokenStore{set: map[string]string{}}
	s := NewSpokenLanguages(l, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.listTimeout = 20 * time.Millisecond
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		s.Ask(ctx, "v")
		cancel()
	}
	if l.asked != 1 {
		t.Fatalf("YouTube listed %d times for 3 asks", l.asked)
	}
}

// QA I2: nothing that is not an answer is written.
func TestQANoneAndRefusalAreNotRecorded(t *testing.T) {
	s, l, st, _ := spokenRig()
	s.Ask(context.Background(), "none")
	l.refused = true
	s.Ask(context.Background(), "refused")
	s.Listed(context.Background(), "fetched", "", false)
	if len(st.set) != 0 {
		t.Fatalf("recorded %v", st.set)
	}
}
