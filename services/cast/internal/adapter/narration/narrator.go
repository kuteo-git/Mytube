// Package narration asks the gateway for a song's narrated sound: the song
// with its narration mixed in, made once and served as a file a speaker can
// fetch and seek like the sound as it is.
package narration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lucnguyen/local-youtube/services/cast/internal/domain"
)

type Narrator struct {
	gateway string
	client  *http.Client
}

// New takes the gateway's address; the one the speakers use will do, it is
// the same server.
func New(gateway string, client *http.Client) *Narrator {
	return &Narrator{gateway: strings.TrimRight(gateway, "/"), client: client}
}

// Prepare is POST /api/videos/{id}/audio/narrated, which starts what is
// missing and answers at once.
func (n *Narrator) Prepare(ctx context.Context, videoID string, fromS int) (domain.NarratedAnswer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		n.gateway+"/api/videos/"+url.PathEscape(videoID)+"/audio/narrated?from="+strconv.Itoa(max(fromS, 0)), nil)
	if err != nil {
		return domain.NarratedAnswer{}, err
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return domain.NarratedAnswer{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnprocessableEntity {
		// A broadcast: never narrated on a speaker, it is never cast at all.
		return domain.NarratedAnswer{File: domain.NarratedNone, Stream: domain.NarratedNone}, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return domain.NarratedAnswer{}, fmt.Errorf("gateway answered %d for narrated sound of %s", resp.StatusCode, videoID)
	}
	var body struct {
		Status string `json:"status"`
		Stream string `json:"stream"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return domain.NarratedAnswer{}, err
	}
	return domain.NarratedAnswer{File: narrated(body.Status), Stream: narrated(body.Stream)}, nil
}

func narrated(word string) domain.Narrated {
	switch word {
	case "ready":
		return domain.NarratedReady
	case "none", "failed":
		return domain.NarratedNone
	default:
		return domain.NarratedPreparing
	}
}
