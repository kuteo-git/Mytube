package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// The most a single line may carry. A browser sending more than this is not
// reporting, it is dumping.
const clientLogMaxBytes = 8 << 10

// How many fields one line may carry, and how long a value may be. Both are
// there because this writes straight into the shared log: a page that decided
// to attach a whole VTT to every line would make the log unreadable for every
// service in it, not only for itself.
const (
	clientLogMaxFields = 24
	clientLogMaxValue  = 512
)

/*
handleClientLog writes a line the browser sent into the server's log.

The translation pass runs entirely in the page — it reads the VTT, batches the
cues, writes the file back — so when it sits on "Loading subtitles…" there is
nothing in any log to read. Everything the server sees is the *result* of a
decision taken in a browser on another machine, which is exactly the evidence
that was missing while this fault was being chased across three videos.

Written at whatever level the page asks for, prefixed so it can never be
mistaken for the gateway's own work, and shown by logview beside the six
services it already reads (CLAUDE.md §8, "Logs") — which is the point: the phone
that hits this has no console anybody can look at.

Unauthenticated like everything else here. The LAN is trusted (§3), and the
worst this offers is writing lines into a log file that is already trimmed at
LOG_CEILING_BYTES.
*/
func (g *Gateway) handleClientLog(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, clientLogMaxBytes))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	var in struct {
		Level  string         `json:"level"`
		Msg    string         `json:"msg"`
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(body, &in); err != nil || in.Msg == "" {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	args := make([]any, 0, clientLogMaxFields*2)
	for k, v := range in.Fields {
		if len(args) >= clientLogMaxFields*2 {
			break
		}
		s := trimField(v)
		args = append(args, k, s)
	}
	msg := "web: " + truncate(in.Msg, clientLogMaxValue)
	switch strings.ToLower(in.Level) {
	case "error":
		g.logger.Error(msg, args...)
	case "warn":
		g.logger.Warn(msg, args...)
	default:
		g.logger.Info(msg, args...)
	}
	w.WriteHeader(http.StatusNoContent)
}

// trimField keeps a value loggable: strings are cut, everything else is left as
// it is so numbers and booleans still read as themselves.
func trimField(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	return truncate(s, clientLogMaxValue)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
