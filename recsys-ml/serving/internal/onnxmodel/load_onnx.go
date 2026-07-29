//go:build onnx

// Real inference, built with `-tags onnx`. Requires ONNX Runtime on the machine:
//
//	brew install onnxruntime
//	export ONNXRUNTIME_LIB_PATH=$(brew --prefix onnxruntime)/lib/libonnxruntime.dylib
package onnxmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var initOnce struct {
	sync.Once
	err error
}

// initRuntime starts ONNX Runtime exactly once per process.
//
// The library is global and initialising it twice is an error, so this must not
// be tied to a bundle's lifetime: bundles come and go on every hot reload, and
// the runtime outlives all of them.
func initRuntime() error {
	initOnce.Do(func() {
		if path := os.Getenv("ONNXRUNTIME_LIB_PATH"); path != "" {
			ort.SetSharedLibraryPath(path)
		}
		initOnce.err = ort.InitializeEnvironment()
	})
	return initOnce.err
}

// onnxRanker runs ranker.onnx.
//
// LightGBM converts to a graph taking one float tensor of shape [batch,
// features]. Scoring a whole candidate set in a single call rather than
// per-candidate is the difference between one graph execution and several
// hundred; the per-call overhead dominates everything else at this size.
type onnxRanker struct {
	mu       sync.Mutex
	session  *ort.DynamicAdvancedSession
	features int
}

func (r *onnxRanker) Score(ctx context.Context, rows [][]float32) ([]float32, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	flat := make([]float32, 0, len(rows)*r.features)
	for i, row := range rows {
		if len(row) != r.features {
			return nil, fmt.Errorf(
				"onnxmodel: row %d has %d features, model expects %d",
				i, len(row), r.features,
			)
		}
		flat = append(flat, row...)
	}

	inputShape := ort.NewShape(int64(len(rows)), int64(r.features))
	input, err := ort.NewTensor(inputShape, flat)
	if err != nil {
		return nil, fmt.Errorf("onnxmodel: input tensor: %w", err)
	}
	defer input.Destroy()

	outputShape := ort.NewShape(int64(len(rows)), 1)
	output, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return nil, fmt.Errorf("onnxmodel: output tensor: %w", err)
	}
	defer output.Destroy()

	// A DynamicAdvancedSession is not safe for concurrent Run calls. One lock
	// per session is cheaper than one session per request; LightGBM inference
	// on a few hundred rows is microseconds, so contention never becomes the
	// bottleneck.
	r.mu.Lock()
	err = r.session.Run([]ort.Value{input}, []ort.Value{output})
	r.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("onnxmodel: run ranker: %w", err)
	}

	scores := make([]float32, len(rows))
	copy(scores, output.GetData())
	return scores, nil
}

// Load reads a bundle from an artifacts directory.
func Load(dir string, expectedFeatures []string) (*Bundle, error) {
	if err := initRuntime(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	spec, err := LoadFeatureSpec(dir)
	if err != nil {
		return nil, err
	}
	if err := spec.Validate(expectedFeatures); err != nil {
		return nil, err
	}

	rankerPath := filepath.Join(dir, RankerFile)
	session, err := ort.NewDynamicAdvancedSession(
		rankerPath,
		[]string{"input"},
		[]string{"variable"},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("onnxmodel: open %s: %w", rankerPath, err)
	}

	fingerprint, err := Fingerprint(dir)
	if err != nil {
		_ = session.Destroy()
		return nil, err
	}

	return &Bundle{
		Spec:        spec,
		Ranker:      &onnxRanker{session: session, features: len(spec.RankingFeatures)},
		Fingerprint: fingerprint,
		closer:      session.Destroy,
	}, nil
}
