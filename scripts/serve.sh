#!/usr/bin/env bash
# Run the stack the way the household uses it: one origin, no dev server.
#
#   scripts/serve.sh          build the web bundle if it is stale, then run
#   scripts/serve.sh --build  build it unconditionally
#
# The difference from dev.sh is only what serves the app. Vite is a development
# server — it rebuilds on file changes, ships no cache headers worth the name,
# and puts the app on a second port so every request crosses an origin. None of
# that is wanted by a machine that stays on and a television that connects to
# it. The gateway serves web/dist instead, on :8180, beside /media and /api.
set -euo pipefail

cd "$(dirname "$0")/.."

DIST="$(pwd)/web/dist"

# Build when there is nothing to serve, when asked, or when a source file is
# newer than the bundle. Cheap to check and it is the fault this would otherwise
# have: the agent restarts, serves January's bundle, and nothing says so.
newer=$(find web/src web/index.html web/package.json web/vite.config.ts \
  -newer "$DIST/index.html" -print -quit 2>/dev/null || true)
if [ "${1:-}" = "--build" ] || [ ! -f "$DIST/index.html" ] || [ -n "$newer" ]; then
  echo "building the web bundle..."
  npm --prefix web run build
fi

export SERVE_BUNDLE=1
export WEB_DIST="$DIST"
exec ./scripts/dev.sh
