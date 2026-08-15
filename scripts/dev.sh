#!/usr/bin/env bash
# Start the whole stack for development.
#
#   scripts/dev.sh
#
# Ports: gateway 8180 (the only one the browser talks to), catalog 8181,
# recsys 8182, ingest 8183, TTS 8002, translate 8005, Vite 5173.
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
# Keys and endpoints for local services live outside git. Sourced before
# anything starts: the gateway reads OMNIROUTE_* as the fallback for its
# translation settings, and sourcing this after it had launched left that
# fallback empty — settings appeared blank the moment the saved file was removed.
# Absent is fine; a service without its configuration reports itself unavailable.
# shellcheck disable=SC1091
[ -f .env.local ] && . ./.env.local

export MEDIA_ROOT="${MEDIA_ROOT:-/Volumes/Data2/Youtube}"
export STORAGE_BUDGET_BYTES="${STORAGE_BUDGET_BYTES:-322122547200}"    # 300 GiB
export EVICTION_HIGH_BYTES="${EVICTION_HIGH_BYTES:-375809638400}"      # 350 GiB
export EVICTION_LOW_BYTES="${EVICTION_LOW_BYTES:-322122547200}"        # 300 GiB
mkdir -p "$MEDIA_ROOT"

pids=()
cleanup() {
  # Nothing started yet is the ordinary case for every failure above: the
  # Postgres check and the port check both exit before anything runs, and
  # under `set -u` an empty array is an unset variable rather than an empty
  # one — so without this guard a clean refusal ends in a shell error.
  [ "${#pids[@]}" -eq 0 ] && return
  echo
  echo "stopping services..."
  for pid in "${pids[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}

if ! pg_isready -q; then
  echo "postgres is not running: brew services start postgresql@17" >&2
  exit 1
fi

# Refuse to start on top of a stack that is already running.
#
# Without this the failures are silent and misleading, both of which happened:
# a Go service prints "address already in use" into a log nobody is tailing and
# this script carries on as though it had started, while Vite quietly moves to
# the next free port and announces it in a line of startup output nobody reads.
# The result looks like the application being broken rather than like two
# copies of it running.
#
# Vite is checked here as well as by strictPort, because by the time Vite runs
# the four services have already started and stopping them again is worse than
# not starting.
busy=""
for port in 8180 8181 8182 8183 8184 5173; do
  if pid=$(lsof -ti:"$port" 2>/dev/null | head -1); then
    busy+=$'\n'"  :$port held by pid $pid ($(ps -o comm= -p "$pid" 2>/dev/null | xargs basename 2>/dev/null))"
  fi
done
if [ -n "$busy" ]; then
  echo "some of the stack is already running:$busy" >&2
  echo >&2
  echo "stop it first — scripts/stop.sh — or leave it alone." >&2
  exit 1
fi

# Registered only now, once the checks that can refuse have passed. There is
# nothing to clean up before this line.
trap cleanup EXIT INT TERM

echo "building services..."
go build -o "$LOG_DIR/catalog" ./services/catalog/cmd/catalog
go build -o "$LOG_DIR/recsys" ./services/recsys/cmd/recsys
go build -o "$LOG_DIR/ingest" ./services/ingest/cmd/ingest
go build -o "$LOG_DIR/gateway" ./services/gateway/cmd/gateway
go build -o "$LOG_DIR/logview" ./services/logview/cmd/logview

# Logs are appended to, not overwritten.
#
# They used to be truncated on every start, which threw away the lines written
# immediately before somebody restarted the stack — and those are reliably the
# ones being looked for. A marker separates one run from the next; logview draws
# it as a divider.
#
# Trimmed rather than rotated: a log past the ceiling keeps its most recent half
# and loses its oldest, which is the right half to lose and needs no second file
# to go looking in.
LOG_CEILING_BYTES="${LOG_CEILING_BYTES:-52428800}"   # 50 MiB
for name in catalog recsys ingest gateway logview translate tts; do
  file="$LOG_DIR/$name.log"
  [ -f "$file" ] || continue
  size=$(wc -c <"$file" | tr -d ' ')
  if [ "$size" -gt "$LOG_CEILING_BYTES" ]; then
    tail -c "$((LOG_CEILING_BYTES / 2))" "$file" >"$file.trimmed" && mv "$file.trimmed" "$file"
    echo "  trimmed $name.log ($((size / 1048576))MB)"
  fi
  echo "--- restart $(date +%Y-%m-%dT%H:%M:%S%z) ---" >>"$file"
done

"$LOG_DIR/catalog" >>"$LOG_DIR/catalog.log" 2>&1 & pids+=($!)
"$LOG_DIR/recsys"  >>"$LOG_DIR/recsys.log"  2>&1 & pids+=($!)
"$LOG_DIR/ingest"  >>"$LOG_DIR/ingest.log"  2>&1 & pids+=($!)
"$LOG_DIR/gateway" >>"$LOG_DIR/gateway.log" 2>&1 & pids+=($!)
# The log viewer, on a port of its own. Deliberately not part of the gateway:
# the moment logs are wanted is the moment something has stopped working.
"$LOG_DIR/logview" >>"$LOG_DIR/logview.log" 2>&1 & pids+=($!)

# The gateway depends on the other two, so wait for it rather than for itself.
for _ in $(seq 1 20); do
  if curl -fsS http://localhost:8180/healthz >/dev/null 2>&1; then break; fi
  sleep 0.5
done

# --- translation server (EN → VI), NLLB and Qwen ---
NLLB_VENV="${NLLB_VENV:-$(pwd)/.venv-nllb}"
if [ -x "$NLLB_VENV/bin/python" ] && [ -f services/translate_server.py ]; then
  echo "starting translation server (port 8005)..."
  "$NLLB_VENV/bin/python" services/translate_server.py >>"$LOG_DIR/translate.log" 2>&1 & pids+=($!)
else
  echo "no translation server: narration will not translate" >&2
fi

# --- speech, for narration ---
#
# Lives in another repository, so this only starts it if that repository is
# where it is expected to be. Started here anyway because the alternative is
# what has been happening: the player asks for speech, gets nothing, and the
# reason is a service somebody forgot to start in a second terminal.
#
# Not fatal when absent. Narration is one feature; the library is the app.
TTS_SERVER="${TTS_SERVER:-$HOME/Documents/git/robot-esp32/services/vieneu_server.py}"
if lsof -ti:8002 >/dev/null 2>&1; then
  echo "speech already running on :8002, leaving it alone"
elif [ -f "$TTS_SERVER" ]; then
  echo "starting speech server (port 8002)..."
  python3 "$TTS_SERVER" >>"$LOG_DIR/tts.log" 2>&1 & pids+=($!)
else
  echo "no speech server at $TTS_SERVER: narration will be silent" >&2
fi

# What actually came up, asked rather than assumed.
#
# Every line above starts something in the background and none of them can fail
# visibly: a service that exits immediately leaves this script printing a list
# of ports as though they were all listening.
sleep 1
echo
for entry in "8181:catalog" "8182:recsys" "8183:ingest" "8180:gateway" "8184:logview" "8005:translate" "8002:speech"; do
  port="${entry%%:*}"
  name="${entry##*:}"
  if lsof -ti:"$port" >/dev/null 2>&1; then
    printf '  %-10s :%s  up\n' "$name" "$port"
  else
    printf '  %-10s :%s  DOWN — see %s/%s.log\n' "$name" "$port" "$LOG_DIR" "$name"
  fi
done
echo
echo "logs in $LOG_DIR  —  and on http://localhost:8184"
echo "starting web on :5173..."
npm --prefix web run dev
