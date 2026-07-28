#!/usr/bin/env bash
# Generate playable placeholder media for every seeded video.
#
# The development seed marks videos READY without any file existing, because the
# ingest worker is not built yet. This produces short H.264 files with hardware
# encoding so the player, seeking, range requests and progress reporting can be
# exercised end to end before ingest lands.
#
#   scripts/generate_placeholder_media.sh [seconds]
#
# Delete media/ and re-run ingest once it exists; nothing here is precious.
set -euo pipefail

cd "$(dirname "$0")/.."
export PATH="/opt/homebrew/opt/postgresql@17/bin:$PATH"

DURATION="${1:-30}"
MEDIA_ROOT="${MEDIA_ROOT:-./media}"

rows=$(PGPASSWORD="${CATALOG_PASSWORD:-catalog_dev}" psql -t -A -F'|' \
  -h localhost -U catalog_svc -d localyoutube \
  -c "SELECT id, media_path FROM videos WHERE media_state = 'READY' AND media_path <> '';")

while IFS='|' read -r id media_path; do
  [ -z "$id" ] && continue
  target="$MEDIA_ROOT/$media_path"

  if [ -f "$target" ]; then
    echo "skip $id"
    continue
  fi

  mkdir -p "$(dirname "$target")"
  # h264_videotoolbox uses the M-series hardware encoder; +faststart moves the
  # moov atom to the front so the browser can start playing before the whole
  # file arrives, which is the progressive playback Phase 1 relies on.
  # -nostdin matters: without it ffmpeg drains the loop's here-string and every
  # iteration after the first gets an empty row.
  ffmpeg -nostdin -hide_banner -loglevel error -y \
    -f lavfi -i "testsrc=size=1920x1080:rate=30:duration=$DURATION" \
    -f lavfi -i "sine=frequency=440:duration=$DURATION" \
    -c:v h264_videotoolbox -b:v 2M -pix_fmt yuv420p \
    -c:a aac -b:a 96k \
    -movflags +faststart \
    "$target"
  echo "made $id -> $target"
done <<< "$rows"

echo
du -sh "$MEDIA_ROOT"
