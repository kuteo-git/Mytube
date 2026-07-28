package domain

import (
	"context"
	"time"
)

// A Topic is a name plus the YouTube playlists or channels it draws from.
// Topics are configuration, not user data: they live in topics.yaml and are
// the only way content enters the system.
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
