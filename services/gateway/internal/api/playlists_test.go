package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
)

// recordingPlaylists remembers what reached catalog, which is the whole
// question for these handlers: they carry the member and the ids across and
// nothing else.
type recordingPlaylists struct {
	catalogv1connect.CatalogServiceClient
	askedAbout string
	added      *catalogv1.AddPlaylistItemRequest
	removed    *catalogv1.RemovePlaylistItemRequest
	deleted    *catalogv1.DeletePlaylistRequest
	updated    *catalogv1.UpdatePlaylistRequest
}

func (c *recordingPlaylists) ListPlaylists(
	_ context.Context, req *connect.Request[catalogv1.ListPlaylistsRequest],
) (*connect.Response[catalogv1.ListPlaylistsResponse], error) {
	c.askedAbout = req.Msg.GetVideoId()
	return connect.NewResponse(&catalogv1.ListPlaylistsResponse{
		Playlists: []*catalogv1.Playlist{
			{Id: "pl_music", Title: "Nhạc", ContainsVideo: req.Msg.GetVideoId() == "gEWF0LL4IPA"},
			{Id: "pl_news", Title: "Tin tức"},
		},
	}), nil
}

func (c *recordingPlaylists) AddPlaylistItem(
	_ context.Context, req *connect.Request[catalogv1.AddPlaylistItemRequest],
) (*connect.Response[catalogv1.AddPlaylistItemResponse], error) {
	c.added = req.Msg
	return connect.NewResponse(&catalogv1.AddPlaylistItemResponse{}), nil
}

func (c *recordingPlaylists) RemovePlaylistItem(
	_ context.Context, req *connect.Request[catalogv1.RemovePlaylistItemRequest],
) (*connect.Response[catalogv1.RemovePlaylistItemResponse], error) {
	c.removed = req.Msg
	return connect.NewResponse(&catalogv1.RemovePlaylistItemResponse{}), nil
}

func (c *recordingPlaylists) DeletePlaylist(
	_ context.Context, req *connect.Request[catalogv1.DeletePlaylistRequest],
) (*connect.Response[catalogv1.DeletePlaylistResponse], error) {
	c.deleted = req.Msg
	return connect.NewResponse(&catalogv1.DeletePlaylistResponse{}), nil
}

func (c *recordingPlaylists) UpdatePlaylist(
	_ context.Context, req *connect.Request[catalogv1.UpdatePlaylistRequest],
) (*connect.Response[catalogv1.UpdatePlaylistResponse], error) {
	c.updated = req.Msg
	return connect.NewResponse(&catalogv1.UpdatePlaylistResponse{
		Playlist: &catalogv1.Playlist{Id: req.Msg.GetPlaylistId(), Title: req.Msg.GetTitle()},
	}), nil
}

// The sheet is one request: which lists are there, and which hold this video.
func TestListPlaylistsCarriesTheVideoParameterAndTheFlag(t *testing.T) {
	catalog := &recordingPlaylists{}
	g := &Gateway{logger: discardLogger(), catalog: catalog}

	req := httptest.NewRequest(http.MethodGet, "/api/playlists?videoId=gEWF0LL4IPA", nil)
	req.Header.Set("X-User-Id", "u_lm")
	rec := httptest.NewRecorder()
	g.handleListPlaylists(rec, req)

	if catalog.askedAbout != "gEWF0LL4IPA" {
		t.Fatalf("catalog was asked about %q", catalog.askedAbout)
	}
	var body playlistsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Playlists[0].ContainsVideo || body.Playlists[1].ContainsVideo {
		t.Fatalf("containsVideo = %v, %v", body.Playlists[0].ContainsVideo, body.Playlists[1].ContainsVideo)
	}
}

// Every existing caller asks without the parameter and must be unaffected.
func TestListPlaylistsWithoutAVideoAsksAboutNone(t *testing.T) {
	catalog := &recordingPlaylists{}
	g := &Gateway{logger: discardLogger(), catalog: catalog}

	rec := httptest.NewRecorder()
	g.handleListPlaylists(rec, httptest.NewRequest(http.MethodGet, "/api/playlists", nil))

	if catalog.askedAbout != "" {
		t.Fatalf("catalog was asked about %q with no parameter", catalog.askedAbout)
	}
	var body playlistsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Playlists[0].ContainsVideo {
		t.Fatal("membership was claimed with no video in the question")
	}
}

func TestAddingAnItemCarriesTheMemberAndAnswers204(t *testing.T) {
	catalog := &recordingPlaylists{}
	g := &Gateway{logger: discardLogger(), catalog: catalog}

	req := httptest.NewRequest(http.MethodPost, "/api/playlists/pl_music/items",
		strings.NewReader(`{"videoId":"gEWF0LL4IPA"}`))
	req.Header.Set("X-User-Id", "u_lm")
	req.SetPathValue("id", "pl_music")
	rec := httptest.NewRecorder()
	g.handleAddPlaylistItem(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if catalog.added.GetPlaylistId() != "pl_music" ||
		catalog.added.GetVideoId() != "gEWF0LL4IPA" ||
		catalog.added.GetUserId() != "u_lm" {
		t.Fatalf("catalog got %+v", catalog.added)
	}
}

// The video is in the path here, not in a body: a DELETE with a body is a
// request half the intermediaries in the world feel free to drop.
func TestRemovingAnItemReadsBothPathValues(t *testing.T) {
	catalog := &recordingPlaylists{}
	g := &Gateway{logger: discardLogger(), catalog: catalog}

	req := httptest.NewRequest(http.MethodDelete, "/api/playlists/pl_music/items/gEWF0LL4IPA", nil)
	req.Header.Set("X-User-Id", "u_lm")
	req.SetPathValue("id", "pl_music")
	req.SetPathValue("videoId", "gEWF0LL4IPA")
	rec := httptest.NewRecorder()
	g.handleRemovePlaylistItem(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if catalog.removed.GetVideoId() != "gEWF0LL4IPA" || catalog.removed.GetPlaylistId() != "pl_music" {
		t.Fatalf("catalog got %+v", catalog.removed)
	}
}

func TestUpdateAnswersWithTheUpdatedPlaylist(t *testing.T) {
	catalog := &recordingPlaylists{}
	g := &Gateway{logger: discardLogger(), catalog: catalog}

	req := httptest.NewRequest(http.MethodPatch, "/api/playlists/pl_music",
		strings.NewReader(`{"title":"Nhạc Việt","description":""}`))
	req.Header.Set("X-User-Id", "u_lm")
	req.SetPathValue("id", "pl_music")
	rec := httptest.NewRecorder()
	g.handleUpdatePlaylist(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body playlistDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Title != "Nhạc Việt" {
		t.Fatalf("title = %q", body.Title)
	}
}

func TestDeleteCarriesTheMemberAndAnswers204(t *testing.T) {
	catalog := &recordingPlaylists{}
	g := &Gateway{logger: discardLogger(), catalog: catalog}

	req := httptest.NewRequest(http.MethodDelete, "/api/playlists/pl_music", nil)
	req.Header.Set("X-User-Id", "u_lm")
	req.SetPathValue("id", "pl_music")
	rec := httptest.NewRecorder()
	g.handleDeletePlaylist(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if catalog.deleted.GetUserId() != "u_lm" {
		t.Fatalf("catalog got %+v", catalog.deleted)
	}
}
