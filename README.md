# Local YouTube

A self-hosted video library for the home LAN. `yt-dlp` is the ingest tool, not a
streaming proxy: videos are downloaded once and then served from local disk.

Architecture decisions and their rationale live in [CLAUDE.md](CLAUDE.md).
UI tokens live in [design-system/local-youtube/MASTER.md](design-system/local-youtube/MASTER.md).

## Layout

```
proto/          Schemas. The single source of truth for every service boundary.
gen/go/         Generated Go (connect-go). Committed.
services/
  catalog/      Videos, channels, comments, per-user interaction, storage.
  recsys/       Ranking only. Returns ordered ids, never metadata.
  ingest/       yt-dlp: discovery, upstream stream resolution, download queue.
  gateway/      The only origin the browser talks to. REST out, ConnectRPC in.
web/            Vite + React client.
db/             Bootstrap and development seeds.
```

Ports: gateway `8180`, catalog `8181`, recsys `8182`, ingest `8183`, Vite `5173`.

## First run

```bash
brew services start postgresql@17

psql -d postgres -f db/bootstrap.sql
PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -f services/catalog/migrations/0001_init.sql
PGPASSWORD=recsys_dev  psql -h localhost -U recsys_svc  -d localyoutube -f services/recsys/migrations/0001_init.sql
PGPASSWORD=ingest_dev  psql -h localhost -U ingest_svc  -d localyoutube -f services/ingest/migrations/0001_init.sql

# Then every later migration in each service's migrations/ directory, in order.
# They are plain files applied by hand; there is no migration runner.
for f in services/*/migrations/0*.sql; do echo "$f"; done

# Optional: sample library so the UI has something to show
PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -f db/seed_dev.sql
PGPASSWORD=recsys_dev  psql -h localhost -U recsys_svc  -d localyoutube -f db/seed_dev_recsys.sql

cd web && npm install && cd ..
```

## Day to day

```bash
scripts/dev.sh     # builds and starts the whole stack, then Vite in the foreground
scripts/stop.sh    # stops it again, however it was started
make check         # buf lint + tsc + go build
make proto         # regenerate after editing a .proto
```

`dev.sh` starts catalog, recsys, ingest, gateway, the translation sidecar and —
if it can find it — the speech server from the `robot-esp32` repository, then
prints which ports actually came up before handing the terminal to Vite.

It refuses to start when any of those ports is already held, rather than
half-starting on top of a stack that is already running. `stop.sh` is the way
out of that: it works by port, so it stops services however they were launched
— as children of `dev.sh`, by hand after a rebuild, or inside tmux.

Speech (`:8002`) belongs to another project. `dev.sh` starts it if it is not
already running and leaves it alone if it is; `stop.sh` never stops it.

### Health check

After making any service changes, verify the stack is healthy:

```bash
scripts/check.sh
```

This confirms all 4 services are listening, `MEDIA_ROOT` is set on the gateway,
media files serve correctly, the feed returns videos, ingest is reachable, and
Postgres is accepting connections.

### Restarting individual services

After rebuilding a service binary, restart it manually (outside `scripts/dev.sh`):

```bash
# catalog
MEDIA_ROOT=/Volumes/Data2/Youtube nohup /tmp/local-youtube/catalog > /tmp/local-youtube/catalog.log 2>&1 &

# recsys
nohup /tmp/local-youtube/recsys > /tmp/local-youtube/recsys.log 2>&1 &

# ingest
MEDIA_ROOT=/Volumes/Data2/Youtube nohup /tmp/local-youtube/ingest > /tmp/local-youtube/ingest.log 2>&1 &

# gateway (MUST include MEDIA_ROOT or /media paths 404)
MEDIA_ROOT=/Volumes/Data2/Youtube nohup /tmp/local-youtube/gateway > /tmp/local-youtube/gateway.log 2>&1 &
```

## Key APIs

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/feed?pageSize=24` | GET | Homepage feed (ranked, paginated) |
| `/api/videos/{id}/stream` | GET | Playback sources (local / instant / remux) |
| `/api/videos/{id}/download` | POST | Enqueue background download |
| `/api/videos/{id}/pinned` | POST | Toggle "Keep" (pin) |
| `/api/search?q=...` | GET | Search (local library + YouTube) |
| `/api/topics/refresh` | POST | Force a scan now (resets the 1-hour interval) |
| `/api/topics/backfill` | POST | Fill in missing metadata (topics + published_at) |
| `/api/topics/backfill` | GET | Backfill progress (running, examined, updated, failed) |
| `/api/history` | GET | Watched history (infinite scroll) |
| `/api/pinned` | GET | Pinned (kept) videos |
| `/api/ingest/jobs` | GET | Download queue status |
| `/api/ingest/storage` | GET | Disk usage and eviction candidates |

### Backfill

Videos discovered via flat-listing scans arrive without a `published_at` date
or a YouTube category (topic). The backfill fetches full metadata one video
at a time and writes both fields back:

```bash
# Fill 200 videos at a time (default). Takes ~55 min for 800+ videos.
curl -X POST http://localhost:8180/api/topics/backfill

# Or with a custom limit
curl -X POST 'http://localhost:8180/api/topics/backfill?limit=50'

# Check progress
curl http://localhost:8180/api/topics/backfill
```

Rate-limited to 1 thread with 4 seconds between calls. Resumable: run again and
it picks up whatever the last pass did not finish. Auto-stops after 15 consecutive
failures to avoid prolonging a YouTube rate-limit block.

Videos that already have both topics and a published date are skipped, so
re-running the backfill is safe and cheap (one listing call to confirm nothing
needs work).

## Playback model

A video that is not on disk yet is still watchable. `GET /api/videos/{id}/stream`
lists every playable source right now instead of picking one:

| Source | Description | Seek |
|---|---|---|
| `local` | File on disk, served with range requests | yes |
| `instant` | YouTube progressive URL (itag 18, 360p) | yes |
| `remux` | ffmpeg muxing adaptive streams to fMP4 (1080p) | re-open stream |

The player climbs the tiers automatically: play `instant` in ~17ms, load `remux`
1080p in a hidden element and swap when ready, switch to `local` when the
download finishes. No transcode — `-c copy` throughout.

## Service boundaries

1. **No service reads another service's tables.** Each owns a Postgres schema
   with its own role, and the roles have no cross-schema grants.
2. **Ranking and data are separate.** `recsys.GetFeed` returns ordered video ids
   plus a reason; the gateway hydrates them with `catalog.BatchGetVideos`, which
   preserves the requested order. Catalog never ranks; recsys never stores titles.

## Ranking

The homepage feed hard-filters videos older than 1 year (`maxPublishedAgeDays = 365`).
When `published_at` is unknown (flat-listing scans don't return it), `added_at`
is used as a fallback. Run the backfill to populate real dates.

Within the 1-year window the feed mixes: 30% never-watched, 25% recently added,
20% subscribed channels, 15% continue-watching, 10% rewatched — with channel
diversity (max 3 videos per channel per 24-slot window).

## Status

Working: scan from `topics.yaml` and subscriptions (1-hour interval), feed and
search with infinite scroll, playback with autoplay and subtitles, background
download with progress, search that reaches YouTube alongside the library,
history/saved/storage pages, eviction sweep, hard filter for >1yr videos on
the homepage, backfill for missing metadata.

See [CLAUDE.md](CLAUDE.md) for the full development history, known traps, and
Phase 2/3 plans.