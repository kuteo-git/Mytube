// Package tail follows the log files on disk and hands out their lines.
//
// Reading files rather than having the services report in: four Go services
// and two Python ones write these, none of them knows this viewer exists, and
// it must stay that way. A collector the services push to would be a second
// thing that can fail on the path that exists to explain the first.
package tail

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lucnguyen/local-youtube/services/logview/internal/logline"
)

// How often each file is checked for new bytes.
//
// Polling rather than fsevents: this watches five files on one machine, and a
// second of latency on a log page is not a thing anybody can perceive against
// the time it takes to look up from the terminal. The dependency and the
// platform-specific failure modes are the whole cost of the alternative.
const pollInterval = 500 * time.Millisecond

// How much of each file is read when somebody opens the page.
//
// Enough to cover the afternoon that went wrong, small enough that the page
// arrives at once. Read from the end: a log is answered from its tail.
const backlogBytes = 512 * 1024

// Tailer follows every *.log in a directory.
type Tailer struct {
	dir string

	mu        sync.Mutex
	listeners map[chan logline.Line]struct{}
}

func New(dir string) *Tailer {
	return &Tailer{dir: dir, listeners: map[chan logline.Line]struct{}{}}
}

// Services lists the log files present, by name, in a stable order.
func (t *Tailer) Services() []string {
	entries, err := filepath.Glob(filepath.Join(t.dir, "*.log"))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(filepath.Base(e), ".log"))
	}
	sort.Strings(names)
	return names
}

// Backlog returns what is already in the files, oldest first across all of
// them, so a page opened now has the recent past on it rather than an empty
// screen waiting for something to happen.
func (t *Tailer) Backlog() []logline.Line {
	var all []logline.Line
	for _, name := range t.Services() {
		all = append(all, readTail(filepath.Join(t.dir, name+".log"), name)...)
	}
	// Interleaved by time, because the interesting sequences cross services:
	// the gateway's 502 and ingest's reason for it are one story told twice.
	// Lines without a timestamp keep the position they were read at, which is
	// beside whatever they belong to.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].At.IsZero() || all[j].At.IsZero() {
			return false
		}
		return all[i].At.Before(all[j].At)
	})
	return all
}

// Subscribe returns a channel of lines written from now on, and a function to
// stop listening.
//
// Buffered and dropping: a reader that cannot keep up loses lines rather than
// stalling the tailer for everybody else. A log viewer that can wedge the
// thing it is watching would be worse than no log viewer.
func (t *Tailer) Subscribe() (<-chan logline.Line, func()) {
	ch := make(chan logline.Line, 512)
	t.mu.Lock()
	t.listeners[ch] = struct{}{}
	t.mu.Unlock()

	return ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, open := t.listeners[ch]; open {
			delete(t.listeners, ch)
			close(ch)
		}
	}
}

func (t *Tailer) publish(line logline.Line) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for ch := range t.listeners {
		select {
		case ch <- line:
		default: // See Subscribe: dropped rather than blocking.
		}
	}
}

// Run follows every log file until the context ends, picking up files that
// appear later — a service started after this one, or a log created on the
// first line it writes.
func (t *Tailer) Run(ctx context.Context) {
	following := map[string]bool{}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		for _, name := range t.Services() {
			if following[name] {
				continue
			}
			following[name] = true
			go t.follow(ctx, name)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// follow reads one file from its current end onwards.
func (t *Tailer) follow(ctx context.Context, name string) {
	path := filepath.Join(t.dir, name+".log")

	var (
		file   *os.File
		reader *bufio.Reader
		offset int64
	)
	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()

	open := func() bool {
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		// From the end: the backlog is served separately, and starting at zero
		// would replay the whole file to every listener on every reopen.
		end, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			_ = f.Close()
			return false
		}
		file, reader, offset = f, bufio.NewReader(f), end
		return true
	}

	if !open() {
		// The file may not exist yet. The loop below tries again.
		file = nil
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if file == nil {
			if !open() {
				continue
			}
		}

		// A file that shrank was replaced or trimmed — dev.sh trims a log that
		// has grown past its ceiling. Reopening from the start is what keeps
		// the viewer following the file rather than a hole where it used to be.
		if info, err := os.Stat(path); err == nil && info.Size() < offset {
			_ = file.Close()
			f, err := os.Open(path)
			if err != nil {
				file = nil
				continue
			}
			file, reader, offset = f, bufio.NewReader(f), 0
		}

		for {
			text, err := reader.ReadString('\n')
			offset += int64(len(text))
			if err != nil {
				// A partial line: the writer is mid-write. Give the bytes back
				// so the line is read whole on the next tick, rather than
				// published in halves.
				if len(text) > 0 {
					offset -= int64(len(text))
					_, _ = file.Seek(offset, io.SeekStart)
					reader.Reset(file)
				}
				break
			}
			t.publish(logline.Parse(name, strings.TrimRight(text, "\r\n")))
		}
	}
}

// readTail reads the last backlogBytes of a file, dropping whatever partial
// line the cut lands in the middle of.
func readTail(path, name string) []logline.Line {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil
	}
	start := int64(0)
	if info.Size() > backlogBytes {
		start = info.Size() - backlogBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil
	}

	scanner := bufio.NewScanner(f)
	// Lines here carry whole yt-dlp errors and resolved googlevideo URLs, which
	// run to several kilobytes. The default 64KB limit would silently truncate
	// exactly the lines worth reading.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var lines []logline.Line
	first := true
	for scanner.Scan() {
		// The first line after a mid-file seek is half a line.
		if first && start > 0 {
			first = false
			continue
		}
		first = false
		lines = append(lines, logline.Parse(name, scanner.Text()))
	}
	return lines
}
