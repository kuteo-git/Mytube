package usecase

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

type stubShortChecker struct {
	answers map[string]bool
	fail    map[string]bool
	asked   []string
}

func (s *stubShortChecker) IsShort(_ context.Context, videoID string) (bool, error) {
	s.asked = append(s.asked, videoID)
	if s.fail[videoID] {
		return false, errors.New("probe refused")
	}
	return s.answers[videoID], nil
}

func probeIngest(t *testing.T, library *recordingLibrary, checker *stubShortChecker) *Ingest {
	t.Helper()
	i := New(nil, nil, nil, library, 0, slog.Default())
	i.shortDelay = time.Nanosecond
	return i.WithShortChecker(checker)
}

func TestTheProbeRecordsBothAnswers(t *testing.T) {
	library := &recordingLibrary{uncheckedShorts: []string{"is_short", "is_not"}}
	checker := &stubShortChecker{answers: map[string]bool{"is_short": true}}

	answered, err := probeIngest(t, library, checker).ProbeShorts(context.Background(), 0)
	if err != nil {
		t.Fatalf("ProbeShorts: %v", err)
	}
	if answered != 2 {
		t.Errorf("answered %d, want 2", answered)
	}
	if !library.shortAnswers["is_short"] {
		t.Error("a Short was not recorded as one")
	}
	if _, ok := library.shortAnswers["is_not"]; !ok {
		t.Error("a no is an answer too, and closes the question")
	}
	if library.shortAnswers["is_not"] {
		t.Error("an ordinary video was recorded as a Short")
	}
}

// A probe that could not be answered leaves the question open.
//
// The stored column is a tri-state for exactly this: writing "not a Short" on a
// 404 or a rate-limited request would close the question permanently on an
// answer YouTube never gave, and nothing would ever ask again.
func TestAFailedProbeWritesNothing(t *testing.T) {
	library := &recordingLibrary{uncheckedShorts: []string{"unreachable"}}
	checker := &stubShortChecker{fail: map[string]bool{"unreachable": true}}

	answered, err := probeIngest(t, library, checker).ProbeShorts(context.Background(), 0)
	if err != nil {
		t.Fatalf("ProbeShorts: %v", err)
	}
	if answered != 0 {
		t.Errorf("answered %d, want 0", answered)
	}
	if _, ok := library.shortAnswers["unreachable"]; ok {
		t.Error("recorded an answer for a probe that failed")
	}
}

// A block presents as everything failing in a row, and pushing through it only
// lengthens the block — the lesson §8 of the charter already paid for once.
func TestTheProbeStopsAfterEnoughFailuresInARow(t *testing.T) {
	ids := make([]string, 40)
	fail := map[string]bool{}
	for i := range ids {
		ids[i] = string(rune('a' + i%26))
		fail[ids[i]] = true
	}
	library := &recordingLibrary{uncheckedShorts: ids}
	checker := &stubShortChecker{fail: fail}

	if _, err := probeIngest(t, library, checker).ProbeShorts(context.Background(), 0); err != nil {
		t.Fatalf("ProbeShorts: %v", err)
	}
	if len(checker.asked) > shortProbeFailureCutoff {
		t.Errorf("asked %d times, want it to stop at %d", len(checker.asked), shortProbeFailureCutoff)
	}
}

// Without a checker configured the pass is a no-op rather than an error: a
// deployment that has not set one still scans, ranks and plays.
func TestWithoutACheckerNothingIsAsked(t *testing.T) {
	library := &recordingLibrary{uncheckedShorts: []string{"a"}}
	i := New(nil, nil, nil, library, 0, slog.Default())

	answered, err := i.ProbeShorts(context.Background(), 0)
	if err != nil {
		t.Fatalf("ProbeShorts: %v", err)
	}
	if answered != 0 || len(library.shortAnswers) != 0 {
		t.Error("asked about videos with no checker configured")
	}
}
