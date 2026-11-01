package usecase

import (
	"context"
	"io"
	"log/slog"
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

// A Nest is handed the stream, which is ready as soon as the pass knows its
// lines: the voice in seconds, not after the whole pass — "chờ cả 1 phút".
func TestASpeakerThatStreamsIsNarratedAtOnce(t *testing.T) {
	r := newRig()
	r.driver.streams = true
	r.narrator.setStream("a", domain.NarratedReady)
	s := r.startNarrated(t, "a")
	loads := r.driver.ops("load")
	if len(loads) != 1 || !loads[0].narrated || loads[0].arg != 30 {
		t.Fatalf("loads = %+v, want the narrated stream from 30 straight away", loads)
	}
	if got := r.engine.NarrationStatus(s); got != domain.NarrationOn {
		t.Fatalf("status = %s", got)
	}
	if r.narrator.from[0] != 30 {
		t.Fatalf("the pass was asked to start at %d, not where the speaker is", r.narrator.from[0])
	}
}

func TestTheFileIsNotWaitedOnByASpeakerThatStreams(t *testing.T) {
	r := newRig()
	r.driver.streams = true
	r.narrator.set("a", domain.NarratedPreparing)
	r.narrator.setStream("a", domain.NarratedReady)
	r.startNarrated(t, "a")
	if !r.driver.ops("load")[0].narrated {
		t.Fatal("waited for the file")
	}
}

// A speaker playing the narrated sound keeps it while the gateway cannot say
// it is ready — the cast service restarted and its answers with it, the
// gateway restarted and is reading the lines again, an ask that timed out.
// Only narration switched off, or the gateway saying there is nothing to
// narrate, takes the voice away mid-song.
func TestANarratedSongKeepsItsVoiceWhileTheGatewayIsUnsure(t *testing.T) {
	r := newRig()
	r.narrator.set("a", domain.NarratedReady)
	r.startNarrated(t, "a")
	r.playing("a~narrated", 60)

	// The cast service restarted: what it knew of the narrated sound is gone,
	// and the gateway is making it again.
	r.engine = NewEngine(r.repo, []Driver{r.driver}, r.catalog, r.narrator, r.clock.Now,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.narrator.set("a", domain.NarratedPreparing)
	r.playing("a~narrated", 61)
	r.playing("a~narrated", 62)
	if loads := r.driver.ops("load"); len(loads) != 1 {
		t.Fatalf("loads = %+v, want the narrated song left playing", loads)
	}

	r.narrator.set("a", domain.NarratedNone)
	r.playing("a~narrated", 63)
	if loads := r.driver.ops("load"); len(loads) != 2 || loads[1].narrated {
		t.Fatalf("loads = %+v, want the song as it is once nothing is narrated", loads)
	}
}

// A song that speaks Vietnamese is played as it is, narration on or not: its
// narration would read its Vietnamese captions over its own Vietnamese voice.
// The remote says narration is off for it, and nothing is made for it.
func TestAVietnameseSongPlaysAsItIs(t *testing.T) {
	r := newRig()
	r.narrator.spoken["a"] = "vi"
	r.narrator.set("a", domain.NarratedReady)
	s := r.startNarrated(t, "a")
	r.playing("a", 40)
	if loads := r.driver.ops("load"); len(loads) != 1 || loads[0].narrated {
		t.Fatalf("loads = %+v, want the song as it is", loads)
	}
	if r.narrator.wasAsked("a") {
		t.Fatal("a narrated sound was made for a Vietnamese song")
	}
	if got := r.engine.NarrationStatus(s); got != domain.NarrationOff {
		t.Fatalf("status = %s, want off", got)
	}
}

// Switched on while a Vietnamese song plays, that song is narrated — the
// listener asked for it on that song — and the next is decided by its own
// language again.
func TestNarrationSwitchedOnForAVietnameseSongNarratesIt(t *testing.T) {
	r := newRig()
	r.narrator.spoken["a"] = "vi"
	r.narrator.spoken["b"] = "vi"
	r.narrator.set("a", domain.NarratedReady)
	r.narrator.set("b", domain.NarratedReady)
	s := r.startNarrated(t, "a", "b")
	r.playing("a", 40)

	got, err := r.engine.SetNarration(context.Background(), s.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	loads := r.driver.ops("load")
	if len(loads) != 2 || !loads[1].narrated || loads[1].video != "a" {
		t.Fatalf("loads = %+v, want a narrated at once", loads)
	}
	if st := r.engine.NarrationStatus(got); st != domain.NarrationOn {
		t.Fatalf("status = %s, want on", st)
	}

	// The next song speaks Vietnamese too, and nobody asked for it.
	r.playing("a~narrated", 198)
	r.driver.setStatus(domain.DeviceStatus{State: domain.DeviceIdle})
	r.clock.advance(time.Second)
	r.engine.Tick(context.Background())
	loads = r.driver.ops("load")
	if last := loads[len(loads)-1]; last.video != "b" || last.narrated {
		t.Fatalf("loads = %+v, want b as it is", loads)
	}
}
