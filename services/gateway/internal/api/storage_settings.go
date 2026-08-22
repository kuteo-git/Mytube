package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	ingestv1 "github.com/lucnguyen/local-youtube/gen/go/ingest/v1"
	"github.com/lucnguyen/local-youtube/internal/mediaroot"
)

// Where the library lives, and whether it is kept at all.
//
// Two settings that shared a page and share nothing else. The path is held by
// three processes and takes a restart; caching is one condition read fresh on
// every request. They are written here together because that is where a person
// looks for them, and kept apart everywhere else.

// handleStorageSettings reads and writes both.
//
// The source of the path — file or environment — is part of the answer, not
// decoration. Somebody who cannot tell a saved setting from an inherited
// default cannot tell a working setting from one being ignored, and this is
// precisely the setting where being ignored is the likely failure: dev.sh
// exports MEDIA_ROOT on every run.
func (g *Gateway) handleStorageSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		g.saveStorageSettings(w, r)
		return
	}

	root, source := mediaroot.Resolve(g.configDir, g.mediaRoot)
	writeJSON(w, http.StatusOK, map[string]any{
		"mediaRoot":     root,
		"source":        string(source),
		"cacheDisabled": g.cacheDisabled(),
	})
}

type storageSettingsBody struct {
	MediaRoot *string `json:"mediaRoot,omitempty"`
	// Explicit presence, so a request about caching alone does not read as a
	// request to blank the path.
	CacheDisabled *bool `json:"cacheDisabled,omitempty"`
	// Acknowledges that the videos at the old root will not be moved.
	Confirmed bool `json:"confirmed,omitempty"`
}

func (g *Gateway) saveStorageSettings(w http.ResponseWriter, r *http.Request) {
	var body storageSettingsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid body"})
		return
	}

	if body.CacheDisabled != nil {
		if err := g.setCacheDisabled(*body.CacheDisabled); err != nil {
			g.writeErr(w, r, err)
			return
		}
	}

	if body.MediaRoot != nil {
		root := strings.TrimSpace(*body.MediaRoot)
		if check := inspectRoot(root); !check.OK {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": check.Problem})
			return
		}

		current, _ := mediaroot.Resolve(g.configDir, g.mediaRoot)
		if root != current && !body.Confirmed {
			// The files are not moved, and saying so with the real number is
			// the difference between a warning and a shrug. Counted from the
			// catalog rather than by walking the disk: it knows instantly and
			// is no less right.
			held := g.readyVideoCount(r)
			if held > 0 {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":           "confirmation required",
					"videosAtOldRoot": held,
					"oldRoot":         current,
				})
				return
			}
		}

		if err := mediaroot.Save(g.configDir, root); err != nil {
			g.writeErr(w, r, err)
			return
		}
		g.logger.Info("media root saved", "path", root, "restart_required", true)
	}

	root, source := mediaroot.Resolve(g.configDir, g.mediaRoot)
	writeJSON(w, http.StatusOK, map[string]any{
		"mediaRoot":     root,
		"source":        string(source),
		"cacheDisabled": g.cacheDisabled(),
		// The path is held by three processes; changing it under a running job,
		// a /media response and an eviction sweep is three things losing their
		// footing at once. So it is written now and read at the next start.
		"restartRequired": body.MediaRoot != nil && root != g.mediaRoot,
	})
}

func (g *Gateway) readyVideoCount(r *http.Request) int32 {
	resp, err := g.catalog.GetStorageUsage(r.Context(),
		connect.NewRequest(&catalogv1.GetStorageUsageRequest{}))
	if err != nil {
		return 0
	}
	return resp.Msg.GetVideoCount()
}

// rootCheck is what the Verify button gets back.
type rootCheck struct {
	OK bool `json:"ok"`
	// What is wrong and what to do about it, in one sentence. "Invalid" tells
	// somebody staring at a path nothing they can act on.
	Problem    string `json:"problem,omitempty"`
	FreeBytes  int64  `json:"freeBytes,omitempty"`
	VideoCount int    `json:"videoCount,omitempty"`
}

// handleVerifyStorageRoot answers "can the library live here, and is it already
// here" without changing anything.
//
// It stats and briefly writes to a path this request names, which lets anyone
// on the LAN learn whether a directory exists. That is the trust model §6b
// already states — the LAN is trusted, anything on it can claim to be anyone,
// and media URLs are unprotected — named here rather than left to be noticed.
func (g *Gateway) handleVerifyStorageRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, inspectRoot(strings.TrimSpace(r.URL.Query().Get("path"))))
}

func inspectRoot(path string) rootCheck {
	switch {
	case path == "":
		return rootCheck{Problem: "Enter a folder."}
	case !filepath.IsAbs(path):
		return rootCheck{Problem: "Use a full path, starting from /. Three services read this, and they do not share a working directory."}
	}

	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		// Deliberately not created here. A typo would become an empty library
		// at /Volumes/Data2/Youtub, and the next thing anybody would notice is
		// that everything needs downloading again.
		return rootCheck{Problem: "No such folder. Create it first, or check the disk is mounted."}
	case err != nil:
		return rootCheck{Problem: "Cannot read that folder: " + err.Error()}
	case !info.IsDir():
		return rootCheck{Problem: "That is a file, not a folder."}
	}

	// Writable in the only way worth trusting: by writing.
	probe, err := os.CreateTemp(path, ".write-check-*")
	if err != nil {
		return rootCheck{Problem: "That folder cannot be written to: " + err.Error()}
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)

	check := rootCheck{OK: true, VideoCount: countVideoDirs(path)}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err == nil {
		check.FreeBytes = int64(stat.Bavail) * int64(stat.Bsize)
	}
	return check
}

// countVideoDirs is the number that makes this button worth pressing: pointing
// at a disk used before brings the whole library back, and that should be
// visible before saving rather than after restarting.
//
// One level deep and names only — the media root holds a directory per video —
// so this is a single readdir rather than a walk of tens of thousands of files.
func countVideoDirs(path string) int {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}

// Whether new videos are kept.
//
// Read fresh on every stream request rather than held in memory, which is what
// lets this take effect without a restart — unlike the path, which three
// processes hold from start-up. Cheap: one small file, and the request behind it
// is about to talk to catalog and possibly YouTube.
func (g *Gateway) cacheDisabled() bool {
	raw, err := os.ReadFile(filepath.Join(g.configDir, cacheSettingsFile))
	if err != nil {
		return false
	}
	var s struct {
		CacheDisabled bool `json:"cacheDisabled"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		// A file nobody can read is not an instruction to stop keeping videos.
		// Failing towards the behaviour the household already had is the safer
		// direction: the alternative silently stops filling the disk.
		return false
	}
	return s.CacheDisabled
}

func (g *Gateway) setCacheDisabled(off bool) error {
	raw, err := json.MarshalIndent(struct {
		CacheDisabled bool `json:"cacheDisabled"`
	}{off}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(g.configDir, 0o755); err != nil {
		return err
	}
	g.logger.Info("cache setting saved", "disabled", off)
	return os.WriteFile(filepath.Join(g.configDir, cacheSettingsFile), raw, 0o644)
}

// Its own file rather than a field in storage.json, because the two are read by
// different people at different times: storage.json is read once at start-up by
// three services, this is read per request by one.
const cacheSettingsFile = "cache.json"

// fetchSubtitlesOnly is what pressing play does when nothing is being kept.
//
// Same guard as ensureDownload, and for the same reason: the player re-asks
// /stream every few seconds, and without it a single video would start a
// caption fetch twelve times a minute.
func (g *Gateway) fetchSubtitlesOnly(sourceURL string) {
	if !g.downloadsAsked.claim(sourceURL, time.Now()) {
		return
	}

	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()

	if _, err := g.ingest.FetchSubtitles(ctx, connect.NewRequest(&ingestv1.FetchSubtitlesRequest{
		Url: sourceURL,
	})); err != nil {
		g.logger.Warn("fetch subtitles", "url", sourceURL, "error", err)
	}
}
