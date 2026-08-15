// Package logline turns a line of a service's log into something a page can
// filter on.
//
// It parses rather than requires, and never refuses. Four Go services write
// slog's text format, two Python servers write logging's default, and both
// occasionally emit something that is neither — a panic trace, a line yt-dlp
// wrote to stderr, the shell's own complaint. Every one of those is worth
// seeing, and a viewer that dropped what it could not parse would hide exactly
// the lines somebody came looking for.
package logline

import (
	"strings"
	"time"
)

// Line is one log line, as much of it as could be understood.
type Line struct {
	// Service is which log file this came from — "gateway", "ingest".
	Service string `json:"service"`
	// At is when it was written. Zero when the line carries no timestamp, in
	// which case the viewer shows it under whatever came before.
	At time.Time `json:"at"`
	// Level is INFO, WARN, ERROR, DEBUG, or "" when the line does not say.
	Level string `json:"level"`
	// Message is the human half: slog's msg, or everything after the level.
	// Empty means the whole line is in Raw and nothing else could be read.
	Message string `json:"message"`
	// Raw is the line exactly as written, always. It is what the page shows;
	// everything above only decides whether it is shown.
	Raw string `json:"raw"`
	// Restart marks the divider dev.sh writes when the stack starts again.
	// Kept as its own flag rather than as a level, because it is not something
	// a service said.
	Restart bool `json:"restart,omitempty"`
}

// The marker scripts/dev.sh appends before starting a service. Logs are opened
// for append so that the lines immediately *before* a restart survive it —
// those being, reliably, the interesting ones — which leaves the reader needing
// to know where one run ends and the next begins.
const RestartMarker = "--- restart "

// Parse reads one line. service names the file it came from.
func Parse(service, raw string) Line {
	line := Line{Service: service, Raw: raw}

	if strings.HasPrefix(raw, RestartMarker) {
		line.Restart = true
		line.Level = "INFO"
		line.At, _ = time.Parse(time.RFC3339, strings.TrimSpace(
			strings.TrimSuffix(strings.TrimPrefix(raw, RestartMarker), " ---")))
		line.Message = strings.TrimSpace(raw)
		return line
	}

	if at, level, msg, ok := parseSlog(raw); ok {
		line.At, line.Level, line.Message = at, level, msg
		return line
	}
	if at, level, msg, ok := parsePython(raw); ok {
		line.At, line.Level, line.Message = at, level, msg
		return line
	}

	// Neither format. A panic, a stack frame, something ffmpeg said. Shown as
	// it is, and given a level only if the word is plainly in it — guessing
	// harder than that would file ordinary lines under ERROR for containing
	// the word.
	line.Level = looseLevel(raw)
	return line
}

// parseSlog reads Go's slog text handler:
//
//	time=2026-08-15T17:57:32.891+07:00 level=INFO msg="gateway listening" addr=:8180
func parseSlog(raw string) (time.Time, string, string, bool) {
	if !strings.HasPrefix(raw, "time=") {
		return time.Time{}, "", "", false
	}
	stamp, rest, ok := strings.Cut(raw[len("time="):], " ")
	if !ok {
		return time.Time{}, "", "", false
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return time.Time{}, "", "", false
	}

	level := ""
	if strings.HasPrefix(rest, "level=") {
		level, rest, _ = strings.Cut(rest[len("level="):], " ")
	}
	// The message only; the attributes stay in Raw, which is what is displayed.
	// Pulling them apart here would mean re-implementing slog's quoting to put
	// them back together again.
	msg := rest
	if after, found := strings.CutPrefix(rest, `msg="`); found {
		if quoted, _, ok := strings.Cut(after, `"`); ok {
			msg = quoted
		}
	} else if after, found := strings.CutPrefix(rest, "msg="); found {
		msg, _, _ = strings.Cut(after, " ")
	}
	return at, strings.ToUpper(level), msg, true
}

// parsePython reads logging's default arrangement, as the translate and speech
// servers write it:
//
//	2026-08-15 16:03:59 - 0.9.4_x - core.handle - INFO - core.handle - message
func parsePython(raw string) (time.Time, string, string, bool) {
	fields := strings.Split(raw, " - ")
	if len(fields) < 2 {
		return time.Time{}, "", "", false
	}
	at, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(fields[0]), time.Local)
	if err != nil {
		return time.Time{}, "", "", false
	}
	// The level is whichever field is a level word. Its position varies with
	// whatever format string the server was configured with, and this only has
	// to find it, not know where it lives.
	level, message := "", strings.TrimSpace(strings.Join(fields[1:], " - "))
	for i, f := range fields {
		if isLevel(strings.TrimSpace(f)) {
			level = strings.ToUpper(strings.TrimSpace(f))
			if i+1 < len(fields) {
				message = strings.TrimSpace(strings.Join(fields[i+1:], " - "))
			}
			break
		}
	}
	return at, level, message, true
}

func isLevel(s string) bool {
	switch strings.ToUpper(s) {
	case "DEBUG", "INFO", "WARN", "WARNING", "ERROR", "FATAL", "CRITICAL":
		return true
	}
	return false
}

// looseLevel guesses only from an unmistakable prefix. A line that merely
// mentions an error — and half the interesting ones do — is not itself one.
func looseLevel(raw string) string {
	trimmed := strings.TrimSpace(raw)
	for _, prefix := range []string{"ERROR", "FATAL", "panic:", "Error", "error:"} {
		if strings.HasPrefix(trimmed, prefix) {
			return "ERROR"
		}
	}
	for _, prefix := range []string{"WARNING", "WARN", "Warning"} {
		if strings.HasPrefix(trimmed, prefix) {
			return "WARN"
		}
	}
	// uvicorn, which the translate server runs under, writes "INFO:     ...".
	// Without this its every line has no level and disappears the moment any
	// level filter is chosen — that server being one of the two this viewer
	// exists to cover.
	for prefix, level := range map[string]string{"INFO:": "INFO", "DEBUG:": "DEBUG"} {
		if strings.HasPrefix(trimmed, prefix) {
			return level
		}
	}
	return ""
}
