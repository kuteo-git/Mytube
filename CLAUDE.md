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
| `instant` | YouTube progressive file (itag 18, 360p), proxied through the gateway and range-requested by the browser | ✅ |
| `remux` | Two adaptive tracks muxed directly into fMP4, 1080p | ⚠️ reopen stream |

The player climbs tiers: `instant` starts immediately → a hidden `remux` element prepares at `position + 20s` (5s after a seek) → handover when the playhead reaches it → `local` once the download lands. No transcoding, no HLS.

`instant`'s URL is proxied (`GET /api/videos/{id}/instant`) rather than handed to the browser raw. Googlevideo signs it to the IP that resolved it — the gateway's — and a viewer's IP differs from that on any LAN behind CGNAT, which is the ordinary case here, not an edge one. The raw URL then gets refused by YouTube in a way the `<video>` element reports as a generic format error, so a video with no local copy yet — everything that has never been downloaded — never starts playing on its own. Resolved fresh per request rather than cached at the gateway, since ingest already caches and refreshes the signed URL for as long as it stays valid.

### Never ask googlevideo for an open-ended range

**An upstream request must always carry a bounded `Range`, and the bound must stay small.** Ask for the rest of the file — `Range: bytes=0-`, or no range at all — and googlevideo answers with a redirect to a host that then refuses: **403**, on 9 of 12 attempts on one video and varying by the minute. The identical URL asked for 1 MiB or 2 MiB answers **206 every time, 10 of 10**.

`bytes=0-` is exactly what Chrome sends to open a video. This is the whole of "some videos play, some don't": a video with a local copy never needs an upstream request, so it always played, while a video without one could not start — and the player has no way to say why, reporting only a generic format error.

- **Size is the safeguard, so it must stay under the line rather than near it.** 8 MiB tracked the open-ended request exactly, succeeding and failing in step with it; `instantChunkBytes` is **2 MiB**, about 40 seconds of the 360p rendition. Never raise it without measuring again.
- **A request that carried no range still gets the whole file under a 200**, fetched a piece at a time. Answering it with one bounded piece would hand a television 2 MiB of a video and call that the end.
- **Both tiers re-resolve once on refusal**, for the residue that bounding does not cover. `ResolveStreamRequest.refresh` drops ingest's cached URL, because retrying the same dead URL fails identically — the refusal is a property of the URL, measured at 20 of 20 on one. Refused twice is a real answer and is passed to the browser; a third request only adds to whatever count upstream keeps against this address.
- **The remux tier waits for the first bytes before committing a status.** `OpenRemux` returns when ffmpeg *starts*, long before it has read anything, so a refusal used to arrive as `200` with an empty body — which the browser reports only as `DEMUXER_ERROR_COULD_NOT_OPEN`. Those first bytes are the fMP4's initialisation segment and are written ahead of the rest, never discarded.
- **An upstream status ≥400 is logged.** It arrives as a *successful* round trip — `err` is nil, the status carries the bad news — so passing it through left no trace anywhere. A day was spent looking at the player for a fault that never logged a line.

- **ffmpeg and ffprobe read open-ended unless told not to, and that is the same rule.** They do their own HTTP, so the gateway's chunking cannot cover them: `-request_size` **and** `-initial_request_size` (2 MiB, matching `instantChunkBytes`) go before every `-i`, per input. Without them, measured on a real 1080p URL — `ffprobe` → 403 Forbidden; `ffprobe -request_size 2M` → the answer in 0.14s; `curl -r 0-1048575` → 206; `curl -r 0-` → 302 to a host that then 403s. This was the whole of `probe keyframe: exit status 1` → `open remux: EOF` → **502**, which the ingest log carried for *every* video; the mux only ever opened when the retry happened to be let through.
- **ffmpeg's stderr is kept** (`tailBuffer`, last 4 KiB). A mux that fails writes no bytes, and the caller can only call that `EOF` — a word about the pipe, not the fault. Every remux failure in the log read `error=EOF` while ffmpeg was saying "403 Forbidden" a few kilobytes away.

### Remux rules

- ffmpeg flags for correct A/V alignment: `-avoid_negative_ts make_zero -muxdelay 0 -muxpreload 0 -frag_duration 1000000 -movflags frag_keyframe+empty_moov+default_base_moof`.
- When opening at `T > 0`, `ProbeKeyframe` finds the actual video keyframe `K` (ffmpeg seeks to the nearest keyframe at or before the mark). The audio input is seeked to `K`, the video to `T`, so both tracks share the same content origin. The stream reports `audioAt = K`; the player uses `K` as the offset.
- Do not seek both inputs to `K`; ffmpeg would step back to the previous keyframe.
- fMP4 through a pipe has no index, so seeking means reopening the stream with `?t=<seconds>`. The seek bar is enabled on every tier.
- A handover record carries the URL it was made for; the player rejects records that do not match the element's current `src`. **A failure is tested the same way**: an `error` on the layer being prepared abandons the climb only when the element's `src` is still the claim's. The claim moves ahead of the element — the local file landing writes a new one, the `src` follows on the next commit — so a 502 travelling from the stream that was there a moment ago used to take the climb to the local file with it.
- **Losing the climb to the local file is not final.** `useStream` stops polling once the answer carries a local file, and that poll was the only thing re-running the climb effect — so an abandoned climb was abandoned for good, and the viewer watched the rest at 360p with the whole file on disk beside them. Pressing 1080p or reloading "fixed" it only by starting the machinery over. Abandoning now bumps `climbAttempt` for the local tier too, capped at `MAX_LOCAL_ATTEMPTS = 3` — not for cost (there is none) but because a drive that has gone away fails identically forever.
- If the remux cannot open within ~20s on Auto, the tier is abandoned for that video.
- Pinning a tier is an order; it does not climb or drop on its own.

### General playback rules

- `?prefetch=1` resolves and caches the stream URL; it **does not** queue a download.
- First viewing is 360p by design; the player labels which tier it is on.
- Cancelled downloads use `NoPart()` + `NoContinue()`: partial data is discarded so a resume does not ask YouTube for an impossible range.
- **Cancelling has to reach the process, and the heartbeat is the only place it can.** The worker runs on its own, so the job row is the one thing both sides see: `Heartbeat` updates only a row still `RUNNING` and returns `ErrJobNotRunning` otherwise, which cancels the transfer's context. Marking the row alone left yt-dlp downloading a video nobody was waiting for. A cancelled video is written `EVICTED`, not `FAILED` — nothing went wrong, and `FAILED` offers a Retry.
- **A live broadcast is refused as a download job.** It has no end to download to: yt-dlp follows it for as long as it lasts, and the single worker slot stayed occupied for hours while every later job sat queued at 0%. `is_live`/`is_upcoming` are refused; `was_live` and `post_live` are ordinary videos with a finite recording.
- **The second attempt waits ~10s before resolving again** (`resolveAgainDelay`). A new URL is only worth having if whatever refused the last one has moved on, and some refusals are the address being turned away for a stretch rather than one dead URL. Measured on `g55XEx2oFaE`, a three-hour mix: claimed at 18:06:17, refused at :21, retried immediately, refused again at :23 — two attempts inside six seconds, both in the same refusal. Minutes later the same URL shape answered **206** to a bounded range and **200** to no range at all, first ask. Cancelling during the wait abandons the attempt.
- **A refused transfer is retried once in a fresh yt-dlp process.** The refusal usually belongs to the signed URL, not the video, and yt-dlp resolves once at start-up — so `Retries(5)`/`FragmentRetries(5)` throw the same dead URL at the same host and fail identically. Measured on `cT_ZlNvkW60`: three attempts, three seconds each, not one byte, then a fourth process carried 70MB. Never for a permanent refusal. The yt-dlp retries stay: they cover a request that drops on its own, which a DASH transfer of hundreds of requests will meet.

### Asking YouTube as somebody

Every call into yt-dlp goes through **one builder** (`ytdlp/session.go`, `newCommand(purpose)`), because there were nine bare `ytdlp.New()` calls and anything to be said to all of them had to be said nine times — during a refusal, which is not the moment to be editing nine call sites.

- **`purposeMedia`** (resolve, download, subtitles, comments, remux URLs) carries credentials. **`purposeListing`** (search, flat playlists, channel info — the scanner and the backfill) carries none, ever. That traffic is the most bot-like thing here (93 sources an hour, 200 videos a pass) and the least often refused; attaching an account to it is the fastest way to lose the account, and §8 risk 6 is already about exactly that volume.
- `YTDLP_COOKIES` is a path to a Netscape `cookies.txt`. **A file, not `--cookies-from-browser`**: this runs as a background service on a Mac whose browser a person is using at the same time, and reading Chrome's jar on macOS goes through the Keychain and fails while Chrome holds it. A missing file is dropped rather than passed on — yt-dlp fails the whole request over a jar it cannot find, so a typo would take down every download instead of doing without.
- `YTDLP_PLAYER_CLIENT` sets `youtube:player_client`. **Unmeasured and off by default**, said plainly: the refusals it exists for come in waves — the same URL answered 206, then 403, then 206 again inside an hour — so on the day it was written there was no way to tell one client from another. It is a lever for the next wave.
- Cookies were considered and *not* switched on: they tie a real YouTube account to every media request, and yt-dlp's own warning about accounts is about traffic that looks like this.

### A transfer that failed

Two rules, answering different questions, both reached through `Submit`:

- **Do not try again immediately.** A URL whose transfer failed under `failureCooldown` (2 min) ago is refused. Measured on `53KMZ_uRJOc`: **three jobs in twenty-six seconds**, each dying on the same 403, because the player re-asks `/stream` every 5s, every ask schedules a download, and `Enqueue` is idempotent only while a job is QUEUED or RUNNING — a failure left nothing to attach to. This is §4's "thirteen jobs in two minutes" reached through the door that must stay open: a temporary 403 is deliberately never recorded as a permanent refusal. The gateway also stops asking (`askedRecently`, 1 min) — ingest's rule is the rule, the gateway's is the manners.
- **Do try again later.** `RequeueFailed` puts **one** failed job back per sweep (1 min), waiting `retryBackoff` = **2 min → 10 min → 30 min** by `attempts`, then leaving it FAILED for good. One at a time because there is a single worker slot, so requeueing ten only produces a burst at an address that has just been refusing them. Skips dismissed jobs and anything in `unavailable_sources` — that question already has exactly one place that answers it.
- **A person pressing Retry is held by neither** (`submit(..., automatic: false)`), the same reasoning that already lets Retry clear a permanent refusal.
- No new job state. A job waiting for its next go stays `FAILED` with the Retry button it always had; `RETRYING` would change the proto, the UI, and the meaning of `FAILED` everywhere, to tell the viewer something they only need to know if it is not working.

**What the retries are for**: googlevideo refuses this address in waves lasting a few minutes, and inside a wave everything is refused — `/instant`, the mux, and yt-dlp's own transfer alike. Three hypotheses for a narrower cause were tested and **all failed to reproduce** outside a wave: the audio track being special (206/206 on both tracks, ×3), two requests close together (3 concurrent pairs, all 206), and the mux competing with the download of the same video (a real download running alongside, all 206). The 6-of-6 log correlation between a mux failure and a download claim is an artefact — pressing play starts both, so they always coincide.

**The remux tier cannot be retried this way.** It already re-resolves once, 1.5s later, which is inside the same wave; waiting minutes is not something a viewer waiting for a picture can do. The answer there is the tier machinery: stay on the low rendition and climb to the local file when it lands.

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

### Shorts

**A Short is confirmed by asking YouTube, never inferred from duration.** `GET https://www.youtube.com/shorts/<id>` serves a Short with **200** and redirects anything else to `/watch` with **303**. Measured on this library: `CpvaxO7Ec30` (40s) and `AgkeJCAJl3Y` (59s) → 200; `tpjJeH1pPws` (**14s**) and `0i78zdYFQGI` (828s) → 303. That 14-second clip is why a length rule is wrong — any threshold catching a 40-second Short throws it out too.

- The redirect **is** the answer, so `CheckRedirect` must refuse to follow it. Followed, the probe lands on `/watch` and reads its 200 — the same status a Short gives — and every video in the library classifies as a Short.
- Stored on `catalog.videos.is_short` as a **tri-state**: `NULL` = never asked, and that is what the pass selects on. A probe that fails (404, 429) writes **nothing**; recording it as "not a Short" would close the question forever on an answer YouTube never gave.
- **Unknown reads as "not a Short"** everywhere (`COALESCE(is_short,false)`). The backlog is thousands of rows, so treating unasked as suspect would empty Home while it cleared.
- `ProbeShorts` runs on `SHORT_PROBE_INTERVAL` (5 min, after `SHORT_PROBE_START_DELAY` of 2 min; zero disables). Bounded at 200 a pass, 4s apart, stopping after 15 consecutive failures — the same discipline as the metadata backfill and for the same reason: §8's block is on the address, not the endpoint. **The interval is idle time between passes, not the request rate**, which stays at one per 4s inside a pass; at 30 min the waiting was most of the wall clock.
- **The order is how long the feed stays wrong.** Candidates likeliest to be a Short go first: `duration BETWEEN 1 AND 180`, then unknown duration, then everything else — newest first within each. Every Short confirmed here ran 0–152s against YouTube's 3-minute cap, and unknown durations ride with them because a confirmed Short in this library has duration 0 (a flat listing carried none). This **orders** the work, it does not answer the question: a long video is still asked, just last, so a change to that cap costs a slow week rather than a wrong column.
- Recsys excludes them as a twelfth rule (`excludedShort`), **Home only** — search and the channel page still find them, exactly as a disliked video does. This is a statement about the feed, not about whether the video exists.
- **They do not arrive through `topics.yaml`.** Every source there ends in `/videos`, and YouTube's Videos tab excludes Shorts — verified: IGN's shortest listed entries (9s, 30s, 31s) all answer 303. They come in through `ExpandLibrary` (InnerTube related + search) and through subscribed channels' RSS, neither of which can be closed without closing what it is for. Hence a per-video question rather than a filter on the way in.

Applying `services/catalog/migrations/0011_is_short.sql` is required before running the new code.

### Videos upstream will not hand over

`MEDIA_STATE_UNAVAILABLE` — members-only, private, removed. Separate from `FAILED`, which means "the attempt did not work" and carries an offer to retry; that offer is what turned one members-only video into **13 download jobs in two minutes** (83 failed jobs over 10 URLs in the library).

- `domain.ClassifyUnavailable` (ingest) is the only place a yt-dlp failure is called permanent. It matches yt-dlp's own wording and **excludes anything mentioning a rate limit, a bot check, or "try again later"** — a temporary refusal recorded as permanent would bury hundreds of videos in one bad afternoon. Age-gating and geo-blocking are deliberately temporary: cookies or a route can answer both.
- Recorded in `ingest.unavailable_sources` (ingest's own schema — "can this URL be fetched" is ingest's question about upstream). **`Submit` refuses a recorded URL**, which is the one chokepoint every route in shares: play, prefetch, repair, scanner.
- Every path that meets upstream records it — Preview, comments, stream resolve, remux — because a video nobody has downloaded is met first by the player asking for a stream. Catalog is told through the service and marked `UNAVAILABLE`; a report the catalogue never received is finished by `ReconcileUnavailable` at start.
- Never retried automatically. **Pressing Retry on `/activity` clears the record**, because a members-only video does sometimes open to everyone later.
- HTTP: **409 + `{"code":"video_unavailable","reason":"members_only|private|removed|unavailable"}`**, never 500/502. Nothing is broken; YouTube answered.
- **A refusal that is only for now gets neither.** `POST /comments/fetch` answers **200 + `{"imported":0,"unavailable":true}`** when upstream declines, logged at WARN. It used to be a 500 — a claim that this system had failed — over "HTTP Error 403" on the one thing on the watch page nothing depends on; the console went red while the video played perfectly. It is not the 409 either: that means permanent, offers no retry and names a reason, and this video answers on the next press. The comment section shows the same line and the same Retry it already had for a failed request.
- Excluded from Home and up-next (`explainOne`), still reachable through search and the channel page.
- The player names the reason and offers no retry; the comments section is not mounted at all.

Applying `services/ingest/migrations/0004_unavailable_sources.sql` and `services/catalog/migrations/0010_media_state_unavailable.sql` is **required before running the new code**: the catalog `media_state` CHECK constraint rejects `UNAVAILABLE` until the second one runs.

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

### Audio graph and the equaliser

Both `<video>` layers are routed through **one shared `AudioContext`** (`application/audio-graph.ts`), which also hosts the 10-band equaliser. Narration keeps its own branch — `masterGain → limiter → destination` — and deliberately does **not** pass through the filters: the EQ shapes music, and a TTS voice read through a bass boost is a fault.

```
video A → gainA ─┐                          ┌→ dry ──────────────┐
                 ├→ eqInput → [10 biquads] ─┤                    ├→ preamp → destination
video B → gainB ─┘                          └→ convolver → wet ──┘
```

- **`createMediaElementSource` is once per element and cannot be undone.** So both layers are attached, always, for everyone — not lazily when the EQ is switched on. Two signal paths for on and off would be two paths that must both be right, and switching mid-playback cuts the sound for a beat on iOS.
- **Attaching is the elements' own ref callbacks, never a mount effect.** The layers sit behind `playable` and are not in the tree on the first render, so an effect with an empty dependency list ran against two nulls and never again. Everything else worked — context built, filters created, every slider writing its value — with no signal passing through any of it. The only symptom was that the equaliser changed nothing, because an unattached element plays perfectly well on its own. Held by `player-equalizer.test.tsx`.
- **A context that is not `running` is now total silence**, not a missing equaliser. Hence three chances at resuming it: the gesture listener is installed unconditionally and **re-arms whenever the state leaves `running`** (iOS suspends after a call or a route change), plus `resumeAudio()` on `play`, on `visibilitychange`, and on the volume slider.
- **Never suspend the context.** Narration's teardown used to, on the reasoning that nothing else was using it. That stopped being true when the video started going through it.
- **Volume, mute and narration ducking are gain nodes**, not `el.volume`. `levelsFor` (`narration-levels.ts`) is unchanged and still decides the number; only where it lands moved. Whether an element's own `volume` still attenuates a signal already routed into Web Audio is not answered the same way by every browser, and that is not a thing to discover on a television.
- **`volumechange` follows `muted` only.** The element's `volume` now sits at 1 for the life of the page, so following it would have set the player to full volume the first time anything fired the event.
- **No Web Audio at all → attach nothing and fall back to `el.volume`.** `isAttached` is what the player asks. Without the fallback an older TV browser gets a volume slider that does nothing and a video stuck at full.
- EQ and reverb live in `localStorage` (`yt-equalizer-v1`), **per device** — an equaliser corrects for the speakers, so the phone and the TV want different curves. Unlike the feed mix, this is not one setting for the household. The key keeps the equaliser's name after reverb was added, and `audio-prefs.ts` reads both shapes: renaming it would have been tidier and would have thrown away every curve already saved.
- Bands are `lowshelf` 32 Hz, `peaking` 64 Hz–8 kHz at `Q = √2`, `highshelf` 16 kHz. Peaking at the extremes would leave the floor and ceiling unmoved. Preamp only ever **cuts** (−12…0 dB): it is the headroom the boosts are paid for, and every preset ships with its own.
- "Off" is every filter at 0 dB, **not** a disconnected chain — a biquad at unity is transparent, so on and off cannot fail differently.
- **On iOS, native fullscreen bypasses Web Audio entirely** and the EQ silently stops applying. The panel says "EQ off in fullscreen" while `webkitPresentationMode === 'fullscreen'` — a state-driven label, because this is a question that only occurs to someone at the moment the sound stops changing.
- **Sound has its own button in the control bar, left of the gear** (`SlidersVertical`, `aria-label="Audio"`). The gear holds what belongs to *this video* — rendition, subtitles, reading them aloud; the equaliser and the room belong to the speakers the viewer is sitting in front of. They also outgrew a menu row: ten sliders, a preamp, four rooms and a mix pushed everything else on the gear below the fold on a phone. `SettingsMenu` takes `icon`/`label` and is no longer "the gear"; everything reusable in it is below the button (portal out of `overflow-hidden`, measured anchoring, dropdown-or-sheet, outside-press). The gear keeps its original condition — without it, moving the EQ out would leave it opening on nothing.

#### Environment (reverb)

eqMac's "Environment" is macOS's `AVAudioUnitReverb` with a preset and its `wetDryMix`; the preset names (Small Room, Large Hall v2, Cathedral) are Apple's `AVAudioUnitReverbPreset` enum verbatim. **A browser has no such unit**, so this is an imitation and not a port: `ConvolverNode` fed an impulse response *synthesised* from decaying stereo noise (`reverb-presets.ts`), never a recording.

- **Four rooms — Room · Plate · Hall · Cathedral — not Apple's dozen.** From synthesised noise, "Large Room" and "Large Room v2" would be two labels over one sound. A control that cannot do what its name says is the dead button §5 forbids, heard rather than seen.
- Impulse responses are **capped at 2.5s** and built **lazily, once per preset, then cached**. Convolution scales with the tail, this is headed for a TV browser, and each build is a loop over hundreds of thousands of samples. No auto-disable on weak hardware: that would be guessing about a device nobody has plugged in yet.
- **The convolver holds no buffer until a room is chosen** — a loaded convolver convolves even at zero wet gain, which is the one avoidable cost in this graph.
- **The room splits off after the EQ and before the preamp.** After, so it answers the sound the viewer chose; before, because reverb *adds* energy, and the preamp is the trim that pays for clipping.
- Dry/wet is a plain crossfade summing to 1, ramped over 60 ms rather than the EQ's 20 ms — a filter moving fast is inaudible, a reverb tail appearing fast is a swell. Default **off**; switching it on starts at **25% wet**.
- **Environment has its own on/off row, the same shape as the equaliser's**, and the rooms and mix appear only when it is on. Pressing the lit room used to be the way off — the only one available with no switch — which made "off" mean pressing whichever room happened to be selected.
- The two halves are pushed by **separate effects** keyed on each: moving an EQ slider has nothing to say to an impulse response.
- **"Spatial Audio" is a different eqMac feature** (stereo widening / HRTF), not reverb. Deliberately not built.

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
- `applyChannelDiversity`: at most 3 videos per channel in each window of 24, **and at least `window/perChannel` = 8 slots apart**. The count alone could not tell a mixed page from a channel's page — measured on the real feed, one channel at positions 6, 7 and 9 of the first window, another at 3 and 4, a third at 13 and 14. It also reset at each window, so a channel could take slot 23 and slot 24. The gap is derived from the cap rather than chosen separately, so the two can never disagree.
- New uploads from followed channels keep their exemption from the *cap* but get a small gap of 3. The full gap would push a channel's sixth upload of the day past the first window, which is what the exemption exists to prevent; nothing about the exemption required them to be adjacent.
- Held-back videos are retried **before every slot**, not only at window boundaries. A video held by the count cannot move until the window resets, so waiting cost nothing; one held by the gap becomes placeable a few slots later, and waiting sent six uploads that should have shared a page onto the next one.
- Retention is computed in recsys, not catalog.

### Likes / dislikes

- Likes add affinity on topic 1.0 / channel 0.8 / hashtag 0.5, accumulated.
- Dislikes remove the video from feed and up-next; it remains reachable through search and channel pages.
- Dislike affinity mirrors likes at −0.7 vs +2.0, with a 90-day half-life.
- Channel suppression needs ≥3 disliked videos **and** ≥30% of that channel's videos, with a ceiling of ≥20. Subscribed channels are immune.
- The ceiling was 8, and 8 is an ordinary rate of "not this one" on a channel with forty videos. It took Igor Presnyakov and Drumeo (both 8 of ~42), Tinh te and Vox Weather (9) out of the library altogether — 143 of the 402 videos in Music between them, which is most of why that feed held 27 videos. Twenty still catches NoCopyrightSounds at 26, the case the ceiling exists for.

### Channel rotation, and signals that age

Measured on this library before any of it existed: three channels held **38 of the first 120 slots**, only **36 distinct channels** appeared in those 120, and **55 of 85 subscribed channels had no video before position 240**. Almost nothing was excluded — 2 channels, for age. The rest was simply outranked for ever.

The cause was not the buckets, which are broad (the affinity bucket holds 174 videos across 106 channels). Sampling only sees the best `samplePoolSize` of a bucket, and the same channels sat at the top of that pool on every request, because **nothing in the score knew that time passes**.

- **Reaction scores are bounded at the scoring site** (`squashReaction`, `x/(1+x)`), not in the builders. Like/dislike totals grow by one per reaction with no ceiling — measured at **+17.6 / −12.4** on one video against `weightSubscribed`'s 2.5 — so two terms an order of magnitude above everything else were deciding the feed alone. Normalising the *maps* was tried and is wrong: it erases both the 1.0/0.8/0.5 axis ordering and the difference between two likes and one. The accumulation is real information; letting it reach the score unbounded was not.
- **Watch affinity ages** — 60-day half-life, against 90 for a dislike. What somebody enjoyed fades faster than what they turned down.
- **Watch affinity counts time, not just fraction** (`watchTimeWeight`, `log1p(seconds)/log1p(180)`). Half of a 60-minute video and half of a 2-minute one were the same evidence.
- **`channelHeat` — half-life 7 days.** How recently a channel was watched *or shown*; being shown counts at 0.25 of being watched, or a channel offered on every page and never opened stays cold for ever and keeps being offered. Subtracted as `channel_fatigue` (`weightChannelHeat = 2.0`, comparable to subscribing) so a hot channel leaves the top of the pool without being buried.
- **`channel_revival`** lifts a channel the viewer once watched and has not returned to, saturating over **21 days** and capped at half of subscribing. Deliberately slower than heat decays: falling quiet should be quick and returning gradual, or the rut becomes a rotation just as predictable. **Never for a channel never watched** — that is the discovery share's job.
- **`ignored_penalty`** — a video shown more than 3 times and never opened loses 0.5 per further showing, capped at 3.0. Nothing here has ever seen a thumbnail; this is as close as it gets to reading one. Watched videos are exempt outright.
- All three are **additive and applied last**, never multipliers: resting a channel must not scale how good its videos are.

After: **49** distinct channels in the first 120 (was 36), top-3 hold **35** (was 38), **38** subscribed channels in the first 240 (was 28).

### Uninvited videos, and the language they are in

Measured: the feed was offering Hindi, Malayalam, Indonesian and Nepali to a household whose entire watch history is **406 English, 200 unknown, 27 Vietnamese, 18 en-US** and not one view in any of the four. Vietnamese was **11 of the first 1000 slots against Hindi's 21**.

Not a ranking fault. Those videos sit in the `affinity` and `discovery` buckets, which are *defined* as channels the viewer has not subscribed to, and the `subscribed`/`fresh_subscribed` slots measured 100% subscribed. The fault is the pool those buckets draw from: **621 of the library's 708 channels arrived through `ExpandLibrary`** reaching InnerTube search, and a search by topic name returns whatever YouTube returns. A third of Home comes from channels nobody asked for.

- **The rule is about provenance, not language as such.** A subscription is a deliberate choice and is honoured in any language; only *uninvited* videos — non-subscribed channels — must be in a language the household demonstrably watches.
- **Learned, never configured** (`buildWatchedLanguages`). It corrects itself: start watching a language and it stops being filtered. The `?lang=` query parameter still exists as a per-request override and nothing in the web app has ever sent it.
- **A language needs ≥3 watched videos** to count. The `language` column is filled from the title on flat listings, so one row is as likely to be a mis-tagged title as a real viewing.
- **Off entirely below 20 watched videos.** A fresh library cannot know what anybody reads, and guessing would leave a new installation showing almost nothing.
- **Unknown language always passes.** 1961 of 8310 videos carry none and 200 of those have been watched; excluding them to catch a handful of Bollywood would be the worst trade in the ranker.
- `en-US` and `en` are one language: primary subtag only.

Result: 54 videos excluded, and hi/ne/ml gone from the feed. **Vietnamese stays scarce for a reason ranking cannot fix** — the library holds 125 Vietnamese videos out of 8310. The feed cannot show what was never ingested; that is a `topics.yaml` and subscriptions question.

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
- When a snapshot falls below 48 videos, `ExpandLibrary` runs: deepen `topics.yaml` sources → InnerTube related, seeded by videos already in the library. Only one expansion at a time.
- **The search-by-topic-name layer is gone.** It fired with the topic chip the viewer was looking at, so a thinly-stocked topic sent its own *name* to YouTube search and stored whatever came back, unfiltered — and `store` skips videos already present, so asking again imported the *next* forty uploads from the same channels. Measured: **708 channels against 87 subscribed and 6 curated sources**, one uninvited channel at 60 videos, another 31, another 25, and **a third of Home** from channels nobody chose. `topics.yaml` opens by calling itself the only content source in the system; that layer is what made the line false.
- **At most `maxExpandPerChannel` = 3 videos from one channel per pass.** Expansion may widen the library; it may not adopt a channel.
- Related is kept because it is anchored — it starts from videos the library already has, so its worst case is a neighbour of something chosen rather than an arbitrary search result.
- Videos older than 365 days (PublishedAt, or AddedAt fallback) are filtered from Home — **but only when no topic chip is picked**. Choosing a chip is a stated intent, and the answer to "show me music" is not "music from this year": 170 of the library's music videos are over a year old against 148 under it, where an ordinary topic loses 7%.
- **`EVICTED` videos are offered.** They were excluded for as long as "no local copy" meant "pressing this does nothing"; the instant tier plays an undownloaded video straight away while the copy is fetched behind it. Excluding them cost 359 videos across the library and 104 of the 402 in Music.

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
6. **YouTube blocks by IP if too many full-metadata fetches are made.** A full metadata fetch is expensive and counted; flat listing is not. Backfill runs one thread, 4s apart, 200 videos/pass, and stops after 15 consecutive failures. A pass is **always** bounded — `limit: 0` means 200, not "all".

### Logs

`logview` on **:8184** serves every service's log as one page — timestamps, level, service, live tail over SSE, and filters for service / level / free text / errors only.

- **Its own process, not an endpoint on the gateway.** Logs are wanted at the moment something has stopped working, and a viewer inside the gateway goes down with the thing it is there to explain. It also needs no Vite, so it is reachable from a phone while the web app is not running.
- **It reads the files in `LOG_DIR`** and no service knows it exists. That is what makes it cover the two Python servers as well as the four Go ones, for nothing.
- **Nothing is ever dropped.** slog's text format and Python `logging` are both parsed; a panic trace, an ffmpeg complaint, a line yt-dlp wrote to stderr are shown raw. A level is read, never guessed — half the interesting lines here mention a 403, and filing those under ERROR would make "errors only" show everything.
- **Filtering is in the browser.** Every line is already being sent for the live view, so a second implementation on the server would only be a predicate that agrees with the first until one is fixed.
- **`dev.sh` appends to logs rather than truncating them**, and writes `--- restart <time> ---` between runs, which logview draws as a divider. Truncating threw away the lines written immediately *before* somebody restarted the stack, which are reliably the ones being looked for. A log past `LOG_CEILING_BYTES` (50 MiB) is trimmed to its most recent half rather than rotated — the older half is the right one to lose, and no second file has to be gone looking in.

### Build status

- 4 services (`catalog` 8181 · `recsys` 8182 · `ingest` 8183) + `gateway` 8180 + `logview` 8184 + web 5173.
- Postgres 17, one schema and one role per service.
- `scripts/dev.sh` runs the stack; `scripts/stop.sh` stops by port. `make check` = buf lint + tsc + go build.
- `dev.sh` refuses to start on a held port and prints `up`/`DOWN` per service.
- `topics.yaml` is the feed's primary source; the scanner runs hourly.
- ~8,000 videos in catalog. RSS backfills missing `published_at` for recent uploads; older gaps are filled by the metadata backfill.
- **The backfill runs on a timer** (`BACKFILL_INTERVAL`, default 6h, after `BACKFILL_START_DELAY` of 10 min; zero disables it). It handled a missing `published_at` from the day it was written and then waited for a button nobody pressed: 1127 of 8056 videos reached the catalogue undated, and the feed excludes an undated video outright. A pass that only runs when someone remembers it does not run.

## 9. Open questions

- What make is the TV at home? (it affects how certificates have to be handled)
- ~~Is there an external SSD?~~ Yes — `/Volumes/Data2/Youtube`, 437 GiB. See §2.
