package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

// playlistRepo models the two things the SQL guarantees and the usecase relies
// on: a playlist belongs to exactly one member, and an item is a set rather
// than a list — adding what is there changes nothing.
type playlistRepo struct {
	domain.Repository
	owner map[string]string   // playlist id -> user id
	items map[string][]string // playlist id -> video ids, in order
	asked string              // the video ListPlaylists was asked about
}

func newPlaylistRepo() *playlistRepo {
	return &playlistRepo{
		owner: map[string]string{"pl_music": "u_lm"},
		items: map[string][]string{},
	}
}

func (r *playlistRepo) own(playlistID, userID string) error {
	if r.owner[playlistID] != userID {
		return domain.ErrNotFound
	}
	return nil
}

func (r *playlistRepo) AddPlaylistItem(_ context.Context, playlistID, userID, videoID string) error {
	if err := r.own(playlistID, userID); err != nil {
		return err
	}
	for _, v := range r.items[playlistID] {
		if v == videoID {
			return nil
		}
	}
	r.items[playlistID] = append(r.items[playlistID], videoID)
	return nil
}

func (r *playlistRepo) RemovePlaylistItem(_ context.Context, playlistID, userID, videoID string) error {
	if err := r.own(playlistID, userID); err != nil {
		return err
	}
	kept := r.items[playlistID][:0]
	for _, v := range r.items[playlistID] {
		if v != videoID {
			kept = append(kept, v)
		}
	}
	r.items[playlistID] = kept
	return nil
}

func (r *playlistRepo) DeletePlaylist(_ context.Context, playlistID, userID string) error {
	if err := r.own(playlistID, userID); err != nil {
		return err
	}
	delete(r.owner, playlistID)
	delete(r.items, playlistID) // ON DELETE CASCADE
	return nil
}

func (r *playlistRepo) UpdatePlaylist(_ context.Context, playlistID, userID, title, description string) (domain.Playlist, error) {
	if err := r.own(playlistID, userID); err != nil {
		return domain.Playlist{}, err
	}
	return domain.Playlist{ID: playlistID, UserID: userID, Title: title, Description: description}, nil
}

func (r *playlistRepo) ListPlaylists(_ context.Context, userID, videoID string) ([]domain.Playlist, error) {
	r.asked = videoID
	out := []domain.Playlist{}
	for id, owner := range r.owner {
		if owner != userID {
			continue
		}
		p := domain.Playlist{ID: id, UserID: owner}
		for _, v := range r.items[id] {
			if videoID != "" && v == videoID {
				p.ContainsVideo = true
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// The sheet may be saved twice — a second Save on an unchanged tick, a retry
// after a slow answer — and a duplicate must not read as a failure.
func TestAddingTheSameVideoTwiceIsOneRow(t *testing.T) {
	repo := newPlaylistRepo()
	c := &Catalog{repo: repo}
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := c.AddPlaylistItem(ctx, "pl_music", "u_lm", "v1"); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if got := repo.items["pl_music"]; len(got) != 1 {
		t.Errorf("items = %v, want exactly one", got)
	}
}

// A playlist belongs to a member: the id alone must never be enough to reach
// one, and somebody else's must be indistinguishable from one that is not there.
func TestAddingToSomebodyElsesPlaylistIsNotFound(t *testing.T) {
	repo := newPlaylistRepo()
	c := &Catalog{repo: repo}

	err := c.AddPlaylistItem(context.Background(), "pl_music", "u_other", "v1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if len(repo.items["pl_music"]) != 0 {
		t.Error("it wrote anyway")
	}
}

func TestRemovingSomethingAbsentSucceeds(t *testing.T) {
	c := &Catalog{repo: newPlaylistRepo()}
	if err := c.RemovePlaylistItem(context.Background(), "pl_music", "u_lm", "v_never"); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestDeletingAPlaylistTakesItsItems(t *testing.T) {
	repo := newPlaylistRepo()
	c := &Catalog{repo: repo}
	ctx := context.Background()
	_ = c.AddPlaylistItem(ctx, "pl_music", "u_lm", "v1")

	if err := c.DeletePlaylist(ctx, "pl_music", "u_lm"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := repo.items["pl_music"]; ok {
		t.Error("the items outlived the playlist")
	}
}

// A list with no name cannot be told from another on the playlists page — the
// rule CreatePlaylist already states, and renaming to nothing is the same fault.
func TestRenamingToNothingIsRefused(t *testing.T) {
	repo := newPlaylistRepo()
	c := &Catalog{repo: repo}

	_, err := c.UpdatePlaylist(context.Background(), "pl_music", "u_lm", "   ", "")
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
}

// The sheet asks "which lists are there, and which hold this video" in one
// call; the video it asks about has to reach the repository to be answered.
func TestListPlaylistsCarriesTheVideoAskedAbout(t *testing.T) {
	repo := newPlaylistRepo()
	c := &Catalog{repo: repo}
	ctx := context.Background()
	_ = c.AddPlaylistItem(ctx, "pl_music", "u_lm", "v1")

	ps, err := c.ListPlaylists(ctx, "u_lm", "v1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if repo.asked != "v1" {
		t.Errorf("repository asked about %q", repo.asked)
	}
	if len(ps) != 1 || !ps[0].ContainsVideo {
		t.Errorf("playlists = %+v, want one holding the video", ps)
	}
}

// Without the parameter the flag is meaningless, so it must be false rather
// than whatever the last question left behind.
func TestListPlaylistsWithoutAVideoSaysNothingAboutMembership(t *testing.T) {
	repo := newPlaylistRepo()
	c := &Catalog{repo: repo}
	ctx := context.Background()
	_ = c.AddPlaylistItem(ctx, "pl_music", "u_lm", "v1")

	ps, err := c.ListPlaylists(ctx, "u_lm", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if ps[0].ContainsVideo {
		t.Error("containsVideo is set with no video in the question")
	}
}
