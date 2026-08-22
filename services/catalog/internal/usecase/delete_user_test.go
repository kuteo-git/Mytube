package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

type deletingRepo struct {
	domain.Repository
	gotUser string
	gotDry  bool
	calls   int
	counts  domain.UserDataCounts
	err     error
}

func (r *deletingRepo) DeleteUserData(
	_ context.Context, userID string, dryRun bool,
) (domain.UserDataCounts, error) {
	r.gotUser, r.gotDry, r.calls = userID, dryRun, r.calls+1
	return r.counts, r.err
}

// Deleting everything belonging to nobody would be deleting everything
// belonging to everybody.
//
// The empty string is not a member: `DEV_USER_ID` is what a request with no
// header falls back to (§6b), so a blank id reaching the repository would take
// out the rows of every browser that has never chosen a profile — which, before
// the picker existed, was all of them.
func TestDeletingWithoutAMemberIsRefused(t *testing.T) {
	repo := &deletingRepo{}
	c := &Catalog{repo: repo}

	_, err := c.DeleteUserData(context.Background(), "", false)
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
	if repo.calls != 0 {
		t.Error("it reached the repository anyway")
	}
}

// A dry run is the same question asked without the consequence.
//
// One code path for both, because the dialog shows what the dry run counted and
// then deletes — and two paths would be two definitions of "what belongs to
// this profile", agreeing until the day they do not.
func TestADryRunCountsAndChangesNothing(t *testing.T) {
	repo := &deletingRepo{counts: domain.UserDataCounts{
		Subscriptions: 351,
		WatchProgress: 889,
		Playlists:     27,
	}}
	c := &Catalog{repo: repo}

	got, err := c.DeleteUserData(context.Background(), "u_luc", true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !repo.gotDry {
		t.Error("the repository was not told this was a dry run")
	}
	if got.Subscriptions != 351 || got.WatchProgress != 889 || got.Playlists != 27 {
		t.Errorf("counts came back as %+v", got)
	}
}

func TestDeletingCarriesTheMemberThrough(t *testing.T) {
	repo := &deletingRepo{}
	c := &Catalog{repo: repo}

	if _, err := c.DeleteUserData(context.Background(), "u_tunkhanh", false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if repo.gotUser != "u_tunkhanh" {
		t.Errorf("user reached the repository as %q", repo.gotUser)
	}
	if repo.gotDry {
		t.Error("a real delete was passed as a dry run")
	}
}
