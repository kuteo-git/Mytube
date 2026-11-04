package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
)

type searchingIngest struct {
	ingestv1connect.IngestServiceClient
	videos []*ingestv1.ExternalVideo
}

func (s searchingIngest) Search(
	context.Context, *connect.Request[ingestv1.SearchRequest],
) (*connect.Response[ingestv1.SearchResponse], error) {
	return connect.NewResponse(&ingestv1.SearchResponse{Videos: s.videos}), nil
}

// channelCatalog knows some channels, and one video.
type channelCatalog struct {
	catalogv1connect.CatalogServiceClient
	channels map[string]*catalogv1.Channel
	video    *catalogv1.Video
}

func (c channelCatalog) GetChannel(
	_ context.Context, req *connect.Request[catalogv1.GetChannelRequest],
) (*connect.Response[catalogv1.GetChannelResponse], error) {
	if ch, ok := c.channels[req.Msg.GetChannelId()]; ok {
		return connect.NewResponse(&catalogv1.GetChannelResponse{Channel: ch}), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("no such channel"))
}

func (c channelCatalog) GetVideo(
	_ context.Context, req *connect.Request[catalogv1.GetVideoRequest],
) (*connect.Response[catalogv1.GetVideoResponse], error) {
	if c.video != nil && c.video.GetId() == req.Msg.GetVideoId() {
		return connect.NewResponse(&catalogv1.GetVideoResponse{Video: c.video}), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("no such video"))
}

func discover(t *testing.T, g *Gateway, q string) []externalVideoDTO {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/discover?q="+q, nil)
	rec := httptest.NewRecorder()
	g.handleDiscover(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	var body struct {
		Videos []externalVideoDTO `json:"videos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal %q: %v", rec.Body.String(), err)
	}
	return body.Videos
}

// A search result is drawn like a card on Home, so it carries what that card
// needs: the channel to open from the avatar, the avatar the library already
// holds for it, and the upload date when YouTube gave one.
func TestDiscoverCarriesTheChannelAndTheDate(t *testing.T) {
	published := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	g := &Gateway{
		logger: discardLogger(),
		ingest: searchingIngest{videos: []*ingestv1.ExternalVideo{
			{Id: "a", Title: "A", ChannelId: "UCknown", ChannelName: "Known", PublishedAt: timestamppb.New(published)},
			{Id: "b", Title: "B", ChannelId: "UCnew", ChannelName: "New", PublishedAt: timestamppb.New(time.Time{})},
		}},
		catalog: channelCatalog{channels: map[string]*catalogv1.Channel{
			"UCknown": {Id: "UCknown", Name: "Known", AvatarPath: "channels/UCknown/avatar.jpg"},
		}},
	}

	got := discover(t, g, "nothing")

	if len(got) != 2 {
		t.Fatalf("got %d videos, want 2", len(got))
	}
	if got[0].ChannelID != "UCknown" || got[0].ChannelAvatarPath != "channels/UCknown/avatar.jpg" {
		t.Errorf("known channel = %q / %q", got[0].ChannelID, got[0].ChannelAvatarPath)
	}
	if got[0].PublishedAt != "2026-10-01T08:00:00Z" {
		t.Errorf("publishedAt = %q", got[0].PublishedAt)
	}
	// A channel the library has never seen still opens; it just has no picture.
	if got[1].ChannelID != "UCnew" || got[1].ChannelAvatarPath != "" {
		t.Errorf("unknown channel = %q / %q", got[1].ChannelID, got[1].ChannelAvatarPath)
	}
	if got[1].PublishedAt != "" {
		t.Errorf("an unknown date is sent as %q, want empty", got[1].PublishedAt)
	}
}

// A pasted address of a video already on this disk is that video, not nothing.
// It used to answer empty, because the library half of the search page showed
// it; search no longer has a library half.
func TestDiscoverAPastedAddressOfALibraryVideoAnswersWithIt(t *testing.T) {
	g := &Gateway{
		logger: discardLogger(),
		catalog: channelCatalog{video: &catalogv1.Video{
			Id: "dQw4w9WgXcQ", Title: "Kept",
			Channel:       &catalogv1.Channel{Id: "UCk", Name: "Kept channel", AvatarPath: "channels/UCk/a.jpg"},
			ThumbnailPath: "thumbnails/dQw4w9WgXcQ.jpg",
			SourceUrl:     "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		}},
	}

	got := discover(t, g, "https://www.youtube.com/watch?v=dQw4w9WgXcQ")

	if len(got) != 1 {
		t.Fatalf("got %d videos, want 1", len(got))
	}
	v := got[0]
	if v.ID != "dQw4w9WgXcQ" || !v.InLibrary || v.ChannelID != "UCk" || v.ChannelAvatarPath != "channels/UCk/a.jpg" {
		t.Errorf("got %+v", v)
	}
	if v.ThumbnailURL != "thumbnails/dQw4w9WgXcQ.jpg" {
		t.Errorf("thumbnail = %q", v.ThumbnailURL)
	}
}
