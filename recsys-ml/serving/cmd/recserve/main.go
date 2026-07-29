// Command recserve serves recommendations over HTTP.
//
// Built without the `onnx` tag it still runs: the registry validates the
// artifacts, reports that inference is unavailable, and the service degrades to
// retrieval order or trending. That is deliberate — the fallback path is the
// one that has to work during an incident, so it is the one exercised by
// default.
//
//	go build -o recserve ./cmd/recserve             # no inference
//	go build -tags onnx -o recserve ./cmd/recserve  # with ONNX Runtime
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"recsys-ml/serving/internal/onnxmodel"
	"recsys-ml/serving/internal/recommendation"
	"recsys-ml/serving/internal/vectorstore"
)

func main() {
	var (
		addr         = flag.String("addr", ":8190", "listen address")
		artifactsDir = flag.String("artifacts", "../artifacts", "directory holding the ONNX artifacts")
		pollInterval = flag.Duration("poll", onnxmodel.DefaultPollInterval, "how often to check for new models")
		topNDefault  = flag.Int("default-top-n", 20, "results returned when the caller does not say")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	registry := onnxmodel.NewRegistry(onnxmodel.RegistryOptions{
		Dir:              *artifactsDir,
		ExpectedFeatures: recommendation.FeatureNames,
		PollInterval:     *pollInterval,
		Logger:           logger,
	})
	// A missing or broken model at startup is not fatal. Refusing to boot would
	// take the whole feed down for a problem the fallback already covers.
	if err := registry.Reload(); err != nil {
		logger.Warn("no model bundle at startup; serving the fallback until one appears",
			"dir", *artifactsDir, "error", err)
	}

	store := vectorstore.NewMemoryStore()
	if err := loadEmbeddings(context.Background(), store, *artifactsDir); err != nil {
		logger.Warn("no embeddings loaded; retrieval will be unavailable", "error", err)
	}

	service, err := recommendation.New(recommendation.Options{
		Store:    store,
		Models:   registry,
		Features: staticFeatureStore{globalMean: 0.35},
		Trending: staticTrending{},
		Logger:   logger,
	})
	if err != nil {
		logger.Error("building the recommendation service", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go registry.Watch(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		indexed, _ := store.Len(context.Background())
		bundle := registry.Current()
		writeJSON(w, http.StatusOK, map[string]any{
			"status":         "ok",
			"indexedVectors": indexed,
			"modelLoaded":    bundle != nil,
		})
	})
	mux.HandleFunc("POST /recommendations", handleRecommendations(service, *topNDefault, logger))

	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("serving", "addr", *addr, "artifacts", *artifactsDir)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

type recommendRequest struct {
	UserID       string   `json:"userId"`
	WatchHistory []string `json:"watchHistory"`
	TopN         int      `json:"topN"`
}

func handleRecommendations(
	service recommendation.Service, defaultTopN int, logger *slog.Logger,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request recommendRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "malformed body"})
			return
		}
		if request.TopN <= 0 {
			request.TopN = defaultTopN
		}

		results, err := service.GetRecommendations(
			r.Context(), request.UserID, request.WatchHistory, request.TopN,
		)
		if err != nil {
			logger.ErrorContext(r.Context(), "recommendation failed", "error", err)
			writeJSON(w, http.StatusInternalServerError,
				map[string]string{"error": "could not build recommendations"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// loadEmbeddings fills the index from the exported catalogue.
//
// Reading Parquet from Go needs a dependency this service does not otherwise
// want, so the pipeline is expected to publish a sidecar JSON for the serving
// path. Kept explicit rather than hidden: the format the index loads from is a
// contract, and a silent fallback to an empty index looks exactly like a
// working service that recommends nothing.
func loadEmbeddings(ctx context.Context, store *vectorstore.MemoryStore, dir string) error {
	path := dir + "/video_embeddings.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var rows []struct {
		VideoID           string    `json:"video_id"`
		Embedding         []float32 `json:"embedding"`
		CompletionRateAvg float32   `json:"completion_rate_avg"`
		UploadedAtUnix    int64     `json:"uploaded_at_unix"`
		CreatorID         string    `json:"creator_id"`
		CategoryID        int32     `json:"category_id"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return err
	}

	embeddings := make([]vectorstore.Embedding, 0, len(rows))
	for _, row := range rows {
		embeddings = append(embeddings, vectorstore.Embedding{
			VideoID:           row.VideoID,
			Vector:            row.Embedding,
			CompletionRateAvg: row.CompletionRateAvg,
			UploadedAtUnix:    row.UploadedAtUnix,
			CreatorID:         row.CreatorID,
			CategoryID:        row.CategoryID,
		})
	}
	slog.Info("loaded embeddings", "count", len(embeddings), "path", path)
	return store.Replace(ctx, embeddings)
}

// staticFeatureStore is a stand-in until user features are wired to a real
// store. It returns the neutral prior for every viewer, which is exactly what
// an unknown viewer should get.
type staticFeatureStore struct{ globalMean float32 }

func (s staticFeatureStore) UserFeatures(
	context.Context, string,
) (recommendation.UserFeatures, error) {
	return recommendation.UserFeatures{GlobalMeanWatchRatio: s.globalMean}, nil
}

// staticTrending is a placeholder fallback. A real one reads a periodically
// refreshed popularity table; the point of the interface is that swapping it
// changes nothing above.
type staticTrending struct{}

func (staticTrending) Trending(_ context.Context, topN int) ([]string, error) {
	ids := make([]string, 0, topN)
	for i := 0; i < topN; i++ {
		ids = append(ids, "trending_"+strconv.Itoa(i))
	}
	return ids, nil
}
