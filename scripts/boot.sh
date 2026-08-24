#!/usr/bin/env bash
# Start the stack at login, under launchd.
#
# Not dev.sh directly: launchd starts things the moment the session exists,
# which is before Postgres is listening and — the one that actually bites —
# before /Volumes/Data2 has mounted. A stack started against an unmounted
# media root is §8 risk 1 happening on every reboot, quietly.
#
# Installed by scripts/install-agent.sh.
set -uo pipefail

cd "$(dirname "$0")/.."

# The library's disk. Waited on rather than assumed: an external volume mounts
# seconds after login, and MEDIA_ROOT's parent existing is what every service
# checks. Give up after five minutes rather than holding a login agent for ever.
ROOT="${MEDIA_ROOT:-/Volumes/Data2/Youtube}"
for _ in $(seq 1 150); do
  [ -d "$ROOT" ] && break
  sleep 2
done
[ -d "$ROOT" ] || echo "boot: $ROOT never appeared, starting anyway" >&2

# Postgres is its own launch agent (homebrew.mxcl.postgresql@17) and has no
# ordering relationship with this one, so ask rather than assume.
export PATH="/opt/homebrew/opt/postgresql@17/bin:$PATH"
for _ in $(seq 1 60); do
  pg_isready -q && break
  sleep 2
done

exec ./scripts/serve.sh
