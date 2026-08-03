package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// narrationCacheFile is the per-video store of machine translations, laid down
// beside the media it belongs to. Partitioned by engine because both engines
// stay live for comparison, and one shared key space would let the second
// engine overwrite the first's answer for the same sentence.
const narrationCacheFile = "narration.vi.json"

var errBadVideoID = errors.New("bad video id")

// safeVideoDir rejects anything that could climb out of MEDIA_ROOT. Video ids
// arrive from the URL path, so they are untrusted input.
func safeVideoDir(root, videoID string) (string, error) {
	if videoID == "" || strings.ContainsAny(videoID, `/\`) || strings.Contains(videoID, "..") {
		return "", errBadVideoID
	}
	return filepath.Join(root, videoID), nil
}

func readNarrationCache(root, videoID, engine string) (map[string]string, error) {
	dir, err := safeVideoDir(root, videoID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, narrationCacheFile))
	if err != nil {
		// A cold cache is the ordinary state of a video nobody has narrated.
		return map[string]string{}, nil
	}
	var all map[string]map[string]string
	if err := json.Unmarshal(raw, &all); err != nil {
		// A corrupt file is worth re-translating over, not worth failing on.
		return map[string]string{}, nil
	}
	if part, ok := all[engine]; ok {
		return part, nil
	}
	return map[string]string{}, nil
}

func writeNarrationCache(root, videoID, engine string, entries map[string]string) error {
	dir, err := safeVideoDir(root, videoID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, narrationCacheFile)

	all := map[string]map[string]string{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &all)
	}
	if all[engine] == nil {
		all[engine] = map[string]string{}
	}
	for k, v := range entries {
		all[engine][k] = v
	}

	blob, err := json.Marshal(all)
	if err != nil {
		return err
	}
	// Write-then-rename so a reader never sees half a file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (g *Gateway) handleGetNarrationCache(w http.ResponseWriter, r *http.Request) {
	engine := r.URL.Query().Get("engine")
	entries, err := readNarrationCache(g.mediaRoot, r.PathValue("id"), engine)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
}

func (g *Gateway) handlePutNarrationCache(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Engine  string            `json:"engine"`
		Entries map[string]string `json:"entries"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if body.Engine == "" || len(body.Entries) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"written": 0})
		return
	}
	if err := writeNarrationCache(g.mediaRoot, r.PathValue("id"), body.Engine, body.Entries); err != nil {
		// MEDIA_ROOT can be an unmounted external SSD (CLAUDE.md §8.1). Losing
		// the cache is survivable; failing the request is not worth it.
		g.logger.Warn("narration cache write", "error", err)
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"written": len(body.Entries)})
}
