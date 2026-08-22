package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
	recsysv1 "github.com/lucnguyen/local-youtube/gen/go/recsys/v1"
	"github.com/lucnguyen/local-youtube/gen/go/recsys/v1/recsysv1connect"
)

// Records which legs ran, and in what order.
type deleteRecorder struct {
	catalogv1connect.CatalogServiceClient
	recsysv1connect.RecommendationServiceClient
	ingestv1connect.IngestServiceClient

	order    []string
	catalogErr error
}

func (d *deleteRecorder) DeleteUserData(
	_ context.Context, req *connect.Request[catalogv1.DeleteUserDataRequest],
) (*connect.Response[catalogv1.DeleteUserDataResponse], error) {
	if req.Msg.GetDryRun() {
		d.order = append(d.order, "catalog:count")
	} else {
		d.order = append(d.order, "catalog")
	}
	if d.catalogErr != nil {
		return nil, d.catalogErr
	}
	return connect.NewResponse(&catalogv1.DeleteUserDataResponse{Subscriptions: 8}), nil
}

type recsysRecorder struct {
	recsysv1connect.RecommendationServiceClient
	rec *deleteRecorder
}

func (r recsysRecorder) DeleteUserData(
	_ context.Context, _ *connect.Request[recsysv1.DeleteUserDataRequest],
) (*connect.Response[recsysv1.DeleteUserDataResponse], error) {
	r.rec.order = append(r.rec.order, "recsys")
	return connect.NewResponse(&recsysv1.DeleteUserDataResponse{Signals: 72}), nil
}

type ingestRecorder struct {
	ingestv1connect.IngestServiceClient
	rec *deleteRecorder
}

func (i ingestRecorder) RemoveAccount(
	_ context.Context, _ *connect.Request[ingestv1.RemoveAccountRequest],
) (*connect.Response[ingestv1.RemoveAccountResponse], error) {
	i.rec.order = append(i.rec.order, "ingest")
	return connect.NewResponse(&ingestv1.RemoveAccountResponse{}), nil
}

func gatewayForDelete(t *testing.T) (*Gateway, *deleteRecorder) {
	t.Helper()
	rec := &deleteRecorder{}
	g := gatewayWithProfiles(t)
	g.logger = discardLogger()
	g.catalog = rec
	g.recsys = recsysRecorder{rec: rec}
	g.ingest = ingestRecorder{rec: rec}
	return g, rec
}

func deleteProfile(t *testing.T, g *Gateway, id, asUser string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/profiles/"+id, nil)
	req.SetPathValue("id", id)
	if asUser != "" {
		req.Header.Set("X-User-Id", asUser)
	}
	w := httptest.NewRecorder()
	g.handleDeleteProfile(w, req)
	return w
}

// The entry goes last, so pressing the button again finishes what a failed run
// started.
//
// Three services and a file, and nothing spans them. While the profile is still
// in profiles.json it is still in the picker and still deletable; every step is
// idempotent, so a second press is a repair rather than a mess. The other
// ordering is what left `u_test_empty` in recsys.impressions with no entry
// anywhere else.
func TestDeletingAProfileRemovesTheEntryLast(t *testing.T) {
	g, rec := gatewayForDelete(t)
	_, added := addProfile(t, g, "Tuan Khanh")

	if w := deleteProfile(t, g, added.ID, "u_luc"); w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d, body %s", w.Code, w.Body.String())
	}

	want := []string{"catalog", "recsys", "ingest"}
	if len(rec.order) != len(want) {
		t.Fatalf("legs ran: %v, want %v", rec.order, want)
	}
	for i := range want {
		if rec.order[i] != want[i] {
			t.Fatalf("legs ran: %v, want %v", rec.order, want)
		}
	}

	for _, p := range getProfiles(t, g) {
		if p.ID == added.ID {
			t.Error("the profile is still listed")
		}
	}
}

// A leg that fails leaves the profile listed, so the button can be pressed
// again. Reporting success here would strand the rest for ever.
func TestAFailedLegLeavesTheProfileInPlace(t *testing.T) {
	g, rec := gatewayForDelete(t)
	_, added := addProfile(t, g, "Tuan Khanh")
	rec.catalogErr = connect.NewError(connect.CodeUnavailable, context.DeadlineExceeded)

	if w := deleteProfile(t, g, added.ID, "u_luc"); w.Code < 500 {
		t.Fatalf("DELETE = %d, want a failure", w.Code)
	}

	found := false
	for _, p := range getProfiles(t, g) {
		if p.ID == added.ID {
			found = true
		}
	}
	if !found {
		t.Error("the profile was removed even though a leg failed")
	}
}

// Deleting the profile you are currently using would leave the browser holding
// an id for somebody who no longer exists, and every request answering for a
// ghost.
func TestTheProfileAskingCannotDeleteItself(t *testing.T) {
	g, rec := gatewayForDelete(t)
	_, added := addProfile(t, g, "Tuan Khanh")

	w := deleteProfile(t, g, added.ID, added.ID)
	if w.Code != http.StatusConflict {
		t.Fatalf("DELETE = %d, want 409", w.Code)
	}
	if len(rec.order) != 0 {
		t.Errorf("it deleted anyway: %v", rec.order)
	}
}

// The last one stays. DEV_USER_ID is where every pre-picker browser's history
// lives (§6b), so an empty list is a library nobody owns.
func TestTheLastProfileCannotBeDeleted(t *testing.T) {
	g, rec := gatewayForDelete(t)
	only := getProfiles(t, g)
	if len(only) != 1 {
		t.Fatalf("expected one profile to start with, got %d", len(only))
	}

	w := deleteProfile(t, g, only[0].ID, "somebody-else")
	if w.Code != http.StatusConflict {
		t.Fatalf("DELETE = %d, want 409", w.Code)
	}
	if len(rec.order) != 0 {
		t.Errorf("it deleted anyway: %v", rec.order)
	}
}
