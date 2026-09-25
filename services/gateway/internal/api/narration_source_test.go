package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Which track a pass reads, and whether it pays a model to read it.
//
// Both halves shipped wrong together and are one fault from the household's
// side: a video with Vietnamese captions on disk was translated from English
// anyway. The first half is why it was English — the folder was read in
// alphabetical order and "…en.vtt" comes before "…vi.vtt". The second is why it
// cost anything — nothing anywhere asked what language had been picked.

// The one the bug was reported on: both tracks present, and the Vietnamese one
// is what gets spoken.
func TestAVietnameseTrackBeatsTheEnglishOneBesideIt(t *testing.T) {
	name, translate := narrationSource([]string{"1080p.mp4.en.vtt", "1080p.mp4.vi.vtt"})

	if name != "1080p.mp4.vi.vtt" {
		t.Errorf("reading %q, want the Vietnamese track", name)
	}
	if translate {
		t.Error("the Vietnamese track was marked as needing translation")
	}
}

// Order in the folder decides nothing. ReadDir is alphabetical, which is what
// made "en" win; a Vietnamese track arriving second must win just the same.
func TestTheFolderOrderDecidesNothing(t *testing.T) {
	for _, names := range [][]string{
		{"1080p.mp4.en.vtt", "1080p.mp4.vi.vtt"},
		{"1080p.mp4.vi.vtt", "1080p.mp4.en.vtt"},
	} {
		if name, _ := narrationSource(names); name != "1080p.mp4.vi.vtt" {
			t.Errorf("narrationSource(%v) = %q, want the Vietnamese track", names, name)
		}
	}
}

// English alone is still translated, which is what the pass is for.
func TestEnglishAloneIsTranslated(t *testing.T) {
	name, translate := narrationSource([]string{"1080p.mp4.en.vtt"})

	if name != "1080p.mp4.en.vtt" || !translate {
		t.Errorf("narrationSource = (%q, %v), want the English track translated", name, translate)
	}
}

// Our own output is not a source. Reading it back would narrate a Vietnamese
// file into Vietnamese — the same waste this whole file is about, wearing this
// pass's own clothes.
func TestThePassDoesNotReadItsOwnTranslation(t *testing.T) {
	name, _ := narrationSource([]string{"1080p.mp4.en.vtt", "1080p.mp4.vi-mt.vtt"})

	if name != "1080p.mp4.en.vtt" {
		t.Errorf("reading %q, want the English track", name)
	}
	if name, _ := narrationSource([]string{"1080p.mp4.vi-mt.vtt"}); name != "" {
		t.Errorf("reading %q, want nothing to narrate", name)
	}
}

// A file that never went through the muxer is named "1080p.vi.vtt", with no
// container segment in the middle. The language is still the last thing before
// the extension.
func TestTheMediaNameIsNotPartOfTheLanguage(t *testing.T) {
	if name, translate := narrationSource([]string{"1080p.vi.vtt"}); name != "1080p.vi.vtt" || translate {
		t.Errorf("narrationSource = (%q, %v), want the Vietnamese track untranslated", name, translate)
	}
}

// And the whole of it through the pass: a Vietnamese video narrates on a
// machine with no translation model configured at all. Before this it failed
// with "no translation model configured" — the model was demanded before
// anybody had looked at the language.
func TestAVietnameseVideoNarratesWithNoTranslatorConfigured(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "vionly")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	vtt := "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nXin chào các bạn.\n"
	if err := os.WriteFile(filepath.Join(dir, "1080p.mp4.vi.vtt"), []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}

	g := &Gateway{
		mediaRoot: root,
		// Nothing configured: no translation model and no synthesiser. Both
		// absent on purpose — a test that reached either would be a test that
		// spends money to pass.
		configDir:    t.TempDir(),
		narration:    newNarrationRegistry(),
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		streamClient: &http.Client{},
	}
	g.narration.passes["vionly"] = &narrationPass{Status: narrationRunning}

	g.runNarration(context.Background(), "vionly", 0, 0)

	pass := g.narration.passes["vionly"]
	if pass.Status == narrationFailed {
		t.Errorf("pass failed with %q, want a pass that needs no translator", pass.Error)
	}
	if pass.Total != 1 {
		t.Errorf("Total = %d, want the one cue read from the Vietnamese track", pass.Total)
	}
}

// The cue list itself comes from the Vietnamese file, which is the half a
// caller can see.
func TestNarrationCuesComeFromTheVietnameseFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "abc123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("1080p.mp4.en.vtt", "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nHello everyone.\n")
	write("1080p.mp4.vi.vtt", "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nXin chào các bạn.\n")

	g := &Gateway{mediaRoot: root}
	cues, translate, err := g.narrationCues("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) == 0 {
		t.Fatal("no cues")
	}
	if translate {
		t.Error("marked as needing translation")
	}
	if !strings.Contains(cues[0].Text, "Xin chào") {
		t.Errorf("narrating %q, want the Vietnamese line", cues[0].Text)
	}
}
