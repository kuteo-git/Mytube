package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
)

// A household member's YouTube session, over REST.
//
// The gateway is the only thing here that speaks REST outward (§3), so this
// proxies to ingest — which owns the files, because ingest is the only thing
// that runs yt-dlp and therefore the only thing that has any business holding a
// session.
//
// Write-only in the direction that matters: a session can be pasted and it can
// be deleted, and nothing returns its content. There is no read path to leak,
// and nothing to accidentally log.

type accountView struct {
	UserID     string `json:"userId"`
	Label      string `json:"label"`
	State      string `json:"state"`
	LastResult string `json:"lastResult,omitempty"`
	LastScanAt string `json:"lastScanAt,omitempty"`
}

// requireSecureTransport refuses a paste that arrived in the clear.
//
// §3 leaves media URLs unprotected because the LAN is trusted, and this is the
// one place that reasoning does not carry. "Trusted with a film" and "trusted
// with a live Google session" are different sentences: anyone who reads this
// body is signed in as that person, everywhere, not merely on YouTube.
//
// Localhost is allowed through, because there is no network to listen on — and
// because refusing it would make the whole feature untestable on the machine it
// runs on.
func requireSecureTransport(w http.ResponseWriter, r *http.Request) bool {
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" || isLoopback(r) {
		return true
	}
	writeJSON(w, http.StatusUpgradeRequired, map[string]any{
		"error": "cookies may only be sent over HTTPS",
	})
	return false
}

func isLoopback(r *http.Request) bool {
	// Host carries a port, so this is a prefix test over the three names a
	// loopback request can arrive under.
	for _, name := range []string{"localhost", "127.0.0.1", "[::1]"} {
		if strings.HasPrefix(r.Host, name) {
			return true
		}
	}
	return false
}

func (g *Gateway) handleYouTubeAccount(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.getYouTubeAccount(w, r)
	case http.MethodPut:
		g.putYouTubeAccount(w, r)
	case http.MethodDelete:
		g.deleteYouTubeAccount(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// getYouTubeAccount reports this viewer's account, and only theirs.
//
// One person's session state is not another's business, and the list of who has
// pasted what is exactly the sort of thing that ends up on a screen somebody
// else is looking at.
func (g *Gateway) getYouTubeAccount(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()

	userID := g.userID(r)
	resp, err := g.ingest.ListAccounts(ctx, connect.NewRequest(&ingestv1.ListAccountsRequest{}))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"account": accountView{UserID: userID, State: "NEVER_SET"},
		})
		return
	}
	for _, a := range resp.Msg.GetAccounts() {
		if a.GetUserId() != userID {
			continue
		}
		view := accountView{
			UserID:     a.GetUserId(),
			Label:      a.GetLabel(),
			State:      a.GetState(),
			LastResult: a.GetLastResult(),
		}
		if ts := a.GetLastScanAt(); ts != nil {
			view.LastScanAt = ts.AsTime().Format(time.RFC3339)
		}
		writeJSON(w, http.StatusOK, map[string]any{"account": view})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account": accountView{UserID: userID, State: "NEVER_SET"},
	})
}

func (g *Gateway) putYouTubeAccount(w http.ResponseWriter, r *http.Request) {
	if !requireSecureTransport(w, r) {
		return
	}
	var body struct {
		Label   string `json:"label"`
		Cookies string `json:"cookies"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}

	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()

	_, err := g.ingest.SetAccountCookies(ctx, connect.NewRequest(&ingestv1.SetAccountCookiesRequest{
		UserId:  g.userID(r),
		Label:   body.Label,
		Cookies: body.Cookies,
	}))
	if err != nil {
		// The message from validation is the useful part — it says which of the
		// several ways a paste can be wrong this one was — and it is built from
		// the *shape* of the file rather than from its contents.
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": connectMessage(err)})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) deleteYouTubeAccount(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()

	if _, err := g.ingest.RemoveAccount(ctx, connect.NewRequest(&ingestv1.RemoveAccountRequest{
		UserId: g.userID(r),
	})); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not remove"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleScanAccounts starts a pass and returns at once.
//
// It used to wait for the whole thing, which was fine when a pass was five
// requests and is not now: a first fill reads every playlist a member has, three
// seconds apart. The browser polls handleAccountScanStatus instead, and a page
// reloaded mid-pass picks the progress straight back up.
//
// Scans the caller's own account. The hourly timer inside ingest is what scans
// the whole household; this button sits on a screen about *your* account.
func (g *Gateway) handleScanAccounts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(30 * time.Second)
	defer cancel()

	if _, err := g.ingest.ScanAccounts(ctx, connect.NewRequest(&ingestv1.ScanAccountsRequest{
		UserId: g.userID(r),
	})); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": connectMessage(err)})
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleAccountScanStatus is what the settings screen polls while a pass runs.
func (g *Gateway) handleAccountScanStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()

	resp, err := g.ingest.GetAccountScanStatus(ctx,
		connect.NewRequest(&ingestv1.GetAccountScanStatusRequest{}))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": connectMessage(err)})
		return
	}
	st := resp.Msg.GetStatus()

	errs := st.GetErrors()
	if errs == nil {
		errs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running":        st.GetRunning(),
		"durationMs":     st.GetDurationMs(),
		"phase":          st.GetPhase(),
		"playlistsRead":  st.GetPlaylistsRead(),
		"playlistsTotal": st.GetPlaylistsTotal(),
		"accounts":       st.GetAccounts(),
		"subscriptions":  st.GetSubscriptions(),
		"videos":         st.GetVideos(),
		"expired":        st.GetExpired(),
		"playlists":      st.GetPlaylists(),
		"playlistVideos": st.GetPlaylistVideos(),
		"errors":         errs,
	})
}

func connectMessage(err error) string {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr.Message()
	}
	return "could not save"
}
