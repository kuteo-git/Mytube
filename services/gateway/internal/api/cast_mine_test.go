package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	castv1 "github.com/lucnguyen/local-youtube/gen/go/cast/v1"
)

type houseCast struct{ fakeCast }

func (houseCast) ListSessions(context.Context, *connect.Request[castv1.ListSessionsRequest]) (*connect.Response[castv1.ListSessionsResponse], error) {
	luc, other, fallback := sessionMsg(), sessionMsg(), sessionMsg()
	other.Id, other.StartedBy = "s2", "u_other"
	fallback.Id, fallback.StartedBy = "s3", "u_default"
	return connect.NewResponse(&castv1.ListSessionsResponse{Sessions: []*castv1.Session{luc, other, fallback}}), nil
}

func sessionIDs(t *testing.T, user, path string) []string {
	t.Helper()
	g := &Gateway{logger: discardLogger(), cast: &houseCast{}, devUserID: "u_default"}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if user != "" {
		req.Header.Set("X-User-Id", user)
	}
	rec := httptest.NewRecorder()
	g.Routes().ServeHTTP(rec, req)
	var out []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("status %d, body %q: %v", rec.Code, rec.Body.String(), err)
	}
	ids := make([]string, len(out))
	for i, s := range out {
		ids[i] = s.ID
	}
	return ids
}

// Reported: profile A casts to a speaker, the phone switches to profile B, and
// B's next video went to A's speaker. A phone asks for its own casts only.
func TestAPhoneSeesOnlyTheCastsItsProfileStarted(t *testing.T) {
	if got := sessionIDs(t, "u_luc", "/api/cast/sessions?mine=true"); len(got) != 1 || got[0] != "s1" {
		t.Errorf("u_luc sees %v, want [s1]", got)
	}
	if got := sessionIDs(t, "u_nobody", "/api/cast/sessions?mine=true"); len(got) != 0 {
		t.Errorf("a profile that cast nothing sees %v", got)
	}
	// No X-User-Id is the gateway's default profile, on a fresh install.
	if got := sessionIDs(t, "", "/api/cast/sessions?mine=true"); len(got) != 1 || got[0] != "s3" {
		t.Errorf("an unidentified phone sees %v, want [s3]", got)
	}
}

// The web app's bar shows what plays anywhere in the house, and still does.
func TestWithoutMineEveryCastInTheHouseIsListed(t *testing.T) {
	if got := sessionIDs(t, "u_luc", "/api/cast/sessions"); len(got) != 3 {
		t.Errorf("listed %v, want all three", got)
	}
}
