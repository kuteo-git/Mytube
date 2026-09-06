package httpapi

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type countingResolver struct {
	calls   atomic.Int32
	release chan struct{}
}

func (r *countingResolver) ResolveTracks(
	ctx context.Context, _ string, _ int32,
) (domain.MediaTracks, error) {
	r.calls.Add(1)
	select {
	case <-r.release:
	case <-ctx.Done():
		return domain.MediaTracks{}, ctx.Err()
	}
	return domain.MediaTracks{
		Videos: []domain.MediaTrack{{Height: 720, URL: "video"}},
		Audio:  domain.MediaTrack{URL: "audio"},
	}, nil
}

// The measured defect: opening a video asks for the master playlist and both
// media playlists at once, all three miss the cache together, and each went
// upstream on its own — 3.01s, 3.05s and 3.50s, three yt-dlp processes and
// three requests to YouTube for one press of a thumbnail.
func TestOneResolvePerVideoHoweverManyPlaylistsAskAtOnce(t *testing.T) {
	resolver := &countingResolver{release: make(chan struct{})}
	h := &Handler{logger: discardLogger()}

	var (
		wg      sync.WaitGroup
		results = make([]domain.MediaTracks, 3)
		errs    = make([]error, 3)
	)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = h.resolveOnce(context.Background(), "v", "key", resolver, "https://example/v")
		}(i)
	}

	// Let all three arrive before any may finish, which is the race being
	// asserted on: released first, they would simply run one after another.
	time.Sleep(20 * time.Millisecond)
	close(resolver.release)
	wg.Wait()

	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("three concurrent requests ran %d resolves, want 1", got)
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if results[i].Audio.URL != "audio" {
			t.Fatalf("caller %d got %+v, want the shared answer", i, results[i])
		}
	}

	// The winner puts the answer in the cache, so the requests that follow the
	// opening burst never reach this path at all.
	if _, cached := h.hls.get("key"); !cached {
		t.Fatal("the shared resolve did not cache what it found")
	}
}

// The shared call must outlive the request that started it. A player that gives
// up on the master playlist would otherwise cancel the resolve the other two
// requests are waiting on, and throw away an answer that was seconds away.
func TestAnAbandonedRequestDoesNotCancelTheSharedResolve(t *testing.T) {
	resolver := &countingResolver{release: make(chan struct{})}
	h := &Handler{logger: discardLogger()}

	first, cancelFirst := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := h.resolveOnce(first, "v", "key", resolver, "https://example/v")
		done <- err
	}()
	<-started

	// Wait until the shared call is actually in flight before abandoning it.
	deadline := time.Now().Add(time.Second)
	for resolver.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancelFirst()

	close(resolver.release)
	<-done

	// Whatever the abandoned caller was told, the answer reached the cache.
	if _, cached := h.hls.get("key"); !cached {
		t.Fatal("cancelling the request that started the resolve threw its answer away")
	}
}
