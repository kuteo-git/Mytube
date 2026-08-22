// Command gateway is the single origin the browser talks to.
//
// It fans out to the internal services over ConnectRPC and serves media files
// straight from disk. Keeping everything on one origin is what makes CORS a
// non-issue and, later, lets one self-signed certificate cover the whole app
// on a TV.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/lucnguyen/local-youtube/internal/mediaroot"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http2"

	"github.com/lucnguyen/local-youtube/gen/go/catalog/v1/catalogv1connect"
	"github.com/lucnguyen/local-youtube/gen/go/ingest/v1/ingestv1connect"
	"github.com/lucnguyen/local-youtube/gen/go/recsys/v1/recsysv1connect"
	"github.com/lucnguyen/local-youtube/services/gateway/internal/api"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// h2cClient speaks HTTP/2 without TLS, which is what the services expose on the
// loopback interface. The client's own Timeout is a hard deadline on the whole
// request that overrides any context deadline a caller sets — so callers with
// slow RPCs need a client built with a longer one, not a longer context.
func h2cClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		},
		Timeout: timeout,
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	var (
		addr       = env("GATEWAY_ADDR", ":8180")
		catalogURL = env("CATALOG_URL", "http://localhost:8181")
		recsysURL  = env("RECSYS_URL", "http://localhost:8182")
		ingestURL  = env("INGEST_URL", "http://localhost:8183")
		configDir  = env("CONFIG_DIR", "./data")
		devUserID  = env("DEV_USER_ID", "u_luc")
		webOrigin  = env("WEB_ORIGIN", "http://localhost:5173")
	)

	// Where the library lives. The saved setting wins over the environment: see
	// internal/mediaroot, and the trap it exists to close — dev.sh always
	// exports MEDIA_ROOT, so the other way round would make the Storage page's
	// setting save, restart, and change nothing.
	mediaRoot, mediaRootFrom := mediaroot.Resolve(configDir, env("MEDIA_ROOT", "./media"))
	logger.Info("media root", "path", mediaRoot, "from", mediaRootFrom)

	// catalog and recsys answer in milliseconds; a slow response there means
	// something is actually wrong, so 15s stays a tight, fast-failing budget.
	// ingest is different by design: Refresh and ExpandLibrary walk real
	// YouTube listings and now fetch full metadata per new video (CLAUDE.md
	// §7), so a scan or expansion can legitimately run for minutes.
	fastClient := h2cClient(15 * time.Second)
	ingestClient := h2cClient(10 * time.Minute)
	gateway := api.NewGateway(
		catalogv1connect.NewCatalogServiceClient(fastClient, catalogURL),
		recsysv1connect.NewRecommendationServiceClient(fastClient, recsysURL),
		ingestv1connect.NewIngestServiceClient(ingestClient, ingestURL),
		logger,
		devUserID,
		ingestURL,
		mediaRoot,
		configDir,
	)

	mux := http.NewServeMux()
	mux.Handle("/", gateway.Routes())

	// Served here during development. In the LAN deployment Caddy takes this
	// route over, since it handles range requests and caching better.
	mux.Handle("GET /media/", http.StripPrefix("/media/", api.MediaHandler(mediaRoot)))

	handler := withCORS(mux, webOrigin)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	go func() {
		logger.Info("gateway listening", "addr", addr, "catalog", catalogURL, "recsys", recsysURL)
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

// withCORS exists only for the Vite dev server, which runs on its own port.
// In the LAN deployment the web bundle is served from this same origin and no
// CORS header is involved at all.
func withCORS(next http.Handler, allowedOrigins string) http.Handler {
	origins := map[string]bool{}
	for _, o := range strings.Split(allowedOrigins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins[o] = true
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-User-Id")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
