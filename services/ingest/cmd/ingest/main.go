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

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	var (
		addr        = env("INGEST_ADDR", ":8083")
		databaseURL = env("INGEST_DATABASE_URL",
			"postgres://ingest_svc:ingest_dev@localhost:5432/localyoutube?search_path=ingest")
		catalogURL = env("CATALOG_URL", "http://localhost:8081")
		mediaRoot  = env("MEDIA_ROOT", "./media")
		topicsPath = env("TOPICS_FILE", "./topics.yaml")
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

	ingest := usecase.New(
		downloader,
		postgres.New(pool),
		catalogclient.New(catalogHTTP, catalogURL),
		defaultHeight,
	)

	go usecase.NewWorker(ingest, logger).Run(ctx)

	// The scanner is what fills the library. Twelve hours between passes keeps
	// the request rate to the source negligible while still surfacing new
	// uploads twice a day.
	scanner := usecase.NewScanner(
		topicfile.New(topicsPath),
		downloader,
		catalogclient.New(catalogHTTP, catalogURL),
		logger,
		12*time.Hour,
	)
	go scanner.Run(ctx)

	mux := http.NewServeMux()
	mux.Handle(ingestv1connect.NewIngestServiceHandler(rpc.NewServer(ingest, scanner)))
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
