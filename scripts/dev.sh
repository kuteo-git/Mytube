#!/usr/bin/env bash
# Start the whole stack for development.
#
#   scripts/dev.sh
#
# Ports: gateway 8180 (the only one the browser talks to), catalog 8181,
# recsys 8182, ingest 8183, logview 8184, transcript 8185, TTS 8002,
# translate 8005, Vite 5173.
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

# Where the library lives — but only as the *default*.
#
# The Storage page writes data/storage.json, and that file **wins over this
# export**. The other way round would be a trap: this line runs on every start,
# so the setting would save, survive a restart, and change nothing, with nothing
# anywhere to say why. See internal/mediaroot.
# Serve the built bundle from the gateway instead of running Vite.
#
#   SERVE_BUNDLE=1 ./scripts/dev.sh     — one origin on :8180, no dev server
#
# This is what scripts/serve.sh and the login agent use. It is a flag on this
# script rather than a second script, because everything above and below it —
# the Postgres check, the port check, the media root, the yt-dlp pin, the five
# services, the two Python servers — is the same stack either way, and a copy
# of it would be a copy that drifts.
SERVE_BUNDLE="${SERVE_BUNDLE:-}"
if [ -n "$SERVE_BUNDLE" ]; then
  export WEB_DIST="${WEB_DIST:-$(pwd)/web/dist}"
fi

export MEDIA_ROOT="${MEDIA_ROOT:-/Volumes/Data2/Youtube}"
export STORAGE_BUDGET_BYTES="${STORAGE_BUDGET_BYTES:-322122547200}"    # 300 GiB
export EVICTION_HIGH_BYTES="${EVICTION_HIGH_BYTES:-375809638400}"      # 350 GiB
export EVICTION_LOW_BYTES="${EVICTION_LOW_BYTES:-322122547200}"        # 300 GiB

# Debugging the streaming tiers: withhold the file on disk, so the player has to
# use them. Off unless asked for, and passed through rather than defaulted here
# so that leaving it on takes a deliberate act:
#
#   DEBUG_SKIP_LOCAL_TIER=1 ./scripts/dev.sh
#
# Why it is needed at all: a download lands in a median of thirteen seconds, and
# every request after that answers `local` and plays from the disk. So a fault in
# the mux or in HLS is observable for a few seconds, once, on a video nobody has
# fetched — and the act of looking is what fetches it. A morning was spent
# discovering that a stream request typed by hand had scheduled the download that
# then hid the thing it was meant to show.
#
# The gateway warns at startup and on every request it changes, because a
# forgotten flag here looks exactly like a serious bug: a library full of
# downloaded videos that all insist on streaming.
export DEBUG_SKIP_LOCAL_TIER="${DEBUG_SKIP_LOCAL_TIER:-}"

# Which yt-dlp runs. A nightly was required for a stretch, because the stable
# release of the day (2026.07.04) resolved URLs that no longer served bytes.
# That is over: 2026.8.19 is stable and postdates the nightly this was pinned
# to, so the pin goes back to a release, which is where a pin belongs — a
# nightly that upgrades itself is a stack that breaks on a morning nobody
# changed anything.
#
#   pipx install "yt-dlp==2026.8.19"
#
# Measured 2026-08-20 before switching, both binaries in one sitting, five
# videos, freshly resolved URLs, one request each at bytes=0-1048575 and
# bytes=4194304-5242879:
#
#   itag 136/137/140   206 head and middle on both, wherever the video
#                      publishes them at all — identical, video for video
#   itag 18            nightly: 206 head, 403 middle, 5 of 5
#                      stable:  not published at all, 5 of 5
#   10s of 137+140     rc=0 in 9s on both, byte-identical output
#
# So stable is equal on everything the tiers rest on, and it stops offering the
# progressive format that stopped serving — which is the answer §4 already
# reached, arriving from upstream.
#
# The nightly stays installed as the way back, and this variable is the whole
# of switching:  YTDLP_PATH=$HOME/.local/bin/yt-dlp ./scripts/dev.sh
#
# Unset, or a path that is not there, falls back to whatever is on PATH.
export YTDLP_PATH="${YTDLP_PATH:-$HOME/.local/bin/yt-dlp-stable}"

# Background traffic to YouTube. Zero switches a timer off entirely — and now
# really does, which it did not before (see cmd/ingest's envDuration).
#
# Back on. They were switched off on the belief that this address had been
# blocked, and that was wrong: metadata resolves normally and plenty of videos
# still download. What is actually happening is that YouTube refuses the *bytes*
# of some videos, stably and per video, which `unavailable_sources` already
# exists to record.
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

# --- captions helper ---
#
# YouTube refuses the caption endpoint by public address, for hours at a time,
# while videos keep playing. Nothing in the app's own requests gets past that,
# so the way out is a proxy — and this tiny Python server is what can use one,
# because youtube_transcript_api is the thing that keeps up with the shape of
# YouTube's player response.
#
# On loopback and with nothing to configure. It used to be meant for another
# machine, on the reasoning that another machine is another address; measured on
# this household's Home Assistant box, that is false. The proxy is named per
# request by the caller and is set on the Settings → Proxy screen.
#
# Not fatal when absent, like the two servers below it: captions fall back to
# yt-dlp, which is refused in the same waves but costs nothing to try.
TRANSCRIPT_VENV="${TRANSCRIPT_VENV:-$(pwd)/.venv-transcript}"
TRANSCRIPT_PORT="${TRANSCRIPT_PORT:-8185}"

# A port is not an identity, and treating it as one cost a release.
#
# This said `lsof -ti:PORT && echo "already running, leaving it alone"`, copied
# from the speech server below. That pattern is safe there — the speech server
# belongs to another project and is *meant* to be left alone — and it is wrong
# here: on :8009 another project's uvicorn app took the port while this stack was
# stopped, so the check saw something listening, said the helper was up, and
# every caption fetch got a 404 from a stranger. Captions silently stopped
# arriving and nothing anywhere named the cause.
#
# So the port moved into this app's own 818x block, and what is listening is
# asked who it is rather than assumed.
transcript_is_ours() {
  curl -sf --max-time 2 "http://127.0.0.1:$TRANSCRIPT_PORT/health" 2>/dev/null \
    | grep -q "local-mytube-transcript"
}

if lsof -ti:"$TRANSCRIPT_PORT" >/dev/null 2>&1; then
  if transcript_is_ours; then
    echo "captions helper already running on :$TRANSCRIPT_PORT, leaving it alone"
  else
    echo "PORT :$TRANSCRIPT_PORT IS HELD BY SOMETHING ELSE — captions will not work." >&2
    echo "  what is there: $(lsof -ti:"$TRANSCRIPT_PORT" | head -1 | xargs -I{} ps -o command= -p {} 2>/dev/null)" >&2
    echo "  set TRANSCRIPT_PORT to a free port and restart." >&2
  fi
elif [ -x "$TRANSCRIPT_VENV/bin/python" ]; then
  echo "starting captions helper (port $TRANSCRIPT_PORT)..."
  TRANSCRIPT_PORT="$TRANSCRIPT_PORT" "$TRANSCRIPT_VENV/bin/python" \
    docs/transcript-server/transcript_server.py \
    >>"$LOG_DIR/transcript.log" 2>&1 & pids+=($!)
else
  echo "no captions helper: create it with" >&2
  echo "  python3 -m venv $TRANSCRIPT_VENV && $TRANSCRIPT_VENV/bin/pip install -r docs/transcript-server/requirements.txt" >&2
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
for entry in "8181:catalog" "8182:recsys" "8183:ingest" "8180:gateway" "8184:logview" "8005:translate" "8002:speech" "$TRANSCRIPT_PORT:transcript"; do
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

if [ -n "$SERVE_BUNDLE" ]; then
  echo "web served by the gateway from $WEB_DIST  —  http://localhost:8180"
  echo
  # Hold the script open. Everything is a background job of this shell, so
  # exiting here would take the whole stack with it through the trap — and the
  # login agent needs exactly one process to watch.
  wait
else
  echo "starting web on :5173..."
  npm --prefix web run dev
fi
