package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
)

// learningCatalog knows no channel until one is upserted.
type learningCatalog struct {
	catalogv1connect.CatalogServiceClient
	mu       sync.Mutex
	channels map[string]*catalogv1.Channel
}

func (c *learningCatalog) GetChannel(
	_ context.Context, req *connect.Request[catalogv1.GetChannelRequest],
) (*connect.Response[catalogv1.GetChannelResponse], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.channels[req.Msg.GetChannelId()]; ok {
		return connect.NewResponse(&catalogv1.GetChannelResponse{Channel: ch}), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("channel not found"))
}

func (c *learningCatalog) UpsertChannel(
	_ context.Context, req *connect.Request[catalogv1.UpsertChannelRequest],
) (*connect.Response[catalogv1.UpsertChannelResponse], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channels[req.Msg.GetChannel().GetId()] = req.Msg.GetChannel()
	return connect.NewResponse(&catalogv1.UpsertChannelResponse{}), nil
}

type resolvingIngest struct {
	ingestv1connect.IngestServiceClient
	mu    sync.Mutex
	asked []string
	delay time.Duration
}

func (i *resolvingIngest) ResolveChannel(
	_ context.Context, req *connect.Request[ingestv1.ResolveChannelRequest],
) (*connect.Response[ingestv1.ResolveChannelResponse], error) {
	i.mu.Lock()
	i.asked = append(i.asked, req.Msg.GetChannel())
	i.mu.Unlock()
	time.Sleep(i.delay)
	if req.Msg.GetChannel() != "UCSvstDQw5-yIHVRzXkOURyg" {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such channel"))
	}
	return connect.NewResponse(&ingestv1.ResolveChannelResponse{
		ChannelId: "UCSvstDQw5-yIHVRzXkOURyg", Name: "Never seen", AvatarUrl: "https://yt3.ggpht.com/a",
	}), nil
}

func getChannel(g *Gateway, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/channels/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	g.handleGetChannel(rec, req)
	return rec
}

// A channel opened from a search result's avatar may be one the library has
// never met. It used to answer 404, and the channel page said "Could not reach
// the library" — reported as "gateway answered 404 for …/api/channels/
// UCSvstDQw5-yIHVRzXkOURyg". It is learned from YouTube and answered.
func TestAChannelTheLibraryHasNeverMetIsLearned(t *testing.T) {
	catalog := &learningCatalog{channels: map[string]*catalogv1.Channel{}}
	ingest := &resolvingIngest{}
	g := &Gateway{logger: discardLogger(), catalog: catalog, ingest: ingest}

	rec := getChannel(g, "UCSvstDQw5-yIHVRzXkOURyg")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	var body struct {
		Channel channelDTO `json:"channel"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	if body.Channel.ID != "UCSvstDQw5-yIHVRzXkOURyg" || body.Channel.Name != "Never seen" {
		t.Errorf("channel = %+v", body.Channel)
	}

	// Learned once: the second visit is the catalog's.
	getChannel(g, "UCSvstDQw5-yIHVRzXkOURyg")
	if len(ingest.asked) != 1 {
		t.Errorf("asked YouTube %d times, want 1", len(ingest.asked))
	}
}

// A channel YouTube does not know either is still a 404.
func TestAChannelNobodyKnowsIsStillNotFound(t *testing.T) {
	g := &Gateway{
		logger:  discardLogger(),
		catalog: &learningCatalog{channels: map[string]*catalogv1.Channel{}},
		ingest:  &resolvingIngest{},
	}

	if rec := getChannel(g, "UCnobody"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func (i *resolvingIngest) ListChannelUploads(
	_ context.Context, req *connect.Request[ingestv1.ListChannelUploadsRequest],
) (*connect.Response[ingestv1.ListChannelUploadsResponse], error) {
	return connect.NewResponse(&ingestv1.ListChannelUploadsResponse{}), nil
}

// The app asks for a channel and its videos together. When the videos came
// first for a channel the library had never met, they answered 404 and the
// page said "This channel isn't on YouTube" — reported for a "guh huub"
// search result's avatar. The listing learns the channel too.
func TestTheVideosOfAChannelTheLibraryHasNeverMetAreListed(t *testing.T) {
	catalog := &learningCatalog{channels: map[string]*catalogv1.Channel{}}
	ingest := &resolvingIngest{}
	g := &Gateway{logger: discardLogger(), catalog: catalog, ingest: ingest}

	req := httptest.NewRequest(http.MethodGet, "/api/channels/UCSvstDQw5-yIHVRzXkOURyg/videos", nil)
	req.SetPathValue("id", "UCSvstDQw5-yIHVRzXkOURyg")
	rec := httptest.NewRecorder()
	g.handleChannelVideos(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	getChannel(g, "UCSvstDQw5-yIHVRzXkOURyg")
	if len(ingest.asked) != 1 {
		t.Errorf("asked YouTube %d times, want 1", len(ingest.asked))
	}
}

// The order the app really sends: both at once, while YouTube is still being
// asked. Both answer, and YouTube is asked once.
func TestAChannelAndItsVideosAskedTogetherLearnOnce(t *testing.T) {
	ingest := &resolvingIngest{delay: 50 * time.Millisecond}
	g := &Gateway{
		logger:  discardLogger(),
		catalog: &learningCatalog{channels: map[string]*catalogv1.Channel{}},
		ingest:  ingest,
	}

	var wg sync.WaitGroup
	var channel, videos *httptest.ResponseRecorder
	wg.Add(2)
	go func() { defer wg.Done(); channel = getChannel(g, "UCSvstDQw5-yIHVRzXkOURyg") }()
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/api/channels/UCSvstDQw5-yIHVRzXkOURyg/videos", nil)
		req.SetPathValue("id", "UCSvstDQw5-yIHVRzXkOURyg")
		videos = httptest.NewRecorder()
		g.handleChannelVideos(videos, req)
	}()
	wg.Wait()

	if channel.Code != http.StatusOK || videos.Code != http.StatusOK {
		t.Fatalf("channel %d, videos %d (%q)", channel.Code, videos.Code, videos.Body.String())
	}
	if len(ingest.asked) != 1 {
		t.Errorf("asked YouTube %d times, want 1", len(ingest.asked))
	}
}
