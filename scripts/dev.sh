#!/usr/bin/env bash
# Start the whole stack for development.
#
#   scripts/dev.sh
#
# Ports: gateway 8080 (the only one the browser talks to), catalog 8081,
# recsys 8082, ingest 8083, Vite 5173. Requires Postgres to be running:
#   brew services start postgresql@17
set -euo pipefail

cd "$(dirname "$0")/.."

export PATH="/opt/homebrew/opt/postgresql@17/bin:$PATH"
LOG_DIR="${TMPDIR:-/tmp}/local-youtube"
mkdir -p "$LOG_DIR"

pids=()
cleanup() {
  echo
  echo "stopping services..."
  for pid in "${pids[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

if ! pg_isready -q; then
  echo "postgres is not running: brew services start postgresql@17" >&2
  exit 1
fi

echo "building services..."
go build -o "$LOG_DIR/catalog" ./services/catalog/cmd/catalog
go build -o "$LOG_DIR/recsys" ./services/recsys/cmd/recsys
go build -o "$LOG_DIR/ingest" ./services/ingest/cmd/ingest
go build -o "$LOG_DIR/gateway" ./services/gateway/cmd/gateway

"$LOG_DIR/catalog" >"$LOG_DIR/catalog.log" 2>&1 & pids+=($!)
"$LOG_DIR/recsys"  >"$LOG_DIR/recsys.log"  2>&1 & pids+=($!)
"$LOG_DIR/ingest"  >"$LOG_DIR/ingest.log"  2>&1 & pids+=($!)
"$LOG_DIR/gateway" >"$LOG_DIR/gateway.log" 2>&1 & pids+=($!)

# The gateway depends on the other two, so wait for it rather than for itself.
for _ in $(seq 1 20); do
  if curl -fsS http://localhost:8080/healthz >/dev/null 2>&1; then break; fi
  sleep 0.5
done

echo "catalog :8081 | recsys :8082 | ingest :8083 | gateway :8080 | logs in $LOG_DIR"
echo "starting web..."
npm --prefix web run dev
