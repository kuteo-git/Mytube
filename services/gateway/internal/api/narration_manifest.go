package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The narration manifest: translation and speech done on the server, handed to
// a client as a list of clips with the moments they belong at.
//
// # Why this is not where narration already lives
//
// It lives in the browser — about three thousand lines under
// `web/src/features/watch/application/` — reading cues, hashing them, looking
// them up, translating in batches, calling TTS per line and scheduling the
// result. That works there and is not being replaced. What it cannot do is run
// on a phone with the screen off: a thousand TTS requests from a backgrounded
// app is exactly the work an operating system suspends, and this app exists
// because the charter's risk 4 says a native app is the only path to background
// audio on iOS. "A manifest and some audio files" is ordinary playback, which is
// what phones are built to keep doing.
//
// # What it reuses rather than reinvents
//
//   - The **VTT parser**, ported beside this file, with the browser's own test
//     cases.
//   - The **translation cache on disk**, under the same partition key the web
//     app uses (`omniroute:<model>`). That is deliberate and worth stating: a
//     video already narrated in the browser is already translated for the app,
//     and the reverse.
//   - `synthesise`, `atempo`, `tempoFor`, `wavDuration` and the **TTS cache**,
//     which is why a clip fitted here is the same file the browser would have
//     fetched, at the same path, under the same key.
//
// So the new code is the pass itself, and nothing underneath it.

// narrationBatch sizes, copied from the browser's pass and for its reasons: a
// full batch was measured at about twenty seconds, and twenty seconds of silence
// after switching narration on reads as broken.
const (
	// How far a viewer must move before the pass is worth reordering.
	//
	// A seek of a few seconds is somebody catching a line again, and reordering
	// for it would restart the sequence on every nudge of the bar. Half a minute
	// is past anything that is not deliberately skipping.
	narrationRetargetSeconds = 30.0

	narrationFirstBatch = 3
	narrationBatchSize  = 15
	narrationContext    = 3
)

// narrationStatus is what a client polls for.
type narrationStatus string

const (
	narrationIdle    narrationStatus = "idle"
	narrationRunning narrationStatus = "running"
	narrationDone    narrationStatus = "done"
	narrationFailed  narrationStatus = "failed"
)

// narrationClip is one spoken line, ready to play.
type narrationClip struct {
	StartSeconds    float64 `json:"startSeconds"`
	DurationSeconds float64 `json:"durationSeconds"`
	// ClipURL is under /media, which the gateway already serves, so the bytes
	// never pass through this handler.
	ClipURL string `json:"clipUrl"`
	Text    string `json:"text"`
}

// narrationPass is the state of one video's pass.
//
// Held in memory on purpose, the same decision the account scan made: a pass
// cannot survive a restart, so neither should the claim that one is running.
// What *does* survive is everything expensive — the translations and the clips
// are on disk, so a restarted pass is fast rather than repeated.
type narrationPass struct {
	Status narrationStatus `json:"status"`
	Done   int             `json:"done"`
	Total  int             `json:"total"`
	Error  string          `json:"error,omitempty"`
	Clips  []narrationClip `json:"cues"`

	cancel context.CancelFunc

	// gen tells a goroutine whether it is still the pass.
	//
	// Retargeting cancels the running one and starts another over the *same*
	// pass object, so the client keeps the clips it already has. The cancelled
	// goroutine is still between cues when that happens, and without this it
	// would report itself idle over its own replacement.
	gen int

	// startedAt is the position the current run began from, so asking again for
	// the same place is not a reason to start over.
	startedAt float64
}

type narrationRegistry struct {
	mu     sync.Mutex
	passes map[string]*narrationPass
}

func newNarrationRegistry() *narrationRegistry {
	return &narrationRegistry{passes: map[string]*narrationPass{}}
}

// handleStartNarration begins a pass, or reports the one already running.
//
// Answers 202 and returns immediately. A full video takes minutes, and a request
// held open for minutes is a request that dies to any proxy, any phone locking
// its screen, any wifi hiccup — and takes the whole pass with it. The client
// polls the GET below, which is also what makes reloading harmless.
func (g *Gateway) handleStartNarration(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("id")
	if _, err := safeVideoDir(g.mediaRoot, videoID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad video id"})
		return
	}

	from := narrationStartAt(r)

	g.narration.mu.Lock()
	pass := g.narration.passes[videoID]
	if pass != nil && pass.Status == narrationRunning {
		// Already running from here. Pressing the switch twice, or a poll
		// racing a start, must not begin the work again.
		if math.Abs(pass.startedAt-from) < narrationRetargetSeconds {
			g.narration.mu.Unlock()
			writeJSON(w, http.StatusAccepted, pass)
			return
		}
		// Somewhere else now: the viewer has seeked far enough that the order
		// this pass is working in is the wrong order. Nothing is thrown away —
		// every line already translated and spoken is on disk and is skipped —
		// so what is abandoned is the sequence, not the work.
		pass.cancel()
	} else {
		pass = &narrationPass{}
		g.narration.passes[videoID] = pass
	}

	pass.Status = narrationRunning
	pass.Error = ""
	// Done counts this run, which walks every cue again from a new place.
	// `Clips` deliberately does not reset: the client replaces its list from
	// every poll, and emptying it here would silence a narration that is
	// already playing correctly.
	pass.Done = 0
	pass.startedAt = from
	pass.gen++
	gen := pass.gen

	// Detached from the request's context, deliberately. The request is about to
	// return 202; a pass tied to it would be cancelled the instant it did.
	ctx, cancel := context.WithCancel(context.Background())
	pass.cancel = cancel
	g.narration.mu.Unlock()

	go g.runNarration(ctx, videoID, gen, from)
	writeJSON(w, http.StatusAccepted, pass)
}

// narrationStartAt reads where the viewer is, in seconds.
//
// Absent or unreadable means the beginning, which is what every caller sent
// before this existed.
func narrationStartAt(r *http.Request) float64 {
	raw := r.URL.Query().Get("from")
	if raw == "" {
		return 0
	}
	at, err := strconv.ParseFloat(raw, 64)
	if err != nil || at < 0 {
		return 0
	}
	return at
}

// handleGetNarration reports what is ready so far.
//
// Progressive, not all-or-nothing. Waiting for a whole video before answering is
// minutes of a still screen; the clips already fitted are playable now, and the
// client polls for the rest exactly as it polls for a stream.
func (g *Gateway) handleGetNarration(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("id")

	g.narration.mu.Lock()
	pass := g.narration.passes[videoID]
	var snapshot narrationPass
	if pass == nil {
		snapshot = narrationPass{Status: narrationIdle, Clips: []narrationClip{}}
	} else {
		// Copied under the lock. Handing out the live slice would let the
		// encoder read it while the pass appends to it.
		snapshot = narrationPass{
			Status: pass.Status,
			Done:   pass.Done,
			Total:  pass.Total,
			Error:  pass.Error,
			Clips:  append([]narrationClip(nil), pass.Clips...),
		}
	}
	g.narration.mu.Unlock()

	writeJSON(w, http.StatusOK, snapshot)
}

// handleStopNarration cancels a running pass.
//
// Nothing already written is thrown away: the translations and the clips are on
// disk, and starting again picks them up. Stopping means "stop spending on this
// now", not "undo it".
func (g *Gateway) handleStopNarration(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("id")

	g.narration.mu.Lock()
	if pass := g.narration.passes[videoID]; pass != nil && pass.cancel != nil {
		pass.cancel()
	}
	g.narration.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) runNarration(ctx context.Context, videoID string, gen int, fromSeconds float64) {
	start := time.Now()

	cues, err := g.narrationCues(videoID)
	if err != nil {
		g.failNarration(videoID, gen, err.Error())
		return
	}
	if len(cues) == 0 {
		// Not a failure. A video with no captions has nothing to narrate, and
		// saying "failed" would send somebody looking for a fault.
		g.finishNarration(videoID, gen, narrationDone)
		return
	}

	g.narration.mu.Lock()
	if pass := g.narration.passes[videoID]; pass != nil && pass.gen == gen {
		pass.Total = len(cues)
	}
	g.narration.mu.Unlock()

	cfg := loadTranslateConfig(g.translateConfigPath())
	if strings.TrimSpace(cfg.Model) == "" {
		// Refused rather than attempted. Translating into a partition named
		// after nothing is worse than a cold cache: the answers land somewhere
		// they will later be read back as another model's work.
		g.failNarration(videoID, gen, "no translation model configured")
		return
	}
	partition := "omniroute:" + cfg.Model

	translations, err := readNarrationCache(g.mediaRoot, videoID, partition)
	if err != nil || translations == nil {
		translations = map[string]string{}
	}

	voice := loadTTSConfig(g.ttsConfigPath()).Voice

	// From where the viewer is, then back to fill in the start.
	//
	// A pass takes minutes and somebody who has seeked to 1:20 is waiting for
	// 1:20 — running from zero spends all of it on lines they have already gone
	// past, and the first thing they hear is a voice reading the opening while
	// the picture is somewhere else entirely. This is what the web app does, and
	// what this endpoint did not.
	//
	// The earlier half is not abandoned: seeking backwards afterwards, or
	// watching it again tomorrow, wants those lines, and they are already paid
	// for by the time anybody looks.
	first := cueIndexAt(cues, fromSeconds)
	if !g.narrateRange(ctx, videoID, gen, cues, first, len(cues), translations, voice, partition) {
		return
	}
	if !g.narrateRange(ctx, videoID, gen, cues, 0, first, translations, voice, partition) {
		return
	}

	g.finishNarration(videoID, gen, narrationDone)
	g.logger.Info("narration pass", "video", videoID, "cues", len(cues),
		"seconds", int(time.Since(start).Seconds()))
}

// cueIndexAt is the first cue that has not finished by this moment.
//
// Not the nearest cue: a viewer sitting inside a line wants that line spoken,
// not the one after it. Past the end of the video it answers 0, so a stale
// position asks for the whole thing from the start rather than for nothing.
func cueIndexAt(cues []vttCue, seconds float64) int {
	if seconds <= 0 {
		return 0
	}
	for i, cue := range cues {
		if slotFor(cues, i)+cue.Start > seconds {
			return i
		}
	}
	return 0
}

// narrateRange translates and speaks cues[from:to], in batches.
//
// Reports whether it finished. A cancelled pass has already been recorded as
// idle by the time this answers false, so the caller stops rather than deciding
// anything.
func (g *Gateway) narrateRange(
	ctx context.Context,
	videoID string,
	gen int,
	cues []vttCue,
	from, to int,
	translations map[string]string,
	voice, partition string,
) bool {
	for i := from; i < to; {
		if ctx.Err() != nil {
			g.finishNarration(videoID, gen, narrationIdle)
			return false
		}

		size := narrationBatchSize
		// A small first batch so the first clips arrive in seconds rather than
		// after a full batch — and it is the first batch *of this range*,
		// because that is the one somebody is waiting on.
		if i == from {
			size = narrationFirstBatch
		}
		end := min(i+size, to)

		g.translateInto(ctx, cues[i:end], cues, i, translations, videoID, partition)

		for k := i; k < end; k++ {
			if ctx.Err() != nil {
				g.finishNarration(videoID, gen, narrationIdle)
				return false
			}
			g.speakCue(ctx, videoID, gen, cues, k, translations, voice)
		}
		i = end
	}
	return true
}

// narrationCues reads the video's caption file and turns it into lines.
func (g *Gateway) narrationCues(videoID string) ([]vttCue, error) {
	dir, err := safeVideoDir(g.mediaRoot, videoID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no folder for this video")
	}

	name := ""
	for _, e := range entries {
		n := e.Name()
		// The machine translation is this pass's own output. Reading it back
		// would narrate a Vietnamese file into Vietnamese.
		if e.IsDir() || !strings.HasSuffix(n, ".vtt") || strings.HasSuffix(n, machineVTTSuffix) {
			continue
		}
		name = n
		break
	}
	if name == "" {
		return nil, nil
	}

	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	// The language is read from the filename ("1080p.mp4.en.vtt"), because the
	// clause rules are tuned for English and Vietnamese punctuation and a
	// language they do not describe is better left in whole cues.
	return parseVTT(string(raw), languageFromVTTName(name)), nil
}

// languageFromVTTName reads "en" out of "1080p.mp4.en.vtt".
func languageFromVTTName(name string) string {
	trimmed := strings.TrimSuffix(name, ".vtt")
	if i := strings.LastIndex(trimmed, "."); i >= 0 {
		return trimmed[i+1:]
	}
	return ""
}

// translateInto fills in whatever the batch is missing, and saves what it got.
//
// Everything already in `translations` is skipped, which is what makes a
// restarted pass cheap and a video narrated in the browser free.
func (g *Gateway) translateInto(
	ctx context.Context,
	batch []vttCue,
	all []vttCue,
	offset int,
	translations map[string]string,
	videoID, partition string,
) {
	var wanted []string
	var slots []float64
	for i, cue := range batch {
		if _, have := translations[cue.Text]; have {
			continue
		}
		wanted = append(wanted, cue.Text)
		slots = append(slots, slotFor(all, offset+i))
	}
	if len(wanted) == 0 {
		return
	}

	// The lines just before this batch, sent for context and not for
	// translation: a clause reads differently when the model can see what came
	// before it.
	var contextLines []string
	for i := max(0, offset-narrationContext); i < offset; i++ {
		contextLines = append(contextLines, all[i].Text)
	}

	body, err := json.Marshal(map[string]any{
		"cues":    wanted,
		"context": contextLines,
		// Sent because this is going to be *spoken over a video*, not read. A
		// line that comes back long is sped up, and past about 3x the voice
		// stops being followable — handing the model the budget turns that from
		// a playback problem into a wording one, which is the only place it can
		// be solved.
		"slots":   slots,
		"baseUrl": loadTranslateConfig(g.translateConfigPath()).BaseURL,
		"model":   loadTranslateConfig(g.translateConfigPath()).Model,
		"apiKey":  loadTranslateConfig(g.translateConfigPath()).APIKey,
	})
	if err != nil {
		return
	}

	out, err := g.postSidecarBatchCtx(ctx, body)
	if err != nil {
		g.logger.Warn("narration translate", "video", videoID, "error", err)
		return
	}

	fresh := map[string]string{}
	for i, text := range wanted {
		if i >= len(out.Translations) {
			// A short answer is padded rather than allowed to shift everything
			// after it onto the wrong cue.
			break
		}
		if line := strings.TrimSpace(out.Translations[i]); line != "" {
			translations[text] = line
			fresh[text] = line
		}
	}
	if len(fresh) > 0 {
		if err := writeNarrationCache(g.mediaRoot, videoID, partition, fresh); err != nil {
			// Losing the cache costs time, not correctness — and MEDIA_ROOT can
			// be an unmounted external disk.
			g.logger.Warn("narration cache write", "error", err)
		}
	}
}

// slotFor is how long a line has before the next one is due.
//
// The gap to the next cue's start, not this cue's own length: a line may be
// written short and still have the silence after it to be said in, and fitting
// to the shorter of the two speeds up lines that had time to spare.
func slotFor(cues []vttCue, i int) float64 {
	if i+1 < len(cues) {
		if gap := cues[i+1].Start - cues[i].Start; gap > 0 {
			return gap
		}
	}
	if d := cues[i].End - cues[i].Start; d > 0 {
		return d
	}
	return 0
}

// speakCue synthesises one line, fitted to its slot, and records where it went.
func (g *Gateway) speakCue(
	ctx context.Context,
	videoID string,
	gen int,
	cues []vttCue,
	i int,
	translations map[string]string,
	voice string,
) {
	defer g.advanceNarration(videoID, gen)

	line := translations[cues[i].Text]
	if line == "" {
		// Untranslated, so there is nothing to say. Counted as done rather than
		// retried here: the batch above already reported why, and re-asking per
		// cue would turn one failed batch into fifteen failed requests.
		return
	}

	slot := slotFor(cues, i)
	speed := naturalSpeed

	natural, cached := readTTSCache(g.mediaRoot, videoID, line, naturalSpeed, voice)
	if !cached {
		var err error
		natural, err = g.synthesise(ctx, line, voice)
		if err != nil {
			g.logger.Warn("narration synthesise", "video", videoID, "error", err)
			return
		}
		if err := writeTTSCache(g.mediaRoot, videoID, line, naturalSpeed, voice, natural); err != nil {
			g.logger.Warn("tts cache write", "error", err)
		}
	}

	if slot > 0 {
		duration, ok := wavDuration(natural)
		if !ok {
			// An unmeasurable clip is used as it is. Whatever the synthesiser
			// returned is closer to narration than silence; only the fitting is
			// lost.
			g.logger.Warn("tts unmeasurable wav", "bytes", len(natural))
		} else if fitted, err := tempoFor(duration, slot); err != nil {
			// Refused, not clamped. Squeezing to the maximum costs two lines to
			// keep one: unintelligible at that tempo, and it overruns its slot,
			// which pushes the next clip past its own cue.
			g.logger.Info("narration too fast", "video", videoID,
				"natural", fmt.Sprintf("%.2f", duration),
				"slot", fmt.Sprintf("%.2f", slot))
			return
		} else {
			speed = fitted
		}
	}

	if math.Abs(speed-naturalSpeed) > 0.005 {
		if _, have := readTTSCache(g.mediaRoot, videoID, line, speed, voice); !have {
			stretched, err := atempo(natural, speed)
			if err != nil {
				g.logger.Warn("narration atempo", "error", err)
				return
			}
			if err := writeTTSCache(g.mediaRoot, videoID, line, speed, voice, stretched); err != nil {
				g.logger.Warn("tts cache write", "error", err)
				return
			}
		}
	}

	g.addNarrationClip(videoID, gen, narrationClip{
		StartSeconds: cues[i].Start,
		// The slot, not the audio's own length. It is what the client uses to
		// duck the video's sound, and ducking has to end when the line's time
		// is up rather than when its file happens to stop.
		DurationSeconds: slot,
		ClipURL: fmt.Sprintf("/media/%s/%s/%s.wav",
			videoID, narrationTTSDir, ttsKey(line, speed, voice)),
		Text: line,
	})
}

// current is the pass this goroutine is still allowed to write to.
//
// Nil once it has been retargeted: a cancelled run is somewhere between two
// cues when that happens, and every write below would otherwise land on its
// replacement. Callers must hold the lock.
func (g *Gateway) current(videoID string, gen int) *narrationPass {
	pass := g.narration.passes[videoID]
	if pass == nil || pass.gen != gen {
		return nil
	}
	return pass
}

// addNarrationClip records one spoken line, in order and at most once.
//
// Sorted by start rather than appended, and replacing a clip that already
// covers this moment. A retargeted pass walks the cues in a different order and
// walks some of them twice, and the client reads this list two ways: `clipAt`
// scans it for the moment now, which does not care, and `nextClipAfter` takes
// the *first* clip beginning after the playhead in order to buffer it — which
// on an unsorted list buffers the wrong file.
func (g *Gateway) addNarrationClip(videoID string, gen int, clip narrationClip) {
	g.narration.mu.Lock()
	defer g.narration.mu.Unlock()
	pass := g.current(videoID, gen)
	if pass == nil {
		return
	}
	at := sort.Search(len(pass.Clips), func(i int) bool {
		return pass.Clips[i].StartSeconds >= clip.StartSeconds
	})
	if at < len(pass.Clips) && pass.Clips[at].StartSeconds == clip.StartSeconds {
		pass.Clips[at] = clip
		return
	}
	pass.Clips = append(pass.Clips, narrationClip{})
	copy(pass.Clips[at+1:], pass.Clips[at:])
	pass.Clips[at] = clip
}

func (g *Gateway) advanceNarration(videoID string, gen int) {
	g.narration.mu.Lock()
	defer g.narration.mu.Unlock()
	if pass := g.current(videoID, gen); pass != nil {
		pass.Done++
	}
}

func (g *Gateway) failNarration(videoID string, gen int, reason string) {
	g.narration.mu.Lock()
	defer g.narration.mu.Unlock()
	if pass := g.current(videoID, gen); pass != nil {
		pass.Status = narrationFailed
		pass.Error = reason
	}
	g.logger.Warn("narration pass", "video", videoID, "error", reason)
}

func (g *Gateway) finishNarration(videoID string, gen int, status narrationStatus) {
	g.narration.mu.Lock()
	defer g.narration.mu.Unlock()
	if pass := g.current(videoID, gen); pass != nil {
		pass.Status = status
	}
}
