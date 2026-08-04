package api

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	// The lock spans the read as well as the write. This is a read-modify-write
	// of one file and the pass fires saves off after every batch without
	// waiting, so two of them overlap routinely; unserialised, the second reads
	// before the first has written and drops that batch on the floor — silently,
	// since the pass has already counted those lines as saved.
	return withFileLock(path, func() error {
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
		return writeFileAtomic(path, blob)
	})
}

// narrationTTSDir holds the synthesised clips for one video's narration.
//
// TTS was never cached anywhere: every clip was re-synthesised and re-stretched
// on every viewing, and clips at a speed other than 1.0 — which is all of them,
// since narration runs at 1.1 — were even sent with `Cache-Control: no-cache`,
// so the browser could not keep them either.
const narrationTTSDir = "narration-tts"

// ttsKey identifies a clip by everything that determines its bytes: the words,
// the tempo they were stretched to, and the voice that read them.
//
// The voice was missing until settings made it selectable. Ten voices are on
// offer, and without it in the key, choosing a different one kept serving
// whichever had been synthesised first — from disk, with nothing anywhere to
// say the setting had been ignored.
func ttsKey(text string, speed float64, voice string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s@@%.2f@@%s", text, speed, voice)))
	return hex.EncodeToString(sum[:])
}

func ttsCachePath(root, videoID, text string, speed float64, voice string) (string, error) {
	dir, err := safeVideoDir(root, videoID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, narrationTTSDir, ttsKey(text, speed, voice)+".wav"), nil
}

func readTTSCache(root, videoID, text string, speed float64, voice string) ([]byte, bool) {
	if videoID == "" {
		return nil, false
	}
	path, err := ttsCachePath(root, videoID, text, speed, voice)
	if err != nil {
		return nil, false
	}
	blob, err := os.ReadFile(path)
	if err != nil || len(blob) == 0 {
		return nil, false
	}
	return blob, true
}

func writeTTSCache(root, videoID, text string, speed float64, voice string, wav []byte) error {
	// A caller that did not say which video this belongs to has nowhere to put
	// it. Not an error — synthesis still worked, it just cannot be kept.
	if videoID == "" || len(wav) == 0 {
		return nil
	}
	path, err := ttsCachePath(root, videoID, text, speed, voice)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// A half-written WAV that another request reads as a hit would be worse
	// than no cache at all.
	return writeFileAtomic(path, wav)
}

// narrationCuesFile is the cue list exactly as the browser grouped it.
//
// Written for inspection and for anything server-side that needs the same cues
// without re-implementing the parser — narration-vtt.ts runs unmodified under
// `node --experimental-strip-types`, but a file on disk is cheaper still.
//
// Deliberately not read back by the player. The grouping rules have been
// retuned a dozen times over this project's life, and a client that trusted a
// stored copy would keep speaking last month's cues until someone thought to
// delete the file.
const narrationCuesFile = "narration-cues.json"

func (g *Gateway) handlePutNarrationCues(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil || len(body) == 0 {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	dir, err := safeVideoDir(g.mediaRoot, r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		g.logger.Warn("narration cues mkdir", "error", err)
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	path := filepath.Join(dir, narrationCuesFile)
	if err := withFileLock(path, func() error { return writeFileAtomic(path, body) }); err != nil {
		g.logger.Warn("narration cues write", "error", err)
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"bytes": len(body)})
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

// machineVTTSuffix marks the track as machine translation.
//
// Not plain ".vi.vtt": subtitleLabel() in the ingest service maps "vi" to
// "Tiếng Việt", so a video that already carries a human Vietnamese track would
// end up with two files claiming the same language, and one would quietly win.
// "vi-mt" shows as VI-MT and is unmistakable.
const machineVTTSuffix = ".vi-mt.vtt"

// machineVTTLanguage is the BCP-47 tag the track is offered under. "vi-x-mt"
// uses the private-use subtag, so anything reading it knows this is Vietnamese
// without mistaking it for the human-written track a video may also carry.
const machineVTTLanguage = "vi-x-mt"

// machineVTTLabel is what the menu shows. Content, not code — this one string is
// read by the viewer in the language it is written in.
const machineVTTLabel = "Tiếng Việt (dịch máy)"

// narrationVTTName picks the filename the track should take.
//
// The base is copied from a subtitle already in the folder ("1080p.mp4.en.vtt"
// -> "1080p.mp4") so the machine track sits alongside its siblings under the
// same media name, which is the convention collectSubtitles reads.
func narrationVTTName(dir string) string {
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".vtt") || strings.HasSuffix(n, machineVTTSuffix) {
				continue
			}
			// "1080p.mp4.en.vtt" -> "1080p.mp4"
			trimmed := strings.TrimSuffix(n, ".vtt")
			if i := strings.LastIndex(trimmed, "."); i > 0 {
				return trimmed[:i] + machineVTTSuffix
			}
		}
	}
	// Nothing to copy a name from — a video narrated from cues that arrived
	// some other way still deserves its file.
	return "narration" + machineVTTSuffix
}

func (g *Gateway) handlePutNarrationVTT(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil || len(body) == 0 {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	dir, err := safeVideoDir(g.mediaRoot, r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		g.logger.Warn("narration vtt mkdir", "error", err)
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	name := narrationVTTName(dir)
	path := filepath.Join(dir, name)
	// One file per video, rewritten after every batch — the writes that
	// overlapped the most, and the ones that surfaced as a 503.
	if err := withFileLock(path, func() error { return writeFileAtomic(path, body) }); err != nil {
		g.logger.Warn("narration vtt write", "error", err)
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	g.logger.Info("narration vtt", "file", name, "bytes", len(body))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"file": name, "bytes": len(body)})
}

// handleDeleteNarrationVTT removes the machine-translated track.
//
// Called when a human Vietnamese track turns up for a video the translator had
// already started on — captions are published a moment after the page opens, so
// a pass can be under way before the list it should have consulted exists. The
// translation is then both redundant and a second "Tiếng Việt" in the menu.
//
// The translation *cache* is deliberately left alone. It has already been paid
// for, nothing reads it while a real track exists, and deleting it only means
// paying again if the viewer ever asks for a translation by hand.
func (g *Gateway) handleDeleteNarrationVTT(w http.ResponseWriter, r *http.Request) {
	dir, err := safeVideoDir(g.mediaRoot, r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No folder is the state being asked for, so it is not a failure.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, machineVTTSuffix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			g.logger.Warn("narration vtt delete", "error", err)
			http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
			return
		}
		g.logger.Info("narration vtt removed", "file", name)
	}
	w.WriteHeader(http.StatusNoContent)
}

// attachMachineTranslation adds the generated Vietnamese track to a video's
// subtitle list when one has been written.
//
// The catalog cannot know about it: collectSubtitles runs once, when the video
// is downloaded, and this file appears later — the first time somebody narrates
// it. Without this the translation was a mode of the player rather than a
// subtitle, which meant it could only be shown by drawing over the picture
// ourselves, and only while the machinery that produced it was switched on.
// As a track it is just another language, drawn by the browser like the rest.
func (g *Gateway) attachMachineTranslation(v *videoDTO) {
	if v == nil || v.ID == "" || g.mediaRoot == "" {
		return
	}
	// Matched on the file, not on the language tag. Once the translation has been
	// on disk long enough for the catalog's own scan to see it, the catalog lists
	// it too — under "vi-mt", read straight off the filename. A tag comparison
	// misses that and the menu ends up offering the same file twice under two
	// names. Adopting the entry instead gives it the label and the tag it should
	// have had, and there is still only one of it.
	for i := range v.Subtitles {
		if strings.HasSuffix(v.Subtitles[i].URL, machineVTTSuffix) {
			v.Subtitles[i].Language = machineVTTLanguage
			v.Subtitles[i].Label = machineVTTLabel
			v.Subtitles[i].Generated = true
			return
		}
	}
	dir, err := safeVideoDir(g.mediaRoot, v.ID)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, machineVTTSuffix) {
			continue
		}
		v.Subtitles = append(v.Subtitles, subtitleDTO{
			Language: machineVTTLanguage,
			Label:    machineVTTLabel,
			URL:      "/media/" + v.ID + "/" + name,
			// Generated in the sense the player already means it: nobody wrote
			// these by hand.
			Generated: true,
		})
		return
	}
}
