// Command ingest serves the ingest service and runs the download worker.
//
// Both live in one process for now. They coordinate purely through the job
// table, so splitting the worker onto its own machine later needs no code
// change — only a flag to decide which half to start.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/catalogclient"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/httpapi"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/innertube"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/postgres"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/rpc"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/topicfile"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/adapter/ytdlp"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/usecase"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envDuration reads a Go duration ("30m", "2h") from the environment.
//
// A bad value falls back rather than refusing to start: the scan interval is a
// tuning knob, and a typo in it should not take the service that fills the
// library offline.
func envDuration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	var (
		addr        = env("INGEST_ADDR", ":8183")
		databaseURL = env("INGEST_DATABASE_URL",
			"postgres://ingest_svc:ingest_dev@localhost:5432/localyoutube?search_path=ingest")
		catalogURL = env("CATALOG_URL", "http://localhost:8181")
		mediaRoot  = env("MEDIA_ROOT", "./media")
		topicsPath = env("TOPICS_FILE", "./topics.yaml")
		// Same default as the gateway's devUserID: until identity exists, Phase 1
		// runs as a single seeded user, and subscriptions are per-user.
		devUserID = env("DEV_USER_ID", "u_luc")
	)

	defaultHeight := int32(1080)
	if raw := os.Getenv("DEFAULT_HEIGHT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			logger.Error("invalid DEFAULT_HEIGHT", "value", raw, "error", err)
			os.Exit(1)
		}
		defaultHeight = int32(parsed)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		logger.Error("connect to postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Error("ping postgres", "error", err)
		os.Exit(1)
	}

	catalogHTTP := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
		Timeout: 30 * time.Second,
	}

	logger.Info("preparing yt-dlp and ffmpeg")
	downloader := ytdlp.New(ctx, mediaRoot)

	// One client, shared: the browse API backs both channel browsing and the
	// related-video layer of library expansion.
	channels := innertube.New(nil)

	jobStore := postgres.New(pool)
	ingest := usecase.New(
		downloader,
		channels,
		jobStore,
		catalogclient.New(catalogHTTP, catalogURL, devUserID),
		defaultHeight,
		logger,
	)
	// The same store keeps scan history; the Activity page lists it through the
	// ingest use cases, while the scanner is what writes it.
	ingest.SetScanStore(jobStore)

	// Reports the catalogue never received, finished now. A refusal is recorded
	// by whichever request met it, and that can be a moment when catalog is
	// restarting — leaving a video that will never arrive looking merely queued,
	// which is a video the feed goes on offering.
	ingest.ReconcileUnavailable(ctx)

	go usecase.NewWorker(ingest, logger).Run(ctx)

	// The scanner is what fills the library, and the interval is the whole of
	// how fresh the feed can be: nothing uploaded to YouTube can appear here
	// before a pass has seen it.
	//
	// Hourly rather than twice a day. A pass walks 63 sources in about three
	// minutes using flat listings, which are cheap — it is the per-video
	// metadata fetch that is expensive, and the scanner deliberately does not do
	// that (see CLAUDE.md §8b). So the cost of an hourly pass is three minutes
	// of background work per hour, against a worst-case staleness that drops
	// from twelve hours to one.
	scanner := usecase.NewScanner(
		topicfile.New(topicsPath),
		downloader,
		channels,
		catalogclient.New(catalogHTTP, catalogURL, devUserID),
		jobStore,
		logger,
		envDuration("SCAN_INTERVAL", time.Hour),
	)
	go scanner.Run(ctx)
	// A second, much cheaper timer over subscribed channels only. The full pass
	// above is bounded by how expensive listings are; this one reads one static
	// RSS document per followed channel, which is what lets it run twelve times
	// as often and bring the worst-case delay on a subscription's new upload down
	// from an hour to five minutes.
	go scanner.RunSubscribed(ctx, envDuration("SUBSCRIBED_SCAN_INTERVAL", 5*time.Minute))

	// Fill in what the listings could not. A flat listing carries no publish
	// date, so videos arrive undated and the feed excludes them outright — 1127
	// of 8056 had accumulated that way, because the only thing that filled the
	// gap was a button on the Activity page.
	//
	// Slow on purpose. Each video costs a full metadata fetch, the expensive
	// request this library has already been blocked for making too many of, so a
	// pass is bounded at 200 and six hours apart is enough to clear a backlog
	// this size in a couple of days without ever looking like a crawl.
	go ingest.RunBackfill(ctx,
		envDuration("BACKFILL_START_DELAY", 10*time.Minute),
		envDuration("BACKFILL_INTERVAL", 6*time.Hour))

	store := jobStore
	expander := usecase.NewExpander(
		downloader,
		channels,
		catalogclient.New(catalogHTTP, catalogURL, devUserID),
		topicfile.New(topicsPath),
		store,
		logger,
	)

	mux := http.NewServeMux()
	mux.Handle(ingestv1connect.NewIngestServiceHandler(rpc.NewServer(ingest, scanner, expander)))

	// Plain HTTP, not ConnectRPC: this is a continuous body of media bytes, and
	// wrapping it in an RPC envelope would buy nothing.
	httpapi.NewHandler(
		downloader,
		catalogclient.New(catalogHTTP, catalogURL, devUserID),
		ingest,
		defaultHeight,
		logger,
	).Routes(mux)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	go func() {
		logger.Info("ingest service listening", "addr", addr, "media", mediaRoot, "topics", topicsPath)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "error", err)
	}
}
