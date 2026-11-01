package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

// ---- fakes ----

type fakeRepo struct {
	mu       sync.Mutex
	sessions map[string]domain.Session
}

func newFakeRepo() *fakeRepo { return &fakeRepo{sessions: map[string]domain.Session{}} }

func (r *fakeRepo) Active(context.Context) ([]domain.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []domain.Session{}
	for _, s := range r.sessions {
		if s.State != domain.Ended {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *fakeRepo) Get(_ context.Context, id string) (domain.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	return s, nil
}

func (r *fakeRepo) Create(_ context.Context, s domain.Session, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, old := range r.sessions {
		if old.DeviceID == s.DeviceID && old.State != domain.Ended {
			r.sessions[id] = domain.End(old, domain.Replaced, now)
		}
	}
	r.sessions[s.ID] = s
	return nil
}

func (r *fakeRepo) Save(_ context.Context, s domain.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions[s.ID].State == domain.Ended {
		return domain.ErrEnded
	}
	r.sessions[s.ID] = s
	return nil
}

func (r *fakeRepo) activeCount(deviceID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sessions {
		if s.DeviceID == deviceID && s.State != domain.Ended {
			n++
		}
	}
	return n
}

type call struct {
	op       string
	device   string
	video    string
	arg      int
	first    bool
	narrated bool
}

type fakeDriver struct {
	streams bool
	mu      sync.Mutex
	calls   []call
	status  domain.DeviceStatus
	failErr error
	devices []domain.Device
}

func (d *fakeDriver) Kind() domain.Kind { return domain.KindR1 }

func (d *fakeDriver) StreamsNarration() bool { return d.streams }

func (d *fakeDriver) Devices(context.Context) ([]domain.Device, error) { return d.devices, nil }

func (d *fakeDriver) record(c call) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, c)
	return d.failErr
}

func (d *fakeDriver) Load(_ context.Context, dev string, it domain.Item, startS int, first, narrated bool) (string, error) {
	if err := d.record(call{op: "load", device: dev, video: it.VideoID, arg: startS, first: first, narrated: narrated}); err != nil {
		return "", err
	}
	if narrated {
		return it.VideoID + "~narrated", nil
	}
	return it.VideoID, nil
}
func (d *fakeDriver) Pause(_ context.Context, dev string) error {
	return d.record(call{op: "pause", device: dev})
}
func (d *fakeDriver) Resume(_ context.Context, dev string) error {
	return d.record(call{op: "resume", device: dev})
}
func (d *fakeDriver) Stop(_ context.Context, dev string) error {
	return d.record(call{op: "stop", device: dev})
}
func (d *fakeDriver) Seek(_ context.Context, dev string, s int) error {
	return d.record(call{op: "seek", device: dev, arg: s})
}
func (d *fakeDriver) SetVolume(_ context.Context, dev string, p int) error {
	return d.record(call{op: "volume", device: dev, arg: p})
}
func (d *fakeDriver) Status(context.Context, string) (domain.DeviceStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status, nil
}

func (d *fakeDriver) setStatus(st domain.DeviceStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status = st
}

func (d *fakeDriver) ops(op string) []call {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []call{}
	for _, c := range d.calls {
		if c.op == op {
			out = append(out, c)
		}
	}
	return out
}

type fakeCatalog struct {
	mu       sync.Mutex
	live     map[string]bool
	gone     map[string]bool
	spoken   map[string]string
	progress []domain.Action
}

func (c *fakeCatalog) Videos(_ context.Context, _ string, ids []string) ([]CatalogVideo, error) {
	out := make([]CatalogVideo, 0, len(ids))
	for _, id := range ids {
		out = append(out, CatalogVideo{
			Item: domain.Item{VideoID: id, Title: "T " + id, DurationS: 200},
			Live: c.live[id], Unavailable: c.gone[id], SpokenLanguage: c.spoken[id],
		})
	}
	return out, nil
}

func (c *fakeCatalog) RecordProgress(_ context.Context, _ string, videoID string, pos, dur int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.progress = append(c.progress, domain.RecordProgress{VideoID: videoID, PositionS: pos, DurationS: dur})
	return nil
}

// fakeNarrator answers the file as set, and the stream as set by setStream
// (preparing otherwise).
type fakeNarrator struct {
	mu      sync.Mutex
	answers map[string]domain.Narrated
	streams map[string]domain.Narrated
	spoken  map[string]string
	// How many times the gateway was asked which language a song speaks.
	spokenAsked int
	asked       []string
	from        []int
}

func (n *fakeNarrator) SpokenLanguage(_ context.Context, videoID string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.spokenAsked++
	return n.spoken[videoID], nil
}

func (n *fakeNarrator) Prepare(_ context.Context, videoID string, fromS int) (domain.NarratedAnswer, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.asked = append(n.asked, videoID)
	n.from = append(n.from, fromS)
	a := domain.NarratedAnswer{File: domain.NarratedPreparing, Stream: domain.NarratedPreparing}
	if f, ok := n.answers[videoID]; ok {
		a.File = f
	}
	if s, ok := n.streams[videoID]; ok {
		a.Stream = s
	}
	return a, nil
}

func (n *fakeNarrator) set(videoID string, a domain.Narrated) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.answers[videoID] = a
}

func (n *fakeNarrator) setStream(videoID string, a domain.Narrated) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.streams[videoID] = a
}

func (n *fakeNarrator) wasAsked(videoID string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, v := range n.asked {
		if v == videoID {
			return true
		}
	}
	return false
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type rig struct {
	engine   *Engine
	repo     *fakeRepo
	driver   *fakeDriver
	catalog  *fakeCatalog
	clock    *clock
	narrator *fakeNarrator
}

func newRig() rig {
	repo := newFakeRepo()
	drv := &fakeDriver{devices: []domain.Device{{ID: "r1", Name: "Loa R1", Kind: domain.KindR1, Online: true}}}
	cat := &fakeCatalog{live: map[string]bool{}, gone: map[string]bool{}}
	clk := &clock{now: time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC)}
	nar := &fakeNarrator{answers: map[string]domain.Narrated{}, streams: map[string]domain.Narrated{}, spoken: map[string]string{}}
	e := NewEngine(repo, []Driver{drv}, cat, nar, clk.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return rig{engine: e, repo: repo, driver: drv, catalog: cat, clock: clk, narrator: nar}
}

func (r rig) start(t *testing.T, ids ...string) domain.Session {
	t.Helper()
	s, err := r.engine.StartSession(context.Background(), StartRequest{
		UserID: "u1", DeviceID: "r1", VideoIDs: ids, StartIndex: 0, StartPositionS: 90,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return s
}

// ---- tests ----

func TestStartLoadsTheFirstSongAnnouncedAtTheGivenPosition(t *testing.T) {
	r := newRig()
	s := r.start(t, "a", "b")
	if s.State != domain.Loading || s.CurrentIndex != 0 {
		t.Fatalf("state=%s index=%d", s.State, s.CurrentIndex)
	}
	loads := r.driver.ops("load")
	if len(loads) != 1 || loads[0].video != "a" || loads[0].arg != 90 || !loads[0].first {
		t.Fatalf("loads = %+v", loads)
	}
}

func TestStartOnABusySpeakerReplacesTheSessionOnIt(t *testing.T) {
	r := newRig()
	first := r.start(t, "a")
	second := r.start(t, "c")

	old, _ := r.repo.Get(context.Background(), first.ID)
	if old.State != domain.Ended || old.EndedReason != domain.Replaced {
		t.Fatalf("old session state=%s reason=%s", old.State, old.EndedReason)
	}
	if second.State != domain.Loading {
		t.Fatalf("new session state = %s", second.State)
	}
	if n := r.repo.activeCount("r1"); n != 1 {
		t.Fatalf("active sessions on the speaker = %d, want 1", n)
	}
}

func TestStartRefusesABroadcast(t *testing.T) {
	r := newRig()
	r.catalog.live["live1"] = true
	_, err := r.engine.StartSession(context.Background(), StartRequest{UserID: "u1", DeviceID: "r1", VideoIDs: []string{"live1", "a"}})
	if !errors.Is(err, domain.ErrLive) {
		t.Fatalf("err = %v, want ErrLive", err)
	}
	if len(r.driver.ops("load")) != 0 {
		t.Fatal("a broadcast reached the speaker")
	}
}

// A broadcast further down the list is not the one asked for: an up-next rail
// or a channel page carries one as often as not, and refusing the whole queue
// for it refused the song that was picked ("a broadcast cannot be cast" on a
// video that was not one).
func TestStartLeavesOutABroadcastLaterInTheQueue(t *testing.T) {
	r := newRig()
	r.catalog.live["live1"] = true
	s, err := r.engine.StartSession(context.Background(), StartRequest{UserID: "u1", DeviceID: "r1", VideoIDs: []string{"a", "live1", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Queue) != 2 || s.Queue[0].VideoID != "a" || s.Queue[1].VideoID != "b" {
		t.Fatalf("queue = %+v", s.Queue)
	}
}

func TestStartLeavesOutVideosUpstreamRefused(t *testing.T) {
	r := newRig()
	r.catalog.gone["a"] = true
	s, err := r.engine.StartSession(context.Background(), StartRequest{UserID: "u1", DeviceID: "r1", VideoIDs: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Queue) != 1 || s.Queue[0].VideoID != "b" {
		t.Fatalf("queue = %+v", s.Queue)
	}
}

func TestStartOnAnUnknownSpeakerIsNotFound(t *testing.T) {
	r := newRig()
	_, err := r.engine.StartSession(context.Background(), StartRequest{UserID: "u1", DeviceID: "nest", VideoIDs: []string{"a"}})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestStartOnAnOfflineSpeakerIsRefused(t *testing.T) {
	r := newRig()
	r.driver.devices[0].Online = false
	_, err := r.engine.StartSession(context.Background(), StartRequest{UserID: "u1", DeviceID: "r1", VideoIDs: []string{"a"}})
	if !errors.Is(err, domain.ErrDeviceOffline) {
		t.Fatalf("err = %v, want ErrDeviceOffline", err)
	}
}

func TestTheLoopFollowsASongToTheNext(t *testing.T) {
	r := newRig()
	s := r.start(t, "a", "b")
	ctx := context.Background()

	r.driver.setStatus(domain.DeviceStatus{State: domain.DevicePlaying, MediaKey: "a", PositionS: 198, DurationS: 200})
	r.clock.advance(time.Second)
	r.engine.Tick(ctx)
	r.driver.setStatus(domain.DeviceStatus{State: domain.DeviceIdle})
	r.clock.advance(time.Second)
	r.engine.Tick(ctx)

	got, _ := r.repo.Get(ctx, s.ID)
	if got.CurrentIndex != 1 || got.State != domain.Loading {
		t.Fatalf("index=%d state=%s", got.CurrentIndex, got.State)
	}
	loads := r.driver.ops("load")
	if len(loads) != 2 || loads[1].video != "b" || loads[1].first {
		t.Fatalf("loads = %+v", loads)
	}
	if got.LoadedMediaKey != "b" || got.PrevMediaKey != "a" {
		t.Fatalf("keys loaded=%q prev=%q", got.LoadedMediaKey, got.PrevMediaKey)
	}
	if len(r.catalog.progress) == 0 {
		t.Fatal("nothing reached the listener's history")
	}
}

func TestALoadThatFailsSkipsToTheNextSong(t *testing.T) {
	r := newRig()
	s := r.start(t, "a", "b", "c")
	ctx := context.Background()

	r.driver.mu.Lock()
	r.driver.failErr = errors.New("speaker refused")
	r.driver.mu.Unlock()
	if _, err := r.engine.Next(ctx, s.ID); err != nil {
		t.Fatalf("next: %v", err)
	}
	got, _ := r.repo.Get(ctx, s.ID)
	if got.State != domain.Ended || got.EndedReason != domain.Failed {
		t.Fatalf("after every load failed: state=%s reason=%s", got.State, got.EndedReason)
	}
}

func TestReplaceQueueWithAStaleVersionIsRefused(t *testing.T) {
	r := newRig()
	s := r.start(t, "a", "b")
	_, err := r.engine.ReplaceQueue(context.Background(), s.ID, s.Version-1, []string{"b", "a"}, 1)
	if !errors.Is(err, domain.ErrStaleVersion) {
		t.Fatalf("err = %v, want ErrStaleVersion", err)
	}
}

func TestReorderingKeepsTheSongPlaying(t *testing.T) {
	r := newRig()
	s := r.start(t, "a", "b", "c")
	got, err := r.engine.ReplaceQueue(context.Background(), s.ID, s.Version, []string{"c", "a", "b"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentIndex != 1 || len(r.driver.ops("load")) != 1 {
		t.Fatalf("index=%d loads=%d", got.CurrentIndex, len(r.driver.ops("load")))
	}
}

func TestRemovingTheCurrentSongLoadsAnother(t *testing.T) {
	r := newRig()
	s := r.start(t, "a", "b", "c")
	got, err := r.engine.ReplaceQueue(context.Background(), s.ID, s.Version, []string{"b", "c"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	loads := r.driver.ops("load")
	if len(loads) != 2 || loads[1].video != "b" || got.CurrentIndex != 0 {
		t.Fatalf("loads=%+v index=%d", loads, got.CurrentIndex)
	}
}

func TestHandoffStopsTheSpeakerAndSaysWhere(t *testing.T) {
	r := newRig()
	s := r.start(t, "a")
	ctx := context.Background()
	r.driver.setStatus(domain.DeviceStatus{State: domain.DevicePlaying, MediaKey: "a", PositionS: 130, DurationS: 200})
	r.clock.advance(time.Second)
	r.engine.Tick(ctx)

	videoID, pos, err := r.engine.Handoff(ctx, s.ID)
	if err != nil || videoID != "a" || pos != 130 {
		t.Fatalf("handoff = %q %d %v", videoID, pos, err)
	}
	got, _ := r.repo.Get(ctx, s.ID)
	if got.State != domain.Ended || got.EndedReason != domain.Handoff {
		t.Fatalf("state=%s reason=%s", got.State, got.EndedReason)
	}
	if len(r.driver.ops("stop")) != 1 {
		t.Fatal("the speaker was not stopped")
	}
	last := r.catalog.progress[len(r.catalog.progress)-1].(domain.RecordProgress)
	if last.VideoID != "a" || last.PositionS != 130 {
		t.Fatalf("history after handoff = %+v, want a at 130", last)
	}
}

func TestStopEndsTheSession(t *testing.T) {
	r := newRig()
	s := r.start(t, "a")
	got, err := r.engine.Stop(context.Background(), s.ID)
	if err != nil || got.State != domain.Ended || got.EndedReason != domain.Stopped {
		t.Fatalf("stop = %+v %v", got, err)
	}
	if _, err := r.engine.Pause(context.Background(), s.ID); !errors.Is(err, domain.ErrEnded) {
		t.Fatalf("a command on an ended session: err = %v", err)
	}
}

func TestVolumeIsClampedAndSent(t *testing.T) {
	r := newRig()
	s := r.start(t, "a")
	got, err := r.engine.SetVolume(context.Background(), s.ID, 140)
	if err != nil || got.Volume != 100 {
		t.Fatalf("volume = %d %v", got.Volume, err)
	}
	if v := r.driver.ops("volume"); len(v) != 1 || v[0].arg != 100 {
		t.Fatalf("volume calls = %+v", v)
	}
}

func TestARestartedEngineFollowsWithoutReloading(t *testing.T) {
	r := newRig()
	s := r.start(t, "a", "b")
	ctx := context.Background()
	r.driver.setStatus(domain.DeviceStatus{State: domain.DevicePlaying, MediaKey: "a", PositionS: 50, DurationS: 200})
	r.clock.advance(time.Second)
	r.engine.Tick(ctx)

	restarted := NewEngine(r.repo, []Driver{r.driver}, r.catalog, r.narrator, r.clock.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.driver.setStatus(domain.DeviceStatus{State: domain.DevicePlaying, MediaKey: "a", PositionS: 52, DurationS: 200})
	r.clock.advance(2 * time.Second)
	restarted.Tick(ctx)

	got, _ := r.repo.Get(ctx, s.ID)
	if got.State != domain.Playing || got.PositionS != 52 {
		t.Fatalf("state=%s position=%d", got.State, got.PositionS)
	}
	if n := len(r.driver.ops("load")); n != 1 {
		t.Fatalf("loads after restart = %d, want the original 1", n)
	}
}

func TestCommandsAndTicksDoNotRace(t *testing.T) {
	r := newRig()
	s := r.start(t, "a", "b", "c")
	ctx := context.Background()
	r.driver.setStatus(domain.DeviceStatus{State: domain.DevicePlaying, MediaKey: "a", PositionS: 10, DurationS: 200})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); r.engine.Tick(ctx) }()
		go func() { defer wg.Done(); _, _ = r.engine.Seek(ctx, s.ID, 30) }()
		go func() { defer wg.Done(); _, _ = r.engine.SetVolume(ctx, s.ID, 10) }()
	}
	wg.Wait()

	got, _ := r.repo.Get(ctx, s.ID)
	if got.State == domain.Ended {
		t.Fatalf("the session ended: %s", got.EndedReason)
	}
	if n := len(r.driver.ops("load")); n != 1 {
		t.Fatalf("loads = %d, want 1", n)
	}
}
