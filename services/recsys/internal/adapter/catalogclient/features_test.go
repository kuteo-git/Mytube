package catalogclient

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
)

// The interface is embedded rather than implemented: every other method would
// be a stub written to be never called, and a nil panic names the mistake
// better than a zero value that quietly answers.
type fakeCatalog struct {
	catalogv1connect.CatalogServiceClient
	calls   atomic.Int32
	release chan struct{}
	id      string
}

func (f *fakeCatalog) ListVideoFeatures(
	_ context.Context, _ *connect.Request[catalogv1.ListVideoFeaturesRequest],
) (*connect.Response[catalogv1.ListVideoFeaturesResponse], error) {
	f.calls.Add(1)
	if f.release != nil {
		<-f.release
	}
	return connect.NewResponse(&catalogv1.ListVideoFeaturesResponse{
		Videos: []*catalogv1.VideoFeatures{{VideoId: f.id}},
	}), nil
}

func newSource(client catalogv1connect.CatalogServiceClient, ttl time.Duration) *FeatureSource {
	return &FeatureSource{client: client, ttl: ttl}
}

// The measured defect: the projection was refreshed in front of the caller, so
// one feed request in every TTL paid for reading the whole library. Measured
// against the running library — warm 0.64s, the request after the TTL expired
// 1.99s, the one after that 0.68s — and a viewer pulling the grid down cannot
// tell a slow server from a broken one.
func TestAStaleProjectionIsServedWhileItRefreshes(t *testing.T) {
	fake := &fakeCatalog{release: make(chan struct{}), id: "first"}
	source := newSource(fake, 20*time.Millisecond)

	// The first caller has nothing stale to be served, so it waits.
	close(fake.release)
	if _, err := source.ListVideoFeatures(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}

	// From here the catalog hangs. A blocking refresh would hang with it.
	fake.release = make(chan struct{})
	fake.id = "second"
	time.Sleep(30 * time.Millisecond)

	done := make(chan []string, 1)
	go func() {
		got, err := source.ListVideoFeatures(context.Background())
		if err != nil {
			done <- nil
			return
		}
		ids := make([]string, 0, len(got))
		for _, f := range got {
			ids = append(ids, f.VideoID)
		}
		done <- ids
	}()

	select {
	case ids := <-done:
		if len(ids) != 1 || ids[0] != "first" {
			t.Fatalf("served %v, want the stale projection [first]", ids)
		}
	case <-time.After(time.Second):
		t.Fatal("the caller waited on the refresh instead of being served what was held")
	}

	close(fake.release)
}

// One refresh, however many requests notice the staleness together.
func TestManyStaleRequestsStartOneRefresh(t *testing.T) {
	fake := &fakeCatalog{release: make(chan struct{}), id: "first"}
	source := newSource(fake, 20*time.Millisecond)

	close(fake.release)
	if _, err := source.ListVideoFeatures(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}
	if fake.calls.Load() != 1 {
		t.Fatalf("the first load made %d calls, want 1", fake.calls.Load())
	}

	fake.release = make(chan struct{})
	time.Sleep(30 * time.Millisecond)

	for i := 0; i < 10; i++ {
		if _, err := source.ListVideoFeatures(context.Background()); err != nil {
			t.Fatalf("stale read %d: %v", i, err)
		}
	}
	// The refresh runs behind the answer, so wait for it to reach the catalog
	// before counting — otherwise this asserts on how fast a goroutine started.
	deadline := time.Now().Add(time.Second)
	for fake.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := fake.calls.Load(); got != 2 {
		t.Fatalf("ten stale reads made %d calls in total, want 2", got)
	}

	close(fake.release)
}

// A refresh that fails leaves what is held in place, and does not wait out
// another TTL before trying again.
func TestARefreshedProjectionReplacesTheStaleOne(t *testing.T) {
	fake := &fakeCatalog{id: "first"}
	source := newSource(fake, 20*time.Millisecond)

	if _, err := source.ListVideoFeatures(context.Background()); err != nil {
		t.Fatalf("first: %v", err)
	}

	fake.id = "second"
	time.Sleep(30 * time.Millisecond)
	if _, err := source.ListVideoFeatures(context.Background()); err != nil {
		t.Fatalf("stale read: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, err := source.ListVideoFeatures(context.Background())
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(got) == 1 && got[0].VideoID == "second" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the background refresh never replaced the projection")
}
