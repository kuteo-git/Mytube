package ytdlp

import (
	"os"
	"path/filepath"
	"testing"
)

// The two caption passes used to run one after the other and tell authored from
// automatic by order: whatever existed after the first pass was written by a
// human. They now run at the same time, into a directory each, and the same
// question is answered by place instead. These cover that swap — a language a
// human captioned must never be replaced by the machine's version of it, and the
// flag has to survive the move into the video's own directory.

func writeVTT(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("WEBVTT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func languages(tracks []struct {
	lang      string
	generated bool
}) map[string]bool {
	out := map[string]bool{}
	for _, t := range tracks {
		out[t.lang] = t.generated
	}
	return out
}

func merged(t *testing.T, authored, auto []string) map[string]bool {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "abc")
	authoredDir := filepath.Join(dir, ".subs-authored")
	autoDir := filepath.Join(dir, ".subs-auto")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range authored {
		writeVTT(t, authoredDir, name)
	}
	for _, name := range auto {
		writeVTT(t, autoDir, name)
	}

	var flat []struct {
		lang      string
		generated bool
	}
	for _, track := range mergeSubtitlePasses(authoredDir, autoDir, dir, "abc") {
		flat = append(flat, struct {
			lang      string
			generated bool
		}{track.Language, track.Generated})

		// The published path is what the browser will ask the gateway for, so it
		// has to be the file's home, not the pass it arrived from.
		if got, want := track.Path, filepath.Join("abc", filepath.Base(track.Path)); got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.Base(track.Path))); err != nil {
			t.Errorf("track %q was published but is not in the video directory: %v", track.Language, err)
		}
	}
	return languages(flat)
}

func TestAuthoredCaptionsAreNotMarkedGenerated(t *testing.T) {
	got := merged(t, []string{"1080p.en.vtt"}, nil)
	if generated, ok := got["en"]; !ok || generated {
		t.Fatalf("en = %v (present %v), want present and not generated", generated, ok)
	}
}

func TestAutomaticCaptionsAreMarkedGenerated(t *testing.T) {
	got := merged(t, nil, []string{"1080p.vi.vtt"})
	if generated, ok := got["vi"]; !ok || !generated {
		t.Fatalf("vi = %v (present %v), want present and generated", generated, ok)
	}
}

func TestAuthoredWinsOverTheMachineForTheSameLanguage(t *testing.T) {
	// Both passes commonly return the same language: the point of running them
	// separately is to know which one to believe.
	got := merged(t, []string{"1080p.en.vtt"}, []string{"1080p.en.vtt", "1080p.vi.vtt"})

	if len(got) != 2 {
		t.Fatalf("languages = %v, want exactly en and vi", got)
	}
	if got["en"] {
		t.Error("en came from the authored pass and must not be marked generated")
	}
	if !got["vi"] {
		t.Error("vi came only from the automatic pass and must be marked generated")
	}
}

func TestNoCaptionsAtAllIsNotAFailure(t *testing.T) {
	if got := merged(t, nil, nil); len(got) != 0 {
		t.Fatalf("languages = %v, want none", got)
	}
}
