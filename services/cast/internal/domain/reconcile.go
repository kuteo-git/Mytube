package domain

import "time"

// DeviceState is what a speaker says it is doing, in one vocabulary for both
// kinds of speaker.
type DeviceState string

const (
	DeviceIdle    DeviceState = "idle"
	DeviceLoading DeviceState = "loading"
	DevicePlaying DeviceState = "playing"
	DevicePaused  DeviceState = "paused"
)

// IdleReason is a Google speaker's account of why it went idle. The R1 gives
// none, and is judged by its last position instead.
type IdleReason string

const (
	IdleNone        IdleReason = ""
	IdleFinished    IdleReason = "FINISHED"
	IdleCancelled   IdleReason = "CANCELLED"
	IdleInterrupted IdleReason = "INTERRUPTED"
	IdleError       IdleReason = "ERROR"
)

// DeviceStatus is one answer from a speaker.
type DeviceStatus struct {
	State DeviceState
	// MediaKey names what it is playing in the same terms the driver used to
	// load it: the R1's video id, a Nest's content URL, or "app:<id>" for an
	// app that is not ours. Empty when nothing is loaded.
	MediaKey   string
	PositionS  int
	DurationS  int
	Volume     int
	IdleReason IdleReason
}

// Action is something the engine must do after a reconcile.
type Action interface{ action() }

// Load hands queue item Index to the speaker. First is the session's opening
// song, the only one the R1 announces.
type Load struct {
	Index  int
	StartS int
	First  bool
}

// StopDevice stops whatever the speaker is playing.
type StopDevice struct{}

// RecordProgress writes the listener's position into their history.
type RecordProgress struct {
	VideoID   string
	PositionS int
	DurationS int
}

func (Load) action()           {}
func (StopDevice) action()     {}
func (RecordProgress) action() {}

const (
	// OfflineAfter is how long a speaker may fail to answer before the
	// session is given up.
	OfflineAfter = 30 * time.Second
	// LoadTimeout is how long a song may take to start before it is skipped.
	LoadTimeout = 90 * time.Second
	// EndSlackS is how close to its end an R1 song must have been last seen
	// for going idle to count as finishing. The R1's position arrives every
	// 3s (play_youtube.py, `_wait_song`), so the last report before the end is
	// at most 3s short of it.
	EndSlackS = 5
	// ProgressEvery is how often listening is written to history.
	ProgressEvery = 10 * time.Second
)

// Reconcile compares a session with what its speaker just said and decides
// what the session becomes and what must be done. err is the status call's
// failure, if it failed.
func Reconcile(s Session, st DeviceStatus, err error, now time.Time) (Session, []Action) {
	if s.State == Ended {
		return s, nil
	}

	// 1. A speaker that stops answering for long enough is gone.
	if err != nil {
		if now.Sub(s.LastSeenOK) > OfflineAfter {
			return End(s, DeviceOffline, now), nil
		}
		return s, nil
	}
	s.LastSeenOK = now
	s.Volume = st.Volume

	ours := st.MediaKey != "" && st.MediaKey == s.LoadedMediaKey
	stillPrevious := s.State == Loading && st.MediaKey != "" && st.MediaKey == s.PrevMediaKey

	// 2. Something that is not ours is playing: somebody else has the speaker,
	// and nothing is sent to it.
	if st.MediaKey != "" && !ours && !stillPrevious {
		return End(s, TakenOver, now), nil
	}

	if s.State == Loading {
		return reconcileLoading(s, st, ours, now)
	}

	if st.State == DeviceIdle {
		switch {
		case st.IdleReason == IdleError:
			return SkipFailed(s, now)
		case finished(s, st):
			// 4. The song ran to its end.
			item, _ := s.Current()
			record := RecordProgress{VideoID: item.VideoID, PositionS: s.DurationS, DurationS: s.DurationS}
			s, actions := Advance(s, now)
			return s, append([]Action{record}, actions...)
		default:
			// 5. Idle part way through: somebody stopped it on the speaker.
			return End(s, StoppedOnDevice, now), nil
		}
	}

	// 6 and 7. Ours, playing or paused. An R1 answering a question reports
	// "playing" with a position that stands still; that is copied as it is.
	return follow(s, st, now)
}

func reconcileLoading(s Session, st DeviceStatus, ours bool, now time.Time) (Session, []Action) {
	if ours {
		switch st.State {
		case DevicePlaying, DevicePaused:
			s.LoadAcked = true
			s.LoadingSince = time.Time{}
			return follow(s, st, now)
		case DeviceLoading:
			s.LoadAcked = true
		}
	}
	if st.State == DeviceIdle && (s.LoadAcked || st.IdleReason == IdleError) {
		// It showed our song and then gave up on it.
		return SkipFailed(s, now)
	}
	// 3. Too long to start.
	if now.Sub(s.LoadingSince) > LoadTimeout {
		return SkipFailed(s, now)
	}
	return s, nil
}

func follow(s Session, st DeviceStatus, now time.Time) (Session, []Action) {
	if st.State == DevicePaused {
		s.State = Paused
	} else if st.State == DevicePlaying {
		s.State = Playing
	}
	s.PositionS = st.PositionS
	if st.DurationS > 0 {
		s.DurationS = st.DurationS
	}
	if now.Sub(s.LastProgressAt) < ProgressEvery {
		return s, nil
	}
	s.LastProgressAt = now
	item, _ := s.Current()
	return s, []Action{RecordProgress{VideoID: item.VideoID, PositionS: s.PositionS, DurationS: s.DurationS}}
}

// finished decides whether an idle speaker ran the song to its end. A Nest
// says so; an R1 is judged by where it was last seen.
func finished(s Session, st DeviceStatus) bool {
	if st.IdleReason != IdleNone {
		return st.IdleReason == IdleFinished
	}
	return s.DurationS > 0 && s.PositionS >= s.DurationS-EndSlackS
}
