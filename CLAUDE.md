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
| Disk | External SSD at `/Volumes/Data2/Youtube`, 437 GiB free. `STORAGE_BUDGET_BYTES`/`EVICTION_HIGH_BYTES`/`EVICTION_LOW_BYTES` come from `scripts/dev.sh` (budget 300 GiB, sweep 350→300 GiB). The **folder** is set on the Storage page and saved to `data/storage.json`, which **wins over `MEDIA_ROOT`** — see §4. |
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
| `hls` | The same two adaptive tracks, described as a playlist the browser combines. **720p** | ✅ |
| `remux` | Two adaptive tracks muxed directly into fMP4, **720p** on auto, **1080p** when pinned | ⚠️ reopen stream |
| ~~`instant`~~ | YouTube progressive file (itag 18, 360p). **No longer offered** — see below | — |

The player opens on the best tier the browser can play — `local` if it is there, else `hls`, else `remux` — and climbs to `local` once the download lands. Still no transcoding: HLS here is a description of YouTube's own files, not a re-encoding of them.

### The mux does not work on a phone, and HLS does (2026-08-20)

**HLS is offered beside the mux, and it is the opening tier wherever the browser can play it.** Measured on the household's iPhone (iOS 18.7), the same video minutes apart, through the app:

| | muxed stream | HLS |
|---|---|---|
| pressing play | **no picture, ever** | plays |
| duration reported | none — no index | **641.8s** |
| seeking | — | **works**, twice, native |

- **This is not a preference, it is the only tier that works there.** iOS has `ManagedMediaSource` and **no `MediaSource`** (measured), so hls.js cannot stand behind native HLS the way it can on Chrome. A phone with no local copy and no HLS has nothing to play.
- **Why it stayed hidden for a week**: the mux works fine in desktop Chrome, and every measurement before the phone was taken there. The reverse is also true — Chrome cannot play these playlists at all (`MEDIA_ERR_SRC_NOT_SUPPORTED`), so it keeps the mux until hls.js is wired in. Neither tier covers both.
- **`canPlayType('application/vnd.apple.mpegurl')` must not be believed.** It answers `"maybe"` on Chrome, which fails, and `"maybe"` on iOS, which succeeds. `web/public/mse-check.html` asked exactly that, and the HLS work was built believing it. `hls-source.ts` reads the engine instead: `ManagedMediaSource` present, or Apple vendor and not Chromium.
- **HLS is seekable because a media playlist *is* an index.** That one difference is what the mux's offsets, marks, leads, reopens and handover timing all exist to work around — so a video on this tier needs none of them. That is the whole argument for Phase 2.
- **`remuxFailed` does not hold HLS back.** That flag records a muxed stream that could not keep up, which says nothing about a tier that never goes through ffmpeg.
- **A `stalled` event is not a stall.** iOS fired one during linear playback with no `waiting` beside it — network idle after the buffer filled, not a starved picture. `waiting` is the one that means the picture stopped, and it appeared only at start-up and after seeks. Segments were measured at 12–510 ms for 3–7 s of video, through the gateway and through the Vite proxy alike, against 0.61 MB per 5.4 s needed.
- **The codec string is now checked before a playlist is written** (`domain.ValidCodec`). It comes straight from yt-dlp, which says `vp9` and `none` as readily as `avc1.4d401f`, and a `CODECS` value a player cannot read is refused before a byte is fetched — no request, no log, a generic error. On the one device with nothing behind it, that is the end. A playlist that cannot be described correctly is not served at all.
- **A refused segment drops the cached URLs and resolves once more.** Only the playlist path did that, and the playlist is fetched once at the start — so a signed URL dying mid-video broke every remaining segment for the rest of the 90-minute TTL while nothing re-resolved, and the player could report only a stream that stopped.
- **Autoplay still does not happen on iOS, and that is §5's decision, not a fault.** Audible autoplay needs a gesture Safari has not been given; the first frame sits still rather than the video playing muted.

### A broadcast still on air is its own tier (2026-08-22)

**A live video publishes no file, so nothing else here can play it.** Measured on `iipR5yUp36o` (ABC News Live, `is_live`, `duration: None`): **all seven formats are `m3u8_native`** — five video-only avc1 rungs from 144p to 720p, two audio-only — and **not one plain https URL among them**. `resolveTracksOnce` filters on https, so live resolved to nothing at all; the local, HLS and remux tiers all begin from a file with an index, and there is no file.

Measured end to end through this server, Chrome and hls.js:

| | |
|---|---|
| ladder | **144p, 240p, 360p, 480p, 720p** |
| opens at | `t=3589.7` — the live edge, not zero |
| `seekable` | **0..3605** — an hour of rewind |
| rewinding 60s | `playing` again **0.4s** later |

Confirmed on the household's iPhone as well, which is the reading that mattered: Safari plays it natively.

- **The segments are MPEG-TS, and that was the open question.** Every other tier serves fragmented MP4. Reading the code could not answer whether Safari and hls.js accept `.ts` through a proxy, so it was measured rather than assumed.
- **The proxy is not a preference.** googlevideo answers a browser's request with **200 and no `access-control-allow-origin` at all**, so a page cannot fetch these playlists directly whatever the URL says. `/api/live/{id}/master.m3u8` builds the master, `/playlist.m3u8` rewrites the media playlist's segment URLs, and `/segment` carries the bytes — the same reason `/instant` was proxied, arriving from CORS instead of from signing.
- **The signed URL travels base64'd in the query, behind a `.googlevideo.com` host allowlist.** Without the allowlist the segment route is an open proxy to anywhere on the internet, reachable by anyone on the LAN.
- **Rewinding is YouTube's window, not ours.** `playlist_duration/3600` is where the hour comes from; there is nothing on this side to extend it.
- **`expire` is about six hours out**, so a playlist held open longer than that stops serving and has to be resolved again. Not yet measured, and recorded as unmeasured.
- **Nothing about this tier downloads.** §4 already refuses a live broadcast as a download job — it has no end to download to, and one occupied the single worker slot for hours. That rule is untouched and is exactly why watching one needs a tier of its own.
- `web/public/live-check.html` is the page these numbers came from, kept for the same reason as `hls-eq-check.html`: the question returns with every browser release, and reading a library's source is not the same as playing a broadcast on the device.

### Finding the broadcasts, and the chip that gathers them (2026-08-22)

**Nothing here could see a live broadcast, and the reason was the tab.** The hourly scan walks `/videos`; measured on ABC News, one request each: `/videos` carried `live_status` on **0 of 40** entries and listed no broadcast at all, while `/streams` carried it on **40 of 40** — 1 `is_live`, 39 `was_live`. RSS is worse than useless for it: no live marker of any kind, and it answered **404** for that channel outright.

- **A flat listing reports liveness perfectly well.** `domain/ingest.go` said only a full metadata fetch does, for a release. That was wrong, and believing it is why nobody looked: yt-dlp fills `live_status`, not `is_live`, and `isStillBroadcasting` has always read that field first. So this costs nothing that §8 risk 6 counts — **0.6s per channel** at `--playlist-end 5`, measured across ABC News, NASA, LofiGirl and MKBHD.
- **`ScanLive` runs on its own ten-minute timer** (`LIVE_SCAN_INTERVAL`, zero disables), over subscribed channels only. A pass is ~3.5 minutes over 375 of them, so five minutes would never rest and an hour would let most of a broadcast finish before anyone was told. **Measured on the real stack: 375 channels, 18 on air.**
- **It must not go through `listSource`.** That prefers the browse API for channels, and browse carries view counts and upload dates but no live status — a pass through it would run, cost the requests, log cheerfully and find nothing, which is the worst kind of failure because it looks like an answer.
- **`is_upcoming` is recorded, and recording is not listing.** Conflating the two was a fault: a scheduled broadcast appears in Home like any other video, and with nothing stored `handleStream` had no idea — measured on `mYPF7KARk5Q`, a subscribed channel's stream, yt-dlp answers **"This live event will begin in a few moments"** while the app offered `hls` and a mux built from adaptive tracks YouTube has not published, and the viewer got a generic failure over a video that waiting would have fixed. The stream answer is now `{"upcoming": true}` and the player says so.
  - **Not `unavailable`**: that means permanent, offers no retry and names a reason. This is the opposite — nothing is wrong and the answer changes on its own.
  - **`cacheDisabled` is deliberately not set here**, unlike the live branch. Its one job is to stop the player re-asking, and re-asking is exactly what has to keep happening: the broadcast starting is a change in this answer and nothing else reports it. Setting it would have made "it will begin playing on its own" a lie.
- **`is_upcoming` is not Live.** It genuinely comes back — NASA had one sorted *above* two `is_live` — and pressing it plays nothing. A dead control is bad; one wearing a red dot is worse.
- **The answer expires, so it is stored with the moment it was given.** `live_status` (yt-dlp's own word, not a boolean — `was_live` is what tells a broadcast from its recording) plus `live_checked_at`, and **one** definition of "on air now": `live_status = 'is_live' AND live_checked_at > now() - 30 min`, computed in SQL beside the index that serves it. Sending the timestamp to each reader would be a second definition, agreeing with the first until one of them changed.
  - **`COALESCE` is not optional there.** A comparison against NULL is NULL, not false, and NULL is the ordinary case — nothing has asked about all but a few hundred rows. Without it every video in the library failed to scan into a bool.
- **A broadcast is exempt from the 365-day age filter.** ABC News Live was published **479 days ago**, so Home dropped it for being stale while it was broadcasting. The filter measures a publish date as a proxy for how old the content is, and for a 24/7 stream that date is the day somebody switched the encoder on.
- **The field had to be copied in *both* directions.** `featuresToProto` carries a note about `is_short` crossing the wire as a field nobody mapped; this went the same way on the way *in*, and it was caught only because a real pass found 18 broadcasts and wrote **none** of them. Nothing failed anywhere — an empty string is absent from proto3 JSON, so the wire looked exactly like a field that had not been added yet.
- **The Live chip is a list, not a ranking.** `GET /api/live`, no page token: "everything on air" is the whole promise, the set is a few dozen, and `applyChannelDiversity` would hide some of it. Filtered to the member's own subscriptions — one member follows 8 channels and another 152.
- **The chip is absent when nothing is on air.** A red dot is a claim that something is happening; one lit over an empty grid teaches people to stop believing it.
- **The dot's ring animates, the dot does not.** A blinking dot takes the word beside it with it and reads as a fault light, in a row that is scanned rather than stared at. It is applied through `motion-safe:` and **not** the global reduced-motion rule at `index.css:270` — that rule shortens animations to `0.01ms`, which is right for a one-shot transition and a **strobe** for one that repeats for ever, the exact hazard the setting exists to prevent.
- **`handleStream` answers a live video and goes no further.** Everything past that point is about a file — whether the disk has one, whether to schedule fetching one, which of two ways to describe one upstream — and a broadcast has none. It returns `live` alone, with `cacheDisabled` set so the player stops polling for a copy that is not coming. Offering `hls` beside it would have the player climb toward a playlist built from adaptive tracks the broadcast does not publish.
- **Nothing else in the player needed changing.** The live URL ends `.m3u8`, so `isHLSPlaylist` routes it through hls.js on Chrome and straight to `src` on iOS, exactly as the ordinary HLS tier is routed. Measured through the running app on `4e0smSaUfhg`: 144p–720p, opening at the live edge (`t=1270`), `seekable 0..1285`, and playing again **0.2s** after rewinding 60s.
- **A broadcast declares no duration, and the bar was drawn against it anyway.** `position / Math.max(duration, 1)` on a stream 26 minutes in is **155,700%** — a bar painted solid red from the first second, reading **"25:57 / 0:00"** beside it. Seen on the phone the first time a live video played.
  - **What a live playlist does declare is `seekable`**, and that is the only honest statement of length here. Measured on two real broadcasts through this server: `0..3605` and `0..1285`. It is read on `progress` rather than once at metadata, because the window slides forward while the broadcast runs.
  - **The bar is measured from the window's start, not from zero.** A broadcast has no zero that can still be played, and drawing from zero gives a bar whose filled part *shrinks* as the picture advances. `SeekBar` gained an `origin`, defaulting to 0 for everything with a beginning.
  - **The readout is a LIVE button, not two numbers.** One of the two was always wrong, and what a viewer wants to know is whether they are watching what is happening — lit means yes, dimmed shows how far back they are and presses to return. Without it, rewinding is a one-way trip short of reloading.
  - **The arithmetic lives in `application/live-timeline.ts`**, and `SeekBar` calls the same function for every tier. Written twice, the bar and the label would agree until one of them was fixed.
- **The card's duration badge says LIVE.** A live row carries `duration_seconds` 0, so it read **"0:00"** — which a viewer reasonably reads as broken. Same corner, same box, so a grid holding both does not step.

### itag 18 stopped serving, so it stopped being a tier (2026-08-18)

**The opening tier is the muxed stream.** This reverses the rule that stood here before it — that 480p/720p cannot be the *opening* tier because itag 18 is the only progressive format YouTube publishes. That was true and is now beside the point: itag 18 is published and does not play.

Measured across 16 videos of this library, freshly resolved URLs, one request each:

| track | head (`bytes=0-1048575`) | middle (`bytes=4194304-5242879`) |
|---|---|---|
| itag 18 — 360p progressive | 206 | **403 on 12 of 14; 206 never** |
| itag 136 — 720p H.264 | 206 | **206 on 13 of 14** |
| itag 137 — 1080p H.264 | 206 | **206** |
| itag 140 — AAC audio | 206 | **206 on 12 of 14** |

- **The head always serves, which is why this took a week to see.** A video either would not open (`MEDIA_ELEMENT_ERROR: Format error`) or opened and lost its source a megabyte in (`PIPELINE_ERROR_READ`), and the player — standing on a dying tier with nothing beneath it — could not climb off. It is also what `verifyURL` probes, so a dead URL passed the check and was handed over as a verified one. The trap `verify.go` warns about, one level deeper.
- **The tier is not offered and not resolved for.** Each resolve is a full metadata fetch repeated three times (`resolveAttempts`) against the address §8 risk 6 counts, to build a tier nothing can play. The route and proxy stay: upstream stopped serving what they fetch, which is not the same as the code being wrong.
- **The 409/`Unavailable` answer no longer comes from that resolve.** The download scheduled on the same request meets upstream and records it, and the catalog check at the top of `handleStream` is what the next poll reads.
- **The one progressive measurement that still holds** is the old one's conclusion in reverse: what YouTube withdrew is progressive delivery, not adaptive. H.264 and AAC serve at any depth.

### A pinned yt-dlp is a requirement, not a preference — and the pin is a release again (2026-08-20)

`YTDLP_PATH` names the binary; `ytdlp/session.go` applies it with `SetExecutable`, before the `purposeListing` return — which binary runs is not a question about credentials, and a listing resolved by a different yt-dlp than the one fetching bytes describes formats nothing here can play.

```sh
pipx install "yt-dlp==2026.8.19"
```

**A nightly was required and no longer is.** The stable release of the day (2026.07.04) resolved URLs that did not serve, so the pin moved to `2026.8.17.73947.dev0`. `2026.8.19` is stable, postdates that nightly, and carries the same YouTube player-client work (`#17261`, `#17185`, `#17461`, `#17462`).

Measured before switching, both binaries in one sitting, five videos of this library, freshly resolved URLs, one request each:

| track | nightly 2026.8.17 | stable 2026.8.19 |
|---|---|---|
| itag 136 / 137 / 140 — adaptive | 206 head **and** middle, wherever published | **identical, video for video** |
| itag 18 — 360p progressive | 206 head, **403 middle, 5 of 5** | **not published at all, 5 of 5** |
| 10s of `137+140` through yt-dlp | rc=0 in 9s | rc=0 in 9s, **byte-identical output** |

- **Stable is equal on everything the tiers rest on**, and it stops offering the progressive format that stopped serving. That is the conclusion "itag 18 stopped serving" reached from measurement, arriving independently from upstream.
- **Pinned, not tracked**, and now that is free: a nightly that upgrades itself is a stack that breaks on a morning nobody changed anything, and a release does not.
- **The nightly stays installed as the way back** (`pipx install --suffix=-stable` put them side by side), reachable by one variable: `YTDLP_PATH=$HOME/.local/bin/yt-dlp`.
- **What did *not* change**, recorded so nobody re-runs it: the scanner's `This channel does not have a videos tab` failures are identical on both binaries. Those channels genuinely publish no Videos tab; `#17386` is not about them.
- A path that is not there falls back to `PATH`, the same rule as `YTDLP_COOKIES` and for the same reason: a typo must not take down every request in the library.
- `docs/youtube-sabr.md` carries the measurements and what the earlier, wrong conclusion got wrong.

`instant`'s URL is proxied (`GET /api/videos/{id}/instant`) rather than handed to the browser raw. Googlevideo signs it to the IP that resolved it — the gateway's — and a viewer's IP differs from that on any LAN behind CGNAT, which is the ordinary case here, not an edge one. The raw URL then gets refused by YouTube in a way the `<video>` element reports as a generic format error, so a video with no local copy yet — everything that has never been downloaded — never starts playing on its own. Resolved fresh per request rather than cached at the gateway, since ingest already caches and refreshes the signed URL for as long as it stays valid.

### Never ask googlevideo for an open-ended range

**An upstream request must always carry a bounded `Range`, and the bound must stay small.** Ask for the rest of the file — `Range: bytes=0-`, or no range at all — and googlevideo answers with a redirect to a host that then refuses: **403**, on 9 of 12 attempts on one video and varying by the minute. The identical URL asked for 1 MiB or 2 MiB answers **206 every time, 10 of 10**.

`bytes=0-` is exactly what Chrome sends to open a video. This is the whole of "some videos play, some don't": a video with a local copy never needs an upstream request, so it always played, while a video without one could not start — and the player has no way to say why, reporting only a generic format error.

- **Size is the safeguard, so it must stay under the line rather than near it.** 8 MiB tracked the open-ended request exactly, succeeding and failing in step with it. ffmpeg's `-request_size` is **1 MiB** (`httpRequestSizeBytes`), lowered from 2 MiB when 2 MiB turned out to be *on* the line: measured on four videos' adaptive tracks, fresh URLs, same second — video track 206 at 512 KiB/1 MiB/2 MiB/4 MiB, **audio track 206 at ≤1 MiB and 403 at ≥2 MiB, 8 of 8**. That is the whole of `in#1 … 403 Forbidden` (in#1 is the second input) and of a remux that answered **502 for every video** while the picture would have opened fine. Never raise it without measuring **both tracks** again.
- **A resolved URL is verified before it is handed over, and re-resolved when refused** (`ytdlp/verify.go`, `resolveAttempts` = 3). Measured over 24 fresh resolves of two videos, same address, same credentials, seconds apart: **17 were refused to any ordinary HTTP client and 7 answered**, while yt-dlp's own transfer succeeded throughout — which is why the download always landed while the player showed a format error, and why this looked like the player being broken. Not the User-Agent (the gateway's, yt-dlp's and none at all were all 206 on a good URL and 403 on a bad one), not the credentials (both directions, both outcomes), not the player client. It is decided per URL when it is issued: every refused URL carried `fexp=…51946838` and every working one `…51946837`, 24 of 24 — YouTube moving progressive playback behind its own delivery protocol. **The flag is not what is tested**: it is an opaque experiment id that will be renumbered. The probe asks upstream the only question that matters, and asks for **the same 1 MiB the readers will go on to ask for** — a `bytes=0-0` probe was the first attempt and under-reports, passing an audio URL that then refused ffmpeg's real request in the same second.
- **When nothing verifies, the tier is not offered** — deliberately the same answer as a video that publishes no progressive format (`ErrNoProgressiveFormat` → `FailedPrecondition`), and the gateway turns that into *no tier and no error* rather than a `streamError`. Nothing is broken and the file is on its way: the download is the one thing here that has never failed. The player already has the progress bar for it. Offering a URL measured seconds earlier to be dead is how the viewer got a red format error instead.
- **`instantChunkBytes` is 2 MiB and always was.** This file said 1 MiB for a release, describing a value the gateway never held. It is moot since the instant tier stopped being offered, and it is recorded because a charter that misquotes a constant is worse than one that omits it.
- **A request that carried no range still gets the whole file under a 200**, fetched a piece at a time. Answering it with one bounded piece would hand a television 2 MiB of a video and call that the end.
- **Both tiers re-resolve once on refusal**, for the residue that bounding does not cover. `ResolveStreamRequest.refresh` drops ingest's cached URL, because retrying the same dead URL fails identically — the refusal is a property of the URL, measured at 20 of 20 on one. Refused twice is a real answer and is passed to the browser; a third request only adds to whatever count upstream keeps against this address.
- **The remux tier waits for the first bytes before committing a status.** `OpenRemux` returns when ffmpeg *starts*, long before it has read anything, so a refusal used to arrive as `200` with an empty body — which the browser reports only as `DEMUXER_ERROR_COULD_NOT_OPEN`. Those first bytes are the fMP4's initialisation segment and are written ahead of the rest, never discarded.
- **An upstream status ≥400 is logged.** It arrives as a *successful* round trip — `err` is nil, the status carries the bad news — so passing it through left no trace anywhere. A day was spent looking at the player for a fault that never logged a line.

- **ffmpeg and ffprobe read open-ended unless told not to, and that is the same rule.** They do their own HTTP, so the gateway's chunking cannot cover them: `-request_size` **and** `-initial_request_size` (2 MiB, matching `instantChunkBytes`) go before every `-i`, per input. Without them, measured on a real 1080p URL — `ffprobe` → 403 Forbidden; `ffprobe -request_size 2M` → the answer in 0.14s; `curl -r 0-1048575` → 206; `curl -r 0-` → 302 to a host that then 403s. This was the whole of `probe keyframe: exit status 1` → `open remux: EOF` → **502**, which the ingest log carried for *every* video; the mux only ever opened when the retry happened to be let through.
- **ffmpeg's stderr is kept** (`tailBuffer`, last 4 KiB). A mux that fails writes no bytes, and the caller can only call that `EOF` — a word about the pipe, not the fault. Every remux failure in the log read `error=EOF` while ffmpeg was saying "403 Forbidden" a few kilobytes away.

### The live rendition is not the stored rendition

`DEFAULT_HEIGHT` (1080) is what goes on disk; `LIVE_HEIGHT` (**720**) is what the mux serves. They were one constant, and one constant answering two questions is how a change meant for one of them reaches the other.

- **The file stays 1080p.** A library holds its files for months; trading that for a smoother thirteen seconds is the wrong direction. Phase 2's answer is to fetch 720p *as well*, not instead.
- **The live mux is 720p** because this tier only bridges the gap until the file lands, and that gap is short: over **109 completed downloads, a median of 13s and 88 of them under 30s**. Half the bytes means the mux is ready sooner — which is the whole difficulty with this tier, since one that is not ready before the viewer reaches its mark cannot be used at all — and it costs half the bandwidth for a picture replaced within a minute. The step the viewer feels is 360p → 720p.
- **480p/720p could not be the *opening* tier, and now are.** The reasoning was that itag 18 is the only progressive format published, so nothing higher can start without a mux. Both halves are still true; what changed is that itag 18 no longer plays, so the mux is the opening move for every video. See "itag 18 stopped serving" above.
- **1080p live is real but never automatic.** `?height=1080` on the remux route serves H.264 1080p with AAC (measured: 124 MB in 30s). Auto stays at `LIVE_HEIGHT`, because this tier is only worth having while it is ready before the viewer reaches its mark and twice the bytes is twice the wait. Pinning it is somebody deciding to wait, which is theirs to decide.

### Remux rules

- ffmpeg flags for correct A/V alignment: `-avoid_negative_ts make_zero -muxdelay 0 -muxpreload 0 -frag_duration 1000000 -movflags frag_keyframe+empty_moov+default_base_moof`.
- When opening at `T > 0`, `ProbeKeyframe` finds the actual video keyframe `K` (ffmpeg seeks to the nearest keyframe at or before the mark). The audio input is seeked to `K`, the video to `T`, so both tracks share the same content origin. The stream reports `audioAt = K`; the player uses `K` as the offset.
- Do not seek both inputs to `K`; ffmpeg would step back to the previous keyframe.
- fMP4 through a pipe has no index, so seeking means reopening the stream with `?t=<seconds>`. The seek bar is enabled on every tier.
- A handover record carries the URL it was made for; the player rejects records that do not match the element's current `src`. **A failure is tested the same way**: an `error` on the layer being prepared abandons the climb only when the element's `src` is still the claim's. The claim moves ahead of the element — the local file landing writes a new one, the `src` follows on the next commit — so a 502 travelling from the stream that was there a moment ago used to take the climb to the local file with it.
- **A player that gave up must unlock when the file lands.** `playable` is `frontSrc && !loadFailed`, and nothing but navigating to another video ever cleared `loadFailed` — survivable while a failed mux could retreat to a progressive tier, and not survivable once the mux is the only source before the copy arrives. A refused mux ended the video for as long as the page stayed open, while the download beside it finished in a median of thirteen seconds; the viewer read "The stream could not be loaded" over a file already on the disk, and reloading was the only way out. The local URL appearing now clears it and **starts over rather than climbing**: the climb protects a picture that is still running, and the source in front is the one upstream just refused. Guarded on `loadFailed`, or the ordinary case — file landing while a mux plays fine — would black out a working video.
- **Losing the climb to the local file is not final.** `useStream` stops polling once the answer carries a local file, and that poll was the only thing re-running the climb effect — so an abandoned climb was abandoned for good, and the viewer watched the rest at 360p with the whole file on disk beside them. Pressing 1080p or reloading "fixed" it only by starting the machinery over. Abandoning now bumps `climbAttempt` for the local tier too, capped at `MAX_LOCAL_ATTEMPTS = 3` — not for cost (there is none) but because a drive that has gone away fails identically forever.
- **A climb is measured from a playhead, and a dead playhead measures nothing.** The 20s lead exists because the viewer keeps moving while the mux is prepared; when the instant tier is refused (403, twice, inside one of the refusal waves above) the front layer stops at 0 with an `error` on it and never moves. The claim parked at 0:20 was then handed over anyway — twice over, because *two* comparisons read the negative difference as "both elements already agree on where they are" instead of "the replacement begins after the viewer". What the viewer saw: the tier flipping itself to 1080p, the clock at 0:19, and play resuming twenty seconds into a video they never started. Reloading "fixed" it only because by then the download had landed. So: the lead is **zero** while the front carries an error, a front error **drops the claim** measured from it (without counting against the remux's three attempts — the mux did not fail), and a replacement starting *after* the viewer is refused at both comparison sites. A backwards jump was already guarded; a forwards one was not.
- **A stream reported `seekable: false` must never be seeked — not even inside what has already arrived.** The handover had two paths that moved the replacement's playhead and only one of them asked. Parking at the mark checked `tier.seekable`; `catchUpToViewer`, which runs when the climb is late — and it usually is, because preparation takes about as long as the lead allows — did not. **The browser then failed on the audio packet at exactly the seek target**: `0.766259` on a stream whose offset was 18.936 with the viewer at 19.70, and the same coincidence on three other videos at three other marks, every time reported as `PIPELINE_ERROR_DECODE: Failed to send audio packet`. The number in the browser's error was the number the player had just written to `currentTime`. A late climb to the muxed tier is now handed over where it stands when it is within `HANDOVER_OVERSHOOT_TOLERANCE`, and given up beyond that — costing a mux rather than the sound. The catch-up remains for tiers that really can seek.
- **That rule now lives in a function, because written down it kept being forgotten** (`application/player-seek.ts`, 2026-08-20). It was enforced at three of the **five** places that move a playhead. The fourth was **restoring the viewer's saved position**, and it did not matter while videos opened on the progressive rendition — which seeks like any file — then became the whole of "the video will not start" once the mux became what every video opens on. Measured on `ZIaOBAjvc38`, left at 336s: the mux opened, delivered 3.6 MB, and closed 175 ms later with **no error on the server at all** — no `remux stream ended early`, no `live mux complained`. A browser asked to seek an unindexed stream does not refuse; it takes the number and buffers toward it, showing nothing, until it has streamed the whole way there. The picture arrived when the *download* finished, 45s later, which is why this read as "you have to wait for it to download".
  - `seekElement(el, tier, seconds)` is the only thing allowed to write `currentTime`, and `player-seek.guard.test.ts` fails the build on any other assignment in the feature. oxlint has no `no-restricted-syntax`, so it is a source scan — crude, and it cannot forget.
  - **A stream that cannot be moved is *opened* where it is wanted instead** — `sourceURL(tier, mark, audioStart)` plus `resolveRemuxStart`, the same two calls the climb already made. Its zero becomes the mark, so by the time the element reports metadata there is nothing left to seek.
  - **The fifth site was the one that had a comment saying it was safe.** Reopening at a mark lands on the keyframe before it, and the code closed that gap by seeking the last couple of seconds "within what has already arrived". That is the same arithmetic as the failure above: `0.766259` is `19.70 − 18.936`, a mark minus its keyframe. Buffered is not seekable. The viewer now lands at the keyframe — two seconds early, and the bar says so honestly, because the position is read from the stream's own origin.
- **A tier that fails after the viewer is on it must retreat, not stop.** `targetTier` has always known how to — once `remuxFailed`, auto asks for the low rendition again — but only an abandoned *climb* ever counted a remux failure, so a mux that broke after being handed over counted nothing. The player sat on a dead element with a working 360p source one step away, and the viewer's only way out was reloading. An `error` on the front layer is a statement about that source, not about the video.
- **A mux can half work, and half working looked exactly like working.** Observed: a stream whose video ran 68s and whose **audio stopped at 0.812698s** — the same timestamp the browser named in `PIPELINE_ERROR_DECODE: Failed to send audio packet`. One input died while the other carried on. Because bytes flowed, `OpenRemux` counted it a success, `Close` killed ffmpeg without ever reading its stderr, and the log said `live mux closed bytes=18889763`. **Root cause not established** — a direct mux from the same formats, and from URLs 25 minutes old, both produced full audio (222.7s), so neither the ffmpeg arguments nor URL age reproduce it. What is in place is the means to catch it next time: ffmpeg's stderr is now logged whenever it says anything at all (`live mux complained`), even on a mux that produced a stream. A complaint also **drops the cached URL pair** — `forget` was only reached when a mux failed to *open*, so a pair that opened and then lost an input stayed cached for its full 90 minutes and every attempt at that video rebuilt the same soundless stream.
- **The lead is measured, not chosen, and being late is not a failure of the tier.** Preparation was measured at ~4.4s for a five-minute video against 10.8s for a seventy-eight-minute one, so no constant can be right: the 20s that cleared the long case spent thirty extra seconds of 360p on the short one. The first climb of a video guesses (`remuxLead` in `application/remux-lead.ts`, a pure decision kept out of the player); every climb after it uses what the previous one actually cost plus a margin, bounded so neither a measurement of nothing nor one of two minutes can set it. A climb that still lands behind the viewer is **reopened at a fresh mark** (`MAX_CLIMB_REOPENS` = 3), not counted against `MAX_REMUX_ATTEMPTS` — counting it was the trap: preparation takes about as long as the lead allows, so every climb on a long video was late, three late climbs switched 1080p off for the rest of the video, and pinning it by hand was the only thing that worked.
- **The server proves the mux is usable before answering, not merely that it opened.** The head read is ~1.5 MiB (`remuxHeadBytes`), about a second and a half of the stream, so ffmpeg has had a second with *both* inputs by the time it is asked how it is getting on; at `-loglevel error` anything it has written is a fault, and a fault there means the stream is withheld — dropping the cached URLs, resolving again, opening again, all before a byte reaches the browser. Measured on the real stack: **0.40s to first byte with the URLs cached, 2.71s cold**, the cold case still dominated by the resolve and the keyframe probe. This does not explain why the audio stops and does not claim to; it withholds the stream without needing to know.
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

### Where the library lives, and whether it is kept (2026-08-22)

**The folder is a setting, and the saved value beats the environment.** `MEDIA_ROOT` is read at start-up by three services — ingest writes there, catalog deletes there, the gateway serves it — and used to be changeable only by editing `scripts/dev.sh`. It is now `data/storage.json`, resolved through `internal/mediaroot`.

- **The file wins, and the order is the whole point.** `dev.sh` exports `MEDIA_ROOT` on every run, so the other way round would mean the Storage page saves a folder, survives a restart, and changes nothing — with nothing anywhere to say why. The environment is the default for a machine never set up.
- **Applies on restart**, deliberately. Three processes hold it; changing it under a running job, an open `/media` response and an eviction sweep is three things losing their footing at once. The switch below takes effect at once because one process reads it per request — two mechanisms, two reasons, not carelessness.
- **The path must be absolute and must already exist.** Relative would mean three different directories the day a service is started outside `dev.sh`. Creating on demand is how a typo becomes an empty library at `/Volumes/Data2/Youtub`, noticed only when everything needs downloading again.
- **Verify reports free space and how many video folders are already there.** That count is the reason to press it: pointing at a disk used before brings the library back, and it should be visible before saving rather than after restarting. Measured on the real disk: 914 folders, 235 GB free.
- **Changing away from a root that still holds files needs confirmation**, with the count from the catalog — 32,377 here. `media_path` is relative, so nothing in the database breaks; the files simply are not under the new base and would be fetched again.
- **Verifying stats a path this request names**, so anyone on the LAN can learn whether a directory exists. Consistent with §6b's trust model — the LAN is trusted, media URLs are unprotected — and recorded rather than left to be noticed.

**Streaming only is one switch, and it stops exactly one thing.** Pressing play no longer schedules a download.

- **Subtitles still arrive**, through a new `FetchSubtitles` RPC. They used to ride inside `Submit` as a side effect of queueing a transfer, so switching caching off would have taken captions with it — and translation and read-aloud with them, since both read the `.vtt`. Captions are tens of kilobytes against hundreds of megabytes; losing them to save that would be the worst trade in the app. Not a flag on `Submit`: that name means "queue a transfer", and a flag that stops it doing so lies to every later reader. Verified on the running stack — `job claimed` unchanged, `1080p.mp4.en.vtt` on disk, no video.
- **Retry still works.** Somebody pressing it on `/activity` is saying "I want this one", and refusing would make the button dead exactly while the mode is on.
- **Nothing is deleted and the sweep still runs.** The switch is not a purge, and somebody may well turn it on *because* the disk is full — the worst moment to also stop the thing that frees space.
- **The stream answer carries `cacheDisabled`.** The player polls it every five seconds until `local` appears, which is how it notices a download landing; with caching off that never comes, and a three-hour video would ask two thousand times about a file that will never exist. One answer says what can play and what is on its way, rather than the client reading a second setting it would have to keep in step.

### Eviction

Every video has `last_accessed_at` + `pinned`. Above the high-water mark, the **media file** of the least-recently-used unpinned video is deleted, keeping metadata + thumbnail + history. The UI shows "Removed — press to fetch again". Thresholds are set by `EVICTION_HIGH_BYTES`/`EVICTION_LOW_BYTES`.

### A media state describes the disk, not an intention

`media_state` is `ABSENT · DOWNLOADING · READY · EVICTED · FAILED · UNAVAILABLE`. **`ABSENT` was called `QUEUED` and nothing was ever queued by it**: it is the column's `DEFAULT`, so every row the scanner inserts starts there — measured at **26,233 of the 26,958 videos in the catalogue**, against zero jobs queued or running in ingest. The activity UI reads jobs, so it was not the cause of "two videos downloading at once" (that was a queued job drawn identically to a running one), but the name was a claim about a download queue on twenty-six thousand rows that had nothing to do with one.

- **Whether a transfer is queued is a question about `ingest.jobs`**, which has its own `QUEUED` and means it. That one is untouched, and the two must not be conflated again.
- `ABSENT` and `EVICTED` are both "no file", and the difference is worth keeping: `EVICTED` means there was one and the sweep took it back, which is what lets the UI say "Removed — press to fetch again". `ABSENT` means there has never been one.
- The proto enum value keeps **number 1** (`MEDIA_STATE_ABSENT`), so the rename is wire-compatible; only the name moved.

Applying `services/catalog/migrations/0018_media_state_absent.sql` is required before running the new code — it rewrites the rows, the `DEFAULT` and the CHECK constraint together, and the constraint rejects `ABSENT` until it runs.

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

- **All source code, identifiers, comments and commit messages MUST be in English.** Chat happens in Vietnamese; the artifacts do not. That rule is unchanged and the reasoning behind it is untouched.
- **In-app copy is a translation key, and the app speaks English and Tiếng Việt** (2026-08-22). This narrows the rule above, which used to include UI copy and so forbade the household reading their own library in their own language. English is still the source: every key's value is authored in it, and `vi.ts` is a translation kept beside it.

### Half-translating is the failure, so it fails the build

The ask was not "translate the app" but "do not leave English lying around", so the guards were built before the translating. **Three layers, because each sees something the others cannot:**

1. **Typed keys** — `CustomTypeOptions` gives `t()` the key union, so `t('nav.acount')` does not compile. Proven by compiling it deliberately. It caught something real on the way: `Item.label` was typed `string`, wide enough to hold a typo that would then render the key itself on screen, in both languages, reported by nothing.
2. **`dictionaries.test.ts`** — catches what the type cannot: an *extra* key left by a one-sided rename, and a value still in English. Its exemption list is four entries and each carries a sentence; two candidates it started with turned out to translate perfectly well.
3. **`untranslated.guard.test.ts`** — a source scan, and **the only layer that can see the fault the request was about.** A string that was never extracted is not a missing key; it is a literal in a component that renders in English while nothing reports it. Proven by putting one back and watching the build fail with `pages/HistoryPage.tsx:15 [jsx-text] Watch history`.
   - Narrow deliberately: JSX text, `aria-label`, `placeholder`, `title`, and quoted literals **starting with a capital** — which is what separates copy from Tailwind class lists, since those have plenty of spaces and no capitals. Measured over the whole app: 170 literal matches, every one real copy, no false positives.
   - **It once missed single words, and that shipped a half-translated app.** The gap was written down as a deliberate trade — "Save" on a button would be caught by review. It was not: most of an interface *is* single words, so the guard went green while the tab bar, the chips, Subscribe, Share, Sort by, and every settings heading were still English, and the person who found out was the one using it. **A guard that passes on the failure it exists to catch is worse than none**, because it is also believed.
   - It now takes **any capitalised quoted string** and enumerates what is *not* copy instead: SCREAMING_CASE, hyphenated header names, identifiers written as strings, and anything being compared or switched on. Naming the exceptions is checkable; guessing at the shape of copy is not.
   - JSX text is scanned in `.tsx` only — in a `.ts` file `Promise<Response>` has the same shape — and expressions containing an operator are skipped, since `{count > 0 && …}` leaves "0 && count" between two brackets.

### What the translation is, and is not

- **Written, not converted.** Vietnamese drops the subject wherever the context carries it and English cannot, so "they belong to the whole household. This cannot be undone" is "chúng là của cả nhà. Xoá rồi không lấy lại được". Carrying every "they", "this" and "it" across is the surest sign of a machine.
- **The formatters carried the most English and the least visibly** — not labels but return values on every card: "248 views", "3 days ago", "4.1M subscribers". They carried English *grammar* too: `formatRelative` appended an "s", which applied to a language with no plural gives "3 ngàys trước". Language is a parameter, the functions stay pure, and components reach them through `useFormat()`. English marks the past before the unit and Vietnamese after the phrase, so relative time is two shapes rather than one shape with a substituted word. Counts use N/Tr/T; dates go through ICU with `vi-VN` (`22 thg 8, 2026`) rather than a table of month names.
- **Technical terms stay English** — Equalizer, HLS, Remux, Reverb, Preamp, Base URL, API key, Picture in picture. Somebody opening the equaliser already knows the word and "bộ chỉnh âm" teaches nobody anything; the sentences *around* them are translated.
- **The server stopped writing prose.** It sends codes — `media_root_unavailable`, `delete_self`, `delete_last` — and the client maps them. It cannot know what language the viewer reads, and translating on both sides would be two sets of words that agree until one changes.
- **A module constant cannot call a hook**, so the sidebar's items, the phone settings rows and `bare-screens.ts` carry *keys*, resolved where they are drawn. Likewise a pure helper: `describeVideo`, `supply`, `unavailableCopy` and `serverCopy` take the translator as an argument rather than becoming components.
- **`infrastructure/` never translates.** A repository has no business knowing what language anybody reads; it throws, and the screen that catches decides what to say.
- **The choice lives in `localStorage`, per device**, read at module load before the first paint — an effect would show English for a frame and swap. `navigator.language` decides for a machine nobody has set up, falling back to English.
- **`documentElement.lang` follows the choice**, because that is what a screen reader picks a voice from: left at `en`, Vietnamese is read by an English voice, which is unintelligible rather than merely wrong.
- **Each language is named in its own words** in the switcher, always. Somebody who pressed the wrong row is looking at an interface they cannot read, and "English" written in English is the way back out.
- **Tests run in English**, pinned in `test/setup.ts`. The 179 assertions that read visible text describe behaviour, not translations; rewriting them to look up keys would make each require a trip to the dictionary and catch nothing the three layers miss.
- **`logview` and the two probe pages stay English.** logview reads six services' English logs, and translating the frame around them is half a job; the probe pages are instruments, not the app.
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
| Watch later | Real. Its own list, per member, and its own page |
| Playlists | Real. Per member, made here or imported |
| Subscribe | Real in P1: adds the channel as a live ingest source |
| Share | → the YouTube link (`video.sourceUrl` or `https://www.youtube.com/watch?v=<id>`) |
| Your videos, YT Music, YT Kids, footer, Shorts, Live | **DROPPED** |

**Watch later and Save are two different intentions and two different controls.** Watch later is a note about what to do next and clears itself once the video has been watched; Save keeps the file on the disk against the eviction sweep and never clears itself. One control could not mean both.

- `catalog.watch_later` and `videoSelect`'s read of it have existed since `0001_init`, so every video has always reported `user_state.in_watch_later` — **nothing could write it**, so it answered false for everybody, forever. The account import read `:ytwatchlater` and stored its videos as ordinary ones, so a list somebody had built deliberately arrived as an anonymous handful of new videos. Nothing here is new but the writing and the page.
- **Not told to the ranker.** Putting something aside is not a statement about taste, and unlike a like it is meant to be undone.

**Watch later and Playlists are a read-only mirror of the member's YouTube account.** Nothing in this app edits them — no create, no rename, no delete, no add or remove, and no write route at the gateway. An edit here would be reverted by the next account scan, which is §5's dead button reached through a control that appears to work for an hour. They sit in the **Account** sidebar group, apart from Saved and History, which are this library's own records and can be changed. That group was called *From YouTube* and meant exactly "read-only mirror"; it now also holds the profile and the YouTube connection, so the promise moved from the heading onto these two items — see "The account has one place" below.

- **The mirror removes as well as adds — but only as far as the read actually saw.** The read is bounded (`playlistItemLimit` = 50, `accountFeedLimit` = 50), so a long list comes back truncated, and mirroring a truncated read would delete everything past the cap. `complete` — the caller's answer to "was this the whole list", true when fewer than the cap came back — decides: a complete read replaces the contents *and rewrites the positions* to match upstream, a truncated one only appends. An **empty** answer never removes anything: a list that answers with nothing is far likelier to be a refusal than a list somebody emptied.
- **A playlist deleted upstream is deleted here** (`PruneImportedPlaylists`), and that is not optional: nothing in this app can delete one, so without it a vanished playlist stays for ever. Same guard — an empty answer prunes nothing.
- **A playlist is read whole, not fifty deep.** `playlistItemLimit` began as `accountFeedLimit`, which is right for a feed — read from the top for what is new — and wrong for a finite list somebody assembled: this household's playlists hold **1186, 776 and 139** videos and every one arrived with 50. The cost is a page fetch per hundred entries rather than a request per video (measured: 1186 in 8s, one invocation), so the cap bought nothing. It also broke the mirror in silence — `complete` means "the read saw the whole list", so at 50 every longer playlist was permanently read-in-part and could never have anything removed.
- **A limit above the ceiling gives the ceiling, never the least.** `ListAccountFeed` clamped anything over 200 back to **50**, so raising the caller's depth changed nothing and said nothing; the playlists came back at exactly 50 a second time before it was found.
- **The first fill takes them all; only re-reads are rationed.** Two different costs, and conflating them was a mistake: four a pass exists so this is not thirty credentialed requests *an hour*, but filling the library happens **once** — 28 requests three seconds apart is shorter and slower than an ordinary hour of the anonymous scanner. So never-read playlists are all read in the pass (`firstFillLimit` = 60, a ceiling not a target) and already-read ones are refreshed four a pass. Two queries, so neither can quietly return the other's rows.
- **A playlist upstream lists but will not open is asked once.** Measured: **10 of this household's 27** answer `"YouTube said: The playlist does not exist"` when fetched by URL — reproducibly, minutes apart, with a live session. `playlists.unavailable` records it, matched narrowly on YouTube's own wording so a network error never buries a readable list. Without it each costs a request every pass for ever and sits at the front of the unread queue ahead of playlists that could have been read.
- **The scan runs detached and reports progress from the server.** A first fill takes minutes, so `POST …/scan` answers **202** and the browser polls `GET …/scan` — which is what makes reloading the page mid-pass harmless. The status is in memory on purpose: a pass cannot survive a restart, so neither should the claim that one is running.
- **The button scans whoever pressed it; the hourly timer scans everybody.** It sits on a screen about *your* account, and on a house of five it was quietly spending four other people's request budget.
- **"Not read yet" is said out loud.** Contents are read four playlists per pass, so a freshly imported one is empty for hours; an empty playlist reads as broken rather than pending, and `items_synced` is the difference between the two.
- **Opening a video from either list plays *that list*.** `?list=playlist:<id>` and `?list=watch-later` join the queue kinds the URL already carries (`queue.ts`), so next is the next entry and the rail names what it is playing from. Without it a playlist was only a way of finding something to leave it by — press next and you were in the recommender.
- **Watch later is built to match the playlist page exactly**: same heading and count, same grid, same card variant, same behaviour on opening. They are the same kind of thing, and any difference between them is one the viewer has to learn for no reason.
- **A card in either page is the same `fromYouTube` variant.** Two variants would be two chances for the two pages to drift; the menu offers only what belongs to this library — mark watched, and Save.
- On a phone they live under **From YouTube** at the top of Settings, beside Library. The bottom bar is full at five, and what earns a place there is what you move between while browsing.

**Playlists are per member, and Watch later is not one of them.** A playlist has a name, is created and can be deleted; Watch later has none of those, so as a row in `playlists` it would be a playlist that is not one — and YouTube keeps them apart for the same reason.

- `playlists`/`playlist_items` fall on the per-user side of the schema, next to `watch_progress`, `reactions`, `subscriptions`, `watch_later` and `saved`. Importing one member's YouTube playlists into a shared table would repeat exactly the defect `0014_saved.sql` was written to fix. Sharing later is a column; splitting a shared table apart afterwards is a migration nobody wants to write.
- **`position` is carried, never derived.** Filled from the order an import returned, appended to at the end, and taken from the playlist's own `max(position)` rather than its length — removing the third of five leaves four rows whose positions still run to five, and counting would hand the next addition a position another row already holds. Drag-to-reorder has no column left to add: this is that column.
- **Ownership is part of the lookup, not a check after it.** A playlist belonging to somebody else is indistinguishable from one that does not exist, and a check written separately from the query is one that can be forgotten at the next call site.
- Opening a playlist queues **no downloads**. §2's budget is 300 GiB and there is one worker slot; a 200-video list fetching itself is the fault the live-broadcast rule already exists to prevent.
- **Imported by name every pass, read a few at a time.** The playlist list is one request; each playlist's contents is another, and this household has 30. Reading them all hourly would be thirty named requests an hour against the address §8's risk 6 is about — so `playlistsPerPass` = 4, stalest first (`items_synced_at`, NULL first), which walks the set in under a day. `items_synced_at` is separate from `updated_at` because the latter moves whenever anybody edits the playlist here, and a list somebody added to this morning would look freshly synced.
- **`WL` and `LL` are refused.** YouTube reports Watch Later and Liked videos in that list; both already arrive through their own feeds, and Watch later is not a playlist here at all.
- **A playlist's contents are read *as the member*.** §6b's rule is narrow, not absent: listings carry no credentials except where the listing *is* a member reading their own account — and a private playlist plainly is one. `ListAccountFeed` takes a URL as readily as an alias, so it is the same call the other feeds make.
- **The import appends and never removes**, the same rule as subscriptions. Verified against a live session: 30 playlists listed, 28 imported, 4 read on the first pass (Luke Music 50/50, random 15, sound 10, Home assistant 5).
- Requires `0015_playlists.sql`, `0016_playlist_sync.sql` and `0017_playlist_unavailable.sql`.

**A cookie's stated expiry is worthless, so nothing counts down to it.** Read from a real jar: the session cookies — `SID`, `HSID`, `SSID`, `SAPISID`, `__Secure-1PSID`, `__Secure-3PSID` — all carry expiry **June 2027**, over 400 days out. Both household sessions died the day that was measured. Google invalidates server-side and the file never learns, so a countdown from those timestamps would read "402 days left" on a session that stopped working that morning.

- What works instead is noticing quickly and saying so: two authentication failures retire the account, an expired one hands out no cookie path at all, and `CookieExpiryBanner` says so for the member it belongs to.
- **The banner lives in the settings screens, not app-wide, and it is inside the scroller** (2026-08-21). Both halves were wrong before, and the second one hid the first.
  - It was rendered in the shell's flow *above* `<main>`, on the reasoning that it belongs above everything. The top bar is `absolute inset-x-0 top-0` and moves for nothing, so the banner took **44px at the very top and was then painted over by it** — measured: banner `top=0 h=44`, header `top=0 h=56` over it, `<main>` starting at 44. Every page began 44px low, which is what a viewer sees as a gap under the search bar, on the phone and the desktop alike since it has nothing to do with the safe area. **This household's session expired on 2026-08-16 and nothing said so for five days.** A warning that cannot be seen is worse than none: it costs the room and gives nothing back.
  - This is the **fourth** thing to learn that the top bar's height belongs in exactly one place, and that place is the scroller — after `WatchPage`'s reserve and the chip row twice. Anything in the shell's flow before `<main>` displaces every page, and the bar cannot move aside to make room.
  - **Narrowed to `/settings` by decision, and the cost is real**: a full-width bar on every page spends 44px of every screen repeating one message, and nobody now meets this until they go looking. If that matters, the answer is not to widen it back — it is a mark on the way in, on the avatar or on the Settings row, which costs no layout at all.
- **Anything waiting on the import says so in its own words.** "Not read yet" becomes a lie the moment the session dies — nothing is coming on the next pass, because there will not be one. The playlists and Watch later pages say "Waiting for a YouTube session" instead.
- **Session state stays per member.** One person's is not another's business (`accounts.go`), and the member who can fix it is the one who already sees the banner.
- **Do not exercise a live cookie file by hand — copy it first.** Three times in one day both sessions died within minutes of manual `yt-dlp --cookies data/cookies/*.txt` runs against the jar the service was using; the third time was five minutes after measuring two playlist sizes that way. Three for three is not proof — Google also simply does this — but the cost of being wrong is a dead session for the whole household, and the cost of the precaution is `cp`.

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
- **On iOS, HLS never reaches Web Audio either — by any road** (2026-08-21). Measured on the household's iPhone (iOS 18.7) with one page, one graph, one `AnalyserNode`, changing only what the element was playing:

  | source | signal in the graph |
  |---|---|
  | ordinary MP4 from disk | **0.0806** |
  | HLS played natively | **0.0000** |
  | HLS played through hls.js | **0.0000** |

  - **The third reading is the one that settles it**, and it was nearly missed. A silent fall back to native playback would look identical, so the element was asked what it was actually reading: `blob (MSE)`. hls.js really was the source. So this is not a native-HLS limitation that a library routes around — it is HLS on this platform, and **nothing can fix it**. Desktop Chrome through hls.js reads 0.1378 on the same page, so the equaliser must not be switched off on the strength of another platform's fault.
  - **`createMediaElementSource` still succeeds.** Nothing throws, `isAttached` says yes, and the gain node is wired to nothing — which is why the fallback that already existed for browsers without Web Audio never fired.
  - **The consequence is wider than the equaliser**: every node in the graph is inert for that stretch, so the room, the master gain and narration's ducking are too. Volume moved off `element.volume` into a gain node deliberately, and on a phone mid-download that made the volume slider a dead control — unnoticed only because a phone has buttons of its own. `bypassesWebAudio(url)` is now asked per layer alongside `isAttached`, and volume falls back to `element.volume` when the graph carries nothing.
  - **It lasts until the download lands** — a median of thirteen seconds — after which the local file plays and everything works again. The panel says "EQ off while the video is still downloading" meanwhile, for the same reason as the fullscreen label.
  - **hls.js does run on iOS**, contrary to what this file said for a day: iOS has `ManagedMediaSource` and hls.js prefers it (`getMediaSource(preferManagedMediaSource = true)`). It plays, it seeks, it reads the ladder. It simply does not restore Web Audio, so there is no reason to spend 179 kB and the battery on it there.
  - `web/public/hls-eq-check.html` is the page these numbers came from. Kept, because this question returns with every iOS release, and because reading a library's source is not the same as playing a video on the device — which is how the wrong answer was reached three times in one afternoon.
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
- **Zero means gone — dropped from Home, not moved to the end.** They used to be appended after everything else so nothing became unreachable by scrolling. That is right for a bucket that ran out and wrong for one somebody switched off: on a household member following 19 channels against a library of 8,000, the subscribed pool is exhausted a page and a half in — sooner, because `applyChannelDiversity` appends what it holds back and so lands it *behind* that tail — and the rest of Home was the discovery share set to 0%. Nothing is lost; search and the channel page still reach them, exactly as they reach a disliked video.
- A feed that runs out says so, and names the setting that decides it (`/settings/feed`). Dropping the tail makes an empty Home an ordinary outcome rather than a fault, and a page that simply stops reads as broken.
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
- **Provenance is recorded** (`catalog.videos.discovered_via`: `SOURCE` / `RELATED` / `SEARCH` / NULL). There was no way to tell where a video came from, and the obvious proxy was wrong: expansion stored videos unfiled, so "no topic" looked like "arrived uninvited" — until the metadata backfill fills YouTube's category in for everything. 4543 topicless videos here belong to *subscribed* channels. First writer wins; NULL means "before the column existed" and is **never** guessed at.
- **`RELATED` videos may be `discovery`, never `affinity`.** The affinity slot means "more of what you watch", and a channel the viewer never chose cannot be more of anything. Uninvited material was taking a fifth of the page under a name saying the viewer asked for it.
- Related is kept because it is anchored — it starts from videos the library already has, so its worst case is a neighbour of something chosen rather than an arbitrary search result.
- Videos older than 365 days (PublishedAt, or AddedAt fallback) are filtered from Home — **but only when no topic chip is picked**. Choosing a chip is a stated intent, and the answer to "show me music" is not "music from this year": 170 of the library's music videos are over a year old against 148 under it, where an ordinary topic loses 7%.
- **`EVICTED` videos are offered.** They were excluded for as long as "no local copy" meant "pressing this does nothing"; the instant tier plays an undownloaded video straight away while the copy is fetched behind it. Excluding them cost 359 videos across the library and 104 of the 402 in Music.

## 6b. Household members and their YouTube accounts

**There is no `identity` service.** §3 lists one; it was never built. `gateway.userID` reads `X-User-Id` and falls back to `DEV_USER_ID`, and until this the web app never sent the header — so every browser in the house was one person, and `recsys.signals` held 39,583 rows under a single id.

- **The account has one place, and the avatar is the way in** (2026-08-22). Profile and YouTube account were filed apart — under Preferences on a phone, nowhere at all on the desktop rail — while Watch later and Playlists sat in a group of their own. All four are the same subject, so all four are in one **Account** group, in the same order on the rail and in phone Settings. The avatar in the top bar opens a menu of the household's profiles, with *Manage profiles* and *YouTube account* beneath a divider.
  - **That avatar was a dead button** — `<Avatar hue={210} name="Luc" />` with no handler, so every member of the house saw Luc's initial and pressing it did nothing. It reads `hueFromId` like every other avatar now.
  - **`/profile` and `/account`**, out of `/settings`, with redirects from the old paths. `CookieExpiryBanner`'s condition had to widen with them: it was narrowed to `/settings` the day before, and following the old path would have taken the expiry warning off the one page it is most about.
- **A profile can be deleted, and it takes only what is keyed to it.** `subscriptions`, `watch_progress`, `reactions`, `saved`, `watch_later`, `playlists` (their items cascade), `comments`, and recsys's `signals` and `impressions`. Then the cookie file, the `accounts.json` entry, and the entry in `profiles.json`.
  - **Videos and channels stay, and this is not a preference.** They carry no `user_id` — the library is the household's — and `videos.channel_id → channels ON DELETE CASCADE` means deleting one channel deletes every video of it, taking every *other* member's history, reactions and playlists with it and orphaning the files on disk where the sweep can no longer see them. 621 of 708 channels arrived through `ExpandLibrary` rather than anyone's subscription, so "nobody is subscribed" is not "nobody wants it".
  - **`pinned` is recomputed in the same transaction.** It is derived from `saved` (below), so a video kept only because the deleted member saved it must become evictable again — otherwise it is pinned for ever against a 300 GiB budget with nobody holding it.
  - **The entry in `profiles.json` goes last.** Four places, no transaction across them; while the entry is still there the profile is still listed and still deletable, and every step is idempotent, so pressing the button again finishes what a failed run started. The other ordering is what left `u_test_empty` in `recsys.impressions` with no entry anywhere else.
  - **The confirmation shows real counts** from the same query that then deletes, run with `dry_run` — two queries would be two definitions of "what belongs to this profile", agreeing until one of them changed. Measured on `u_tunkhanh` before removal: 8 subscriptions, 4 watched, 41 reactions, 6 in Watch later, 72 signals, 342 impressions; after, all zero, with `videos` at 32,304 and `channels` at 1,691 unchanged.
  - **Not the profile asking, and not the last one.** The first would leave a browser holding the id of somebody who no longer exists; the second empties a list whose fallback, `DEV_USER_ID`, is where every pre-picker browser's history lives.
  - **A device holding a deleted id repairs itself.** `ProfileGate` compared nothing but "has a choice been made", so a stale id was sent in `X-User-Id` for ever. It now checks the id against the list and falls through to the picker.
- **A profile picker, not a login.** §2 is 2–5 people with no public sign-up and §3 leaves media URLs unprotected; a password here would be a stricter trust model guarding metadata about films anyone on the LAN can already fetch. The separation is **convenience, not security** — anything on the LAN can claim to be anyone by setting a header. The cookie files are the one place that bites, and they are protected by file mode on the server, never by who claims to be whom.
- The header is **omitted, never sent empty**, when nobody has chosen: absence is what triggers the gateway's fallback, and that fallback is what keeps a pre-existing install working with its history intact. For the same reason `data/profiles.json` falls back to `DEV_USER_ID` under a readable name rather than to an empty picker.
- **The picker appears only once a second person exists.** A household of one has no question to answer.
- Switching profile **clears the whole query cache** — nearly everything is answered per user, and a leftover key shows one person's shelf under another's name.
- `shared/api/http.ts` is the only thing that calls `fetch` for the gateway. "Every request must say who is asking" does not survive being remembered at forty call sites.

**Cookies (`accountfile`)** — files, mode 0600 in a directory 0700, never the database: a cookies.txt is a live Google session, and in Postgres it would be in every backup, every `SELECT *` and the query log. **Write-only across the API**: no route returns cookie content, so there is no read path to leak and none to log. The paste endpoint **refuses plaintext HTTP** (426), loopback excepted.

- **`purposeAccount` carries a session; `purposeListing` still carries none.** The old rule said cookies never touch listings because the scanner walks 93 sources an hour; reading one person's own subscription feed is a listing too, so the rule is made *narrower*, not dropped. There is a test saying so that should not be deleted — verified by removing the guard and watching it fail.
- **Two authentication failures in a row expire an account**, and an expired account is skipped entirely. One is ordinary — the same URL here has answered 206, then 403, then 206 within an hour — and a 403 is deliberately **not** read as a dead session. Replaying a dead cookie hourly is how a blocked address (§8 risk 6) becomes a banned account. A good pass clears the count; re-pasting revives it.
- **Who somebody follows comes from the subscription list, never inferred from the uploads feed.** `:ytsubs` is the *uploads* of everything a member follows, read 50 deep — measured, those 50 videos came from **19 channels for a member who follows 152**, and a channel gone quiet for a fortnight could not be imported at all. `https://www.youtube.com/feed/channels` returns one entry per channel in a single request (verified: 152). Not a yt-dlp alias — there is none — so it is the URL. **Uncapped**, unlike the video feeds: a page of a subscription list is not "the newest few" but an arbitrary subset of who somebody follows.
- **The import only ever adds.** A channel missing from the answer stays subscribed. Reconciling would let a short answer, an unfinished page or a refusal unsubscribe a member from everything in one pass — and ranking reads that record, so a bad minute upstream would empty a Home feed with no trace of why. Unsubscribing stays something done in this app.
- **The fast RSS pass follows every member's channels, deduplicated** (`ListSubscriptionsRequest.all_members`). It asked as one user id, so channels only the other members follow were never scanned. An explicit flag rather than an empty `user_id`, which stays refused — that guard is what stops a browser being handed the whole household's subscriptions by omission.
- Imported per member into tables that already exist: `:ytsubs` → `catalog.subscriptions`, `:ytfav` → `catalog.reactions`. **`:ythis` (watch history) is not imported** — the catalogue already keeps its own from actual playback.
- **`:ytrec` is tagged `YOUTUBE_REC` and fenced to the discovery bucket**, like `RELATED`. §6's value is that every score can be explained, and YouTube's ordering cannot be; it is allowed to be material, not a fifth of the page.
- `ACCOUNT_SCAN_INTERVAL` (1h), on its own schedule apart from the anonymous scanner — this is the only traffic carrying a name, and it must be stoppable without stopping the library being scanned at all.
- The settings screen names **Get cookies.txt LOCALLY** and warns against **"Get cookies.txt"** without LOCALLY, which was pulled from the Chrome Web Store as malware. A reader is about to hand an extension their Google session; naming the wrong one is the worst thing that page can cause.

**Save is a personal shelf; pinning is a fact about the disk.** They were one boolean — `videos.pinned` — so one member pressing Save put the video on everybody's Saved page. It was the only per-viewer state in catalog not keyed by `user_id`, next to `watch_progress`, `reactions`, `subscriptions` and `watch_later`, which all are.

- They cannot be separated by adding `user_id` to `pinned`, because they are two facts. Whose shelf a video is on is personal; whether the sweep may delete its bytes is a question about one disk and one 300 GiB budget, and the answer must be "no" while **anybody** has it saved.
- So `catalog.saved (user_id, video_id)` is the shelf and `videos.pinned` is **derived** from it — recomputed as `EXISTS(SELECT 1 FROM saved …)` inside the same transaction as the save, never set from the direction of the call. Eviction and its partial index go on reading the column untouched.
- **`Video.pinned` on the wire means "this viewer saved it"**, resolved through `videoSelect`'s join like every other per-viewer field. Nothing outside catalog wants the household-wide flag; every card on the page wants this one to label its own button.
- Pre-picker saves go to `DEV_USER_ID`, the same answer §6b already gives for signals. The literal is in the migration, because a migration must produce the same database twice.
- Storage shows a **Kept** count — household-wide, deliberately. That page is where somebody asks why the disk is full, and "people are keeping things" is the answer. The button itself stays personal.

Applying `0012_discovered_via.sql`, `0013_discovered_via_youtube_rec.sql` and `0014_saved.sql` is required.

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
