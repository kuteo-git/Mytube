#!/usr/bin/env bash
# Move aside a postmaster.pid left by a Postgres that is no longer running.
#
# Postgres clears its own lock when the PID in it is dead. It cannot when the
# PID is alive and belongs to something else, which is what a power cut leaves:
# macOS hands out the same low PIDs at every boot, so the old postmaster's PID
# is often some other login item's by the time Postgres asks. It then refuses
# with `lock file "postmaster.pid" already exists` every ten seconds, for ever,
# and boot.sh waits on it (2026-10-31: PID 830 was a Python server).
#
# Only that case is touched. A dead PID is left to Postgres, which may be
# rewriting the file at that moment; a live postgres is a running server.
# Moved, not deleted, so the evidence survives.
#
# Usage: pg-stale-lock.sh [data directory]
set -uo pipefail

DATA="${1:-/opt/homebrew/var/postgresql@17}"
LOCK="$DATA/postmaster.pid"

[ -f "$LOCK" ] || exit 0
pid=$(head -1 "$LOCK")
[[ "$pid" =~ ^[0-9]+$ ]] || exit 0

comm=$(ps -p "$pid" -o comm= 2>/dev/null) || exit 0
[ "$(basename "$comm")" = postgres ] && exit 0

stale="$LOCK.stale-$(date +%Y%m%d-%H%M%S)"
mv "$LOCK" "$stale"
echo "boot: postmaster.pid named PID $pid, which is now $comm; moved to $stale" >&2
