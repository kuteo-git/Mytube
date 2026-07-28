.PHONY: proto proto-lint proto-breaking web-dev web-build check

# Regenerate Go and TypeScript from proto/. Generated code is committed so a
# fresh checkout builds without running buf.
proto:
	cd proto && buf generate

proto-lint:
	cd proto && buf lint

# Guard against wire-incompatible schema edits, compared to the committed state.
proto-breaking:
	cd proto && buf breaking --against '../.git#subdir=proto'

web-dev:
	cd web && npm run dev

web-build:
	cd web && npm run build

check: proto-lint
	cd web && npx tsc --noEmit -p tsconfig.app.json
	go build ./...
