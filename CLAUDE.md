# Local YouTube — Project Charter

> Source of truth for every architectural decision. If a new decision contradicts this file, update this file rather than quietly going another way.
> Long-form history and debug narrative has been moved to `CLAUDE-history.md`.

## 1. What the system is

A **self-hosted** media library running on a Mac M4 at home. `yt-dlp` is an **ingest tool**, not a realtime YouTube proxy — a video is fetched to disk once and then served from the LAN.

- 2–5 users in one household (light multi-user, no public sign-up)
- Final target: watching in a **Smart TV browser**; Phase 1 builds desktop web first
- Reference layout: `Example/home.png`, `Example/play.png` (YouTube desktop)

## 2. Hard constraints

| | |
|---|---|
| Disk | External SSD at `/Volumes/Data2/Youtube`, 437 GiB free. `MEDIA_ROOT`/`STORAGE_BUDGET_BYTES`/`EVICTION_HIGH_BYTES`/`EVICTION_LOW_BYTES` point there via `scripts/dev.sh` (budget 300 GiB, sweep 350→300 GiB). |
| Machine | Apple M4, 10 cores |
| Installed | `ffmpeg`, `yt-dlp`, `go`, `node`, `python3` |
| Not installed | `docker`, `postgres`, `redis` (Postgres via Homebrew) |
| Network | The Mac must stay on; needs a static LAN IP + HTTPS (internal cert) for the TV to play |

## 3. Architecture

| Item | Decision |
|---|---|
| Model | Self-hosted library (NOT a stream proxy to YouTube) |
| Topology | **Real microservices**: `identity` · `catalog` · `ingest` · `recsys` + **API Gateway** |
| Language | **Go throughout**, using [`lrstanley/go-ytdlp`](https://github.com/lrstanley/go-ytdlp) |
| Transport | **ConnectRPC + protobuf** (`buf` codegen). The gateway is the **only** place that speaks REST/JSON outward |
| DB | **One Postgres instance**, each service with **its own schema + its own DB user** |
| Queue | Postgres table + `SELECT … FOR UPDATE SKIP LOCKED` |
| Serving | **Caddy** reverse proxy: TLS + serving `/media` statically; everything else proxied to the gateway |
| Media URLs | Unprotected (the LAN is trusted) |
| CDN | **Out of scope** — meaningless on a LAN |

### Immovable rules

1. **No service queries another service's database.** The boundary is enforced by DB permissions, not by a promise. Breaking it makes this a distributed monolith.
2. **Clean architecture is about the direction of dependencies**, not the number of processes. `domain` imports no DB, HTTP or framework.
3. The ingest worker runs in **its own process** (ffmpeg/yt-dlp block; sharing would stall the API).

## 4. Media pipeline

`GET /api/videos/{id}/stream` **lists** every source that can play right now. The player chooses.

| Source | What it is | Seek |
|---|---|---|
| `local` | File on disk | ✅ |
| `instant` | YouTube raw progressive URL (itag 18, 360p), range-requested by the browser | ✅ |
| `remux` | Two adaptive tracks muxed directly into fMP4, 1080p | ⚠️ reopen stream |

The player climbs tiers: `instant` starts immediately → a hidden `remux` element prepares at `position + 20s` (5s after a seek) → handover when the playhead reaches it → `local` once the download lands. No transcoding, no HLS.

### Remux rules

- ffmpeg flags for correct A/V alignment: `-avoid_negative_ts make_zero -muxdelay 0 -muxpreload 0 -frag_duration 1000000 -movflags frag_keyframe+empty_moov+default_base_moof`.
- When opening at `T > 0`, `ProbeKeyframe` finds the actual video keyframe `K` (ffmpeg seeks to the nearest keyframe at or before the mark). The audio input is seeked to `K`, the video to `T`, so both tracks share the same content origin. The stream reports `audioAt = K`; the player uses `K` as the offset.
- Do not seek both inputs to `K`; ffmpeg would step back to the previous keyframe.
- fMP4 through a pipe has no index, so seeking means reopening the stream with `?t=<seconds>`. The seek bar is enabled on every tier.
- A handover record carries the URL it was made for; the player rejects records that do not match the element's current `src`.
- If the remux cannot open within ~20s on Auto, the tier is abandoned for that video.
- Pinning a tier is an order; it does not climb or drop on its own.

### General playback rules

- `?prefetch=1` resolves and caches the stream URL; it **does not** queue a download.
- First viewing is 360p by design; the player labels which tier it is on.
- Cancelled downloads use `NoPart()` + `NoContinue()`: partial data is discarded so a resume does not ask YouTube for an impossible range.

### Eviction

Every video has `last_accessed_at` + `pinned`. Above the high-water mark, the **media file** of the least-recently-used unpinned video is deleted, keeping metadata + thumbnail + history. The UI shows "Removed — press to fetch again". Thresholds are set by `EVICTION_HIGH_BYTES`/`EVICTION_LOW_BYTES`.

### Files that disappear without being evicted

`handleStream` stats the file before offering `local`:

| Disk state | Result |
|---|---|
| File exists | `local` offered |
| Root present, file gone | `SetMediaState(EVICTED)`, fall through to `instant`/`remux`, queue repair with `repaired: true` |
| Root unreadable | Return `streamError`; write nothing |

Order matters: check the root first. A loose cable must not mark the whole library evicted.

### Phase 2 upgrade

Also fetch the 720p rendition YouTube already publishes and remux `-c copy` into HLS → real ABR at ≈ 0 CPU. "No transcoding" ≠ "no ABR"; the expensive thing is re-encoding.

## 4b. Code conventions

- **All source code, identifiers, comments, commit messages and in-app UI copy MUST be in English.** Vietnamese is allowed only as *content data* (e.g. a genuine video title). Chat/discussion happens in Vietnamese; the artifacts do not.
- Go: standard layout per service — `cmd/`, `internal/domain`, `internal/usecase`, `internal/adapter`.
- Frontend: feature-sliced. `ui/` never calls `fetch` directly.

## 5. Frontend

**Vite + React + TypeScript + plain Tailwind + TanStack Query.**

- **No shadcn/ui** — components are built against design tokens taken from `Example/*.png`.
- Use the `ui-ux-pro-max` skill when designing or building UI.

### Feature-sliced structure

```
src/features/<feature>/
  domain/          # entities and plain types, NO React import
  application/     # use cases: hooks calling repositories, knows no HTTP
  infrastructure/  # repository implementations: call the gateway's REST API
  ui/              # components
```

`ui/` never calls `fetch` directly. That is what makes `/tv` (Phase 3) a rewrite of the `ui/` layer only.

### UI principle: RENDER NO DEAD BUTTONS

Every element either does something real or is dropped.

| YouTube's original | Here |
|---|---|
| Create (+) | → "Add video" (ingest entry point) |
| Notifications + badge | → ingest events |
| Downloads | → Storage |
| Explore / chip filter | → driven by real tags and categories in the catalog |
| Your videos, YT Music, YT Kids, footer, Shorts, Live | **DROPPED** |
| Subscribe | Real in P1: adds the channel as a live ingest source |
| Share | → the YouTube link (`video.sourceUrl` or `https://www.youtube.com/watch?v=<id>`) |

### Player behavior

- Two stacked `<video>` elements. The new source is prepared hidden and swapped in at `now + 0.6s`.
- Autoplay: if blocked, stay on the first frame; do not self-mute.
- Controls hide after 3s (mouse) / 5s (finger). Mouse click = play/pause; touch tap = show/hide controls.
- Touch bar trims the volume slider and collapses subtitles/quality/narration/autoplay into a ⚙ button. The ⚙ panel portals to `document.body`.
- Fullscreen/PiP: prefer `webkit*` APIs where they exist.
- Mute preference is written only by the mute button and volume slider, not from `volumechange`.
- Leaving fullscreen on iOS may pause the video; restore playback state on exit.
- A finished/unfinished video state is persisted locally (`last-watched.ts`). Reopening the app offers the miniplayer paused.

### Mobile navigation

- On phones, the watch screen is a layer over the previous tab; pulling it down reveals the tab underneath.
- Scroll lives on `<main>`, not `window`, to keep browser chrome stable.
- Tab bar: Home · Subscriptions · History · Settings. Storage and Activity live at the top of Settings.
- `/subscriptions` lists channels; tapping one opens `/channel/:id`.
- Settings panels have their own screens on mobile (`/settings/feed`, `/settings/narration`, `/settings/translation`).

### Search & ingest behavior

- **Search** always asks YouTube alongside the library. Results split into "In your library" / "On YouTube". Autocomplete from local data only.
- **Pasted links** are fetched, not searched. The catalog is asked first by id; a hit needs no upstream call.
- Subtitles are fetched before the media file. The subtitle pass runs on play, never on prefetch.
- `/activity` shows the download queue and scan history.
- The scanner runs every hour (`SCAN_INTERVAL`). It uses flat listings; RSS fills in missing `published_at` and `viewCount` gaps without overwriting existing values.
- A **second, fast pass** (`SUBSCRIBED_SCAN_INTERVAL`, default 5 min) reads RSS only, for subscribed channels only, and upserts uploads published within 48h. Metadata only — it queues no downloads and requests no listings, which is what makes it affordable at that rate. It exists because an hour is the whole of how late a followed channel's upload can be.
- Feed and search use `useInfiniteQuery` with a real "Load more" button.

## 6. Recommendation

**Heuristic, NOT ML.** ~150 videos and 5 users mean collaborative filtering returns nonsense, and embeddings beat tag matching by nothing while being impossible to debug.

### Feed mix

`/settings` → "Home feed" has three adjustable sliders totaling 100, dividing whatever the fixed shares leave (72% by default):

| Slider | Meaning | Default |
|---|---|---|
| Channels you follow | `profile.Subscribed[channel]` | 25% |
| More of what you watch | not subscribed, `combinedAffinity ≥ 0.15` | 60% |
| Something new | not subscribed, below threshold | 15% |

The remaining 28% is fixed: continue watching 10% + rewatch 8% + **new from followed channels 10%**.

The last of those (`slotFreshSubscribed`: subscribed channel, published within `freshnessWindow`) is fixed for the same reason as the other two — it is not a taste. It is also the one slot exempt from the per-channel cap, because a channel that posted twice this morning is not the thing that cap was written to stop.

- `feedSlot` is separate from `Reason`.
- Zero means gone; those videos go to the end.
- Buckets are ordered by **softmax sampling on the score** (`exp(score/T)`, Gumbel top-k, `softmaxTemperature = 0.6`), never uniformly shuffled. A uniform shuffle makes `freshnessBoost` and `penaltyImpression` inert and the score decorative.
- Sampling is confined to the best `samplePoolSize = 5 × quotaWindow` of each bucket. Gumbel noise grows like `log N`, so over the whole bucket a library of a few thousand always floats something worthless to the front — measured as 8 of the first 24 scoring below zero. Capping the pool makes the behaviour independent of library size; lowering the temperature alone does not.
- The mix must match the shape of the library, not just taste. Here nearly everything comes from subscribed channels, so the `affinity` slot holds ~25 videos against 3,400 subscribed — a 60% affinity share spent half the page scraping that bucket's floor. Check bucket sizes with `/api/feed/explain` before tuning the sliders.
- Stored at the gateway (`data/feed-mix.json`), sent in `GetFeedRequest.mix`. Recsys holds no config.
- Default mix is **60/20/20**. It was 25/60/15 — the split the fixed quota it replaced produced — which on this library gave 60% of the page to a bucket holding 25 videos. A default that leads somewhere bad is worse than none, because it is where the reset button sends you.
- The fixed shares are **sent to the browser**, never hard-coded there. The page carried its own copy once and spent a release quoting 82% after the fresh-subscribed share took ten of it.
- One mix for the household.
- Saving drops the `['feed']` cache.

### Scoring signals

| Signal | How computed | Weight |
|---|---|---|
| Continue watching | `0.02 < fraction <= 0.95` | +3.0 |
| Never opened | absent from `WatchedFraction` | +1.5 |
| Opened and abandoned | `fraction <= 0.02` | −2.5 |
| Subscribed channel | | +1.2 |
| Recently added | `exp(-days/14)` | ×1.0 |
| Channel affinity (watching) | Σ fraction per channel, normalised | ×1.0 |
| Topic affinity (watching) | Σ fraction per topic, normalised | ×1.0 |
| Affinity from likes | topic 1.0 / channel 0.8 / hashtag 0.5 | ×2.0 |
| Affinity from dislikes | same axes, decaying 90-day half-life | ×−0.7 |
| Global retention | `avg(max(fraction) per viewer)` | ×1.5 |
| Shown in last 24h | | −2.0 |
| Session intent | affinity over the last ≤3 videos watched in 2h | blended 50/50 into `combinedAffinity` |
| Never shown to anyone | `exp(-shown_count/3)`, **discovery slot only** | ×1.5 |

Key rules:
- `BOUNCED` is its own Reason.
- Affinity is read from watch history, not likes alone.
- A 3-hour `RecentlyWatched` window with a large penalty breaks the two-video loop.
- `applyChannelDiversity`: at most 3 videos per channel in each window of 24.
- Retention is computed in recsys, not catalog.

### Likes / dislikes

- Likes add affinity on topic 1.0 / channel 0.8 / hashtag 0.5, accumulated.
- Dislikes remove the video from feed and up-next; it remains reachable through search and channel pages.
- Dislike affinity mirrors likes at −0.7 vs +2.0, with a 90-day half-life.
- Channel suppression needs ≥3 disliked videos **and** ≥30% of that channel's videos, with a ceiling of ≥8. Subscribed channels are immune.

### Up-next

Reversed 2026-07-29: subject leads, channel is one way of sharing the subject. `weightSameChannel = weightSharedTags = 2.5` per matching tag, with a hard cap of 3 videos per channel in the rail.

In up-next, everything not a relationship to the video playing (channel affinity, topic affinity, retention, continue-watching) is multiplied by `upNextTasteDamping = 0.35`. Relatedness leads; taste only breaks ties.

### Feed mechanics

- `GetFeedPage` freezes ranking into a per-session snapshot (TTL 30 minutes) so pages do not repeat. The TTL runs from creation, **not** from last read — a sliding window meant a viewer who kept scrolling never re-ranked.
- A request with no page token **reuses the viewer's live snapshot** rather than minting a new one, and the offset is **never rewound**. An infinite query refetches every page it holds — page one with no token, page two with the token it already had — so building a fresh ordering per tokenless request spliced two orderings together and repeated whatever they shared.
- A WATCH signal above the bounce threshold invalidates that viewer's snapshots, so the next Home load reflects what they just watched. Appending would not do: new material goes to the tail by design.
- `GET /api/feed/explain[?video=<id>]` returns every video's score component by component, its slot, its position, and for excluded videos which rule dropped it. Debug only, no UI. Tune with it rather than by eye.
- `GET /api/settings/feed-mix/buckets` counts how many videos each share can draw on. The sliders divide a page; a share can only be filled from a bucket that has videos in it, and that was invisible until it was measured.

### Advanced settings

`/settings/advanced` exposes seven ranking constants: `sessionBlend`, `freshSubscribedPercent`, `freshnessWindowHours`, `maxPublishedAgeDays`, `recencyHalfLifeDays`, `softmaxTemperature`, `samplePoolSize`.

- Stored at the gateway (`data/ranking.json`, hand-editable), sent in `GetFeedRequest.tuning` / `ExplainFeedRequest.tuning`. Recsys still holds no config.
- Every field is **optional with explicit presence**, all the way from proto to JSON. Zero is a real setting for several — a session blend of zero means "ignore this sitting" — and an absent field means "use the built-in value". Flattening them would make an older gateway look like one asking for a zero maximum age, which is an empty feed.
- Recsys **clamps** out-of-range values rather than refusing them, and publishes the ranges via `TuningBounds()` so the sliders cannot disagree with the clamp.
- **Up-next is deliberately not tuned.** It answers a different question, and a number moved on a settings screen must not silently reorder the rail beside a playing video.
- The two dozen `weight*`/`penalty*` constants are **not** exposed. §6's value is that every score can be explained; twenty knobs nobody remembers setting is how that is lost.
- When a snapshot falls below 48 videos, `ExpandLibrary` runs: deepen `topics.yaml` sources → InnerTube related → search by topic name. Only one expansion at a time.
- Videos older than 365 days (PublishedAt, or AddedAt fallback) are filtered from Home.

## 7. Scope

### Phase 1 — the core loop

Has: ingest one URL → watchable · Home (3-column grid) · Watch (player + info + Next sidebar) · login with 2 seeded accounts · full-text search

Does not have: comments · transcript panel · history/likes/watch-later · dynamic chip filters · ABR/hls.js · playlist/channel import · "Ask"

### Phase 2

Nested comments · click-to-seek transcript · history/likes/playlists · mixed recsys · notifications · Storage page + eviction · playlist/channel import · move up to ABR

### Phase 3

Auto-follow channels · `/tv` UI driven by D-pad · mobile app · *(optional)* feed that pulls in outside videos

### Cut for good

- **CDN** — meaningless on a LAN
- **"Ask" AI** — prefer full-text search within transcripts over a 5GB model
- **144p / 4K** — nobody watches 144p; 4K kills the disk
- **Flutter Web** — canvas rendering, cannot be pixel-perfect

## 8. Known risks

1. **External SSD drops.** If `/Volumes/Data2` disappears, services write errors into a path that does not exist. There is no test for this case.
2. **Microservices + gRPC learning curve.** P1 is slower than a monolith; accepted. ConnectRPC keeps each service debuggable with curl.
3. **HTTPS on a Smart TV is unproven.** Try it early against a real TV.
4. **Background playback on iOS is impossible from the web.** Media Session + PiP are the web limits; a native app is the only path for background iOS playback.
5. **yt-dlp breaks periodically** when YouTube changes something → ingest must handle failures gracefully and allow retry.
6. **YouTube blocks by IP if too many full-metadata fetches are made.** A full metadata fetch is expensive and counted; flat listing is not. Backfill runs one thread, 4s apart, 200 videos/pass, and stops after 15 consecutive failures.

### Build status

- 4 services (`catalog` 8181 · `recsys` 8182 · `ingest` 8183) + `gateway` 8180 + web 5173.
- Postgres 17, one schema and one role per service.
- `scripts/dev.sh` runs the stack; `scripts/stop.sh` stops by port. `make check` = buf lint + tsc + go build.
- `dev.sh` refuses to start on a held port and prints `up`/`DOWN` per service.
- `topics.yaml` is the feed's primary source; the scanner runs hourly.
- ~4,400 videos in catalog. RSS backfills missing `published_at`; remaining older gaps need manual backfill or a YouTube Data API key.

## 9. Open questions

- What make is the TV at home? (it affects how certificates have to be handled)
- ~~Is there an external SSD?~~ Yes — `/Volumes/Data2/Youtube`, 437 GiB. See §2.
