package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Who lives here.
//
// Not accounts and not logins. §2 is two to five people in one household with
// no public sign-up, and §3 leaves media URLs unprotected because the LAN is
// trusted — a password guarding this would be a stricter trust model than the
// one already protecting the video files themselves.
//
// What the list is for is separation. `catalog.subscriptions`,
// `catalog.reactions`, `catalog.watch_later` and `recsys.signals` are all
// already keyed by a user id, and until now nothing ever sent one: every
// browser in the house fell through to DEV_USER_ID, so a single profile
// accumulated 39,583 watch signals that belonged to whoever happened to be
// holding the remote.
//
// Held as a file beside feed-mix.json and ranking.json rather than in a
// database, for the same reason those are: it is configuration for one
// household, it is worth being able to edit by hand, and it belongs to no
// service's schema.
type profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

const maxProfileNameLen = 40

// The file is small and read on nearly every page load, so it is guarded rather
// than re-read blind — two browsers adding a profile at once would otherwise
// each write a list missing the other's.
var profilesMu sync.Mutex

func (g *Gateway) profilesPath() string {
	return filepath.Join(g.configDir, "profiles.json")
}

// loadProfiles reads the list, falling back to the household's single
// pre-existing user.
//
// The fallback is not cosmetic. Every install that predates this has one user
// id holding all of its history, and returning an empty list would offer a
// picker with nothing in it — the viewer would create a new profile, and their
// two years of watching would stay attached to an id nothing points at any
// more.
func (g *Gateway) loadProfiles() []profile {
	raw, err := os.ReadFile(g.profilesPath())
	if err != nil || len(raw) == 0 {
		return g.defaultProfiles()
	}
	var saved []profile
	if err := json.Unmarshal(raw, &saved); err != nil {
		return g.defaultProfiles()
	}
	out := make([]profile, 0, len(saved))
	for _, p := range saved {
		if p.ID != "" && p.Name != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return g.defaultProfiles()
	}
	return out
}

func (g *Gateway) defaultProfiles() []profile {
	return []profile{{ID: g.devUserID, Name: defaultProfileName(g.devUserID)}}
}

// defaultProfileName turns "u_luc" into "Luc" so the first picker is not a row
// of database identifiers.
func defaultProfileName(id string) string {
	name := strings.TrimPrefix(id, "u_")
	if name == "" {
		return id
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func (g *Gateway) saveProfiles(list []profile) error {
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(g.configDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(g.profilesPath(), raw, 0o644)
}

// newProfileID builds an id from the name, which is what makes it readable in
// the database afterwards: `u_vo` rather than `u_7f3a`. Collisions get a
// suffix, because two people called the same thing is a household problem and
// not an error.
func newProfileID(name string, existing []profile) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return -1
		}
	}, name)
	if slug == "" {
		slug = fmt.Sprintf("%d", time.Now().Unix())
	}
	if len(slug) > 16 {
		slug = slug[:16]
	}

	taken := map[string]bool{}
	for _, p := range existing {
		taken[p.ID] = true
	}
	id := "u_" + slug
	for n := 2; taken[id]; n++ {
		id = fmt.Sprintf("u_%s%d", slug, n)
	}
	return id
}

func (g *Gateway) handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profilesMu.Lock()
		list := g.loadProfiles()
		profilesMu.Unlock()
		sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"profiles": list})

	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" || len([]rune(name)) > maxProfileNameLen {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid name"})
			return
		}

		profilesMu.Lock()
		defer profilesMu.Unlock()
		list := g.loadProfiles()
		for _, p := range list {
			if strings.EqualFold(p.Name, name) {
				// Already there. Returning it rather than erroring means a
				// second person typing the same name lands on the profile they
				// meant instead of being told off.
				writeJSON(w, http.StatusOK, map[string]any{"profile": p})
				return
			}
		}
		created := profile{ID: newProfileID(name, list), Name: name}
		if err := g.saveProfiles(append(list, created)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not save"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": created})

	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
