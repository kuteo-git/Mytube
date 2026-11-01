package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// The next song is prepared only when narration would cover it.
func TestPrefetchSkipsAVietnameseNextSong(t *testing.T) {
	r := newRig()
	r.narrator.spoken["a"] = "en"
	r.narrator.spoken["b"] = "vi"
	r.narrator.set("a", domain.NarratedReady)
	r.narrator.set("b", domain.NarratedReady)
	r.startNarrated(t, "a", "b")
	r.playing("a~narrated", 40)
	r.playing("a~narrated", 41)
	if r.narrator.wasAsked("b") {
		t.Fatal("a narrated sound was prepared for the Vietnamese next song")
	}
}

// Off then on again on the same Vietnamese song narrates it again.
func TestOffThenOnAgainOnAVietnameseSong(t *testing.T) {
	r := newRig()
	r.narrator.spoken["a"] = "vi"
	r.narrator.set("a", domain.NarratedReady)
	s := r.startNarrated(t, "a")
	r.playing("a", 40)
	ctx := context.Background()
	if _, err := r.engine.SetNarration(ctx, s.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := r.engine.SetNarration(ctx, s.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.NarrationForcedFor != "a" {
		t.Fatalf("forced for %q", got.NarrationForcedFor)
	}
	loads := r.driver.ops("load")
	if last := loads[len(loads)-1]; !last.narrated {
		t.Fatalf("loads = %+v, want a narrated", loads)
	}
}

// A restarted engine (empty cache) on a session playing a Vietnamese song
// asks the gateway again, and a song forced narrated stays narrated.
func TestRestartWithAnEmptyCache(t *testing.T) {
	r := newRig()
	r.narrator.spoken["a"] = "vi"
	r.narrator.set("a", domain.NarratedReady)
	s := r.startNarrated(t, "a")
	r.playing("a", 40)
	forced, err := r.engine.SetNarration(context.Background(), s.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	r.playing("a~narrated", 45)
	loadsBefore := len(r.driver.ops("load"))
	askedBefore := r.narrator.spokenAsked

	fresh := NewEngine(r.repo, []Driver{r.driver}, r.catalog, r.narrator, r.clock.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.clock.advance(time.Second)
	fresh.Tick(context.Background())
	r.clock.advance(time.Second)
	fresh.Tick(context.Background())
	if n := len(r.driver.ops("load")); n != loadsBefore {
		t.Fatalf("a forced Vietnamese song was reloaded after a restart: %+v", r.driver.ops("load"))
	}
	if r.narrator.spokenAsked == askedBefore {
		t.Fatal("the restarted engine never asked which language the song speaks")
	}
	if st := fresh.NarrationStatus(forced); st == domain.NarrationOff {
		t.Fatalf("status = %s for a forced song", st)
	}
}

// An ask that timed out narrates as the switch says; once the answer
// is heard (Vietnamese) the song goes back to its own sound.
func TestTimedOutAskThenVietnameseGoesBackToPlain(t *testing.T) {
	r := newRig()
	r.narrator.spokenErr = context.DeadlineExceeded
	r.narrator.set("a", domain.NarratedReady)
	s := r.startNarrated(t, "a")
	r.playing("a~narrated", 40)
	r.narrator.spokenErr = nil
	r.narrator.spoken["a"] = "vi"
	r.clock.advance(spokenRetry)
	r.playing("a~narrated", 56)
	r.playing("a", 57)
	loads := r.driver.ops("load")
	if last := loads[len(loads)-1]; last.narrated {
		t.Fatalf("loads = %+v, want a back to its own sound", loads)
	}
	if st := r.engine.NarrationStatus(s); st != domain.NarrationOff {
		t.Fatalf("status = %s, want off", st)
	}
}

// An English language read with the queue is not asked of the gateway.
func TestCatalogueSeededEnglishIsNotAsked(t *testing.T) {
	r := newRig()
	r.catalog.spoken = map[string]string{"a": "en"}
	r.narrator.set("a", domain.NarratedReady)
	r.startNarrated(t, "a")
	r.playing("a~narrated", 40)
	if r.narrator.spokenAsked != 0 {
		t.Fatalf("asked %d times", r.narrator.spokenAsked)
	}
	if loads := r.driver.ops("load"); !loads[0].narrated {
		t.Fatalf("loads = %+v, want narrated", loads)
	}
}
