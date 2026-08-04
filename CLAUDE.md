# Local YouTube — Project Charter

> The result of the grilling session on 2026-07-28. This is the source of truth for every
> architectural decision. If a new decision contradicts this file, update this file rather
> than quietly going another way.

## 1. What the system is

A **self-hosted** media library running on a Mac M4 at home. `yt-dlp` is an **ingest tool**,
**not** a realtime YouTube proxy — a video is fetched to disk once and then served from the LAN.

- 2–5 users in one household (light multi-user, no public sign-up)
- Final target: watching in a **Smart TV browser**; **Phase 1 builds desktop web first**
- Reference layout: `Example/home.png`, `Example/play.png` (YouTube desktop)

## 2. Hard constraints (verified on the machine)

| | |
|---|---|
| Disk | ~~34 GiB free~~ **resolved (2026-07-28): external SSD at `/Volumes/Data2/Youtube`, 437 GiB free.** `MEDIA_ROOT`/`STORAGE_BUDGET_BYTES`/`EVICTION_HIGH_BYTES`/`EVICTION_LOW_BYTES` point there via `scripts/dev.sh` (budget 300 GiB, sweep 350→300 GiB). The internal disk is no longer a hard constraint for development — it is still a real one for deploying to a machine without an external drive. |
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

**Phase 1 (hybrid — settled for the third time on 2026-07-29):** a video that is not yet on
disk is still watchable immediately. `GET /api/videos/{id}/stream` **lists** every source that
can play right now instead of choosing on the client's behalf:

| Source | What it is | Seek |
|---|---|---|
| `local` | the file on disk. When it exists, no other source is worth offering | ✅ |
| `instant` | YouTube's raw progressive URL (itag 18, 360p), range-requested by the browser itself | ✅ |
| `remux` | the two adaptive tracks muxed directly into fMP4, 1080p | ⚠️ reopens the stream |

**Stage 2, finished 2026-07-29 — the player climbs all three tiers:**
`instant` plays within ~17ms → a 1080p `remux` is built in a hidden element and taken up when
ready → `local` once the download lands. No transcoding, no HLS.

**The ffmpeg flags that stop audio drifting from video (2026-07-29).** The two inputs are
seeked **separately**, so timestamps have to be normalised rather than trusted:
`-avoid_negative_ts make_zero` brings them to a common origin, and `-muxdelay 0 -muxpreload 0`
removes the delay the muxer inserts between the two streams. And `-frag_duration 1000000`:
with `frag_keyframe` alone, fragments run **1.9–4.9 seconds and unevenly** (measured), and a
browser reading a file that is still growing cannot present anything until it has a whole
fragment — so sound and picture arrive in blocks that do not line up. Cutting evenly at one
second brings fragments down to ~0.77s. After the fix, video and audio share a `start_time`;
before it they differed by 0.04s.

**That fix only ever covered opening at zero (corrected 2026-08-04).** Opened part way in, sound
ran **2.008 seconds ahead of picture** — measured on `2el-stE5mGM` at `-ss 600`. The cause is the
separate seeks themselves, not the muxer: an input seek lands on the nearest keyframe **at or
before** the mark, so the video began at **597.972** while the audio began at **599.980**. The
flags above then pulled each track down to zero **independently**, which erases that difference
rather than preserving it. At offset 0 both inputs genuinely start together, which is why the
2026-07-29 measurement looked settled.

**No timestamp flag can fix it — measured, do not try again.** `-copyts`, `-start_at_zero`,
`-avoid_negative_ts disabled` and `-af aresample=async=1` all produce byte-identical framing:
the **fragmented** muxer rebases every track to its own first packet whatever it is told. The
same two inputs written to an **ordinary** MP4 keep `start_time` 597.972 / 599.980 correctly —
that contrast is what identified the muxer rather than the seek as the collapsing step.

**The fix is to seek the two inputs to the same content time.** `ProbeKeyframe` asks
`ffprobe -read_intervals "T%+#1" -select_streams v:0` where the video will really land —
**1.29s**, a few hundred kilobytes — and the audio is then given `-ss K` while the video keeps
`-ss T`. Result: **597.972 / 597.960, a 0.012s gap**. Seeking goes from ~3.0s to ~4.3s, which is
the price. Probed only when `T > 0`.
> **Do not send both inputs to `K`.** ffmpeg reads `-ss` as "at or before", so a mark that *is* a
> keyframe steps back to the previous one — 593.593, a 4.4s gap, worse than the bug.

**The stream therefore begins at `K`, not at `T`**, up to a group of pictures early; ffmpeg
cannot cut between keyframes without re-encoding, which §4 forbids. The player is told: it asks
`GET /api/videos/{id}/remux/start?t=` (a separate request, because the stream body goes to a
`<video>` element and script never sees its headers), uses `K` as the offset so every absolute
position stays honest, and then moves the element the remaining second or two **inside what has
already arrived**. The answer is handed back as `audioAt` so the probe is paid for once, not
twice.

**Seeking in the remux tier means reopening the stream.** fMP4 through a pipe carries no index,
so the browser cannot seek it; the player sends `?t=<seconds>` and ffmpeg uses `-ss` **before
`-i`** (an HTTP range seek, not decode-and-discard). Measured: opening from the start takes
4.4s, seeking to 120s takes **3.0s** — cheaper because the URL is already cached. Fired only on
**release** of the seek bar; dragging just moves the number. There is a "Seeking…" label,
because three seconds of a frozen picture looks like being ignored.

**Except that it no longer does, and the way it used to was broken (2026-08-04).** A seek in the
remux tier showed "Seeking…" and then simply did not arrive. Two ways of asking for the same
picture had drifted apart: the *upgrade* path hands over when the playhead reaches the mark the
replacement is parked on, while the *seek* path — whose replacement starts somewhere the playhead
will never reach — waited instead for `buffered.end(0) >= 0.5`, a condition that on a stream it
could not satisfy never became true. On Auto the 45s patience timer eventually cancelled it; on a
**pinned 1080p that timer was skipped entirely**, so the wait had no end at all.

The report contained its own diagnosis: seeking at 360p and *then* switching to 1080p worked
perfectly, because that goes through the upgrade path. So **a seek now takes that road**: down to
`instant` (progressive, natively seekable, at the mark within milliseconds), then the ordinary
climb back to 1080p. Three consequences worth keeping:
- **The climb after a seek uses a 5s lead, not 20s.** Twenty is for the first climb of a video
  whose network behaviour has not been seen; by then reopening is measured at ~3s.
- **A seek gets one climb attempt that is not counted** against `MAX_REMUX_ATTEMPTS`. Failing to
  reopen at a new mark says nothing about the connection, and counted, two turns of the scrub bar
  would take 1080p away for the rest of the video. One attempt, not an exemption that lasts — and
  it re-climbs immediately, since only `remuxFailed` is a dependency of that effect.
- **The seek bar was `disabled` on the remux tier**, so this path was reachable only by the arrow
  keys — the broken road was also the invisible one. It is enabled on every tier now.

**Overshooting the mark is caught up, not thrown away (2026-08-04).** The climb opens the mux at
`position + 20s` and hands over when the playhead reaches it. On a long video preparation takes
about as long as that lead allows, so the climb kept missing by a second or two — and missing
meant **abandoning**, because handing over would have wound the viewer back. Visible in the
ingest log as a mux opened and closed every dozen seconds, each at a slightly later mark:
`from=1289.2` → closed after 12s → `from=1300.5` → closed after 9s → `from=1324.215`. Three
misses and auto switched the tier off for the rest of the video, which is why **pinning 1080p by
hand was the only thing that worked** — pinning ignores that count. The replacement is now wound
forward to where the viewer actually is, **inside data that has already arrived**, the same move
a seek makes. It is given up only when nothing has arrived at that point. The **paused** branch
does it too: it used to hand over without positioning anything, so pausing during a climb wound
the video back to the stream's keyframe.

**A handover must be bound to the source it is about (2026-08-04).** Three things write the
pending-tier record — the climb, the probe that refines where a muxed stream begins, and a seek —
and two of them can be in flight within a second of each other. Whichever wrote last decided what
the player believed it was watching, regardless of what had been loaded. Measured on a real seek
to **2059.5s**: the picture was the 360p rendition at 2059.5 while the offset came from the muxed
stream's keyframe at **2056.8**, so the clock read **4130** — the two added together. Worse, the
tier was recorded as `remux` while `remux` was exactly what the player was still trying to reach,
so `targetTier` returned nothing and **it never climbed again** for the rest of the video;
toggling Auto → 1080p → Auto by hand was the only way out, which is how it was found. The record
now carries the URL it was made for, and both the handover and the positioning at
`loadedmetadata` refuse a record that does not match the element's own `src` — the one witness
that cannot disagree with itself.

**Every position is absolute.** A remux stream considers itself to start at zero wherever it was
opened, so everything outside the video element works in `offset + currentTime`.

**The Auto / 1080p / 360p menu** lists only what can actually be served. **Pinning is an
order**: a manual choice neither climbs nor drops on its own. On Auto, if the remux cannot make
it within 20 seconds it is abandoned, the player falls back to 360p and **does not try again for
that video** — a network that could not manage it once will not manage it the second time.

`?prefetch=1` means the pointer has rested on a card without play being pressed: the URL is
resolved and cached so a later press is instant, and **nothing is queued for download**. Without
that line, scrolling a feed would fill a disk with a hard ceiling.

> **A deliberate trade:** the first viewing is 360p. That is the price of "plays immediately and
> seeks immediately", and it lasts a few seconds (see the measurements in §8b). The player
> labels which tier it is on.

**Phase 2 (a cheap upgrade):** also fetch the 720p rendition **YouTube already publishes** and
remux `-c copy` into HLS → real ABR at ≈ 0 CPU.
> Remember: **"no transcoding" ≠ "no ABR"**. The expensive thing is *re-encoding*; YouTube has
> already encoded several rungs.

### Eviction
Every video has `last_accessed_at` + `pinned`. Above ~22 GiB, the **media file** of the
least-recently-used unpinned video is deleted, **keeping metadata + thumbnail + history** → the
UI shows "Removed — press to fetch again", and re-ingesting is one click.

### Files that disappear without being evicted (2026-08-04)

Deleting a file inside `MEDIA_ROOT` by hand left the database **still saying READY**, `/stream`
still offering `local`, and the player holding a `/media/...` URL that 404s — the video simply
did not play, with nothing but a red line in the network tab to say why. `handleStream` now
**stats the file** before offering `local`:

| What the disk says | What happens |
|---|---|
| the file is there | `local` as before |
| **root present, file gone** | `SetMediaState(EVICTED)` → fall through to `instant`/`remux` + queue the download again, with `repaired: true` |
| **root unreadable** | **write nothing**, return `streamError` about the drive |

- **The order of the two checks is the whole safety of it.** `MEDIA_ROOT` is an external SSD
  (§8, risk 1); unplug it and **every** file is "gone", so without the distinction one loose
  cable would mark the entire library evicted. Check the root **first** — one syscall, decisive:
  `/Volumes/Data2` disappears from the filesystem when the drive is removed rather than becoming
  empty.
- **Deleting the whole video folder is still "file gone"**, not "infrastructure broken" —
  deleting by folder is how people delete things by hand.
- **`?prefetch=1` still repairs the row and still downloads nothing.** The expensive, counted
  thing is a request upstream, and that boundary is unchanged; an internal `UPDATE` for a video
  the disk has just denied is cheap, and declining to record it only means discovering the same
  fact again on the next hover.
- **The `repaired` flag is required, not decoration.** The watch page is already holding a video
  row that says READY, and `videoPollInterval` **stops polling** once the state looks settled —
  so with nobody to tell it, the "downloaded" badge stays wrong until the page is left. On
  seeing the flag the player invalidates `['video', id]` exactly once.
- **There is no periodic reconciliation sweep.** This rule lives in **one** place; in two places
  it is two places that can be wrong differently, and a sweep's mistakes are bulk and silent.
  The price: a video whose file was deleted and which nobody opens still counts toward Storage,
  and because the eviction sweep reads `size_bytes` from the database rather than the disk, it
  will **evict more than it needs to**.
- This repair path **does not clear `media_path`** (the sweep does). Harmless: everywhere that
  reads `media_path` checks `media_state == READY` first, and no UI reads it. It records where
  the file *used to* be.
- **Measured 2026-08-04**: moving `XXplTbQR9to`'s file → `/stream` answered `local:false,
  instant:true, remux:true, repaired:true`, the row became `EVICTED`, the job ran on its own,
  and **twenty seconds later it was READY again**. A gateway pointed at a `MEDIA_ROOT` that does
  not exist returned the right `streamError` and **changed not one row** (all 175 EVICTED rows in
  the database came from the sweep — they have `media_path=''`; this path keeps the path, which
  is how the two can be told apart).

## 4b. Code conventions

- **All source code, identifiers, comments, commit messages and in-app UI copy MUST be in English.**
  Vietnamese is allowed only as *content data* (e.g. a video title that is genuinely Vietnamese).
  Chat/discussion happens in Vietnamese; the artifacts do not.
- Go: standard layout per service — `cmd/`, `internal/domain`, `internal/usecase`, `internal/adapter`.
- Frontend: feature-sliced (see §5). No `fetch` inside `ui/`.

## 5. Frontend

**Vite + React + TypeScript + plain Tailwind + TanStack Query.**

- **No shadcn/ui** — when the goal is a pixel-perfect clone, a component library is only
  something to override. Components are built against design tokens taken from `Example/*.png`.
- Use the `ui-ux-pro-max` skill when designing or building UI.

### Feature-sliced structure
```
src/features/<feature>/
  domain/          # entities and plain types, NO React import
  application/     # use cases: hooks calling repositories, knows no HTTP
  infrastructure/  # repository implementations: call the gateway's REST API
  ui/              # components
```
`ui/` never calls `fetch` directly. That is what makes `/tv` (Phase 3) a rewrite of the **`ui/`
layer only**.

### UI principle: RENDER NO DEAD BUTTONS
Every element either does something real or is dropped.

| YouTube's original | Here |
|---|---|
| Create (+) | → **"Add video"** (the ingest entry point) |
| Notifications + badge | → **ingest events** ("3 videos downloaded", "1 failed") |
| Downloads | → **Storage** (usage, pinned, about to be evicted) |
| Explore / chip filter | → driven by **real tags and categories** in the catalog |
| Your videos, YT Music, YT Kids, footer, Shorts, Live | **DROPPED** |
| Subscribe | **Real in P1**: adds the channel as a live ingest source, scanned like any other |
| Share | Copies the LAN link to the clipboard |

## 6. Recommendation

**Heuristic, NOT ML.** Why: ~150 videos and 5 users mean collaborative filtering returns
nonsense, and embeddings beat tag matching by nothing while being impossible to debug.

- **P1 (done):** a grid mixing 30% unwatched / 25% recently added / 20% subscribed channels /
  15% continue watching / 10% rewatch; impressions suppressed for 24h.
- **A feed mix the household can adjust (2026-08-04) — `/settings` → "Home feed".**
  Three sliders that always total 100, dividing **82%** of the page; the other 18% is
  **continue watching 10% + rewatch 8%**, **fixed and not adjustable** (those are states of the
  watch history, not sources of new material — making them compete with recommendations
  misreads what they are).

  | Slider | Meaning | Default |
  |---|---|---|
  | Channels you follow | `profile.Subscribed[channel]` | 25% |
  | More of what you watch | not subscribed, `combinedAffinity ≥ 0.15` | 60% |
  | Something new | not subscribed, below that threshold | 15% |

  - **`feedSlot` is separate from `Reason`.** A reason answers *"why is this here"* — there are
    nine of them and they **overlap** (a video can be both unwatched and from a subscribed
    channel). A slot answers *"whose share of the page does it take"* — every video has exactly
    **one**. Conflating the two is precisely why "more of what you watch" could not be adjusted
    before: those videos were spread across `NeverWatched` and `RecentlyAdded`, mixed in with
    everything else that happened to be new. `dto.Reason` sent to the client is **unchanged**.
  - **The defaults are the old fixed quota, converted.** `NeverWatched 30 + RecentlyAdded 15`
    becomes the affinity share; normalised over 82% that is 25/60/15. **Installing it and
    touching nothing leaves the feed identical** — if the defaults shifted there would be no way
    to tell a slider working from a default changing.
  - **Zero means gone.** The `take < 1` floor is dropped for these three buckets (kept for the
    two fixed ones) — a slider dragged to the bottom that still produces videos is a control
    that lies. Those videos go to the **end** of the list rather than being dropped: the quota
    has never dropped a video.
  - **Stored at the gateway** (`config/feed-mix.json`, following `translate_config.go`), sent in
    `GetFeedRequest.mix`. **Recsys holds no configuration** — ranking stays a pure function of
    the request and the signals. **One mix for the household**, not per user: two seeded
    accounts and no sign-up screen, so per-person taste would be a table, a migration and an RPC
    bought for a privacy nobody has asked for. Changing that means changing only where it is read.
  - **A ratio, not absolute numbers**: 3/2/1 and 50/33/17 produce the same feed. The UI forces
    the total to 100 and the server normalises anyway — correct on both sides.
  - **Saving drops the `['feed']` cache** on the client. The feed is frozen into a 30-minute
    snapshot, so without that the change would be invisible for half an hour — indistinguishable
    from it not working. The trade: the scroll position is lost.
- **Scoring signals (2026-07-29)** — all deterministic, **nothing is trained**:

  | Signal | How it is computed | Weight |
  |---|---|---|
  | Continue watching | `0.02 < fraction <= 0.95` | +3.0 |
  | Never opened | the video is **absent** from `WatchedFraction` | +1.5 |
  | **Opened and abandoned** | present in the map, `fraction <= 0.02` | **−2.5** |
  | Subscribed channel | | +1.2 |
  | Recently added | `exp(-days/14)` | ×1.0 |
  | Channel affinity (from **watching**) | Σ fraction per channel, normalised 0..1 | ×1.0 |
  | Topic affinity (from **watching**) | Σ fraction per topic, normalised 0..1 | ×1.0 |
  | Affinity from **likes** | topic 1.0 / channel 0.8 / hashtag 0.5 | ×2.0 |
  | Affinity from **dislikes** | the same three axes, **decaying** with a 90-day half-life | **×−0.7** |
  | **Global retention** | `avg(max(fraction) per viewer)` | ×1.5 |
  | Shown in the last 24h | | −2.0 |

  Three things worth recording:
  - **`BOUNCED` is a Reason of its own.** `fraction <= 0.02` used to fall into the `default`
    branch and collect the same +1.5 as never having been watched — meaning **the surest way to
    keep a video in the feed was to reject it**. Told apart with comma-ok, not by comparing to zero.
  - **Affinity is read from watch history, not from likes alone.** This library holds 9 likes
    against 2,045 watch signals; reading taste from likes alone is reading almost nothing.
  - **Just watched ≠ ever watched.** `WatchedFraction` says *whether* something was watched, not
    *when*. Up-next needs exactly "the one just now": two videos on the **same channel with the
    same topic** are each other's strongest suggestion (`weightSameChannel` 2.5 +
    `weightSharedTags` 1.5), so pressing Next twice returns you to where you started — an
    **inescapable two-video loop**, observed for real. Added `RecentlyWatched` (a **3-hour**
    window) with `penaltyRecentlyWatched = 8.0` — **deliberately larger than those two weights
    combined**, because anything smaller cannot break the loop. Fixed on the **server**, not the
    client: the previous attempt relied on a trail in the browser's sessionStorage, and a
    structural loop should not depend on client-side storage working.
  - **Quotas by reason CANNOT stop one channel taking the whole page.** Measured: 44% of watch
    time concentrated in one channel → its affinity normalised to 1.0 while the next channel sat
    at 0.23 → **23 of the first 24 videos were that one channel**, with every quota reported as
    satisfied. The reason: that channel's videos are **simultaneously** unwatched, recently added
    and subscribed, so they fill every bucket at once. It has to be blocked on **another axis**:
    `applyChannelDiversity`, at most **3 videos per channel in each window of 24**. Afterwards
    the dominant channel held 12%, and the number of channels on the first page went from 2 → 10.
    Still only a reordering; nothing is dropped.
  - **Retention is computed in recsys**, not asked of catalog: catalog does not know who watched
    how much, and that boundary is what keeps this several services rather than one program
    running in four processes. The `max` must be taken **per viewer first** and only then
    averaged — WATCH signals are written periodically during playback, so averaging the rows
    directly would measure "the average moment we sampled", which is about half of every video
    regardless of how good it is.
- **Likes:** add affinity by topic 1.0 / channel 0.8 / hashtag 0.5, accumulated over every like.
  Brought forward to P1 because a like that changes nothing is a dead button.
- **Dislikes**: removed from the feed and up-next, **still reachable through search and the
  channel page**. Not a feature that had to be built — it is true by construction because of the
  service boundary: catalog cannot see recsys signals, so it cannot filter by them.
  **Extended 2026-08-04 — a dislike now teaches, but only a third as loudly:**
  - **The video that was pressed is gone for good.** That is a decision, not a preference; it
    does not decay and there is no way back into the feed. The three points below are only about
    what a dislike *teaches* beyond that video.
  - **`buildDislikeAffinity` mirrors likes** — the same three axes, the same per-axis weights —
    but multiplied by **−0.7** against a like's **+2.0**. The asymmetry: a like usually approves
    of *a kind of thing*, while a dislike is usually aimed at *this video* (its thumbnail, its
    title, its first two seconds). It tilts the feed rather than emptying a subject.
    Before this a dislike **taught nothing at all**: rejecting ten videos of a kind removed those
    ten and left the eleventh exactly where it was.
  - **A 90-day half-life.** Every other signal in the ranker decays; this one did not, because it
    was only a `map[string]bool`. `UserProfile.Disliked` now carries `occurred_at` (the query was
    already `ORDER BY occurred_at DESC` — it simply did not select it). A dislike with no date
    (older data) counts in **full**, not as zero.
  - **Suppressing a channel needs a share, not just a count**: `≥3` videos **and** `≥30%` of that
    channel's videos. It used to be a bare count, so 3-of-5 and 3-of-200 were treated alike — the
    first is a verdict, the second is three ordinary rejections in a library used for months.
    Subscribed channels remain immune.
    **Plus a ceiling of `≥8`, whatever the share (2026-08-04).** The share is measured against
    everything the channel has in the library, which made large channels effectively
    unsuppressible. Measured: **NoCopyrightSounds, 162 videos, 20 of them turned down** — 12%,
    against a threshold that wanted **49**. And it was worse than slow: every scan added more of
    that channel, so the denominator grew and pressing "not interested" again moved the share
    *down*. The one thing anyone does when a control seems not to work is use it more, and here
    that made it work less. Subscribing still overrides the ceiling — following a channel is a
    deliberate statement, and the ceiling must not become a way to lose a channel you asked for.
    > How 162 of them arrived is its own lesson: `topics.yaml` never named that channel. The
    > **`Music` topic has `sources: []`**, so when the feed ran low `ExpandLibrary` fell through to
    > its last layer — searching YouTube for the topic's name — and that channel is what "Music"
    > returns. A topic with no sources is not a topic with no content; it is a topic filled by a
    > search box.
  - `penaltyDisliked = 5.0` was **never applied to a dislike** — its only reader was `/2` for an
    already-watched video, and disliked videos `continue` several lines earlier. Renamed
    `penaltyAlreadyWatched = 2.5`; the number is unchanged.
- **`Next` (watch page) — REVERSED 2026-07-29:** it used to be *same channel > same tag*. It is
  now **the subject that leads, with the channel merely one way of sharing that subject rather
  than a better way**: `weightSameChannel = weightSharedTags = 2.5`, added **per matching tag**,
  so a video sharing both a topic and a hashtag beats one sharing only a channel.
  With a **hard cap of 3 videos per channel** in the rail (`capPerChannel`).
  Why it was reversed: the old order made the rail **20 out of 20 from one channel** — on topic,
  but a dead end. Measured afterwards: an *Entertainment* video → **20/20 Entertainment across 9
  channels**; a *Music* video → **20/20 Music across 10 channels**.
  `capPerChannel` differs from `applyChannelDiversity` (the feed) in that **the cap is absolute**
  — what it holds back is not pushed to the end. In a rail of twenty, "pushed to the end" is
  still on the page, and the capped channel quietly takes 8 slots instead of 3.
  **That order has to be enforced by weights, not by intent (2026-07-29).** Measured: watching an
  *Entertainment* video from a gaming channel produced **20/20 SOOBIN music videos**, sharing
  neither its channel nor its topic. Three causes compounded, each harmless alone:
  - `TopicScore` **summed** across a video's topics → a video tagged both `Music` and
    `Vietnamese music` collected the affinity **twice**. Fixed: take the **strongest matching
    topic**, do not add.
  - `weightContinueWatching` (+3.0) in up-next **exceeded same-channel + same-topic** (2.5+1.5).
    "Continue watching" answers *"what should I watch"*, not *"what follows this"*.
  - `weightRetention` with **one user** is that user's own history → counting their taste twice.

  The shared fix: in **up-next**, everything that is **not a relationship to the video playing**
  (channel affinity, topic affinity, retention, continue-watching) is multiplied by
  `upNextTasteDamping = 0.35`. Relatedness leads; taste only breaks ties. The feed is **not**
  damped — there, taste *should* win.
  A known trade: up-next is now nearly all one channel, exactly as the charter settled.

> **A recorded limit:** with ~150 hand-imported videos, recommendation is only *rearranging your
> own wardrobe* — it cannot produce YouTube's sense of discovery. Getting that requires a feed
> that pulls in outside videos (Phase 3).

## 7. Scope

### Phase 1 — the core loop
**Has:** ingest one URL → watchable · Home (3-column grid) · Watch (player + info + Next
sidebar) · login with 2 seeded accounts (no sign-up screen) · full-text search

**Does not have:** comments · transcript panel · history/likes/watch-later · dynamic chip
filters · ABR/hls.js · playlist/channel import · "Ask" (cut)

### Phase 2
Nested comments · click-to-seek transcript · history/likes/playlists · a mixed recsys ·
notifications · Storage page + eviction · playlist/channel import · move up to ABR

### Phase 3
Auto-follow channels (subscribe becoming real) · a `/tv` UI driven by a D-pad · a mobile app ·
*(optional)* a feed that pulls in outside videos

### Cut for good
- **CDN** — meaningless on a LAN
- **"Ask" AI** — cut from P1. If it comes back, prefer full-text search within transcripts (no
  LLM) over putting a 5GB model on a disk that is already tight
- **144p / 4K** — nobody watches 144p; 4K kills the disk
- **Flutter Web** — canvas rendering, cannot be a pixel-perfect clone

## 8. Known risks

1. ~~34 GiB is the sorest point~~ **solved with an external SSD** (see §2). The remaining risk:
   if the external drive drops, services write errors into a file on a path that does not exist —
   there is no test for that case.
2. **Microservices + gRPC with somebody who has never done gRPC** → P1 is considerably slower
   than a monolith, and the early time goes mostly into setup. Accepted at a known price. Using
   **ConnectRPC** (each service is debuggable with curl) rather than plain gRPC.
3. **HTTPS on a Smart TV is unproven.** Try it early against a real TV; do not leave it to Phase 3.
3b. **Background playback on iOS is impossible from the web (settled 2026-08-02).** iOS suspends
   both `<video>` and `AudioContext` when the tab goes to the background or the screen locks. No
   flag and no PWA gets past it. What the web does allow has been done:
   **Media Session** (metadata + lock-screen controls) and **Picture-in-Picture**.

   | | Android Chrome | iOS Safari |
   |---|---|---|
   | switching to another app | ✓ | ✓ **only while in PiP** |
   | locking the screen | ✓ | ✗ |
   | lock-screen controls | ✓ | ✗ |
   | narration (TTS) in the background | ✗ | ✗ |

   **Background narration — half fixed 2026-08-03, and the two halves are two different
   causes.** The older note here treated them as one, wrongly:

   | Cause | Where | Fixable? |
   |---|---|---|
   | **throttled timers** → nothing further is scheduled | Android, and hidden tabs on desktop | **Fixed** |
   | **the OS suspends `AudioContext`** | iOS backgrounded/locked | Not from the web |

   The first is what users actually hit on Android, and it is **not** a browser limit:
   `source.start(when)` is executed by the **audio thread** and needs no JS running. But the
   scheduling horizon was only ten seconds, so backgrounding killed the tick → silence within
   ≤10s. It is now `PREFETCH_SEC = 60`.

   **A required companion**: pause and seek moved to **listening to the video element's events**
   (`bindNarration`) rather than polling every 100ms. Scheduling far ahead while still relying on
   a timer means pressing pause from the lock screen — where timers do not run — leaves the
   narration talking for a minute after the picture has stopped.

   **`createMediaStreamDestination` → `<audio>` was tried and ABANDONED (2026-08-03).**
   The hypothesis: phones keep media elements alive in the background, so routing through one
   might rescue iOS. Measured on a real iPhone: **distinctly worse** — Safari puts a MediaStream
   through its real-time communication path, and the TTS came out **distorted** and **cut off
   after ~1 second** of every cue. Reverted to `ctx.destination`. Do not try this road again.

   **The distortion had a second, pre-existing cause:** `NARRATION_GAIN = 2.5` multiplied by
   `video.volume`. At full volume every sample above 0.4 amplitude clips, and TTS is normalised
   close to full scale — so it was distorting in the foreground too, just with nothing to compare
   against. There is now a `DynamicsCompressor` acting as a limiter before `destination`: the
   loudness stays, the clipping goes.

   Narration still breaks in the background on iOS. The only way through is for the server to
   build a track and mix it into the file — and that would require TTS over the whole video
   **before the first line could be heard**, destroying the "audible within seconds" property
   just built. A phase's worth of work.

   This is the concrete technical reason for "a mobile app" in Phase 3 (§7): not to look nicer,
   but because background playback on an iPhone is only possible natively.
   **A trap that comes with it:** the player holds two `<video>` elements; when switching tiers,
   PiP must be requested on the new element **before** the old one's source is cleared, or the
   PiP window closes mid-switch.
4. **yt-dlp breaks periodically** when YouTube changes something → ingest must handle failures
   gracefully and allow a retry.
5. **YouTube blocks by IP if too many full-metadata fetches are made (THIS HAPPENED 2026-07-29).**
   Topic backfill ran 8 threads in parallel; at around 800 videos every full fetch began
   returning *"Sign in to confirm you're not a bot"*. **The block is not confined to backfill** —
   it killed `ResolveStream` too, meaning **no video that was not already on disk could play**.
   Flat listing (the scanner's path) was **unaffected**.
   The lesson: a full metadata fetch is **expensive and counted**; flat listing is not.
   Backfill now runs **one thread, 4 seconds apart, 200 videos per pass by default**, and **stops
   itself after 15 consecutive failures** — pushing through a block only makes it longer.

## 8b. Build status (updated 2026-07-28)

### Working, verified with real requests

**Infrastructure:** 4 services (`catalog` 8181 · `recsys` 8182 · `ingest` 8183) + `gateway` 8180
+ web 5173. Moved off the 808x block because another project on this machine holds 8080 and 8082
permanently. Postgres 17, one schema and one role per service. ConnectRPC inside, REST outward.
`scripts/dev.sh` runs the whole stack — **6 processes**: the 4 Go services + the translation
sidecar (8005) + speech (8002, which lives in the `robot-esp32` repository and is started only if
found and not already running) — before handing the terminal to Vite. `scripts/stop.sh` stops it
again, **working by port**, so it also stops services started by hand after a rebuild or running
inside tmux. `make check` = buf lint + tsc + go build.

**dev.sh refuses to start on a held port (2026-08-04).** Both silent failures had happened for
real: a Go service printed `address already in use` into a log nobody was tailing while the
script carried on as though it had started, and Vite **quietly moved to another port**,
announcing it in a line of startup output nobody reads — so when the server holding 5173 later
stopped, the browser tab became a dead page while Vite was demonstrably running. Both look like
"the app is broken" rather than "two copies are running". Paired with `strictPort: true` in
`vite.config.ts`. And dev.sh **asks each port after starting** and prints `up`/`DOWN` — every
start line is a background job, so none of them can fail **visibly**.

**A trap already hit:** dev.sh's `trap cleanup EXIT` kills **every** child when it exits —
including when it exits because `npm run dev` (its last foreground process) was killed. Killing
an old Vite therefore killed catalog and the translation server too, and the symptom surfaced
somewhere else entirely ("Could not reach the library service", "502 translate"). Hit twice in
one session.

**Content:** `topics.yaml` is the feed's only source. The scanner runs **every hour** (changed
from twelve hours on 2026-07-29; adjustable through `SCAN_INTERVAL`, e.g. `SCAN_INTERVAL=30m`).
That interval **is** the feed's maximum freshness — nothing posted to YouTube can appear here
before a pass has seen it. A pass walks 63 sources in ~3 minutes using flat listings (cheap); the
expensive part is per-video metadata, which the scanner deliberately **does not** fetch (see
§8b). `POST /api/topics/refresh` scans now. Currently **280 videos / 6 topics / 7 sources**,
scanned in ~8 seconds, metadata only.

**Plays from the first second and seeks immediately (2026-07-29):** pressing play → `/stream`
lists the sources (§4), the player starts `instant` within ~17ms and **queues a background
download**; when it lands, it switches to `local`. Resting the pointer on a card for 250ms
prefetches the resolve, so pressing play usually has **no request left** to wait for. The resolve
cache in ingest measured **1.85s → 0.008s**.
**Seeking is always enabled** except in the `remux` tier.
**Two stacked `<video>` elements**: the new source is loaded and pre-seeked in the hidden one at
`now + 0.6s`, and at that moment their opacity is swapped. No black flash, no jumping backwards.
Changing one element in place is what caused the flash before.
The player autoplays (and if the browser blocks it, **stays on the first frame** rather than
muting itself — a video that clearly has not started is easier to understand than one that looks
like it is playing with no sound).
**The code drifted from that rule and was pulled back on 2026-08-03**: for a while it muted
itself and played on, with a "restore sound on the first gesture" mechanism attached. On desktop
that reads as a convenience; on an iPhone, Safari refuses autoplay with sound almost always, so
the fallback branch **is** the normal outcome — the video plays silently. And unmuting is not
just assigning `muted = false`: Safari requires `play()` to be called again within the same
gesture, which nothing did. That is why the rule exists.

The controls **hide after 3 seconds** with a mouse and **5 seconds with a finger** (reappearing
on move/press/focus, always visible while paused or with a menu open). **Mouse: click =
play/pause. Touch: tap = show/hide the controls** — a finger cannot hover, so tapping is the only
way to bring the controls up, and if it also toggled playback then every look at the controls
would interrupt what you were watching. Told apart by the event's own `pointerType`, not guessed
from screen width.
**`pointerleave` must NOT be used to hide on touch**: a touch pointer does not leave, it *ceases
to exist*, and the browser reports that with the same event — so every tap hid the controls on
lift, and because the sequence is `pointerdown → pointerup → pointerleave → click`, the bar had
`pointer-events` disabled exactly one event before the click meant for it. That is why "the menu
button does nothing" on an iPhone.
On touch the bar is trimmed: the volume slider goes (there are hardware buttons), and
subtitles/quality/narration/autoplay collapse into a ⚙ button; touch targets are 44px rather
than 36.
**The ⚙ panel on touch must portal to `document.body`** rather than being a dropdown inside the
player: the player has `overflow-hidden` (needed, to keep its rounded corners) and on a phone is
only ~220px tall, so a list opening upward **has its top cut off**. Observed for real.
**The swipe-down-to-minimise gesture was REMOVED (2026-08-03)** — users found it useless. The
whole mechanism went, not just the wiring. A consequence: on a phone the watch page **no longer
has** a mini state at all, because `IntersectionObserver` only ran on desktop. The bar appears
only when leaving the watch page.
**Mute must not be recorded from `volumechange`.** That event also fires for the player's own
changes (ducking for narration, and formerly the autoplay policy muting itself). It used to write
to `localStorage`, so one blocked autoplay stored silence as a **preference** and returned it on
every later visit — indistinguishable from a real choice. The key moved to `yt-player-muted-v2`
to discard the old values, and only the mute button and the volume slider write it.
**Fullscreen and PiP: where `webkit*` exists, PREFER `webkit*`, not the other way round (fixed
2026-08-03).** The first attempt put the standard API first and fell back to webkit only when the
standard was absent — wrong. Safari on iPhone **does** report Fullscreen API support, but what it
gives is an element enlarged *within the page*: **it does not rotate to landscape**, and there is
no system player to hand playback state back to on exit. Only `webkitEnterFullscreen` opens
Apple's native player. The same reasoning applies to `webkitSetPresentationMode`.
"Does this browser know the concept" is asked of `HTMLVideoElement.prototype` (the ref is empty
on the first render). But "does THIS video qualify" can only be answered by the element —
`webkitSupportsPresentationMode`, asked in an effect once the element exists.
**Leaving fullscreen on iOS returns the video paused** even when it was playing on the way in.
Listen for `webkitbeginfullscreen`/`webkitendfullscreen` to remember and restore it — otherwise
you leave a video and come back to a still frame. **Two traps, and the first fix hit both:**
(a) the memory of "it was playing on the way in" **must not** live in the effect's closure — the
source can change mid-flight (the local file finishing) and re-run the effect, taking the memory
with it; it has to be a ref.
(b) `pause` does **not** reliably arrive before `webkitendfullscreen`; checking once at that
moment can see a video still playing which then pauses immediately after. The late `pause` has to
be caught too.
**Reopening the app puts an unfinished video back in the corner, PAUSED (2026-08-03).**
`last-watched.ts` keeps `{videoId, position, savedAt}` in localStorage, written on the same
cadence as `recordProgress`. Three rules: past **95%** counts as finished and is **forgotten**;
past **7 days** is no longer "just now"; pressing ✕ is an answer to the offer, so that is
forgotten too.
**It does not autoplay** — sound coming out of the corner of a page you have just opened is a
fright, and this would be the second time in this project that a video started when nobody asked.
**The server's history is not used for this**: history says *what has been watched, on any
device*, while this says *what THIS browser is part way through*. Using history would re-offer
something already finished on another machine.
For the position, the server's number wins; the local one is used only when the tab closed before
the last report.
**A player that arrived by any other route counts as the offer having been taken up (2026-08-04).**
The "already offered" flag recorded only *this hook's own* offer, which was too narrow by exactly
one case: **reload the watch page** and the entry describing the video on screen is already in
storage, so the offer sits armed while the *watch page* activates the player. Walk out to Home and
that player becomes the corner window — and pressing ✕ produces precisely the state the offer is
waiting for, so the same video came straight back. The close button looked broken, and to anyone
who pressed it twice it looked like **two players had been stacked in the corner all along**,
which is how it was reported. The rule is now what it always meant: the offer is for a browser
that is part way through something and showing nothing, so once anything has been playing there
is nothing left to offer.

**Choosing the machine-translated track is itself a request to translate (2026-08-04).** The
translation pass ran only while **read-aloud** was on — it was written to feed the narration —
while the gateway attaches `*.vi-mt.vtt` as a track only **once a file exists**. So the option
appeared in the subtitle menu after somebody had narrated the video, and nothing but narrating it
again could fill it: a control that does nothing unless an unrelated feature is switched on,
which §5 rules out, and worse than dead because the track looked real and stayed nearly empty.
Measured on `2el-stE5mGM`: **68 successful translate batches in one session left 4 lines on
disk**, each short spell of read-aloud being cancelled before it covered much of a 2h14 video.
Now the pass starts on `narrationOn && autoTranslate` **or** on the track being selected, and the
menu offers **"VI (auto)" before any file exists** — gated on the same two conditions the pass
itself checks (English to translate from, no human Vietnamese track), so it never offers work
that would be refused.

**And the "Auto translate" switch is gone with it.** It described nothing a viewer decides — a
translation is wanted when the track is selected or when there is something to read aloud, and
both of those are said elsewhere in words about the thing itself. As a third control it only
qualified the other two, and being **on by default** it read as broken in both directions:
pressing it turned translation *off*, and pressing it again appeared to do nothing, because the
pass still needed read-aloud. What is left in its place is the progress line, shown only while a
translation is actually being made — a report, not a setting. **"Read aloud" is now "Vietnamese
narration"**: it reads the Vietnamese translation and nothing else, and switching it on also
brings that translation into being, which is a great deal to hide behind two words that never
mention Vietnamese.

**Uniform padding: `px-4` on phones, `px-6` from 700px.** Applied to every page.
The exception is Home's `ChipBar`, which bleeds to the edge on phones (`-mx-4` plus `px-4`
inside): a horizontally scrolling row that stops short of the edge looks finished rather than
continuing.
**A trap already hit:** WatchPage used to reserve `pt-[calc(3.5rem+56.25vw)]` — adding the
`TopBar`'s height **twice**, because `sticky` **still occupies space in the flow** so the content
already began below it. Exactly 56px of extra space between the picture and the title. It is now
just `pt-[56.25vw]`.

`Space`/`←`/`→`/`m`, a volume slider, a real buffered range, subtitles (en/vi) with a CC menu —
subtitles are fetched **before** the media file, so they are usable while the upstream copy is
still playing. At the end of a video there is a 5-second countdown before the next one (with an
Autoplay switch, stopping itself after 3 videos with no interaction).

**Search:** **always** asks YouTube alongside the library. The results page has two blocks (In
your library / On YouTube). Autocomplete comes from local data (topics → channels → titles).
Vietnamese diacritics are handled both ways through `unaccent`. Opening a video from YouTube
writes its metadata **without assigning a topic**; the feed is still decided by topics.yaml.

**Pagination:** feed and search use `useInfiniteQuery`, prefetching 600px ahead, with a real
"Load more" button for keyboards and remotes.

**Activity:** `/activity` brings together the download queue (with yt-dlp's error verbatim) and
the **scan history** (naming any source that failed). It has a real "Scan now" button. It is not
a log viewer — the four services still log to stdout.

**Extended 2026-08-04:**
- **A scan history, not a single pass.** `Scanner.lastScan` used to be **a variable in memory**:
  it could answer "how did the most recent pass go" and nothing else, and a restart lost even
  that. The question people actually bring spans days ("this channel has been quiet for a few
  days, has the scan been running?"). There is now an `ingest.scans` table, written after each
  pass, which **deletes rows older than 30 days in the same write** — tying what makes the table
  shrink to the only thing that makes it grow, with no second schedule. Pruned **by age rather
  than by row count**: 500 rows is three weeks at hourly and ten days at `SCAN_INTERVAL=30m` —
  the meaning would change with nobody touching it. `GET /api/scans?limit=&offset=`, **paged at
  the server**, because this table grows by a row an hour, forever.
- **"View more", ten at a time.** Jobs are paged **on the client** (`usePagedList`): the three
  groups Failed/In progress/Completed come from **one request**, so paging them at the server
  would be three queries and three cursors for a diagnostics page. Scans are the opposite case —
  paged at the server.
- **`[X]` on every job row, with two meanings by state**: running = **cancel the download**;
  finished or failed = **dismiss** (`jobs.dismissed_at`, `POST /api/ingest/jobs/{id}/dismiss`).
  Same place, same icon, told apart by `aria-label`/tooltip — a control that moves is a control
  you have to look for. The store allows dismissing **terminal states only**: hiding work that is
  still running is the one thing this button must never do.
- **`hideDismissed` is a parameter, not a rule.** Two callers read the same
  `GET /api/ingest/jobs` for different reasons: the Activity page is being tidied, while the
  **player reads it to learn its download has landed**. Filtering by default would let a
  dismissed, completed job leave the player waiting for a copy that had already arrived — the
  same shape as the "job falls off the list → stuck at 360p" incident above, from another
  direction. For the same reason the React Query key is **separate**
  (`['ingest-jobs','activity',limit]`): sharing one, the Activity page's fetch of 200 would
  overwrite the player's 50.
- **Retry on every Failed row** (`POST .../retry`): creates a **new job** with the same
  `sourceUrl` and `preferredHeight`, then dismisses the old row. The old row is not reset in
  place — `attempts`, the timings and the error reported **are** what this page exists to show.
  A job that has not finished is refused: two transfers of one video is exactly what got this
  address blocked (§8, risk 5).
  Retry exists because **there is no undo for `[X]`** (settled): if the only thing you can do
  with a failure is hide it, hiding becomes the default way failures are handled.
- **The scan history has no `[X]`** — it asks for no action, and hiding a row puts a hole in
  something meant to be read as a continuous sequence: "did it run last week" would then answer
  wrongly.

**An endless feed:** `GetFeedPage` freezes the ranking into a per-session snapshot (in recsys
memory, TTL 30 minutes) — later pages read from that snapshot rather than re-ranking, so videos
do not repeat. When a snapshot falls below 48 videos, the gateway calls `ExpandLibrary` (ingest)
in the background: deepening the sources in topics.yaml (with a cursor in Postgres so the next
pass continues where it left off) → related videos through InnerTube (`/youtubei/v1/next`,
hand-written, no contract) → searching by topic name. Only one expansion runs at a time.

**Cancelling a download on leaving a video (2026-07-29).** Pressing play queues a copy so the
next viewing comes from disk — but a copy **nobody is waiting for** is also a request to YouTube
nobody is waiting for, and this address has been blocked once for making too many (§8, risk 5).
Leaving the watch page sends `POST /api/videos/{id}/download/cancel`. Attached in the **effect's
cleanup**, so it covers both ways of leaving: moving to the next video, and closing the page.
**A known trade:** `NoPart()` is on, so cancelling midway loses everything fetched so far and the
next attempt starts from zero.

**And that was only half true until 2026-08-04 — the other half poisoned videos.** yt-dlp *does*
try to resume; with no part file it resumes **into the finished name**. A track a cancelled
attempt had already completed therefore made it ask for a range beginning at the end of the file,
and YouTube answered **416 Requested range not satisfiable**. The download failed and went on
failing: every retry found the same file and asked the same impossible question, so **one
cancelled download broke that video for good** — a state nothing else in this system can reach.
Measured on `2el-stE5mGM`: a 131MB audio track left complete beside a 3.3GB video track left
partial, and six consecutive job failures. `NoContinue()` makes the behaviour match the sentence
above. Keeping the bytes instead would mean dropping `NoPart()` so partial data lives in `.part`
files — resumable, but also litter the eviction sweep knows nothing about.

**Eviction:** catalog runs a sweep every hour
(`services/catalog/internal/usecase/evict.go`). Above 20 GiB it deletes the media files of
least-recently-used unpinned videos down to 16 GiB, keeping metadata, thumbnails and history.
The thresholds are set by `EVICTION_HIGH_BYTES`/`EVICTION_LOW_BYTES`.

### Not done — suggested order when picking this up again

(What follows became out of date after the 2026-07-31 session; see the newer sections below.)

1. ~~**Three missing pages**: `/history` · `/saved` · `/storage`. The APIs already exist
   (`ListHistory`, `GetStorageUsage`, `SetPinned`) — only the `ui/` layer is missing.
   **They are dead links in the sidebar.**~~ **DONE 2026-07-31.**

### Done 2026-07-31 — the "nine bugs" follow-up session

#### Three new pages
| Page | Components |
|---|---|
| `/history` | `HistoryPage.tsx` — infinite scroll over `GET /api/history`, a grid of `VideoCard` |
| `/saved` | `SavedPage.tsx` — infinite scroll over `GET /api/pinned` (pinned videos), a grid of `VideoCard` |
| `/storage` | `StoragePage.tsx` — stat cards (`usedBytes`/`budgetBytes`/etc.) plus an eviction-candidates grid with inline Pin/Unpin |

#### Sidebar
- Three links added: `Bookmark` → `/saved`, `Clock` → `/history`, `HardDrive` → `/storage`

#### New backend
- `POST /api/videos/{id}/pinned` — a REST route for the `SetPinned` RPC (which existed but was not exposed)
- `ListPinnedVideos` RPC — added through proto → domain → postgres repo → use case → RPC server → gateway `GET /api/pinned`

#### Keep/Pin UI
- `VideoActions.tsx`: the "Keep"/"Kept" button already had an onClick calling `useSetPinned`; the bookmark fills when pinned
- `VideoCard.tsx`: the ⋮ button opens a "Keep"/"Unkeep" dropdown
- `StorageBanner.tsx`: the dead "Manage storage" link became `<Link to="/storage">`

#### Feed ranking improvements (`ranker.go`)
- **Filter FAILED**: `f.MediaState == "MEDIA_STATE_FAILED"` → skip
- **Filter EVICTED**: `f.MediaState == "MEDIA_STATE_EVICTED"` → skip
- **Filter 85%+ watched**: `fraction >= 0.85` → skip (kept in up-next; filtered only on Home)
- **publishedAt penalty**: `score *= exp(-days/365)` with a 365-day half-life, plus a flat −4.0 beyond a year. **Out of date: there is now a hard filter skipping every video older than a year, using AddedAt as a fallback when PublishedAt is missing (2026-07-31).** See the "old videos" follow-up.

#### "Popular with you" improvements (`collections.go`)
- **READY only**: filter out `dto.MediaState != "READY"`
- **A composite hot score**: `viewCount × recencyMultiplier(addedAt, <30d) × log2(duration+1) × exp(-pubDays/365)` — rather than sorting by view_count alone
- **Recency decay**: addedAt 0→30 days maps 1.0→0.3; publishedAt uses exponential decay with a 365-day half-life

#### YouTube topic injection (`HomePage.tsx`)
- When browsing a topic (not "All"), call `useDiscover(topicName, 6)` → show a "From YouTube · {topic}" row using `ExternalVideoCard`

#### Superpowers plugin
- Added `"plugin": ["superpowers@git+https://github.com/obra/superpowers.git"]` to `~/.config/opencode/opencode.json` (global)

### Done 2026-07-31 — the "old videos" follow-up session

#### Hard filter on homepage videos older than a year (`ranker.go`)
- **`maxPublishedAgeDays = 365`** (a constant at `ranker.go:132`): a hard filter in `rankAll` — skip videos published more than 365 days ago. The old penalty (multiplicative + a flat −4.0) was not enough, because `applyDiscoveryQuota` reorders by reason bucket regardless of absolute score.
- **Epoch detection**: `hasPub = !PublishedAt.IsZero() && PublishedAt.Unix() > 0` — protobuf decodes a nil `Timestamp` as `1970-01-01T00:00:00Z`, **not** as Go's zero time (`0001-01-01`). `IsZero()` returns false for the epoch, so `20665 days > 365` filtered out the entire library (3567/3722). Using `Unix() > 0` catches both cases.
- **AddedAt fallback**: videos with no `PublishedAt` (824 of them, because a flat-listing scan does not return one) use `AddedAt` as a proxy.

#### Backfill widened: filling in `published_at` where it is missing
- **`server.go:288` bug**: the `ListVideoFeatures` RPC did not populate `PublishedAt` into the proto response, so the ingest client never saw the field. Fixed by adding `if !f.PublishedAt.IsZero() { feat.PublishedAt = timestamppb.New(f.PublishedAt) }`.
- **Renamed `ListVideosMissingTopics` → `ListVideosNeedingBackfill`**: instead of selecting only videos with no topic, it now selects videos missing a topic **or** missing `published_at` (a topic from `topics.yaml` but no date).
- **`VideoRef.MissingPublishedAt`**: a flag telling `backfillOne` that this video needs a date rather than a topic → skip the `preview.Category == ""` check and always upsert, so the date is written.
- **No proto or endpoint changes**: `POST /api/topics/backfill` works as before, just more widely.
- **824 videos are missing a date**, needing ~55 minutes of backfill (one thread, 4s between calls).

#### Bugfix: a stale ingest binary
- `/tmp/local-youtube/ingest` had been compiled before the port-change commit (17:54 vs 18:03 on Jul 28), so its default catalog URL was `:8081` rather than `:8181` → "connection refused". Rebuilt and restarted.

#### Bugfix: gateway without MEDIA_ROOT
- The gateway was running without the `MEDIA_ROOT` env var, defaulting to `./media` instead of `/Volumes/Data2/Youtube` → `/media/...` returned 404. Restarted with `MEDIA_ROOT=/Volumes/Data2/Youtube`.

### Decisions reversed along the way

- **"The stream cannot seek" → WRONG, remeasured (2026-07-29)**: an earlier note concluded that
  the remux stream "cannot seek until the local file has downloaded". Not so. `ffmpeg -ss 120` on
  an adaptive URL seeks by HTTP range and produces its first fragment after **2.1s** — expensive,
  not impossible. Recorded so nobody quotes the old sentence as a law of physics.
- **Baseline measurements, this machine, this network (2026-07-29)** — every playback decision
  must be checked against these:
  | | |
  |---|---|
  | one `yt-dlp -J` (returning **both** itag18 and adaptive) | 1.37s |
  | itag 18: TTFB / range | 17ms / `206` — native seeking |
  | remux → first fragment | 2.2s (before resolving) |
  | **full 1080p download, a 289s/42MB video, cold** | **2.3s** |
  | **full 1080p download, an 850s/67MB video, cold** | **7.6s** |
  | sequential read on one connection | throttled to 3.15 Mbps (yt-dlp is not affected) |

  **The consequence**: downloading the whole file is **faster** than the remux producing its
  first fragment. So the remux was demoted to a fallback for videos with no progressive format,
  rather than the main path.
- **Serve-while-downloading → remux fMP4**: the charter once settled on "the gateway serves the
  file **as it is being written**, answering 206 for the part it has". **Impossible, measured**: a
  1080p download is two separate streams (`1080p.f399.mp4` + `1080p.f251.webm`) merged at the
  end — the file `1080p.mp4` **does not exist** until the last second. There is nothing to serve.
  **Replaced by**: ffmpeg remuxing the two adaptive URLs into a **fragmented MP4** pushed straight
  through a pipe (`-movflags frag_keyframe+empty_moov+default_base_moof`). An ordinary MP4 puts
  its index at the end, so an unfinished one cannot play; fMP4 plays from the first fragment.
  `-c copy`, no re-encoding, CPU ≈ 0 — which honours §4's "no transcoding" rule.
  **The price**: the stream has no index → **it cannot seek** until the local file arrives (the
  seek bar is disabled, and says so). h264 is preferred over AV1/VP9 because older TVs can decode
  h264.
  **Context**: YouTube has dropped every high-resolution progressive format — only itag 18
  remains, at 360p. That is why the first viewing used to be blurry.
- **An old yt-dlp hid the adaptive formats**: version 2026.02.04 saw only itag 18; upgrading to
  2026.07.04 produced the full 144p→1080p set. Exactly risk §8.4 — **check the yt-dlp version
  before concluding "YouTube does not publish that format"**.

- **Search**: once settled as "local only" (question 3 of 12) → **reversed**: search always asks
  YouTube. The reason: the feed is what is served to you, search is what you go looking for —
  there is no reason to confine search to the feed's sources.
- **Playlists**: once planned as a `playlists` table plus watch-later → **dropped entirely**.
  Topics take their place, and "Keep" (pin) is the only personal collection.
- **`categories` → `topics`, first time**: YouTube has only ~15 global categories → dropped in
  favour of a hand-curated topics.yaml.
- **`categories` → `topics`, second time (2026-07-28, reversing the first)**: a video's topic
  comes from YouTube's own category (e.g. "Science & Technology"), the same taxonomy YouTube uses
  for its Subscriptions/Explore chips. Older videos keep their old topic names (Tech, Gaming…) —
  the two sets of names coexist in the sidebar.
  **Where the category comes from**: `--flat-playlist` (how scanning works) does **not** return a
  category — only a full per-video fetch (`Preview`, ~2.2s) does. So scanning and expansion
  **never** call Preview; the category is picked up **for free** in the two places that already
  call it: `EnsureVideo` (opening a video from search or a channel page) and the download worker.
  The trade: a video nobody has opened has no topic yet.
  **An alternative was tried and dropped**: having the scan fetch a category for each new video.
  Measured: an 8-second scan became 101 seconds for 40 new videos; across 55 subscribed channels
  that is ~73 minutes. Not worth it.
  **Added 2026-07-29 — not reversing the above, but compensating for its consequence**: because
  scanning assigns no topics, 2,337 of 3,092 videos (three quarters of the library) had none, and
  they were nearly invisible both to the filter chips and to the "topic" half of affinity. Added
  `BackfillTopics` (`POST /api/topics/backfill`, optional `?limit=`): **a separate pass, run when
  called**, 8 threads in parallel. Measured: 0.32s per video → ~12 minutes for the whole library,
  against ~96 minutes sequentially. The pass resumes naturally because it selects by "has no
  topic".
  It is safe because of catalog's own upsert: `media_state`, `media_path` and `added_at` are
  **not** in the `DO UPDATE SET`, and `topics` are merged rather than replaced — so a backfill
  cannot downgrade a downloaded video or erase a topic assigned by topics.yaml.
- **The channel page**: once read from the local catalog (`ListChannelVideos`) → **reversed**:
  read **live from YouTube**, paged by offset (`ListChannelUploads`). The reason: a scan brings
  back only the ~40 newest videos, so a channel page reading the catalog would stop at that
  number for a reason the viewer cannot see. Clicking a video that is not in the library →
  `EnsureVideo` writes its metadata and opens it, the same flow as a search result. Subscribing
  **does not require waiting for a scan** before the channel is browsable.
- **The feed**: once settled as "topics.yaml is the feed's only source" → **reversed**: when the
  feed runs low, the gateway calls `ExpandLibrary` to pull in more — deepening the sources in
  topics.yaml first, then related videos through InnerTube, and only then search. The reason:
  infinite scrolling is a requirement, and 280 videos run out after ~12 pages. The order of the
  layers is deliberate — the deepening layer cannot break, so if InnerTube fails the feed is
  still endless, only less varied.
- **Content sources**: once settled as "topics.yaml is the only source" → **reversed**: there are
  two, topics.yaml (curated ahead of time, in git) and subscriptions (chosen while using the app,
  in the database). Both feed the same scanner. The reason: subscribing to an unfamiliar channel
  without pulling its content in makes it a dead button — the catalog holds one video of that
  channel and the feed has nothing to promote.
  **The app never writes to topics.yaml** — that file belongs to the user.
- **Feed pagination**: an offset into a freshly re-ranked list → **a per-session frozen snapshot**
  (in recsys memory, TTL 30 minutes). The reason: `recordImpressions` penalises exactly the videos
  just shown, so the next page ranked against an already-different list and produced duplicates.

### Traps already hit — do not repeat them

- **A subtitle failure used to kill the whole video**: folding `--write-subs` into the download
  command meant a 429 from the caption endpoint made yt-dlp exit 1 → losing a video that had
  already downloaded. It is a separate pass now, and must not be folded back in.
- **Upgrading quality depended on a truncated list (fixed 2026-07-29)**: the player learned "the
  local file has arrived" by looking for its job in `GET /api/ingest/jobs` — a list of **every**
  job, capped at 50 and ordered by `created_at DESC`. A burst of finished jobs (a scan, or
  several videos opened in a row) pushed the **running** job off the list → the player never saw
  it finish → **the picture stayed at 360p until the page was reloaded**, with no progress bar
  either. Fixed in two places: (a) `List` orders unfinished work **first** and only then by
  recency; (b) **`useStream` polls every 5 seconds while there is no `local`** — that endpoint is
  what knows "what can play", so climbing a tier must not depend on the queue.
- **The DOWNLOAD path did not filter codecs; only the remux path did (fixed 2026-07-29)**:
  `Download` used a bare `bestvideo[height<=N]+bestaudio`, so yt-dlp took "best" = **AV1**.
  Measured on disk: **28 AV1 files + 4 AV1 + 2 VP9, and not a single h264**. Directly against the
  "prefer h264 for older TVs" decision above — and ironically **the local copy (the thing that
  replaces the stream) was the one at risk of not playing**, while the remux stream was h264.
  `downloadFormat` now copies `remuxFormat` exactly. Checked on `rYap5zVNYf8`: before, `av01`
  itag 399; after, `avc1` itag 299, **still 1080p**.
  **Files downloaded earlier are still AV1** — only deleting and re-downloading changes them.
- **`watch_ratio` was inflated at the source (fixed 2026-07-29)**: the client computed
  `element.currentTime / element.duration`, but an fMP4 remux stream reports a duration that is
  **still growing** — so the fraction approached 1.0 from the first second. A measured case: a
  243s video watched to 0:41 was recorded as **92% complete**. Because ranking treats watch_ratio
  as evidence that a video was worth opening, that one division quietly told it that everything
  abandoned early was excellent. The denominator now comes from the catalog's duration.
  **The old data is still skewed** — `BuildProfile` takes `max()`, so an inflated value does not
  fade on its own.
- **The channel page used a handle instead of an id (fixed 2026-07-29)**: the gateway converted a
  `UC…` id into an `@handle` before calling `ListChannelUploads`, with a comment giving the
  reason as "YouTube resolves handles more reliably". **The opposite is true**: a `UC…` id **is**
  InnerTube's `browseId` and needs no resolving, while a handle needs an extra step that does
  fail in practice (`@tinhte`, `@guinnessworldrecords`). Failing dropped it to flat listing → **a
  whole channel page with no upload dates and 0 views**, while other channels had both. After
  preferring the id: **0/30 → 30/30**.
  **Still outstanding**: the scanner still uses handles, because `topics.yaml` records sources as
  `youtube.com/@x/videos`, so videos scanned from the handle-broken channels still lack dates and
  view counts.
- **`--flat-playlist` omits a great many fields**: no per-entry channel (use `playlist_uploader`),
  no view count, no upload date, no `thumbnail` (use the `thumbnails` array).
  **Never default the upload date to now** — it renders as "1 minute ago" on every card.
- **yt-dlp gives human-written and machine-generated subtitles the same filename** → two passes
  are needed to tell them apart.
  **Fixed 2026-08-02 — still two passes, but in parallel.** They used to run one after the other
  and tell authored from automatic by **order**: whatever existed after the first pass was
  human-written. That meant the second pass waited on the first purely because of a filename
  collision. Each pass now writes into **its own temporary directory** (`.subs-authored` /
  `.subs-auto`), they run at the same time, and the results are merged — the same question
  answered by **where it was written** rather than by order, and nobody waits for anybody.
  **And the call site moved**: `FetchSubtitles` used to run only in the worker, meaning it queued
  behind `pollInterval` (3s), then `Preview` (~2s), then two sequential passes — measured at
  **5–12 seconds**, landing exactly in the window where subtitles matter most (while the
  low-quality upstream copy is playing). `Submit` now fires it the moment **play is pressed**, in
  the background. `FetchSubtitles` skips itself when the files are already on disk, so the worker
  and the new path do not duplicate work.
  **It never runs for `?prefetch=1`** — hovering across a feed is dozens of cards, each a full
  extract for a video nobody chose to watch. That is exactly the shape of the incident in §8, risk
  5. Only pressing play reaches `Submit`, so the boundary is already there rather than being an
  `if` somebody has to remember.
- **`ffmpeg` eats stdin** inside a bash loop → always pass `-nostdin`.
- **pgx encodes a nil slice as NULL** → violating an array column's NOT NULL constraint.

## 9. Open questions
- What make is the TV at home? (it affects how certificates have to be handled)
- ~~Is there an external SSD?~~ **Yes — `/Volumes/Data2/Youtube`, 437 GiB (2026-07-28).** See §2.
