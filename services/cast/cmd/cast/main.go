// Command cast plays the household's videos, as sound, on a speaker in the
// house, and serves the sessions doing it over ConnectRPC.
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
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/lucnguyen/local-youtube/gen/go/cast/v1/castv1connect"
	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	"github.com/lucnguyen/local-youtube/services/cast/internal/adapter/catalog"
	"github.com/lucnguyen/local-youtube/services/cast/internal/adapter/googlecast"
	"github.com/lucnguyen/local-youtube/services/cast/internal/adapter/postgres"
	"github.com/lucnguyen/local-youtube/services/cast/internal/adapter/r1"
	"github.com/lucnguyen/local-youtube/services/cast/internal/adapter/rpc"
	"github.com/lucnguyen/local-youtube/services/cast/internal/usecase"
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
		addr        = env("CAST_ADDR", ":8186")
		databaseURL = env("CAST_DATABASE_URL",
			"postgres://cast_svc:cast_dev@localhost:5432/localyoutube?search_path=cast")
		catalogURL    = env("CATALOG_URL", "http://localhost:8181")
		devicesFile   = env("CAST_DEVICES_FILE", "data/cast-devices.json")
		castbridgeURL = env("CASTBRIDGE_URL", "http://127.0.0.1:8187")
		// The gateway as a speaker reaches it. No default: on the speaker,
		// localhost is the speaker, and a song it cannot fetch is skipped
		// without anything saying why.
		mediaBase = os.Getenv("CAST_MEDIA_BASE_URL")
	)
	if mediaBase == "" {
		logger.Error("CAST_MEDIA_BASE_URL is not set: speakers would have no address to fetch audio from")
		os.Exit(1)
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

	// h2c so this client speaks HTTP/2 to catalog without TLS on the LAN.
	catalogHTTP := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
		Timeout: 10 * time.Second,
	}

	engine := usecase.NewEngine(
		postgres.New(pool),
		[]usecase.Driver{
			// One second is what "online" means for an R1 (it answers in
			// milliseconds on the LAN when it is up).
			r1.New(devicesFile, mediaBase, time.Second),
			googlecast.New(castbridgeURL, mediaBase, 3*time.Second),
		},
		catalog.New(catalogv1connect.NewCatalogServiceClient(catalogHTTP, catalogURL)),
		time.Now,
		logger,
	)
	// Sessions found here at start are followed, not reloaded: the speaker is
	// still playing what it was handed before the restart.
	go engine.Run(ctx, time.Second)

	mux := http.NewServeMux()
	mux.Handle(castv1connect.NewCastServiceHandler(rpc.NewServer(engine)))
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
		logger.Info("cast service listening", "addr", addr, "catalog", catalogURL,
			"devices", devicesFile, "castbridge", castbridgeURL, "media_base", mediaBase)
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
