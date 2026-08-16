package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gatewayWithProfiles(t *testing.T) *Gateway {
	t.Helper()
	return &Gateway{configDir: t.TempDir(), devUserID: "u_luc"}
}

func getProfiles(t *testing.T, g *Gateway) []profile {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handleProfiles(rec, httptest.NewRequest(http.MethodGet, "/api/profiles", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/profiles = %d", rec.Code)
	}
	var body struct {
		Profiles []profile `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Profiles
}

func addProfile(t *testing.T, g *Gateway, name string) (*httptest.ResponseRecorder, profile) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/profiles",
		strings.NewReader(`{"name":`+jsonString(name)+`}`))
	g.handleProfiles(rec, req)
	var body struct {
		Profile profile `json:"profile"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body.Profile
}

func jsonString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// A household that predates profiles must find its own user in the list.
//
// Returning an empty list would offer a picker with nothing in it: the viewer
// would create a profile, and the 39,583 watch signals already recorded against
// DEV_USER_ID would stay attached to an id nothing points at any more.
func TestAnInstallWithNoFileOffersItsExistingUser(t *testing.T) {
	g := gatewayWithProfiles(t)
	list := getProfiles(t, g)

	if len(list) != 1 {
		t.Fatalf("got %d profiles, want the existing user alone", len(list))
	}
	if list[0].ID != "u_luc" {
		t.Errorf("id = %q, want the configured DEV_USER_ID", list[0].ID)
	}
	if list[0].Name != "Luc" {
		t.Errorf("name = %q — the first picker should not be a row of database ids", list[0].Name)
	}
}

func TestAddingSomebodyKeepsEveryoneElse(t *testing.T) {
	g := gatewayWithProfiles(t)
	if rec, created := addProfile(t, g, "Vợ"); rec.Code != http.StatusOK || created.ID == "" {
		t.Fatalf("POST = %d, profile = %+v", rec.Code, created)
	}

	list := getProfiles(t, g)
	if len(list) != 2 {
		t.Fatalf("got %d profiles, want the default plus the new one", len(list))
	}
	names := map[string]bool{}
	for _, p := range list {
		names[p.Name] = true
	}
	if !names["Luc"] || !names["Vợ"] {
		t.Errorf("list lost somebody: %+v", list)
	}
}

// Ids are readable, because they end up in the database next to every
// subscription and watch signal that person makes.
func TestIdsAreDerivedFromTheName(t *testing.T) {
	g := gatewayWithProfiles(t)
	_, created := addProfile(t, g, "Minh")
	if created.ID != "u_minh" {
		t.Errorf("id = %q, want u_minh", created.ID)
	}
}

// Two people with the same name is a household problem, not an error.
func TestASecondPersonWithTheSameNameGetsTheSameProfile(t *testing.T) {
	g := gatewayWithProfiles(t)
	_, first := addProfile(t, g, "Minh")
	rec, second := addProfile(t, g, "minh")

	if rec.Code != http.StatusOK {
		t.Fatalf("POST = %d, want the existing profile back", rec.Code)
	}
	if second.ID != first.ID {
		t.Errorf("created a second profile %q for the same name", second.ID)
	}
	if len(getProfiles(t, g)) != 2 {
		t.Error("the duplicate was added to the list")
	}
}

// A name that cannot be shown is refused before it reaches the file.
func TestAnUnusableNameIsRefused(t *testing.T) {
	g := gatewayWithProfiles(t)
	for _, name := range []string{"", "   ", strings.Repeat("a", 41)} {
		rec, _ := addProfile(t, g, name)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name %q returned %d, want 400", name, rec.Code)
		}
	}
	if len(getProfiles(t, g)) != 1 {
		t.Error("a refused name was written anyway")
	}
}

// A file somebody has been editing by hand must not take the app down.
//
// Same rule as feed-mix.json and ranking.json: this decides who is asking, and
// there is no version of "the app will not load" that beats "the household
// looks the way it did before the file was touched".
func TestAnUnreadableFileFallsBackRatherThanFailing(t *testing.T) {
	g := gatewayWithProfiles(t)
	if err := os.WriteFile(filepath.Join(g.configDir, "profiles.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	list := getProfiles(t, g)
	if len(list) != 1 || list[0].ID != "u_luc" {
		t.Errorf("got %+v, want the fallback", list)
	}
}

// Rows with nothing in them are dropped rather than offered as blank buttons.
func TestEmptyEntriesAreIgnored(t *testing.T) {
	g := gatewayWithProfiles(t)
	raw := `[{"id":"u_a","name":"A"},{"id":"","name":"B"},{"id":"u_c","name":""}]`
	if err := os.WriteFile(filepath.Join(g.configDir, "profiles.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	list := getProfiles(t, g)
	if len(list) != 1 || list[0].ID != "u_a" {
		t.Errorf("got %+v, want only the complete row", list)
	}
}
