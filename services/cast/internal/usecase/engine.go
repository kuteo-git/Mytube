// Package usecase runs cast sessions: a loop that follows every speaker with a
// session on it, and the commands the remotes send.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// Repo keeps sessions. Create ends any session still active on the same
// speaker as replaced, in the same transaction. Save refuses (ErrEnded) to
// write over a session already ended in storage.
type Repo interface {
	Active(ctx context.Context) ([]domain.Session, error)
	Get(ctx context.Context, id string) (domain.Session, error)
	Create(ctx context.Context, s domain.Session, now time.Time) error
	Save(ctx context.Context, s domain.Session) error
}

// Driver talks to one family of speaker. Load returns the media key the
// speaker will report while playing what it was handed; narrated hands it the
// song's narrated sound instead of its own.
type Driver interface {
	Kind() domain.Kind
	Devices(ctx context.Context) ([]domain.Device, error)
	Load(ctx context.Context, deviceID string, it domain.Item, startS int, first, narrated bool) (string, error)
	Pause(ctx context.Context, deviceID string) error
	Resume(ctx context.Context, deviceID string) error
	Stop(ctx context.Context, deviceID string) error
	Seek(ctx context.Context, deviceID string, s int) error
	SetVolume(ctx context.Context, deviceID string, pct int) error
	Status(ctx context.Context, deviceID string) (domain.DeviceStatus, error)
}

// CatalogVideo is what the catalogue says about a video being queued.
type CatalogVideo struct {
	Item        domain.Item
	Live        bool
	Unavailable bool
}

// Catalog is the catalogue, as far as casting needs it.
type Catalog interface {
	// Videos answers in the order asked.
	Videos(ctx context.Context, userID string, ids []string) ([]CatalogVideo, error)
	RecordProgress(ctx context.Context, userID, videoID string, positionS, durationS int) error
}

// Narrator is the gateway, as far as narration on a speaker needs it.
type Narrator interface {
	// Prepare asks for the song's narrated sound — starting its narration
	// pass and the mix if they have not run — and says where it is. Asking
	// again is how it is followed.
	Prepare(ctx context.Context, videoID string) (domain.Narrated, error)
}

// How long one speaker gets to answer a status request.
const statusTimeout = 2 * time.Second

// How long the gateway gets to say whether a narrated sound is ready. It
// answers from memory; the work it starts runs on after the answer.
const narratorTimeout = 2 * time.Second

type Engine struct {
	repo     Repo
	drivers  map[domain.Kind]Driver
	catalog  Catalog
	narrator Narrator
	now      func() time.Time
	logger   *slog.Logger

	// One lock per speaker, held by a tick and by every command. A speaker has
	// at most one session, so this also serialises everything done to it: a
	// tick can never load the next song over a session that a command has just
	// replaced.
	mu    sync.Mutex
	locks map[string]*sync.Mutex

	// The gateway's last word on each song's narrated sound. Kept in memory:
	// it is asked again every tick it matters, and lost on a restart costs
	// one question.
	narratedMu sync.Mutex
	narrated   map[string]domain.Narrated
}

func NewEngine(repo Repo, drivers []Driver, catalog Catalog, narrator Narrator, now func() time.Time, logger *slog.Logger) *Engine {
	byKind := map[domain.Kind]Driver{}
	for _, d := range drivers {
		byKind[d.Kind()] = d
	}
	return &Engine{
		repo: repo, drivers: byKind, catalog: catalog, narrator: narrator, now: now, logger: logger,
		locks: map[string]*sync.Mutex{}, narrated: map[string]domain.Narrated{},
	}
}

func (e *Engine) deviceLock(deviceID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.locks[deviceID]
	if !ok {
		l = &sync.Mutex{}
		e.locks[deviceID] = l
	}
	return l
}

// Run ticks every `every` until ctx ends.
func (e *Engine) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.Tick(ctx)
		}
	}
}

// Tick follows every active session once, each speaker in parallel. A speaker
// still busy from the last tick or a command is skipped this time.
func (e *Engine) Tick(ctx context.Context) {
	sessions, err := e.repo.Active(ctx)
	if err != nil {
		e.logger.Error("list active sessions", "error", err)
		return
	}
	var wg sync.WaitGroup
	for _, s := range sessions {
		lock := e.deviceLock(s.DeviceID)
		if !lock.TryLock() {
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer lock.Unlock()
			e.follow(ctx, id)
		}(s.ID)
	}
	wg.Wait()
}

func (e *Engine) follow(ctx context.Context, sessionID string) {
	s, err := e.repo.Get(ctx, sessionID)
	if err != nil || s.State == domain.Ended {
		return
	}
	drv, ok := e.drivers[s.DeviceKind]
	if !ok {
		e.logger.Error("no driver for session", "session", s.ID, "kind", s.DeviceKind)
		return
	}
	statusCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	st, statusErr := drv.Status(statusCtx, s.DeviceID)
	cancel()

	next, actions := domain.Reconcile(s, st, statusErr, e.now())
	next = e.apply(ctx, drv, next, actions)
	next = e.followNarration(ctx, drv, next)
	e.save(ctx, s, next)
}

// followNarration switches the song playing to its narrated sound once the
// gateway has it, or back once narration is off, from where it is. Only while
// playing: a reload starts the speaker, and a paused one must stay paused.
// With the narrated song playing, the next one is asked for, so it is ready
// by the time it is reached.
func (e *Engine) followNarration(ctx context.Context, drv Driver, s domain.Session) domain.Session {
	if s.State != domain.Playing {
		return s
	}
	item, ok := s.Current()
	if !ok {
		return s
	}
	want := s.Narration && e.narratedReady(ctx, item.VideoID)
	if want != s.LoadedNarrated {
		s, load := domain.Reload(s, e.now())
		return e.apply(ctx, drv, s, []domain.Action{load})
	}
	if s.LoadedNarrated && s.CurrentIndex+1 < len(s.Queue) {
		e.narratedReady(ctx, s.Queue[s.CurrentIndex+1].VideoID)
	}
	return s
}

// narratedReady asks the gateway about a song and remembers the answer. A
// gateway that does not answer is "still preparing": the song plays as it is.
func (e *Engine) narratedReady(ctx context.Context, videoID string) bool {
	if e.narrator == nil {
		return false
	}
	e.narratedMu.Lock()
	known := e.narrated[videoID]
	e.narratedMu.Unlock()
	// Ready is final; anything else is asked again, so a pass that failed
	// for a moment (the TTS server restarting) is tried again.
	if known == domain.NarratedReady {
		return true
	}
	askCtx, cancel := context.WithTimeout(ctx, narratorTimeout)
	answer, err := e.narrator.Prepare(askCtx, videoID)
	cancel()
	if err != nil {
		e.logger.Warn("prepare narrated sound", "video", videoID, "error", err)
		return false
	}
	e.narratedMu.Lock()
	e.narrated[videoID] = answer
	e.narratedMu.Unlock()
	return answer == domain.NarratedReady
}

// NarrationStatus is what the remote shows about narration on s.
func (e *Engine) NarrationStatus(s domain.Session) domain.NarrationStatus {
	item, _ := s.Current()
	e.narratedMu.Lock()
	known := e.narrated[item.VideoID]
	e.narratedMu.Unlock()
	return domain.NarrationStatusOf(s, known)
}

// apply carries out what a decision asked for. A load the speaker refuses
// skips that song, which may ask for another load; the queue bounds it.
func (e *Engine) apply(ctx context.Context, drv Driver, s domain.Session, actions []domain.Action) domain.Session {
	for i := 0; i < len(actions); i++ {
		switch a := actions[i].(type) {
		case domain.Load:
			item := s.Queue[a.Index]
			narrated := s.Narration && e.narratedReady(ctx, item.VideoID)
			key, err := drv.Load(ctx, s.DeviceID, item, a.StartS, a.First, narrated)
			if err != nil {
				e.logger.Warn("load refused, skipping", "session", s.ID, "video", item.VideoID, "error", err)
				var more []domain.Action
				s, more = domain.SkipFailed(s, e.now())
				actions = append(actions, more...)
				continue
			}
			s = s.Loaded(key)
			s.LoadedNarrated = narrated
			e.logger.Info("loaded", "session", s.ID, "device", s.DeviceID, "video", item.VideoID, "start", a.StartS,
				"narrated", narrated)
		case domain.StopDevice:
			if err := drv.Stop(ctx, s.DeviceID); err != nil {
				e.logger.Warn("stop speaker", "session", s.ID, "error", err)
			}
		case domain.RecordProgress:
			if err := e.catalog.RecordProgress(ctx, s.StartedBy, a.VideoID, a.PositionS, a.DurationS); err != nil {
				e.logger.Warn("record progress", "session", s.ID, "video", a.VideoID, "error", err)
			}
		}
	}
	return s
}

func (e *Engine) save(ctx context.Context, before, after domain.Session) {
	after.UpdatedAt = e.now()
	if err := e.repo.Save(ctx, after); err != nil && !errors.Is(err, domain.ErrEnded) {
		e.logger.Error("save session", "session", after.ID, "error", err)
		return
	}
	if after.State == domain.Ended && before.State != domain.Ended {
		e.logger.Info("session ended", "session", after.ID, "device", after.DeviceID, "reason", after.EndedReason)
	}
}

// ---- commands ----

// StartRequest is a remote asking to play on a speaker.
type StartRequest struct {
	UserID         string
	DeviceID       string
	VideoIDs       []string
	StartIndex     int
	StartPositionS int
	Narration      bool
}

// Devices lists the speakers answering now.
func (e *Engine) Devices(ctx context.Context) ([]domain.Device, error) {
	var out []domain.Device
	for _, d := range e.drivers {
		devices, err := d.Devices(ctx)
		if err != nil {
			e.logger.Warn("list devices", "kind", d.Kind(), "error", err)
			continue
		}
		for _, dev := range devices {
			if dev.Online {
				out = append(out, dev)
			}
		}
	}
	return out, nil
}

func (e *Engine) findDevice(ctx context.Context, id string) (domain.Device, Driver, error) {
	for _, d := range e.drivers {
		devices, err := d.Devices(ctx)
		if err != nil {
			continue
		}
		for _, dev := range devices {
			if dev.ID == id {
				return dev, d, nil
			}
		}
	}
	return domain.Device{}, nil, fmt.Errorf("device %q: %w", id, domain.ErrNotFound)
}

func (e *Engine) StartSession(ctx context.Context, req StartRequest) (domain.Session, error) {
	device, drv, err := e.findDevice(ctx, req.DeviceID)
	if err != nil {
		return domain.Session{}, err
	}
	if !device.Online {
		return domain.Session{}, fmt.Errorf("device %q: %w", device.ID, domain.ErrDeviceOffline)
	}
	items, start, err := e.queueFor(ctx, req.UserID, req.VideoIDs, req.StartIndex)
	if err != nil {
		return domain.Session{}, err
	}

	lock := e.deviceLock(device.ID)
	lock.Lock()
	defer lock.Unlock()

	now := e.now()
	s := domain.Session{
		ID: uuid.NewString(), DeviceID: device.ID, DeviceName: device.Name, DeviceKind: device.Kind, StartedBy: req.UserID,
		Queue: items, LastSeenOK: now, CreatedAt: now, UpdatedAt: now, Narration: req.Narration,
	}
	startS := req.StartPositionS
	if start != req.StartIndex {
		// The video asked for was left out; whatever plays instead starts at 0.
		startS = 0
	}
	s, load := domain.LoadAt(s, start, max(startS, 0), true, now)
	narrated := s.Narration && e.narratedReady(ctx, s.Queue[start].VideoID)
	key, err := drv.Load(ctx, s.DeviceID, s.Queue[start], load.(domain.Load).StartS, true, narrated)
	if err != nil {
		return domain.Session{}, fmt.Errorf("load on %q: %v: %w", device.ID, err, domain.ErrDeviceOffline)
	}
	s = s.Loaded(key)
	s.LoadedNarrated = narrated
	if err := e.repo.Create(ctx, s, now); err != nil {
		_ = drv.Stop(ctx, s.DeviceID)
		return domain.Session{}, err
	}
	e.logger.Info("session started", "session", s.ID, "device", s.DeviceID, "user", req.UserID,
		"items", len(items), "video", s.Queue[start].VideoID, "start", startS)
	return s, nil
}

// queueFor describes the videos, refusing a broadcast that is the one asked
// for and leaving out the rest: a broadcast further down (an up-next rail or a
// channel page carries one as often as not) and what upstream has refused for
// good. It returns where `startIndex` landed.
func (e *Engine) queueFor(ctx context.Context, userID string, ids []string, startIndex int) ([]domain.Item, int, error) {
	if len(ids) == 0 {
		return nil, 0, domain.ErrEmptyQueue
	}
	videos, err := e.catalog.Videos(ctx, userID, ids)
	if err != nil {
		return nil, 0, err
	}
	items := make([]domain.Item, 0, len(videos))
	start := -1
	for i, v := range videos {
		if v.Live && i == startIndex {
			return nil, 0, fmt.Errorf("video %q: %w", v.Item.VideoID, domain.ErrLive)
		}
		if v.Live || v.Unavailable {
			continue
		}
		if i >= startIndex && start < 0 {
			start = len(items)
		}
		items = append(items, v.Item)
	}
	if len(items) == 0 {
		return nil, 0, domain.ErrEmptyQueue
	}
	if start < 0 {
		start = len(items) - 1
	}
	return items, start, nil
}

func (e *Engine) Session(ctx context.Context, id string) (domain.Session, error) {
	return e.repo.Get(ctx, id)
}

func (e *Engine) Sessions(ctx context.Context) ([]domain.Session, error) {
	return e.repo.Active(ctx)
}

// command runs fn on a live session under its speaker's lock and saves what
// it returns.
func (e *Engine) command(
	ctx context.Context, id string,
	fn func(s domain.Session, drv Driver, now time.Time) (domain.Session, []domain.Action, error),
) (domain.Session, error) {
	s, err := e.repo.Get(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	lock := e.deviceLock(s.DeviceID)
	lock.Lock()
	defer lock.Unlock()

	// Again, under the lock: the tick before it may have moved it on.
	if s, err = e.repo.Get(ctx, id); err != nil {
		return domain.Session{}, err
	}
	if s.State == domain.Ended {
		return s, domain.ErrEnded
	}
	drv, ok := e.drivers[s.DeviceKind]
	if !ok {
		return s, fmt.Errorf("no driver for %q", s.DeviceKind)
	}
	next, actions, err := fn(s, drv, e.now())
	if err != nil {
		return s, err
	}
	next = e.apply(ctx, drv, next, actions)
	e.save(ctx, s, next)
	return next, nil
}

func offline(err error) error {
	return fmt.Errorf("%v: %w", err, domain.ErrDeviceOffline)
}

func (e *Engine) Pause(ctx context.Context, id string) (domain.Session, error) {
	return e.command(ctx, id, func(s domain.Session, drv Driver, _ time.Time) (domain.Session, []domain.Action, error) {
		if err := drv.Pause(ctx, s.DeviceID); err != nil {
			return s, nil, offline(err)
		}
		if s.State == domain.Playing {
			s.State = domain.Paused
		}
		return s, nil, nil
	})
}

func (e *Engine) Resume(ctx context.Context, id string) (domain.Session, error) {
	return e.command(ctx, id, func(s domain.Session, drv Driver, _ time.Time) (domain.Session, []domain.Action, error) {
		if err := drv.Resume(ctx, s.DeviceID); err != nil {
			return s, nil, offline(err)
		}
		if s.State == domain.Paused {
			s.State = domain.Playing
		}
		return s, nil, nil
	})
}

func (e *Engine) Next(ctx context.Context, id string) (domain.Session, error) {
	return e.command(ctx, id, func(s domain.Session, _ Driver, now time.Time) (domain.Session, []domain.Action, error) {
		s, actions := domain.Advance(s, now)
		return s, actions, nil
	})
}

func (e *Engine) Stop(ctx context.Context, id string) (domain.Session, error) {
	return e.command(ctx, id, func(s domain.Session, drv Driver, now time.Time) (domain.Session, []domain.Action, error) {
		// Ended whether or not the speaker heard: the person pressing stop
		// wants this session over, and a speaker that is gone has stopped.
		if err := drv.Stop(ctx, s.DeviceID); err != nil {
			e.logger.Warn("stop speaker", "session", s.ID, "error", err)
		}
		return domain.End(s, domain.Stopped, now), nil, nil
	})
}

func (e *Engine) Seek(ctx context.Context, id string, positionS int) (domain.Session, error) {
	positionS = max(positionS, 0)
	return e.command(ctx, id, func(s domain.Session, drv Driver, _ time.Time) (domain.Session, []domain.Action, error) {
		if s.DurationS > 0 {
			positionS = min(positionS, s.DurationS)
		}
		if err := drv.Seek(ctx, s.DeviceID, positionS); err != nil {
			return s, nil, offline(err)
		}
		s.PositionS = positionS
		return s, nil, nil
	})
}

func (e *Engine) SetVolume(ctx context.Context, id string, percent int) (domain.Session, error) {
	percent = min(max(percent, 0), 100)
	return e.command(ctx, id, func(s domain.Session, drv Driver, _ time.Time) (domain.Session, []domain.Action, error) {
		if err := drv.SetVolume(ctx, s.DeviceID, percent); err != nil {
			return s, nil, offline(err)
		}
		s.Volume = percent
		return s, nil, nil
	})
}

// ErrBadIndex is a jump to a position the queue does not have.
var ErrBadIndex = errors.New("index is outside the queue")

func (e *Engine) Jump(ctx context.Context, id string, index int) (domain.Session, error) {
	return e.command(ctx, id, func(s domain.Session, _ Driver, now time.Time) (domain.Session, []domain.Action, error) {
		if index < 0 || index >= len(s.Queue) {
			return s, nil, ErrBadIndex
		}
		s, load := domain.LoadAt(s, index, 0, false, now)
		return s, []domain.Action{load}, nil
	})
}

func (e *Engine) ReplaceQueue(ctx context.Context, id string, version int, ids []string, currentIndex int) (domain.Session, error) {
	s, err := e.repo.Get(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	// Described before the lock: it is a request across a service boundary,
	// and nothing about it depends on the session.
	items, _, err := e.queueFor(ctx, s.StartedBy, ids, 0)
	if err != nil {
		return domain.Session{}, err
	}
	return e.command(ctx, id, func(s domain.Session, _ Driver, now time.Time) (domain.Session, []domain.Action, error) {
		if s.Version != version {
			return s, nil, domain.ErrStaleVersion
		}
		s, reload := domain.ReplaceQueue(s, items, currentIndex)
		if !reload {
			return s, nil, nil
		}
		s, load := domain.LoadAt(s, s.CurrentIndex, 0, false, now)
		return s, []domain.Action{load}, nil
	})
}

// SetNarration turns narration on the speaker on or off. The switch happens
// now when it can — the narrated sound ready, or narration going off — and
// otherwise on the tick that finds it ready.
func (e *Engine) SetNarration(ctx context.Context, id string, on bool) (domain.Session, error) {
	return e.command(ctx, id, func(s domain.Session, drv Driver, now time.Time) (domain.Session, []domain.Action, error) {
		s.Narration = on
		item, ok := s.Current()
		if !ok || s.State != domain.Playing {
			return s, nil, nil
		}
		if want := on && e.narratedReady(ctx, item.VideoID); want == s.LoadedNarrated {
			return s, nil, nil
		}
		s, load := domain.Reload(s, now)
		return s, []domain.Action{load}, nil
	})
}

// Handoff stops the speaker and says where the listener was, so the phone can
// carry on from there.
func (e *Engine) Handoff(ctx context.Context, id string) (string, int, error) {
	var videoID string
	var position int
	_, err := e.command(ctx, id, func(s domain.Session, drv Driver, now time.Time) (domain.Session, []domain.Action, error) {
		if err := drv.Stop(ctx, s.DeviceID); err != nil {
			e.logger.Warn("stop speaker for handoff", "session", s.ID, "error", err)
		}
		item, _ := s.Current()
		videoID, position = item.VideoID, s.PositionS
		// Written to history, so the watch page opening it elsewhere resumes
		// here rather than from the last ten-second report.
		record := domain.RecordProgress{VideoID: item.VideoID, PositionS: s.PositionS, DurationS: s.DurationS}
		return domain.End(s, domain.Handoff, now), []domain.Action{record}, nil
	})
	return videoID, position, err
}
