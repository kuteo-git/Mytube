#!/usr/bin/env bash
# Stop everything dev.sh starts, however it was started.
#
#   scripts/stop.sh
#
# By port rather than by pid, because the stack does not always come up as one
# process tree. dev.sh runs its services as children and kills them on exit —
# but a service rebuilt and restarted by hand, or one moved into a tmux session
# to survive a terminal closing, is nobody's child, and there is then no single
# thing to Ctrl-C. The ports are the one description that stays true.
#
# Speech (:8002) belongs to another project and is deliberately left running.
set -uo pipefail

cd "$(dirname "$0")/.."

stopped=0
for entry in "5173:web" "8180:gateway" "8183:ingest" "8182:recsys" "8181:catalog" "8184:logview" "8005:translate" "8009:transcript"; do
  port="${entry%%:*}"
  name="${entry##*:}"
  pids=$(lsof -ti:"$port" 2>/dev/null || true)
  if [ -z "$pids" ]; then
    printf '  %-10s :%s  already stopped\n' "$name" "$port"
    continue
  fi
  # TERM first: the services close their database pools and finish the request
  # in hand. KILL is for whatever has not gone a second later.
  kill $pids 2>/dev/null || true
  stopped=$((stopped + 1))
  printf '  %-10s :%s  stopped\n' "$name" "$port"
done

sleep 1
for entry in "5173:web" "8180:gateway" "8183:ingest" "8182:recsys" "8181:catalog" "8184:logview" "8005:translate" "8009:transcript"; do
  port="${entry%%:*}"
  pids=$(lsof -ti:"$port" 2>/dev/null || true)
  [ -n "$pids" ] && kill -9 $pids 2>/dev/null || true
done

# tmux sessions outlive the process they were started for, and an empty one
# left behind makes `tmux ls` a list of things that are not running.
if command -v tmux >/dev/null 2>&1; then
  for session in yt-web yt-translate; do
    tmux kill-session -t "$session" 2>/dev/null && echo "  tmux $session closed"
  done
fi

echo
if [ "$stopped" -eq 0 ]; then
  echo "nothing was running."
else
  echo "stopped. speech on :8002 left alone — it belongs to another project."
fi
