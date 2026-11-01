package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// Narration on a speaker: the song plays as it is while the gateway makes its
// narrated sound, and moves onto it, from where it is, once it is ready —
// "bật thuyết minh thì ko có work khi cast".

func (r rig) startNarrated(t *testing.T, ids ...string) domain.Session {
	t.Helper()
	s, err := r.engine.StartSession(context.Background(), StartRequest{
		UserID: "u1", DeviceID: "r1", VideoIDs: ids, StartPositionS: 30, Narration: true,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return s
}

func (r rig) playing(key string, at int) {
	r.driver.setStatus(domain.DeviceStatus{State: domain.DevicePlaying, MediaKey: key, PositionS: at, DurationS: 200})
	r.clock.advance(time.Second)
	r.engine.Tick(context.Background())
}

func TestNarrationNotReadyStartsTheSongAsItIs(t *testing.T) {
	r := newRig()
	s := r.startNarrated(t, "a")
	loads := r.driver.ops("load")
	if len(loads) != 1 || loads[0].narrated {
		t.Fatalf("loads = %+v, want the sound as it is", loads)
	}
	if !r.narrator.wasAsked("a") {
		t.Fatal("the gateway was never asked to make the narrated sound")
	}
	if got := r.engine.NarrationStatus(s); got != domain.NarrationPreparing {
		t.Fatalf("status = %s, want preparing", got)
	}
}

func TestTheSongMovesOntoItsNarratedSoundOnceReadyFromWhereItIs(t *testing.T) {
	r := newRig()
	s := r.startNarrated(t, "a", "b")
	r.playing("a", 40)
	if n := len(r.driver.ops("load")); n != 1 {
		t.Fatalf("reloaded before the narrated sound was ready: %d loads", n)
	}

	r.narrator.set("a", domain.NarratedReady)
	r.playing("a", 47)

	loads := r.driver.ops("load")
	if len(loads) != 2 || !loads[1].narrated || loads[1].video != "a" || loads[1].arg != 47 || loads[1].first {
		t.Fatalf("loads = %+v, want a narrated reload of a at 47", loads)
	}
	got, _ := r.repo.Get(context.Background(), s.ID)
	if got.Version != s.Version {
		t.Fatalf("version %d -> %d: the queue did not move, a remote's edit must not be refused", s.Version, got.Version)
	}
	r.playing("a~narrated", 48)
	got, _ = r.repo.Get(context.Background(), got.ID)
	if got.State != domain.Playing || r.engine.NarrationStatus(got) != domain.NarrationOn {
		t.Fatalf("state=%s narration=%s", got.State, r.engine.NarrationStatus(got))
	}
	if !r.narrator.wasAsked("b") {
		t.Fatal("the next song was not asked for while this one played narrated")
	}
}

func TestAReadyNarratedSoundIsLoadedStraightAway(t *testing.T) {
	r := newRig()
	r.narrator.set("a", domain.NarratedReady)
	r.startNarrated(t, "a")
	loads := r.driver.ops("load")
	if len(loads) != 1 || !loads[0].narrated || loads[0].arg != 30 {
		t.Fatalf("loads = %+v", loads)
	}
}

func TestTheNextSongIsNarratedWhenItIsReady(t *testing.T) {
	r := newRig()
	r.narrator.set("a", domain.NarratedReady)
	r.narrator.set("b", domain.NarratedReady)
	r.startNarrated(t, "a", "b")
	r.playing("a~narrated", 198)
	r.driver.setStatus(domain.DeviceStatus{State: domain.DeviceIdle})
	r.clock.advance(time.Second)
	r.engine.Tick(context.Background())

	loads := r.driver.ops("load")
	if len(loads) != 2 || loads[1].video != "b" || !loads[1].narrated {
		t.Fatalf("loads = %+v", loads)
	}
}

func TestTurningNarrationOffGoesBackToTheSoundAsItIsFromWhereItIs(t *testing.T) {
	r := newRig()
	r.narrator.set("a", domain.NarratedReady)
	s := r.startNarrated(t, "a")
	r.playing("a~narrated", 60)

	got, err := r.engine.SetNarration(context.Background(), s.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	loads := r.driver.ops("load")
	if len(loads) != 2 || loads[1].narrated || loads[1].arg != 60 {
		t.Fatalf("loads = %+v, want a plain reload at 60", loads)
	}
	if r.engine.NarrationStatus(got) != domain.NarrationOff {
		t.Fatalf("status = %s", r.engine.NarrationStatus(got))
	}
}

func TestTurningNarrationOnWhenReadySwitchesAtOnce(t *testing.T) {
	r := newRig()
	s := r.start(t, "a")
	r.playing("a", 100)
	r.narrator.set("a", domain.NarratedReady)

	if _, err := r.engine.SetNarration(context.Background(), s.ID, true); err != nil {
		t.Fatal(err)
	}
	loads := r.driver.ops("load")
	if len(loads) != 2 || !loads[1].narrated || loads[1].arg != 100 {
		t.Fatalf("loads = %+v", loads)
	}
}

func TestAPausedSongIsNotReloaded(t *testing.T) {
	r := newRig()
	s := r.startNarrated(t, "a")
	r.playing("a", 40)
	r.narrator.set("a", domain.NarratedReady)
	r.driver.setStatus(domain.DeviceStatus{State: domain.DevicePaused, MediaKey: "a", PositionS: 41, DurationS: 200})
	r.clock.advance(time.Second)
	r.engine.Tick(context.Background())

	if n := len(r.driver.ops("load")); n != 1 {
		t.Fatalf("a paused speaker was reloaded (it would start playing): %d loads", n)
	}
	got, _ := r.repo.Get(context.Background(), s.ID)
	if got.State != domain.Paused {
		t.Fatalf("state = %s", got.State)
	}
}

func TestASongWithNothingToNarrateSaysSoAndPlaysOn(t *testing.T) {
	r := newRig()
	r.narrator.set("a", domain.NarratedNone)
	s := r.startNarrated(t, "a")
	r.playing("a", 40)
	got, _ := r.repo.Get(context.Background(), s.ID)
	if n := len(r.driver.ops("load")); n != 1 {
		t.Fatalf("loads = %d", n)
	}
	if st := r.engine.NarrationStatus(got); st != domain.NarrationUnavailable {
		t.Fatalf("status = %s, want unavailable", st)
	}
}

func TestNarrationOffNeverAsksTheGateway(t *testing.T) {
	r := newRig()
	r.start(t, "a")
	r.playing("a", 40)
	if r.narrator.wasAsked("a") {
		t.Fatal("asked the gateway for a narration nobody wanted")
	}
}
