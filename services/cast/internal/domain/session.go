// Package domain is what a cast session is and how it follows a speaker. It
// imports no database, HTTP or framework.
package domain

import (
	"errors"
	"time"
)

// Kind is the family of speaker, which decides the driver that talks to it.
type Kind string

const (
	KindR1     Kind = "r1"
	KindGoogle Kind = "google"
)

// State is the session's, not the speaker's. Ended is final.
type State string

const (
	Loading State = "loading"
	Playing State = "playing"
	Paused  State = "paused"
	Ended   State = "ended"
)

// EndedReason says why a session ended, so the remote can say it too.
type EndedReason string

const (
	QueueFinished   EndedReason = "queue_finished"
	Stopped         EndedReason = "stopped"
	Replaced        EndedReason = "replaced"
	TakenOver       EndedReason = "taken_over"
	StoppedOnDevice EndedReason = "stopped_on_device"
	DeviceOffline   EndedReason = "device_offline"
	Handoff         EndedReason = "handoff"
	Failed          EndedReason = "failed"
)

// Narrated is the gateway's word on a song's narrated sound.
type Narrated string

const (
	// NarratedPreparing: its narration pass or the mix is still running.
	NarratedPreparing Narrated = "preparing"
	NarratedReady     Narrated = "ready"
	// NarratedNone: nothing to narrate (no captions), or the pass failed.
	NarratedNone Narrated = "none"
)

// NarratedAnswer is the gateway's word on both forms of a song's narrated
// sound: the file, made once the whole pass is done, and the stream, made as
// it is fetched and ready as soon as the pass knows its lines.
type NarratedAnswer struct {
	File, Stream Narrated
}

// NarrationStatus is what the remote shows about narration on the speaker.
type NarrationStatus string

const (
	NarrationOff         NarrationStatus = "off"
	NarrationPreparing   NarrationStatus = "preparing"
	NarrationOn          NarrationStatus = "on"
	NarrationUnavailable NarrationStatus = "unavailable"
)

// Vietnamese is the language narration speaks. A song that speaks it is not
// narrated unless asked for on that song: its narration would read its
// Vietnamese captions over its own Vietnamese voice.
const Vietnamese = "vi"

// NarratesSong is whether narration on s covers the song videoID, which
// speaks `spoken` ("" when not known: narrated, as the switch says).
func NarratesSong(s Session, videoID, spoken string) bool {
	return s.Narration && (spoken != Vietnamese || s.NarrationForcedFor == videoID)
}

// NarrationStatusOf decides it from the session, whether narration covers
// the current song, and the gateway's last word on it.
func NarrationStatusOf(s Session, covers bool, current Narrated) NarrationStatus {
	switch {
	case !s.Narration || !covers:
		return NarrationOff
	case s.LoadedNarrated:
		return NarrationOn
	case current == NarratedNone:
		return NarrationUnavailable
	default:
		return NarrationPreparing
	}
}

var (
	ErrNotFound      = errors.New("not found")
	ErrEnded         = errors.New("session has ended")
	ErrStaleVersion  = errors.New("queue version is stale")
	ErrLive          = errors.New("a broadcast cannot be cast")
	ErrDeviceOffline = errors.New("device is offline")
	ErrEmptyQueue    = errors.New("nothing to play")
)

// Device is a speaker the household has. Online is whether it answered just
// now; one that did not is known but cannot be cast to.
type Device struct {
	ID     string
	Name   string
	Kind   Kind
	Model  string
	Online bool
}

// Item is one song in the queue, described when it was queued so the remote
// can draw it without asking the catalogue.
type Item struct {
	VideoID      string
	Title        string
	Channel      string
	ThumbnailURL string
	DurationS    int
}

// Session is one sitting on one speaker.
type Session struct {
	ID         string
	DeviceID   string
	DeviceName string
	DeviceKind Kind
	StartedBy  string

	Queue        []Item
	CurrentIndex int

	State       State
	EndedReason EndedReason

	// What the speaker was last seen doing with our song.
	PositionS int
	DurationS int
	Volume    int

	// Narration is asked for; LoadedNarrated is whether what the speaker was
	// handed for the current item is the narrated sound. The two differ while
	// the gateway makes it, and the song plays as it is meanwhile.
	Narration      bool
	LoadedNarrated bool
	// NarrationForcedFor is a song that speaks Vietnamese and is narrated
	// all the same: narration was switched on while it played.
	NarrationForcedFor string

	// LoadedMediaKey is what was handed to the speaker for the current item,
	// and how a takeover is recognised. PrevMediaKey is the one before it,
	// which may still be sounding for a moment after a load.
	LoadedMediaKey string
	PrevMediaKey   string
	// LoadAcked: the speaker has shown the loaded song at least once. An idle
	// speaker before that has not started yet; after it, it has given up.
	LoadAcked bool

	LoadingSince   time.Time
	LastSeenOK     time.Time
	LastProgressAt time.Time

	// Version changes with the queue or the current item, never with a
	// position, so a remote's edit is refused only when the queue moved.
	Version int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Current is the item being played or loaded.
func (s Session) Current() (Item, bool) {
	if s.CurrentIndex < 0 || s.CurrentIndex >= len(s.Queue) {
		return Item{}, false
	}
	return s.Queue[s.CurrentIndex], true
}

// Loaded records what the driver handed the speaker for the current item,
// and whether it was the narrated form.
func (s Session) Loaded(mediaKey string, narrated bool) Session {
	s.PrevMediaKey, s.LoadedMediaKey = s.LoadedMediaKey, mediaKey
	s.LoadedNarrated = narrated
	return s
}

// End finishes a session. A session already ended keeps its first reason.
func End(s Session, reason EndedReason, now time.Time) Session {
	if s.State == Ended {
		return s
	}
	s.State, s.EndedReason, s.UpdatedAt = Ended, reason, now
	return s
}

// LoadAt makes item `index` the current one and asks for it to be loaded.
func LoadAt(s Session, index, startS int, first bool, now time.Time) (Session, Action) {
	s.CurrentIndex = index
	s.State = Loading
	s.LoadingSince = now
	s.LoadAcked = false
	s.PositionS = startS
	s.DurationS = s.Queue[index].DurationS
	s.LastProgressAt = time.Time{}
	s.Version++
	return s, Load{Index: index, StartS: startS, First: first}
}

// Reload hands the speaker the current item again from where it is, to switch
// it between its sound as it is and narrated. The queue has not moved, so
// neither does Version: a remote's edit in flight is still about this queue.
func Reload(s Session, now time.Time) (Session, Action) {
	version := s.Version
	s, load := LoadAt(s, s.CurrentIndex, s.PositionS, false, now)
	s.Version = version
	return s, load
}

// Advance moves to the next item, or finishes the queue and stops the speaker.
func Advance(s Session, now time.Time) (Session, []Action) {
	next := s.CurrentIndex + 1
	if next >= len(s.Queue) {
		return End(s, QueueFinished, now), []Action{StopDevice{}}
	}
	s, load := LoadAt(s, next, 0, false, now)
	return s, []Action{load}
}

// SkipFailed moves past an item the speaker could not play. The last one
// failing ends the session as failed.
func SkipFailed(s Session, now time.Time) (Session, []Action) {
	if s.CurrentIndex+1 >= len(s.Queue) {
		return End(s, Failed, now), nil
	}
	return Advance(s, now)
}

// ReplaceQueue swaps the queue. When the song playing is still in it, it keeps
// playing and only the index moves to where it now is; otherwise the item at
// `currentIndex` (clamped) is to be loaded, which `reload` reports.
func ReplaceQueue(s Session, items []Item, currentIndex int) (Session, bool) {
	current, hasCurrent := s.Current()
	s.Queue = items
	s.Version++
	if hasCurrent {
		if currentIndex >= 0 && currentIndex < len(items) && items[currentIndex].VideoID == current.VideoID {
			s.CurrentIndex = currentIndex
			return s, false
		}
		for i, it := range items {
			if it.VideoID == current.VideoID {
				s.CurrentIndex = i
				return s, false
			}
		}
	}
	s.CurrentIndex = min(max(currentIndex, 0), len(items)-1)
	s.State = Loading
	return s, true
}
