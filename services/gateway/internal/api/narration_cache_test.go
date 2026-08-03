package api

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestNarrationCacheRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	err := writeNarrationCache(root, "../../etc", "qwen",
		map[string]string{"h": "x"})
	if err == nil {
		t.Fatal("video id traversal must be rejected")
	}
}
