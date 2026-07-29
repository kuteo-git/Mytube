//go:build !onnx

package onnxmodel

import "fmt"

// Load reports that this build cannot run inference.
//
// Built without `-tags onnx`, so ONNX Runtime is not linked in. The service is
// expected to carry on and serve its fallback: a recommender that refuses to
// start because a model is missing is worse than one that returns trending
// videos, and making that the default build keeps the fallback path honest
// instead of leaving it to be discovered during an incident.
//
// The feature spec is still read and validated, so a broken export is caught
// here rather than after ONNX Runtime is installed.
func Load(dir string, expectedFeatures []string) (*Bundle, error) {
	spec, err := LoadFeatureSpec(dir)
	if err != nil {
		return nil, err
	}
	if err := spec.Validate(expectedFeatures); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf(
		"%w: built without the onnx tag; rebuild with `go build -tags onnx`", ErrUnavailable,
	)
}
