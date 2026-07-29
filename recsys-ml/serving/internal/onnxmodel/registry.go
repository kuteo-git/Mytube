package onnxmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Registry holds the live bundle and swaps in new ones without a restart.
//
// # How reloading works
//
// A goroutine polls the artifacts directory on an interval. Polling rather than
// an fsnotify watch, for three reasons that all point the same way:
//
//  1. A training run writes four files. A watcher fires on the first one, when
//     the set is inconsistent — the new ranker beside the old embeddings, whose
//     vector spaces are unrelated. Polling a *fingerprint of the whole set*
//     only ever observes complete states.
//  2. The artifacts directory is frequently a network mount or an object-store
//     sync target, where inotify/FSEvents either do not fire or fire on the
//     sync tool's temporary files.
//  3. A missed poll costs one interval of staleness. A missed watch event costs
//     staleness until the next restart, and nothing reports it.
//
// The fingerprint is size and modification time of each artifact, not a content
// hash: models run to tens of megabytes and hashing them on every tick would
// read the whole set from disk every few seconds to answer a question that
// almost always turns out to be "no".
//
// A change is acted on only after the fingerprint has been *stable across two
// consecutive polls*. A run still writing its files produces a fingerprint that
// keeps moving; requiring stability means the loader never opens a half-written
// model. The training pipeline also renames each artifact into place
// atomically, so the two mechanisms together make a torn read impossible rather
// than merely unlikely.
//
// Swapping is an atomic pointer store, so in-flight requests keep using the
// bundle they started with. The superseded bundle is closed after a grace
// period rather than immediately: closing an ONNX session out from under a
// running inference is a use-after-free, and the grace period is far cheaper
// than reference counting every scoring call.
type Registry struct {
	dir              string
	expectedFeatures []string
	pollInterval     time.Duration
	// How long a superseded bundle is kept alive after being replaced. Must
	// comfortably exceed the longest inference; requests are milliseconds, so
	// seconds of slack costs nothing but removes the race entirely.
	drainPeriod time.Duration
	logger      *slog.Logger

	current atomic.Pointer[Bundle]

	mu               sync.Mutex
	pendingPrint     string
	pendingSince     int
	lastLoadedPrint  string
	lastLoadAttempt  time.Time
	consecutiveFails int
}

// RegistryOptions configures a Registry.
type RegistryOptions struct {
	Dir              string
	ExpectedFeatures []string
	PollInterval     time.Duration
	DrainPeriod      time.Duration
	Logger           *slog.Logger
}

// Defaults for reload timing.
const (
	DefaultPollInterval = 30 * time.Second
	DefaultDrainPeriod  = 30 * time.Second
)

// NewRegistry creates a registry. It does not load anything: call Reload once
// at startup, and treat a failure there as non-fatal so the service can come up
// on its fallback.
func NewRegistry(options RegistryOptions) *Registry {
	if options.PollInterval <= 0 {
		options.PollInterval = DefaultPollInterval
	}
	if options.DrainPeriod <= 0 {
		options.DrainPeriod = DefaultDrainPeriod
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Registry{
		dir:              options.Dir,
		expectedFeatures: options.ExpectedFeatures,
		pollInterval:     options.PollInterval,
		drainPeriod:      options.DrainPeriod,
		logger:           options.Logger,
	}
}

// Current returns the live bundle, or nil when none has loaded.
//
// Callers must handle nil rather than assume a model exists: that is the state
// before the first training run, after a bad export, and in any build without
// the onnx tag.
func (r *Registry) Current() *Bundle {
	return r.current.Load()
}

// Reload loads the artifacts now, regardless of the poller's schedule.
func (r *Registry) Reload() error {
	fingerprint, err := Fingerprint(r.dir)
	if err != nil {
		return err
	}
	return r.loadFingerprint(fingerprint)
}

func (r *Registry) loadFingerprint(fingerprint string) error {
	bundle, err := Load(r.dir, r.expectedFeatures)
	if err != nil {
		return err
	}
	bundle.Fingerprint = fingerprint

	previous := r.current.Swap(bundle)

	r.mu.Lock()
	r.lastLoadedPrint = fingerprint
	r.mu.Unlock()

	r.logger.Info("loaded model bundle",
		"dir", r.dir,
		"fingerprint", fingerprint[:12],
		"generated_at", bundle.Spec.GeneratedAt,
		"ranker_rmse", bundle.Spec.Metrics.RankerValidationRMSE,
	)

	if previous != nil {
		go func() {
			time.Sleep(r.drainPeriod)
			if err := previous.Close(); err != nil {
				r.logger.Warn("closing superseded bundle", "error", err)
			}
		}()
	}
	return nil
}

// Watch polls until the context is cancelled. Run it in its own goroutine.
func (r *Registry) Watch(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if bundle := r.current.Swap(nil); bundle != nil {
				if err := bundle.Close(); err != nil {
					r.logger.Warn("closing bundle at shutdown", "error", err)
				}
			}
			return
		case <-ticker.C:
			r.poll()
		}
	}
}

// poll checks the directory once and reloads if a new, settled set appeared.
func (r *Registry) poll() {
	fingerprint, err := Fingerprint(r.dir)
	if err != nil {
		// Missing artifacts are the normal state before the first training run.
		// Logged at debug so it does not drown the log every interval.
		r.logger.Debug("fingerprinting artifacts", "dir", r.dir, "error", err)
		return
	}

	r.mu.Lock()
	if fingerprint == r.lastLoadedPrint {
		r.mu.Unlock()
		return
	}
	if fingerprint == r.pendingPrint {
		r.pendingSince++
	} else {
		r.pendingPrint = fingerprint
		r.pendingSince = 1
	}
	// Two consecutive identical readings mean the writer has stopped.
	settled := r.pendingSince >= 2
	r.mu.Unlock()

	if !settled {
		r.logger.Debug("artifacts changing, waiting for them to settle", "dir", r.dir)
		return
	}

	if err := r.loadFingerprint(fingerprint); err != nil {
		r.mu.Lock()
		r.consecutiveFails++
		failures := r.consecutiveFails
		// Do not retry the same broken fingerprint every interval forever.
		// Marking it loaded means the next *different* fingerprint is picked up
		// while this one is left alone.
		r.lastLoadedPrint = fingerprint
		r.mu.Unlock()

		r.logger.Error("loading new model bundle failed; keeping the current one",
			"dir", r.dir, "error", err, "consecutive_failures", failures)
		return
	}

	r.mu.Lock()
	r.consecutiveFails = 0
	r.mu.Unlock()
}

// Fingerprint identifies the published artifact set.
//
// It hashes the *contents of feature_spec.json only*, after checking that every
// other artifact is present. The spec is the manifest: the training pipeline
// writes it last, once both trainers have finished, and it carries a
// generated_at that changes on every run.
//
// Fingerprinting all four files instead — the obvious first design — has a
// fault that only shows up under a real publish. The two trainers run as
// separate processes seconds apart, so between them the directory holds a new
// video tower beside an old ranker. That state is perfectly stable: it survives
// any number of polls, so the "wait until it settles" guard reports it as
// settled and the service loads a mismatched pair whose embedding spaces are
// unrelated. Observed, not hypothesised — an early build of this registry
// reloaded twice per training run, once on the torn set.
//
// Keying on the manifest makes intermediate states invisible, because the
// manifest does not exist in a new form until the run is complete.
func Fingerprint(dir string) (string, error) {
	for _, name := range []string{VideoTowerFile, RankerFile, EmbeddingsFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return "", fmt.Errorf("onnxmodel: fingerprint %s: %w", name, err)
		}
	}

	spec, err := os.ReadFile(filepath.Join(dir, FeatureSpecFile))
	if err != nil {
		return "", fmt.Errorf("onnxmodel: fingerprint %s: %w", FeatureSpecFile, err)
	}
	hash := sha256.Sum256(spec)
	return hex.EncodeToString(hash[:]), nil
}
