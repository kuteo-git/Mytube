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

Ports: gateway `8080`, catalog `8081`, recsys `8082`, ingest `8083`, Vite `5173`.

## First run

```bash
brew services start postgresql@17

psql -d postgres -f db/bootstrap.sql
PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -f services/catalog/migrations/0001_init.sql
PGPASSWORD=recsys_dev  psql -h localhost -U recsys_svc  -d localyoutube -f services/recsys/migrations/0001_init.sql
PGPASSWORD=ingest_dev  psql -h localhost -U ingest_svc  -d localyoutube -f services/ingest/migrations/0001_init.sql

# Optional: sample library so the UI has something to show
PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -f db/seed_dev.sql
PGPASSWORD=recsys_dev  psql -h localhost -U recsys_svc  -d localyoutube -f db/seed_dev_recsys.sql

cd web && npm install && cd ..
```

## Day to day

```bash
scripts/dev.sh     # builds and starts catalog, recsys, gateway, then Vite
make check         # buf lint + tsc + go build
make proto         # regenerate after editing a .proto
```

## Service boundaries

Two rules make the split real rather than decorative:

1. **No service reads another service's tables.** Each owns a Postgres schema
   with its own role, and the roles have no cross-schema grants. Recsys keeps
   its own copy of behaviour signals instead of reading catalog's history.
2. **Ranking and data are separate.** `recsys.GetFeed` returns ordered video ids
   plus a reason; the gateway hydrates them with `catalog.BatchGetVideos`, which
   preserves the requested order. Catalog never ranks; recsys never stores
   titles.

Everything internal speaks ConnectRPC, so any service can be exercised with
curl:

```bash
curl -X POST http://localhost:8081/catalog.v1.CatalogService/GetVideo \
  -H 'Content-Type: application/json' -d '{"videoId":"v1","userId":"u_luc"}'
```

## Playback model

A video that is not on disk yet is still watchable. `GET /api/videos/{id}/stream`
answers with one of two things:

- `local` — the file under `MEDIA_ROOT`, served with range requests.
- `upstream` — a short-lived URL resolved through yt-dlp, used while the
  background download runs.

Only progressive (muxed) formats can be resolved for instant playback, because a
bare `<video>` element cannot play adaptive streams. In practice that means the
first watch is lower quality than the copy that lands afterwards; the player
labels it. Once the download finishes the endpoint returns `local` and upstream
is never touched again for that video.

## Status

Working end to end: search YouTube from inside the app, queue a download, watch
progress, and play the result. Ranking reacts to a watch within one request.

Not yet built: the identity service (the gateway trusts an `X-User-Id` header
and falls back to a seeded account), automatic channel following, LRU eviction
enforcement, and Caddy in front for TLS on the TV.

`db/seed_dev.sql` inserts sample rows whose ids are not real YouTube ids, so
those entries cannot resolve an upstream stream. `scripts/generate_placeholder_media.sh`
creates playable files for them.
