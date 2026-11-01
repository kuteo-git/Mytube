package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// QA C5: the next song is prepared only when narration would cover it.
func TestQAPrefetchSkipsAVietnameseNextSong(t *testing.T) {
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

// QA C5 control: an English next song is prepared.
func TestQAPrefetchPreparesAnEnglishNextSong(t *testing.T) {
	r := newRig()
	r.narrator.spoken["a"] = "en"
	r.narrator.spoken["b"] = "en"
	r.narrator.set("a", domain.NarratedReady)
	r.narrator.set("b", domain.NarratedReady)
	r.startNarrated(t, "a", "b")
	r.playing("a~narrated", 40)
	if !r.narrator.wasAsked("b") {
		t.Fatal("the English next song was not prepared")
	}
}

// QA C3b: forced on a Vietnamese song, the English song after it is narrated.
func TestQAForcedVietnameseThenEnglishNextIsNarrated(t *testing.T) {
	r := newRig()
	r.narrator.spoken["a"] = "vi"
	r.narrator.spoken["b"] = "en"
	r.narrator.set("a", domain.NarratedReady)
	r.narrator.set("b", domain.NarratedReady)
	s := r.startNarrated(t, "a", "b")
	r.playing("a", 40)
	if _, err := r.engine.SetNarration(context.Background(), s.ID, true); err != nil {
		t.Fatal(err)
	}
	r.playing("a~narrated", 198)
	r.driver.setStatus(domain.DeviceStatus{State: domain.DeviceIdle})
	r.clock.advance(time.Second)
	r.engine.Tick(context.Background())
	loads := r.driver.ops("load")
	if last := loads[len(loads)-1]; last.video != "b" || !last.narrated {
		t.Fatalf("loads = %+v, want b narrated", loads)
	}
}

// QA C7: off then on again on the same Vietnamese song narrates it again.
func TestQAOffThenOnAgainOnAVietnameseSong(t *testing.T) {
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

// QA C6: a restarted engine (empty cache) on a session playing a Vietnamese
// song: one tick asks and settles it plain; a forced song stays narrated.
func TestQARestartWithAnEmptyCache(t *testing.T) {
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

	fresh := NewEngine(r.repo, []Driver{r.driver}, r.catalog, r.narrator, r.clock.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.clock.advance(time.Second)
	fresh.Tick(context.Background())
	r.clock.advance(time.Second)
	fresh.Tick(context.Background())
	if n := len(r.driver.ops("load")); n != loadsBefore {
		t.Fatalf("a forced Vietnamese song was reloaded after a restart: %+v", r.driver.ops("load"))
	}
	if st := fresh.NarrationStatus(forced); st == domain.NarrationOff {
		t.Fatalf("status = %s for a forced song", st)
	}
}

// QA C8: an ask that timed out narrates as the switch says; once the answer
// is heard (Vietnamese) the song goes back to its own sound.
func TestQATimedOutAskThenVietnameseGoesBackToPlain(t *testing.T) {
	r := newRig()
	r.narrator.spokenErr = context.DeadlineExceeded
	r.narrator.set("a", domain.NarratedReady)
	s := r.startNarrated(t, "a")
	r.playing("a~narrated", 40)
	r.narrator.spokenErr = nil
	r.narrator.spoken["a"] = "vi"
	r.clock.advance(spokenFailedRetry)
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

// QA C9: an English language read with the queue is not asked of the gateway.
func TestQACatalogueSeededEnglishIsNotAsked(t *testing.T) {
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
