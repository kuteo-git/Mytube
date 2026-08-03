package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNarrationCacheRoundTrip(t *testing.T) {
	root := t.TempDir()

	if err := writeNarrationCache(root, "abc123", "qwen",
		map[string]string{"h1": "xin chào"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A second engine must not disturb the first.
	if err := writeNarrationCache(root, "abc123", "nllb",
		map[string]string{"h1": "chào bạn"}); err != nil {
		t.Fatalf("write nllb: %v", err)
	}

	got, err := readNarrationCache(root, "abc123", "qwen")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["h1"] != "xin chào" {
		t.Fatalf("qwen partition clobbered: %q", got["h1"])
	}

	nllb, _ := readNarrationCache(root, "abc123", "nllb")
	if nllb["h1"] != "chào bạn" {
		t.Fatalf("nllb partition wrong: %q", nllb["h1"])
	}

	raw, _ := os.ReadFile(filepath.Join(root, "abc123", "narration.vi.json"))
	var onDisk map[string]map[string]string
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("file is not the documented shape: %v", err)
	}
	if len(onDisk) != 2 {
		t.Fatalf("want 2 engine partitions, got %d", len(onDisk))
	}
}

func TestNarrationCacheMissingIsEmptyNotError(t *testing.T) {
	got, err := readNarrationCache(t.TempDir(), "nope", "qwen")
	if err != nil {
		t.Fatalf("a cold cache must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

func TestTTSCacheRoundTrip(t *testing.T) {
	root := t.TempDir()
	wav := []byte("RIFF....fake wav")

	if _, ok := readTTSCache(root, "vid1", "hello", 1.1, "Ngọc Linh"); ok {
		t.Fatal("cold cache must miss")
	}
	if err := writeTTSCache(root, "vid1", "hello", 1.1, "Ngọc Linh", wav); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, ok := readTTSCache(root, "vid1", "hello", 1.1, "Ngọc Linh")
	if !ok || string(got) != string(wav) {
		t.Fatalf("read back wrong: ok=%v %q", ok, got)
	}
}

func TestTTSCacheKeyedByVoice(t *testing.T) {
	// Ten voices are on offer. Without the voice in the key, changing it kept
	// serving whichever voice happened to be synthesised first, from disk, with
	// nothing to indicate the setting had been ignored.
	root := t.TempDir()
	_ = writeTTSCache(root, "vid1", "hello", 1.1, "Ngọc Linh", []byte("linh"))
	_ = writeTTSCache(root, "vid1", "hello", 1.1, "Gia Bảo", []byte("bao"))

	a, _ := readTTSCache(root, "vid1", "hello", 1.1, "Ngọc Linh")
	b, _ := readTTSCache(root, "vid1", "hello", 1.1, "Gia Bảo")
	if string(a) != "linh" || string(b) != "bao" {
		t.Fatalf("voices collided: %q %q", a, b)
	}
}

func TestTTSCacheKeyedBySpeed(t *testing.T) {
	// atempo output differs per speed, so the same sentence at 1.1 and 1.6 are
	// different audio and must not share an entry.
	root := t.TempDir()
	_ = writeTTSCache(root, "vid1", "hello", 1.1, "V", []byte("slow"))
	_ = writeTTSCache(root, "vid1", "hello", 1.6, "V", []byte("fast"))

	a, _ := readTTSCache(root, "vid1", "hello", 1.1, "V")
	b, _ := readTTSCache(root, "vid1", "hello", 1.6, "V")
	if string(a) != "slow" || string(b) != "fast" {
		t.Fatalf("speeds collided: %q %q", a, b)
	}
}

func TestTTSCacheWithoutVideoIDIsANoOp(t *testing.T) {
	// A caller that did not say which video this belongs to has nowhere to put
	// it. Synthesis must still work, just uncached.
	root := t.TempDir()
	if err := writeTTSCache(root, "", "hello", 1.1, "V", []byte("x")); err != nil {
		t.Fatalf("must not error: %v", err)
	}
	if _, ok := readTTSCache(root, "", "hello", 1.1, "V"); ok {
		t.Fatal("must not claim a hit")
	}
}

func TestNarrationCacheRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	err := writeNarrationCache(root, "../../etc", "qwen",
		map[string]string{"h": "x"})
	if err == nil {
		t.Fatal("video id traversal must be rejected")
	}
}

func TestNarrationVTTNameCopiesTheMediaBase(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "1080p.mp4.en.vtt"), []byte("WEBVTT"), 0o644)

	if got := narrationVTTName(dir); got != "1080p.mp4.vi-mt.vtt" {
		t.Fatalf("want 1080p.mp4.vi-mt.vtt, got %q", got)
	}
}

func TestNarrationVTTNameIgnoresItsOwnOutput(t *testing.T) {
	// Writing twice must not produce "1080p.mp4.vi-mt.vi-mt.vtt".
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "1080p.mp4.vi-mt.vtt"), []byte("WEBVTT"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "1080p.mp4.en.vtt"), []byte("WEBVTT"), 0o644)

	if got := narrationVTTName(dir); got != "1080p.mp4.vi-mt.vtt" {
		t.Fatalf("want 1080p.mp4.vi-mt.vtt, got %q", got)
	}
}

func TestNarrationVTTNameWithNothingToCopy(t *testing.T) {
	if got := narrationVTTName(t.TempDir()); got != "narration.vi-mt.vtt" {
		t.Fatalf("want narration.vi-mt.vtt, got %q", got)
	}
}

func TestAttachMachineTranslation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "vid1")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "1080p.mp4.vi-mt.vtt"), []byte("WEBVTT"), 0o644)

	g := &Gateway{mediaRoot: root}
	v := videoDTO{ID: "vid1", Subtitles: []subtitleDTO{
		{Language: "en", Label: "English", URL: "/media/vid1/1080p.mp4.en.vtt"},
	}}
	g.attachMachineTranslation(&v)

	if len(v.Subtitles) != 2 {
		t.Fatalf("want 2 tracks, got %d", len(v.Subtitles))
	}
	got := v.Subtitles[1]
	if got.Language != "vi-x-mt" {
		t.Fatalf("want vi-x-mt so it cannot be confused with a human track, got %q", got.Language)
	}
	if got.URL != "/media/vid1/1080p.mp4.vi-mt.vtt" {
		t.Fatalf("wrong url: %q", got.URL)
	}
}

func TestAttachMachineTranslationWithNoFile(t *testing.T) {
	// Nothing translated yet: the track simply is not offered.
	g := &Gateway{mediaRoot: t.TempDir()}
	v := videoDTO{ID: "vid1"}
	g.attachMachineTranslation(&v)
	if len(v.Subtitles) != 0 {
		t.Fatalf("invented a track: %+v", v.Subtitles)
	}
}

func TestAttachMachineTranslationIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "vid1")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "1080p.mp4.vi-mt.vtt"), []byte("WEBVTT"), 0o644)

	g := &Gateway{mediaRoot: root}
	v := videoDTO{ID: "vid1"}
	g.attachMachineTranslation(&v)
	g.attachMachineTranslation(&v)
	if len(v.Subtitles) != 1 {
		t.Fatalf("added it twice: %d", len(v.Subtitles))
	}
}

func TestConcurrentCacheWritesKeepEveryEntry(t *testing.T) {
	// Every write is a read-modify-write of one file, and the pass fires them
	// off after each batch without waiting. Unserialised, two overlapping saves
	// lose whichever read first: the pass believes those lines are on disk and
	// never translates them again.
	root := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = writeNarrationCache(root, "vid1", "omniroute:m1",
				map[string]string{fmt.Sprintf("h%02d", n): "vi"})
		}(i)
	}
	wg.Wait()

	got, err := readNarrationCache(root, "vid1", "omniroute:m1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 40 {
		t.Fatalf("lost entries: want 40, got %d", len(got))
	}
}

func TestConcurrentWritesToOneFileDoNotFail(t *testing.T) {
	// They shared a single "<path>.tmp": the first rename moved it away and the
	// second had nothing left to rename, which surfaced as a 503 on the client.
	root := t.TempDir()
	dir := filepath.Join(root, "vid1")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "narration-cues.json")

	errs := make(chan error, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			errs <- writeFileAtomic(path, []byte(fmt.Sprintf("payload %d", n)))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent write failed: %v", err)
		}
	}

	// And the survivor is one whole payload, never a blend of two.
	blob, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(blob), "payload ") {
		t.Fatalf("torn write: %q", blob)
	}
}
