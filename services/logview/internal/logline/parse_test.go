package logline

import (
	"strings"
	"testing"
)

func TestParsesGoServices(t *testing.T) {
	line := Parse("gateway",
		`time=2026-08-15T17:57:32.891+07:00 level=WARN msg="instant proxy refused, resolving again" video=abc status=403`)

	if line.Level != "WARN" {
		t.Errorf("level = %q, want WARN", line.Level)
	}
	if line.Message != "instant proxy refused, resolving again" {
		t.Errorf("message = %q", line.Message)
	}
	if line.At.IsZero() {
		t.Error("no timestamp read")
	}
	// The attributes stay in the raw line. Taking them apart here would mean
	// re-implementing slog's quoting in order to put them back together, and
	// the page displays the raw line anyway.
	if !strings.Contains(line.Raw, "status=403") {
		t.Error("the raw line lost its attributes")
	}
}

func TestParsesThePythonServers(t *testing.T) {
	line := Parse("translate",
		"2026-08-15 16:03:59 - 0.9.4_x - core.providers.tts.base - INFO - core.providers.tts.base - TTS OK")

	if line.Level != "INFO" {
		t.Errorf("level = %q, want INFO", line.Level)
	}
	if line.At.IsZero() {
		t.Error("no timestamp read")
	}
	if !strings.Contains(line.Message, "TTS OK") {
		t.Errorf("message = %q", line.Message)
	}
}

// Nothing is ever dropped.
//
// A panic, a line ffmpeg wrote to stderr, the shell's own complaint — none of
// them are slog and every one of them is worth seeing. A viewer that hid what
// it could not parse would hide exactly what somebody came looking for.
func TestKeepsWhatItCannotParse(t *testing.T) {
	for _, raw := range []string{
		"goroutine 1 [running]:",
		"ERROR: unable to download video data: HTTP Error 403: Forbidden",
		"",
	} {
		line := Parse("ingest", raw)
		if line.Raw != raw {
			t.Errorf("Parse(%q).Raw = %q", raw, line.Raw)
		}
	}
}

// A level is read, not guessed at. Half the interesting lines in this system
// mention a 403 or the word error; filing them under ERROR would make the
// "errors only" filter show everything and mean nothing.
func TestDoesNotInventLevels(t *testing.T) {
	quiet := Parse("ingest", `time=2026-08-15T17:57:32.891+07:00 level=INFO msg="live mux opened, no error"`)
	if quiet.Level != "INFO" {
		t.Errorf("level = %q, want INFO", quiet.Level)
	}

	loud := Parse("ingest", "ERROR: unable to download video data")
	if loud.Level != "ERROR" {
		t.Errorf("level = %q, want ERROR", loud.Level)
	}

	mentioning := Parse("ingest", "the error was in the message we printed")
	if mentioning.Level != "" {
		t.Errorf("level = %q, want none", mentioning.Level)
	}
}

// The divider between one run of the stack and the next. Logs are appended to
// rather than truncated, so the lines just before a restart survive it — and
// those are reliably the ones being looked for.
func TestReadsTheRestartMarker(t *testing.T) {
	line := Parse("gateway", RestartMarker+"2026-08-15T18:20:00+07:00 ---")
	if !line.Restart {
		t.Fatal("restart marker not recognised")
	}
	if line.At.IsZero() {
		t.Error("restart marker carried no time")
	}
}
