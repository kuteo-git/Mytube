// Command catalog serves the catalog service over ConnectRPC.
//
// ConnectRPC rather than plain gRPC so the same handler answers gRPC and
// HTTP/JSON, which keeps every endpoint reachable with curl during development.
package main

import (
	"context"
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
	"github.com/lucnguyen/local-youtube/internal/mediaroot"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	"github.com/lucnguyen/local-youtube/services/catalog/internal/adapter/postgres"
	"github.com/lucnguyen/local-youtube/services/catalog/internal/adapter/rpc"
	"github.com/lucnguyen/local-youtube/services/catalog/internal/usecase"
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
		addr        = env("CATALOG_ADDR", ":8181")
		databaseURL = env("CATALOG_DATABASE_URL",
			"postgres://catalog_svc:catalog_dev@localhost:5432/localyoutube?search_path=catalog")
		configDir = env("CONFIG_DIR", "./data")
	)

	// Where the library lives. The saved setting wins over the environment: see
	// internal/mediaroot, and the trap it exists to close — dev.sh always
	// exports MEDIA_ROOT, so the other way round would make the Storage page's
	// setting save, restart, and change nothing.
	mediaRoot, mediaRootFrom := mediaroot.Resolve(configDir, env("MEDIA_ROOT", "./media"))
	logger.Info("media root", "path", mediaRoot, "from", mediaRootFrom)

	budgetBytes := int64(25) << 30 // 25 GiB, per the storage budget in CLAUDE.md
	if raw := os.Getenv("STORAGE_BUDGET_BYTES"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			logger.Error("invalid STORAGE_BUDGET_BYTES", "value", raw, "error", err)
			os.Exit(1)
		}
		budgetBytes = parsed
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

	// Charter §4: sweep above 20 GiB, down to 16 GiB.
	var (
		highWatermark int64 = 20 << 30
		lowWatermark  int64 = 16 << 30
	)
	if raw := os.Getenv("EVICTION_HIGH_BYTES"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			highWatermark = v
		}
	}
	if raw := os.Getenv("EVICTION_LOW_BYTES"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			lowWatermark = v
		}
	}

	repo := postgres.New(pool, mediaRoot)

	// Fix videos whose channel_id is an @handle instead of the canonical UC… id.
	// Flat playlist listings sometimes return the handle form, and scanning from
	// both a channel source and a playlist source creates two channel rows for
	// one real channel.
	if err := repo.NormaliseChannelIDs(ctx); err != nil {
		logger.Warn("normalising channel ids", "error", err)
	}

	// Download thumbnails for videos that still reference a remote URL.
	// Run in the background so it never delays startup; each thumbnail is
	// a few kilobytes, and YouTube rate-limiting is the only real cost.
	go repo.DownloadMissingThumbnails(ctx)

	server := rpc.NewServer(usecase.NewCatalog(repo, budgetBytes))

	evictor := usecase.NewEvictor(repo, mediaRoot, highWatermark, lowWatermark, logger)
	go evictor.Run(ctx)

	mux := http.NewServeMux()
	mux.Handle(catalogv1connect.NewCatalogServiceHandler(server))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	// h2c lets gRPC clients reach the service over plaintext HTTP/2 on the LAN,
	// while browsers keep using HTTP/1.1 JSON through the gateway.
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	go func() {
		logger.Info("catalog service listening", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "error", err)
	}
}
