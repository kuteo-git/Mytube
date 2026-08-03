package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// cueSettings matches the trailing settings on a WebVTT timing line, e.g.
// "00:00:02.720 --> 00:00:05.829 align:start position:0%".
var cueSettings = regexp.MustCompile(`\s+(align|position|line|size|vertical):\S+`)

// timingLine matches a cue's timing line and nothing else.
//
// Not "contains -->": subtitle text may legitimately contain an arrow, and a
// line of dialogue is not a place to go deleting words that happen to look like
// cue settings.
var timingLine = regexp.MustCompile(`^\s*(\d+:)?\d{2}:\d{2}[.,]\d{3}\s+-->`)

// stripCuePositioning removes per-cue placement from a WebVTT file.
//
// YouTube's auto-captions carry `align:start position:0%` on every cue, which
// pins them to the bottom-left. The machine translation this player generates
// carries no settings at all, so it centres — and a viewer switching between
// the two watched the text jump across the picture.
//
// CSS cannot fix it: ::cue styles a cue's text, but placement comes from the
// cue's own settings and wins. The only reliable way to make both agree is to
// take the settings out of the file. Measured on one real subtitle file: 463
// cues carried them.
func stripCuePositioning(vtt []byte) []byte {
	lines := strings.Split(string(vtt), "\n")
	for i, line := range lines {
		if timingLine.MatchString(line) {
			lines[i] = cueSettings.ReplaceAllString(line, "")
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

// MediaHandler serves MEDIA_ROOT, rewriting subtitles on the way out.
//
// Everything but *.vtt is handed to the file server untouched: video files are
// large, support range requests, and must not be buffered into memory here.
func MediaHandler(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ".vtt") {
			files.ServeHTTP(w, r)
			return
		}
		clean := filepath.Clean("/" + r.URL.Path)
		blob, err := os.ReadFile(filepath.Join(root, clean))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		_, _ = w.Write(stripCuePositioning(blob))
	})
}
