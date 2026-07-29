// Separate module from the repository root on purpose.
//
// The ONNX Runtime binding needs cgo and a shared library that most machines do
// not have. Keeping it out of the root module means `make check` at the top of
// the repo keeps building the four production services without anyone having to
// install onnxruntime first.
module recsys-ml/serving

go 1.25.0

require (
	github.com/jackc/pgx/v5 v5.10.0
	github.com/yalue/onnxruntime_go v1.31.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)
