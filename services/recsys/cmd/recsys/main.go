// Command recsys serves the recommendation service over ConnectRPC.
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

	"github.com/lucnguyen/local-youtube/gen/go/recsys/v1/recsysv1connect"
	"github.com/lucnguyen/local-youtube/services/recsys/internal/adapter/catalogclient"
	"github.com/lucnguyen/local-youtube/services/recsys/internal/adapter/postgres"
	"github.com/lucnguyen/local-youtube/services/recsys/internal/adapter/rpc"
	"github.com/lucnguyen/local-youtube/services/recsys/internal/usecase"
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
		addr        = env("RECSYS_ADDR", ":8082")
		databaseURL = env("RECSYS_DATABASE_URL",
			"postgres://recsys_svc:recsys_dev@localhost:5432/localyoutube?search_path=recsys")
		catalogURL = env("CATALOG_URL", "http://localhost:8081")
	)

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

	features := catalogclient.New(catalogHTTP, catalogURL, 30*time.Second)
	ranker := usecase.NewRanker(postgres.New(pool), features)

	mux := http.NewServeMux()
	mux.Handle(recsysv1connect.NewRecommendationServiceHandler(rpc.NewServer(ranker)))
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
		logger.Info("recsys service listening", "addr", addr, "catalog", catalogURL)
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
