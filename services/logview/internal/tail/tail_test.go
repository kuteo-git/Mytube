package tail

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestFollowsLinesWrittenAfterSubscribing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ingest.log")
	write(t, path, "time=2026-08-15T18:00:00+07:00 level=INFO msg=\"already here\"\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := New(dir)
	lines, unsubscribe := tailer.Subscribe()
	defer unsubscribe()
	go tailer.Run(ctx)

	// The follower opens at the end of the file, so what was already there is
	// the backlog's job and must not arrive twice.
	time.Sleep(3 * pollInterval)
	write(t, path, "time=2026-08-15T18:00:01+07:00 level=ERROR msg=\"open remux\"\n")

	select {
	case line := <-lines:
		if line.Level != "ERROR" || line.Service != "ingest" {
			t.Fatalf("got %+v", line)
		}
		if line.Message != "open remux" {
			t.Errorf("message = %q", line.Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing arrived")
	}
}

// A line is published whole or not at all.
//
// The writer is a separate process and the reader polls, so landing in the
// middle of a write is ordinary rather than rare. Publishing the half that had
// arrived would put a line in the viewer that nothing ever wrote.
func TestDoesNotPublishHalfALine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.log")
	write(t, path, "seed\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := New(dir)
	lines, unsubscribe := tailer.Subscribe()
	defer unsubscribe()
	go tailer.Run(ctx)
	time.Sleep(3 * pollInterval)

	write(t, path, "time=2026-08-15T18:00:02+07:00 level=WARN msg=\"half a li")
	time.Sleep(3 * pollInterval)
	select {
	case line := <-lines:
		t.Fatalf("published a partial line: %q", line.Raw)
	default:
	}

	write(t, path, "ne\"\n")
	select {
	case line := <-lines:
		if line.Message != "half a line" {
			t.Errorf("message = %q, want the whole line", line.Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the completed line never arrived")
	}
}

// The backlog is what the page opens onto: the recent past, across every
// service, in the order it happened. Interleaved because the interesting
// sequences cross services — the gateway's 502 and ingest's reason for it are
// one story told twice.
func TestBacklogReadsEveryServiceInTimeOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "gateway.log"),
		"time=2026-08-15T18:00:02+07:00 level=WARN msg=\"second\"\n")
	write(t, filepath.Join(dir, "ingest.log"),
		"time=2026-08-15T18:00:01+07:00 level=INFO msg=\"first\"\n")

	lines := New(dir).Backlog()
	if len(lines) != 2 {
		t.Fatalf("read %d lines, want 2", len(lines))
	}
	if lines[0].Message != "first" || lines[1].Message != "second" {
		t.Errorf("out of order: %q then %q", lines[0].Message, lines[1].Message)
	}
}
