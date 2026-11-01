package domain

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC)

func queue(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{VideoID: string(rune('a' + i)), DurationS: 245}
	}
	return items
}

// playing is a session that has been playing item `index` of an n-item queue.
func playing(n, index int) Session {
	return Session{
		ID: "s1", DeviceID: "r1", DeviceKind: KindR1,
		Queue: queue(n), CurrentIndex: index,
		State: Playing, LoadedMediaKey: string(rune('a' + index)), LoadAcked: true,
		PositionS: 100, DurationS: 245, Version: 3,
		LastSeenOK: t0, LastProgressAt: t0,
	}
}

func loading(n, index int) Session {
	s := playing(n, index)
	s.State, s.LoadAcked, s.PositionS, s.LoadingSince = Loading, false, 0, t0
	s.LastProgressAt = time.Time{}
	if index > 0 {
		s.PrevMediaKey = string(rune('a' + index - 1))
	}
	return s
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name        string
		session     Session
		status      DeviceStatus
		err         error
		now         time.Time
		wantState   State
		wantReason  EndedReason
		wantIndex   int
		wantActions []Action
	}{
		{
			name:      "the song is still playing: the position follows the speaker",
			session:   playing(3, 0),
			status:    DeviceStatus{State: DevicePlaying, MediaKey: "a", PositionS: 104, DurationS: 245, Volume: 40},
			now:       t0.Add(4 * time.Second),
			wantState: Playing, wantIndex: 0,
		},
		{
			name:      "progress is recorded once ten seconds have passed, not on every tick",
			session:   playing(3, 0),
			status:    DeviceStatus{State: DevicePlaying, MediaKey: "a", PositionS: 110, DurationS: 245},
			now:       t0.Add(10 * time.Second),
			wantState: Playing, wantIndex: 0,
			wantActions: []Action{RecordProgress{VideoID: "a", PositionS: 110, DurationS: 245}},
		},
		{
			name:      "a song that ran to its end moves the queue on",
			session:   func() Session { s := playing(3, 0); s.PositionS = 243; return s }(),
			status:    DeviceStatus{State: DeviceIdle},
			now:       t0.Add(time.Second),
			wantState: Loading, wantIndex: 1,
			wantActions: []Action{
				RecordProgress{VideoID: "a", PositionS: 245, DurationS: 245},
				Load{Index: 1, StartS: 0, First: false},
			},
		},
		{
			name:      "the last song ending finishes the queue and stops the speaker",
			session:   func() Session { s := playing(2, 1); s.PositionS = 242; return s }(),
			status:    DeviceStatus{State: DeviceIdle},
			now:       t0.Add(time.Second),
			wantState: Ended, wantReason: QueueFinished, wantIndex: 1,
			wantActions: []Action{RecordProgress{VideoID: "b", PositionS: 245, DurationS: 245}, StopDevice{}},
		},
		{
			name:      "idle part way through is someone stopping it on the speaker",
			session:   func() Session { s := playing(3, 0); s.PositionS = 30; return s }(),
			status:    DeviceStatus{State: DeviceIdle},
			now:       t0.Add(time.Second),
			wantState: Ended, wantReason: StoppedOnDevice,
		},
		{
			name:      "another song on the R1 is somebody else's, and nothing is sent to it",
			session:   playing(3, 0),
			status:    DeviceStatus{State: DeviceLoading, MediaKey: "son-tung"},
			now:       t0.Add(time.Second),
			wantState: Ended, wantReason: TakenOver,
		},
		{
			name: "another app on a Nest is a takeover",
			session: func() Session {
				s := playing(3, 0)
				s.DeviceKind = KindGoogle
				s.LoadedMediaKey = "http://mac/a"
				return s
			}(),
			status:    DeviceStatus{State: DevicePlaying, MediaKey: "app:spotify"},
			now:       t0.Add(time.Second),
			wantState: Ended, wantReason: TakenOver,
		},
		{
			name: "a Nest finishing the song moves on whatever the position said",
			session: func() Session {
				s := playing(3, 0)
				s.DeviceKind = KindGoogle
				s.LoadedMediaKey = "http://mac/a"
				s.PositionS = 120
				return s
			}(),
			status:    DeviceStatus{State: DeviceIdle, MediaKey: "http://mac/a", IdleReason: IdleFinished},
			now:       t0.Add(time.Second),
			wantState: Loading, wantIndex: 1,
			wantActions: []Action{
				RecordProgress{VideoID: "a", PositionS: 245, DurationS: 245},
				Load{Index: 1, StartS: 0, First: false},
			},
		},
		{
			name: "a Nest stopped from Google Home is stopped on the device",
			session: func() Session {
				s := playing(3, 0)
				s.DeviceKind = KindGoogle
				s.LoadedMediaKey = "http://mac/a"
				s.PositionS = 243
				return s
			}(),
			status:    DeviceStatus{State: DeviceIdle, MediaKey: "http://mac/a", IdleReason: IdleCancelled},
			now:       t0.Add(time.Second),
			wantState: Ended, wantReason: StoppedOnDevice,
		},
		{
			name:      "an R1 answering a question holds its position, and that is not a stop",
			session:   playing(3, 0),
			status:    DeviceStatus{State: DevicePlaying, MediaKey: "a", PositionS: 100, DurationS: 245},
			now:       t0.Add(40 * time.Second),
			wantState: Playing, wantIndex: 0,
			wantActions: []Action{RecordProgress{VideoID: "a", PositionS: 100, DurationS: 245}},
		},
		{
			name:      "29 seconds without an answer is not yet offline",
			session:   playing(3, 0),
			err:       errors.New("timeout"),
			now:       t0.Add(29 * time.Second),
			wantState: Playing, wantIndex: 0,
		},
		{
			name:      "31 seconds without an answer is offline",
			session:   playing(3, 0),
			err:       errors.New("timeout"),
			now:       t0.Add(31 * time.Second),
			wantState: Ended, wantReason: DeviceOffline,
		},
		{
			name:      "just after a load the speaker still says stopped, and that is waited out",
			session:   loading(3, 1),
			status:    DeviceStatus{State: DeviceIdle},
			now:       t0.Add(2 * time.Second),
			wantState: Loading, wantIndex: 1,
		},
		{
			name:      "just after a load the previous song of ours is still sounding",
			session:   loading(3, 1),
			status:    DeviceStatus{State: DevicePlaying, MediaKey: "a", PositionS: 244},
			now:       t0.Add(time.Second),
			wantState: Loading, wantIndex: 1,
		},
		{
			name:      "the loaded song starting playing makes the session playing",
			session:   loading(3, 1),
			status:    DeviceStatus{State: DevicePlaying, MediaKey: "b", PositionS: 0, DurationS: 200},
			now:       t0.Add(5 * time.Second),
			wantState: Playing, wantIndex: 1,
			wantActions: []Action{RecordProgress{VideoID: "b", PositionS: 0, DurationS: 200}},
		},
		{
			name: "a song the R1 could not download is skipped",
			session: func() Session {
				s := loading(3, 1)
				s.LoadAcked = true
				return s
			}(),
			status:    DeviceStatus{State: DeviceIdle},
			now:       t0.Add(5 * time.Second),
			wantState: Loading, wantIndex: 2,
			wantActions: []Action{Load{Index: 2, StartS: 0, First: false}},
		},
		{
			name:      "loading for more than 90 seconds skips the song",
			session:   loading(3, 1),
			status:    DeviceStatus{State: DeviceIdle},
			now:       t0.Add(91 * time.Second),
			wantState: Loading, wantIndex: 2,
			wantActions: []Action{Load{Index: 2, StartS: 0, First: false}},
		},
		{
			name:      "the last song failing to load ends the session as failed",
			session:   loading(3, 2),
			status:    DeviceStatus{State: DeviceIdle},
			now:       t0.Add(91 * time.Second),
			wantState: Ended, wantReason: Failed, wantIndex: 2,
		},
		{
			name:      "a session already ended is left alone",
			session:   func() Session { s := playing(3, 0); s.State, s.EndedReason = Ended, Stopped; return s }(),
			status:    DeviceStatus{State: DevicePlaying, MediaKey: "x"},
			now:       t0.Add(time.Second),
			wantState: Ended, wantReason: Stopped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, actions := Reconcile(tt.session, tt.status, tt.err, tt.now)
			if got.State != tt.wantState {
				t.Errorf("state = %q, want %q", got.State, tt.wantState)
			}
			if got.EndedReason != tt.wantReason {
				t.Errorf("ended reason = %q, want %q", got.EndedReason, tt.wantReason)
			}
			if got.State != Ended && got.CurrentIndex != tt.wantIndex {
				t.Errorf("current index = %d, want %d", got.CurrentIndex, tt.wantIndex)
			}
			if !reflect.DeepEqual(actions, tt.wantActions) {
				t.Errorf("actions = %#v, want %#v", actions, tt.wantActions)
			}
		})
	}
}

func TestReconcileFollowsTheSpeaker(t *testing.T) {
	got, _ := Reconcile(playing(3, 0),
		DeviceStatus{State: DevicePaused, MediaKey: "a", PositionS: 130, DurationS: 240, Volume: 60},
		nil, t0.Add(time.Second))
	if got.State != Paused || got.PositionS != 130 || got.DurationS != 240 || got.Volume != 60 {
		t.Fatalf("got state=%s position=%d duration=%d volume=%d", got.State, got.PositionS, got.DurationS, got.Volume)
	}
	if !got.LastSeenOK.Equal(t0.Add(time.Second)) {
		t.Fatalf("last seen = %v", got.LastSeenOK)
	}
}

func TestAdvanceBumpsTheVersionAndTheTickDoesNot(t *testing.T) {
	s := playing(3, 0)
	ticked, _ := Reconcile(s, DeviceStatus{State: DevicePlaying, MediaKey: "a", PositionS: 101}, nil, t0.Add(time.Second))
	if ticked.Version != s.Version {
		t.Fatalf("a position update changed the version: %d -> %d", s.Version, ticked.Version)
	}
	advanced, _ := Advance(s, t0)
	if advanced.Version != s.Version+1 {
		t.Fatalf("advance version = %d, want %d", advanced.Version, s.Version+1)
	}
}

func TestLoadedRemembersThePreviousKey(t *testing.T) {
	s := loading(3, 1)
	s.LoadedMediaKey = "a"
	got := s.Loaded("b")
	if got.LoadedMediaKey != "b" || got.PrevMediaKey != "a" {
		t.Fatalf("loaded = %q, prev = %q", got.LoadedMediaKey, got.PrevMediaKey)
	}
}

func TestReplaceQueue(t *testing.T) {
	ids := func(s Session) []string {
		out := []string{}
		for _, it := range s.Queue {
			out = append(out, it.VideoID)
		}
		return out
	}

	t.Run("reordering around the current song does not reload it", func(t *testing.T) {
		s := playing(3, 1) // a, [b], c
		got, reload := ReplaceQueue(s, []Item{{VideoID: "c"}, {VideoID: "b"}, {VideoID: "a"}}, 0)
		if reload {
			t.Fatal("the current song was reloaded")
		}
		if got.CurrentIndex != 1 || !reflect.DeepEqual(ids(got), []string{"c", "b", "a"}) {
			t.Fatalf("index=%d queue=%v", got.CurrentIndex, ids(got))
		}
		if got.Version != s.Version+1 {
			t.Fatalf("version = %d", got.Version)
		}
	})

	t.Run("removing the current song loads the one at the index given", func(t *testing.T) {
		s := playing(3, 1)
		got, reload := ReplaceQueue(s, []Item{{VideoID: "a"}, {VideoID: "c"}}, 1)
		if !reload || got.CurrentIndex != 1 || got.State != Loading {
			t.Fatalf("reload=%v index=%d state=%s", reload, got.CurrentIndex, got.State)
		}
	})

	t.Run("an index past the end is clamped", func(t *testing.T) {
		s := playing(3, 1)
		got, reload := ReplaceQueue(s, []Item{{VideoID: "a"}}, 5)
		if !reload || got.CurrentIndex != 0 {
			t.Fatalf("reload=%v index=%d", reload, got.CurrentIndex)
		}
	})
}

func TestEndIsFinal(t *testing.T) {
	s := End(playing(3, 0), Stopped, t0)
	again := End(s, TakenOver, t0)
	if again.EndedReason != Stopped {
		t.Fatalf("a second end replaced the reason: %s", again.EndedReason)
	}
}
