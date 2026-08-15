// Command logview serves the stack's logs as one page.
//
// It is a separate process on a port of its own, and that is the whole design
// decision. The moment logs are wanted is the moment something has stopped
// working, so a viewer living inside the gateway would be a viewer that goes
// down with the thing it is there to explain. It reads the log files and holds
// no connection to any service, so it survives all four of them.
//
//	LOG_DIR    where scripts/dev.sh writes the logs
//	LOGVIEW_ADDR  what to listen on (default :8184)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lucnguyen/local-youtube/services/logview/internal/tail"
)

func main() {
	dir := os.Getenv("LOG_DIR")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "local-youtube")
	}
	addr := os.Getenv("LOGVIEW_ADDR")
	if addr == "" {
		addr = ":8184"
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tailer := tail.New(dir)
	go tailer.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})

	// The backlog, so a page opened now shows the recent past rather than an
	// empty screen waiting for something to go wrong.
	mux.HandleFunc("GET /backlog", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": tailer.Services(),
			"lines":    tailer.Backlog(),
		})
	})

	// Everything written from now on. Server-sent events rather than a socket:
	// this is one direction, the browser reconnects on its own, and it is a
	// handler rather than a dependency.
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		lines, unsubscribe := tailer.Subscribe()
		defer unsubscribe()

		// A quiet stack writes nothing for minutes at a time, and a proxy in
		// between is entitled to call that a dead connection.
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				_, _ = fmt.Fprint(w, ": keep-alive\n\n")
				flusher.Flush()
			case line, open := <-lines:
				if !open {
					return
				}
				payload, err := json.Marshal(line)
				if err != nil {
					continue
				}
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	})

	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	log.Printf("logview on %s reading %s", addr, dir)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
