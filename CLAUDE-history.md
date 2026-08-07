# CLAUDE.md — Historical Notes & Debug Narrative

> This file holds the long-form history, incident reports, and debugging narrative that was removed from `CLAUDE.md` during optimization. It is kept for reference, but the source of truth for current decisions is `CLAUDE.md`.

---

## §4 Media pipeline — detailed history

### Audio/video drift fix (2026-07-29)

The two inputs are seeked separately, so timestamps have to be normalised rather than trusted:
`-avoid_negative_ts make_zero` brings them to a common origin, and `-muxdelay 0 -muxpreload 0`
removes the delay the muxer inserts between the two streams. And `-frag_duration 1000000`:
with `frag_keyframe` alone, fragments run **1.9–4.9 seconds and unevenly** (measured), and a
browser reading a file that is still growing cannot present anything until it has a whole
fragment — so sound and picture arrive in blocks that do not line up. Cutting evenly at one
second brings fragments down to ~0.77s. After the fix, video and audio share a `start_time`;
before it they differed by 0.04s.

### That fix only covered opening at zero (2026-08-04)

Opened part way in, sound ran **2.008 seconds ahead of picture** — measured on `2el-stE5mGM` at `-ss 600`. The cause is the separate seeks themselves, not the muxer: an input seek lands on the nearest keyframe **at or before** the mark, so the video began at **597.972** while the audio began at **599.980**. The flags above then pulled each track down to zero **independently**, which erases that difference rather than preserving it. At offset 0 both inputs genuinely start together, which is why the 2026-07-29 measurement looked settled.

No timestamp flag can fix it — measured, do not try again. `-copyts`, `-start_at_zero`, `-avoid_negative_ts disabled` and `-af aresample=async=1` all produce byte-identical framing: the **fragmented** muxer rebases every track to its own first packet whatever it is told. The same two inputs written to an **ordinary** MP4 keep `start_time` 597.972 / 599.980 correctly — that contrast is what identified the muxer rather than the seek as the collapsing step.

The fix is to seek the two inputs to the same content time. `ProbeKeyframe` asks `ffprobe -read_intervals "T%+#1" -select_streams v:0` where the video will really land — **1.29s**, a few hundred kilobytes — and the audio is then given `-ss K` while the video keeps `-ss T`. Result: **597.972 / 597.960, a 0.012s gap**. Seeking goes from ~3.0s to ~4.3s, which is the price. Probed only when `T > 0`.

> Do not send both inputs to `K`. ffmpeg reads `-ss` as "at or before", so a mark that *is* a keyframe steps back to the previous one — 593.593, a 4.4s gap, worse than the bug.

### Remux seek bug (2026-08-04)

A seek in the remux tier showed "Seeking…" and then simply did not arrive. Two ways of asking for the same picture had drifted apart: the *upgrade* path hands over when the playhead reaches the mark the replacement is parked on, while the *seek* path — whose replacement starts somewhere the playhead will never reach — waited instead for `buffered.end(0) >= 0.5`, a condition that on a stream it could not satisfy never became true. On Auto the 45s patience timer eventually cancelled it; on a **pinned 1080p that timer was skipped entirely**, so the wait had no end at all.

The report contained its own diagnosis: seeking at 360p and *then* switching to 1080p worked perfectly, because that goes through the upgrade path. So a seek now takes that road: down to `instant` (progressive, natively seekable, at the mark within milliseconds), then the ordinary climb back to 1080p. Three consequences:
- The climb after a seek uses a 5s lead, not 20s.
- A seek gets one climb attempt that is not counted against `MAX_REMUX_ATTEMPTS`.
- The seek bar was `disabled` on the remux tier, so this path was reachable only by the arrow keys — the broken road was also the invisible one. It is enabled on every tier now.

### Overshooting the mark (2026-08-04)

The climb opens the mux at `position + 20s` and hands over when the playhead reaches it. On a long video preparation takes about as long as that lead allows, so the climb kept missing by a second or two — and missing meant **abandoning**, because handing over would have wound the viewer back. Visible in the ingest log as a mux opened and closed every dozen seconds, each at a slightly later mark: `from=1289.2` → closed after 12s → `from=1300.5` → closed after 9s → `from=1324.215`. Three misses and auto switched the tier off for the rest of the video, which is why **pinning 1080p by hand was the only thing that worked** — pinning ignores that count. The replacement is now wound forward to where the viewer actually is, **inside data that has already arrived**, the same move a seek makes. It is given up only when nothing has arrived at that point. The **paused** branch does it too.

### Handover bound to source (2026-08-04)

Three things write the pending-tier record — the climb, the probe that refines where a muxed stream begins, and a seek — and two of them can be in flight within a second of each other. Whichever wrote last decided what the player believed it was watching, regardless of what had been loaded. Measured on a real seek to **2059.5s**: the picture was the 360p rendition at 2059.5 while the offset came from the muxed stream's keyframe at **2056.8**, so the clock read **4130** — the two added together. Worse, the tier was recorded as `remux` while `remux` was exactly what the player was still trying to reach, so `targetTier` returned nothing and **it never climbed again** for the rest of the video; toggling Auto → 1080p → Auto by hand was the only way out. The record now carries the URL it was made for, and both the handover and the positioning at `loadedmetadata` refuse a record that does not match the element's own `src`.

### Files that disappear without being evicted (2026-08-04)

Deleting a file inside `MEDIA_ROOT` by hand left the database **still saying READY**, `/stream` still offering `local`, and the player holding a `/media/...` URL that 404s. `handleStream` now stats the file before offering `local`:

| What the disk says | What happens |
|---|---|
| the file is there | `local` as before |
| root present, file gone | `SetMediaState(EVICTED)` → fall through to `instant`/`remux` + queue the download again, with `repaired: true` |
| root unreadable | write nothing, return `streamError` about the drive |

- The order of the two checks is the whole safety of it. `MEDIA_ROOT` is an external SSD; unplug it and every file is "gone", so without the distinction one loose cable would mark the entire library evicted.
- `?prefetch=1` still repairs the row and still downloads nothing.
- The `repaired` flag is required: the watch page stops polling once the state looks settled, so without it the "downloaded" badge stays wrong.
- There is no periodic reconciliation sweep.
- This repair path does not clear `media_path` (the sweep does).

Measured 2026-08-04: moving `XXplTbQR9to`'s file → `/stream` answered `local:false, instant:true, remux:true, repaired:true`, the row became `EVICTED`, the job ran on its own, and twenty seconds later it was READY again.

---

## §5 Frontend — detailed history

### Share button reversal (2026-08-05)

The LAN address is a link only to somebody sitting in the house and on the same network; sent anywhere else it is a dead string. The address that means the same thing to everybody is `video.sourceUrl`, falling back to `https://www.youtube.com/watch?v=<id>`.

On a phone it was broken immediately: a plain-HTTP LAN address is **not a secure context**, and browsers withhold both `navigator.share` and `navigator.clipboard` there — the properties are absent, not permission-gated. `localhost` is the one exemption. So `document.execCommand('copy')` is the clipboard every device on this network has until HTTPS is settled. The share sheet is unreachable for the same reason.

### Background playback on iOS (2026-08-02)

iOS suspends both `<video>` and `AudioContext` when the tab goes to the background or the screen locks. No flag and no PWA gets past it.

| | Android Chrome | iOS Safari |
|---|---|---|
| switching to another app | ✓ | ✓ only while in PiP |
| locking the screen | ✓ | ✗ |
| lock-screen controls | ✓ | ✗ |
| narration (TTS) in the background | ✗ | ✗ |

Background narration was half fixed 2026-08-03:
- Throttled timers → nothing scheduled (Android/desktop hidden tabs): fixed by extending prefetch horizon to `PREFETCH_SEC = 60`.
- OS suspends `AudioContext` (iOS backgrounded/locked): not fixable from the web.

`createMediaStreamDestination` → `<audio>` was tried and abandoned: on a real iPhone it came out distorted and cut off after ~1 second of every cue. Reverted to `ctx.destination`.

The distortion had a second, pre-existing cause: `NARRATION_GAIN = 2.5` multiplied by `video.volume`. At full volume every sample above 0.4 amplitude clips. There is now a `DynamicsCompressor` acting as a limiter before `destination`.

### TTS pre-generation (2026-08-05)

Clips used to be synthesised in the playback loop three cues ahead, and anything not ready when its cue arrived was dropped. It fell hardest on long lines: reading speed is `natural / slot`, and the natural length is an output of the synthesiser, so a line that overran its slot needed a second request at a corrected tempo, and because tempo was part of the cache key that second request was always a miss, taken synchronously mid-commit.

Three changes:
- `narration-pregen.ts` sweeps the whole video while the video is paused, never decoding.
- The gateway computes tempo: `POST /api/tts` takes `slotSeconds`, synthesises once at natural speed, measures the WAV, stretches with `atempo`, and reports the tempo in `X-TTS-Speed`. The natural copy is cached under its own key.
- A line needing more than `MAX_SPEED` is refused (HTTP 422), not clamped.

The sweep stops, waits (15s → 30s → 60s, reset by any success), and resumes on its own. It is idempotent. Lifetime is the miniplayer's (`[videoId]` cleanup). Changing voice clears the clips and re-sweeps. The translation prompt now carries a per-line time budget and is written in English.

### Mobile watch layer (2026-08-05)

On a phone the watch screen is a layer over the tab you came from. Pulling the player down does not "go to Home" — it puts the layer away and reveals whatever was there. Implemented with one route table in `app/routes.tsx`; the layer underneath is a second `<Routes>` driven by a remembered location in AppShell. `canGoBack` is derived from that same memory, not `window.history.state.idx`.

- `DISMISS_FADE_FRACTION = 0.1`: everything except the player clears within a tenth of the screen's height.
- The picture is pinned below the status bar using `--safe-top`.
- The way out is the drag and the browser's own back button.

### Scroll moved from window to `<main>` (2026-08-05)

Restoring a scroll position made the top bar flicker on every tab switch. The cause was scrolling the window itself: on a phone, that slides the browser's address bar in and out, resizing the viewport. An element scrolling inside a frame that fills the screen is what a native app does. `html, body, #root` are pinned at `height:100%; overflow:hidden`.

Scroll memory is three rules:
- switching tabs → keyed by path;
- going back → keyed by history entry;
- drilling in → starts at the top.

Four things reset a scroll position that should not have:
1. `HomePage` reset on mount, not only on topic change.
2. Restore ran in an ordinary effect (after paint).
3. With the watch layer up, `<main>` holds the page underneath while the location says `/watch`.
4. `onExpand` scrolled the page to the top on mobile where there is no slot.

Horizontal rows remember separately (`useRememberedScrollX`, `sessionStorage`). Pull to refresh on Home, phones only.

### Mobile subscriptions & settings (2026-08-05)

`/subscriptions` lists channels; tapping one opens `/channel/:id`. The bottom bar is five entries max, so Storage and Activity moved to the top of Settings. Settings on a phone became a list of rows; each panel has its own screen (`/settings/feed`, `/settings/narration`, `/settings/translation`).

- A channel drops the search bar and tab bar and draws `BackBar`.
- `bare-screens.ts` is the single list of those screens.
- A screen names itself once; panels drop their own heading (`headless`).
- The player is inside the scroller; `data-player-host` marks it as not part of the page's scroll surface.
- Every bar grows into the home indicator; only its content stops short.
- A drag aims at where the player will land, not at the screen it is leaving; that memory lives in `PlayerProvider`.
- "Is the chrome drawn" is one fact and lives in `PlayerProvider`.

### Player control quirks

- Autoplay: if the browser blocks it, the player **stays on the first frame** rather than muting itself. The code drifted from this rule and was pulled back 2026-08-03.
- Controls hide after 3s (mouse) / 5s (finger). Mouse click = play/pause; touch tap = show/hide controls.
- `pointerleave` must NOT be used to hide on touch.
- On touch the bar is trimmed: volume slider removed; subtitles/quality/narration/autoplay collapse into a ⚙ button; touch targets 44px.
- The ⚙ panel on touch portals to `document.body`.
- Swipe-down-to-minimise was removed 2026-08-03.
- Mute must not be recorded from `volumechange`.
- Fullscreen and PiP: prefer `webkit*` where it exists.
- Leaving fullscreen on iOS returns the video paused; listen for `webkitbeginfullscreen`/`webkitendfullscreen`. Corrected 2026-08-05: the state that matters is the one on the way OUT.
- Reopening the app puts an unfinished video back in the corner, PAUSED. `last-watched.ts` keeps `{videoId, position, savedAt}`; past 95% counts as finished; past 7 days is no longer "just now"; pressing ✕ is forgotten.
- A player that arrived by any other route counts as the offer having been taken up (2026-08-04).
- Uniform padding: `px-4` on phones, `px-6` from 700px.
- WatchPage used to reserve `pt-[calc(3.5rem+56.25vw)]` — adding `TopBar`'s height twice.

### Pasted link handling (2026-08-05)

Search ran `ytsearch20:<string>`, so dropping an address in asked YouTube to go looking for text that already stated where the video was. And the library half is full-text over titles/channels, which an address never matches. `videoIDFromSearch` now reads the id out of five shapes and asks the catalog first by id. A hit ends the request with no upstream call. This required a new `PreviewVideo` RPC because the gateway previously only had `EnsureVideo`, which writes.

### Subtitles and translation fixes (2026-08-05)

Three faults reported together, all only on a video that had just been added:
1. Subtitles stayed on after being switched off — the preference was applied to the front `<video>` only.
2. Narration was silent until the viewer seeked — `pump` advanced its cursor before asking for the audio.
3. The translated track stopped showing part way through — the track's address never changed, so the browser did not refetch.

`narration-watchdog.ts` is a net, not a sweep. The top bar's height has been counted twice four separate times. Swapping `<Outlet/>` for `<Routes>` at the same position tears the page down. Two adjacent `backdrop-filter` layers can never line up.

---

## §6 Recommendation — detailed history

### Feed mix sliders (2026-08-04)

The old fixed quota (30% unwatched / 25% recently added / 20% subscribed / 15% continue / 10% rewatch) became adjustable sliders on `/settings` → "Home feed". Three sliders total 100, dividing 82% of the page; the other 18% is continue watching 10% + rewatch 8%, fixed and not adjustable.

- `feedSlot` is separate from `Reason`.
- Defaults are the old quota converted: 25/60/15.
- Zero means gone; those videos go to the end of the list.
- Stored at the gateway (`config/feed-mix.json`), sent in `GetFeedRequest.mix`.
- One mix for the household, not per user.
- Saving drops the `['feed']` cache.

### Scoring signal history

- `BOUNCED` became a Reason of its own because `fraction <= 0.02` used to fall into the default branch and collect the same +1.5 as never watched.
- Affinity is read from watch history, not from likes alone: 9 likes vs 2,045 watch signals.
- `RecentlyWatched` (3-hour window) with `penaltyRecentlyWatched = 8.0` was added to break a two-video loop.
- `applyChannelDiversity` was added after 44% watch time concentrated in one channel produced 23 of the first 24 videos from that channel.
- Retention is computed in recsys, not asked of catalog.

### Dislikes evolution (2026-08-04)

Before 2026-08-04 a dislike taught nothing. Extended to:
- The video pressed is gone for good.
- `buildDislikeAffinity` mirrors likes but multiplied by −0.7 vs +2.0.
- 90-day half-life.
- Suppressing a channel needs ≥3 videos and ≥30% of that channel's videos, plus a ceiling of ≥8. Subscribed channels remain immune.

NoCopyrightSounds lesson: the `Music` topic had `sources: []`, so `ExpandLibrary` fell through to searching YouTube for the topic's name.

### Up-next reversal (2026-07-29)

It used to be same channel > same tag. Reversed to subject leads, channel is one way of sharing subject. `weightSameChannel = weightSharedTags = 2.5`, added per matching tag, with a hard cap of 3 videos per channel in the rail.

Why reversed: old order made the rail 20/20 from one channel. Measured afterwards: Entertainment video → 20/20 Entertainment across 9 channels; Music video → 20/20 Music across 10 channels.

Three causes of the earlier wrong behavior:
1. `TopicScore` summed across a video's topics.
2. `weightContinueWatching` (+3.0) in up-next exceeded same-channel + same-topic.
3. `weightRetention` with one user counted their taste twice.

Fix: in up-next, everything not a relationship to the video playing is multiplied by `upNextTasteDamping = 0.35`.

---

## §8 Risks & build status — detailed history

### Dev.sh port checking (2026-08-04)

Silent failures had happened: a Go service printed "address already in use" into a log nobody was tailing while the script carried on, and Vite quietly moved to another port. Now `dev.sh` refuses to start on a held port and asks each port after starting, printing `up`/`DOWN`. Paired with `strictPort: true` in `vite.config.ts`.

A trap already hit: `dev.sh`'s `trap cleanup EXIT` killed every child when it exited — including when it exited because `npm run dev` was killed. Hit twice in one session.

### Player quality-upgrade bug (2026-07-29)

The player learned "the local file has arrived" by looking for its job in `GET /api/ingest/jobs` — a list of every job, capped at 50 and ordered by `created_at DESC`. A burst of finished jobs pushed the running job off the list → the picture stayed at 360p until reload. Fixed by (a) `List` orders unfinished work first, then recency; (b) `useStream` polls every 5 seconds while there is no `local`.

### Codec filter missing from download (2026-07-29)

`Download` used a bare `bestvideo[height<=N]+bestaudio`, so yt-dlp took "best" = AV1. Measured on disk: 28 AV1 files + 4 AV1 + 2 VP9, and not a single h264. `downloadFormat` now copies `remuxFormat` exactly. Files downloaded earlier are still AV1.

### watch_ratio inflation (2026-07-29)

The client computed `element.currentTime / element.duration`, but an fMP4 remux stream reports a duration that is still growing. A 243s video watched to 0:41 was recorded as 92% complete. The denominator now comes from the catalog's duration. Old data is still skewed because `BuildProfile` takes `max()`.

### Channel page handle bug (2026-07-29)

The gateway converted a `UC…` id into an `@handle` before calling `ListChannelUploads`, with a comment saying "YouTube resolves handles more reliably". The opposite is true: the `UC…` id is InnerTube's `browseId`. Failing dropped it to flat listing → whole channel page with no upload dates and 0 views. After preferring the id: 0/30 → 30/30. Scanner still uses handles because `topics.yaml` records sources as `youtube.com/@x/videos`.

### Download cancellation bug (2026-08-04)

`NoPart()` was on, so cancelling midway loses everything fetched so far. But yt-dlp tries to resume; with no part file it resumes **into the finished name**. A track already completed made it ask for a range beginning at the end of the file, and YouTube answered 416. One cancelled download broke that video for good. `NoContinue()` makes the behaviour match the intent.

### Baseline measurements (2026-07-29)

| | |
|---|---|
| one `yt-dlp -J` (returning both itag18 and adaptive) | 1.37s |
| itag 18: TTFB / range | 17ms / 206 — native seeking |
| remux → first fragment | 2.2s (before resolving) |
| full 1080p download, 289s/42MB video, cold | 2.3s |
| full 1080p download, 850s/67MB video, cold | 7.6s |
| sequential read on one connection | throttled to 3.15 Mbps |

---

## Done sessions

### Done 2026-07-31 — three new pages

| Page | Components |
|---|---|
| `/history` | `HistoryPage.tsx` — infinite scroll over `GET /api/history`, grid of `VideoCard` |
| `/saved` | `SavedPage.tsx` — infinite scroll over `GET /api/pinned`, grid of `VideoCard` |
| `/storage` | `StoragePage.tsx` — stat cards plus eviction-candidates grid with inline Pin/Unpin |

Sidebar links added: `Bookmark` → `/saved`, `Clock` → `/history`, `HardDrive` → `/storage`.

Backend:
- `POST /api/videos/{id}/pinned`
- `ListPinnedVideos` RPC → gateway `GET /api/pinned`

UI:
- `VideoActions.tsx`: Keep/Kept button
- `VideoCard.tsx`: ⋮ dropdown with Keep/Unkeep
- `StorageBanner.tsx`: Manage storage link

Feed ranking improvements:
- Filter FAILED, EVICTED
- Filter 85%+ watched on Home
- publishedAt penalty (later replaced by hard filter)

"Popular with you" improvements:
- READY only
- Composite hot score: `viewCount × recencyMultiplier(addedAt, <30d) × log2(duration+1) × exp(-pubDays/365)`

YouTube topic injection: `useDiscover(topicName, 6)` on topic browse.

### Done 2026-07-31 — old videos

- Hard filter: skip videos published more than 365 days ago. Epoch detection: `PublishedAt.Unix() > 0`. AddedAt fallback when PublishedAt missing.
- Backfill widened: `ListVideosMissingTopics` → `ListVideosNeedingBackfill`; selects missing topic or missing `published_at`.
- Bugfix: `ListVideoFeatures` RPC did not populate `PublishedAt`.
- Bugfix: stale ingest binary had old port.
- Bugfix: gateway running without `MEDIA_ROOT`.

### Done 2026-08-05 — RSS feed for publish dates

Problem: 791/4427 videos had no `published_at` because `--flat-playlist` omits upload dates.

Solution: YouTube RSS feeds (`youtube.com/feeds/videos.xml?channel_id=UC...`) carry exact timestamps, view counts, and the 15 most recent uploads per channel. ~0.26s per channel, no API key.

Implementation:
- `services/ingest/internal/domain/ingest.go`: `RSSEntry` + `FetchChannelFeed`
- `services/ingest/internal/adapter/ytdlp/downloader.go`: XML parsing
- `services/ingest/internal/usecase/scanner.go`: patches gaps without overwriting existing values
- Tests added

Key decisions:
- RSS is supplementary, not replacement.
- Gap-filling, not overwriting.
- Existing `COALESCE` in catalog upsert handles preference.
- No new endpoint; runs inside every scan.

---

## Decisions reversed along the way

- **"The stream cannot seek" → WRONG.** Remux can seek via `ffmpeg -ss`; expensive (~2.1s), not impossible.
- **Serve-while-downloading → remux fMP4.** A 1080p download is two separate streams merged at the end; the file does not exist until the last second. Replaced by ffmpeg remuxing adaptive URLs into fragmented MP4 through a pipe (`-movflags frag_keyframe+empty_moov+default_base_moof`).
- **Old yt-dlp hid adaptive formats.** Version 2026.02.04 saw only itag 18; upgrading to 2026.07.04 produced full 144p→1080p set.
- **Search: local only → always ask YouTube.** Search is what you go looking for; feed is what is served.
- **Playlists dropped entirely.** Topics take their place; "Keep" (pin) is the only personal collection.
- **Categories → topics, twice.** First to hand-curated topics.yaml, then to YouTube's own category taxonomy. Scanning assigns no topics; category picked up for free in `EnsureVideo` and download worker. `BackfillTopics` added as separate pass.
- **Channel page: local catalog → live YouTube.** Read via `ListChannelUploads` paged by offset.
- **Feed: topics.yaml only → ExpandLibrary.** Deepen sources → InnerTube related → search by topic name.
- **Content sources: topics.yaml only → topics.yaml + subscriptions.** Both feed the same scanner; app never writes to topics.yaml.
- **Feed pagination: offset into fresh rank → frozen snapshot.** Per-session snapshot in recsys memory, TTL 30 minutes, to avoid duplicates from impression penalty.
