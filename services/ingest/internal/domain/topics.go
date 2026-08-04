package domain

import (
	"context"
	"time"
)

// A Topic is a name plus the YouTube playlists or channels it draws from.
// Topics are configuration, not user data: they live in topics.yaml and are
// curated ahead of time. They are one of two content sources — the other is
// subscriptions, chosen while using the app and stored in the database. Both
// are scanned the same way.
type Topic struct {
	Name    string
	Sources []string
}

type TopicConfig struct {
	Topics []Topic
	// Videos taken from each source per scan. Sources list newest first, so
	// this is effectively "the most recent N".
	PerSourceLimit int32
}

// TopicSource loads the configuration. Kept behind a port so the scanner can
// be exercised without a file on disk.
type TopicSource interface {
	Load(ctx context.Context) (TopicConfig, error)
}

// ScanResult reports what a scan pass did, for logging and the Refresh button.
type ScanResult struct {
	StartedAt      time.Time
	Duration       time.Duration
	SourcesScanned int32
	SourcesFailed  int32
	VideosSeen     int
	VideosAdded    int
	Errors         []string
}

// ScanStore keeps what each pass did.
//
// The scanner used to hold only its most recent result, in a variable, so the
// page could answer "how did the last pass go" and nothing else — not even
// that across a restart. The question people actually bring to that page spans
// days: a channel has stopped producing new videos, and the first thing worth
// knowing is whether the scan has been running at all.
type ScanStore interface {
	// RecordScan saves one pass and drops anything older than the retention
	// window. Pruning here rather than on a timer keeps the table's growth tied
	// to the only thing that grows it.
	RecordScan(ctx context.Context, r ScanResult, retain time.Duration) error
	// ListScans reads them newest first, and reports the total so the page
	// knows whether there is more.
	ListScans(ctx context.Context, limit, offset int32) (scans []ScanResult, total int32, err error)
}
