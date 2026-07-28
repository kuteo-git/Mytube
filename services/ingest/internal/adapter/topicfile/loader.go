// Package topicfile reads topics.yaml, the only content source in the system.
package topicfile

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

const defaultPerSourceLimit = 40

type fileShape struct {
	Topics []struct {
		Name    string   `yaml:"name"`
		Sources []string `yaml:"sources"`
	} `yaml:"topics"`
	PerSourceLimit int32 `yaml:"per_source_limit"`
}

// Loader re-reads the file when it changes on disk, so editing topics.yaml
// takes effect on the next scan without restarting the service.
type Loader struct {
	path string

	mu       sync.Mutex
	cached   domain.TopicConfig
	modTime  time.Time
	loadedOK bool
}

func New(path string) *Loader {
	return &Loader{path: path}
}

func (l *Loader) Load(_ context.Context) (domain.TopicConfig, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	info, err := os.Stat(l.path)
	if err != nil {
		return domain.TopicConfig{}, fmt.Errorf("read %s: %w", l.path, err)
	}
	if l.loadedOK && info.ModTime().Equal(l.modTime) {
		return l.cached, nil
	}

	raw, err := os.ReadFile(l.path)
	if err != nil {
		return domain.TopicConfig{}, fmt.Errorf("read %s: %w", l.path, err)
	}

	var parsed fileShape
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return domain.TopicConfig{}, fmt.Errorf("parse %s: %w", l.path, err)
	}

	config := domain.TopicConfig{PerSourceLimit: parsed.PerSourceLimit}
	if config.PerSourceLimit <= 0 {
		config.PerSourceLimit = defaultPerSourceLimit
	}

	for _, t := range parsed.Topics {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}

		var sources []string
		for _, s := range t.Sources {
			if s = strings.TrimSpace(s); s != "" {
				sources = append(sources, s)
			}
		}
		// A topic with no sources can never produce a video, and would show up
		// as an empty chip. Drop it rather than render a dead filter.
		if len(sources) == 0 {
			continue
		}
		config.Topics = append(config.Topics, domain.Topic{Name: name, Sources: sources})
	}

	if len(config.Topics) == 0 {
		return domain.TopicConfig{}, fmt.Errorf("%s defines no usable topics", l.path)
	}

	l.cached, l.modTime, l.loadedOK = config, info.ModTime(), true
	return config, nil
}
