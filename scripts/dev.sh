#!/usr/bin/env bash
# Start the whole stack for development.
#
#   scripts/dev.sh
#
# Ports: gateway 8180 (the only one the browser talks to), catalog 8181,
# recsys 8182, ingest 8183, TTS 8002, NLLB 8005, Vite 5173.
# Chosen off the 808x block because other projects on this machine hold 8080
# and 8082 permanently.
# Requires Postgres to be running:
#   brew services start postgresql@17
set -euo pipefail

cd "$(dirname "$0")/.."

export PATH="/opt/homebrew/opt/postgresql@17/bin:$PATH"
LOG_DIR="${TMPDIR:-/tmp}/local-youtube"
mkdir -p "$LOG_DIR"

# This machine has an external drive, which retires the 34 GiB internal-disk
# constraint from CLAUDE.md §1 for local development. Media, budget and
# eviction watermarks all move to reflect the real capacity available.
export MEDIA_ROOT="${MEDIA_ROOT:-/Volumes/Data2/Youtube}"
export STORAGE_BUDGET_BYTES="${STORAGE_BUDGET_BYTES:-322122547200}"    # 300 GiB
export EVICTION_HIGH_BYTES="${EVICTION_HIGH_BYTES:-375809638400}"      # 350 GiB
export EVICTION_LOW_BYTES="${EVICTION_LOW_BYTES:-322122547200}"        # 300 GiB
mkdir -p "$MEDIA_ROOT"

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
  if curl -fsS http://localhost:8180/healthz >/dev/null 2>&1; then break; fi
  sleep 0.5
done

# --- NLLB translation server (EN → VI) ---
NLLB_VENV="${NLLB_VENV:-/tmp/nllb-venv}"
if [ -x "$NLLB_VENV/bin/python" ] && [ -f services/nllb_server.py ]; then
  echo "starting NLLB translation server (port 8005)..."
  "$NLLB_VENV/bin/python" services/nllb_server.py >"$LOG_DIR/nllb.log" 2>&1 & pids+=($!)
fi

echo "catalog :8181 | recsys :8182 | ingest :8183 | gateway :8180 | logs in $LOG_DIR"
echo "starting web..."
npm --prefix web run dev
