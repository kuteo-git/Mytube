# YouTube stopped handing over the bytes — and then handed most of them back

**Status: playable again, on a pinned nightly yt-dlp.**
Last measured 2026-08-18.

This file used to say "blocked upstream, nothing in this repository can fix it".
That was wrong, and wrong in an instructive way: the measurement behind it asked
only about the one rendition that really had died, using a yt-dlp a month old.

## What was actually broken

**itag 18 — the 360p progressive rendition, and the tier every video opened on.**

Measured across 16 videos of this library, on URLs resolved seconds earlier, one
request each:

| track | head of file (`bytes=0-1048575`) | middle (`bytes=4194304-5242879`) |
|---|---|---|
| itag 18 — 360p progressive | 206 | **403, 12 of 14. 206 never** |
| itag 136 — 720p H.264 | 206 | **206, 13 of 14** |
| itag 137 — 1080p H.264 | 206 | **206** |
| itag 140 — AAC audio | 206 | **206, 12 of 14** |

The remaining cases are not refusals: one video publishes no 720p at all, and a
`416` is a range past the end of a short file.

**The head always serves.** That is the whole reason this was so hard to see —
and it is what `verifyURL` probes. A dead progressive URL passes the probe, gets
handed to the browser as a checked URL, and dies a megabyte in. The comment in
`verify.go` warns about exactly this trap one level up; this is the same trap one
level down.

What the viewer got, both halves visible in one console:

```
/api/videos/9n4Ui1xT_Sg/instant  403 (Forbidden)
  → MEDIA_ELEMENT_ERROR: Format error          (refused outright)
/api/videos/b_5Z7D-g4fA/instant  403 (Forbidden)
  → PIPELINE_ERROR_READ: FFmpegDemuxer: data source error   (died mid-file)
```

## What the old measurement got wrong

- It used **yt-dlp 2026.07.04**, the current stable, which is a month old and
  resolves URLs that no longer serve. A nightly resolves adaptive tracks that do.
- It read the format list by exact id, so `140` was reported as "no URL" on
  videos with more than one audio track — where it appears as `140-0`, `140-1`,
  one per language. The audio was there the whole time.
- It concluded "SABR has taken everything". SABR has taken **progressive**.
  Adaptive H.264 and AAC still serve, at the head and the middle alike.

The `android_vr` / `web_safari` PO-token trap it describes is real and unchanged.
It just is not what was stopping playback here.

## What changed as a result

- **yt-dlp is a pinned nightly.** `YTDLP_PATH` in `scripts/dev.sh`, read by
  `ytdlp/session.go` and applied with `SetExecutable`. Pinned, not tracked: a
  nightly that upgrades itself is a stack that breaks on a morning nobody
  touched it. The stable install stays where it is as the way back.

  ```sh
  pipx install --pip-args=--pre "yt-dlp==2026.8.17.73947.dev0"
  ```

- **The instant tier is no longer offered**, and no longer resolved for. The
  route and its proxy stay: upstream stopped serving what they fetch, which is
  not the same as the code being wrong.
- **The muxed tier opens every video**, H.264 + AAC, `-c copy`, 720p on auto and
  1080p when pinned.
- **A player that gave up unlocks when the file lands.** `loadFailed` was never
  cleared, so a refused mux ended the video for as long as the page stayed open
  — while the download beside it finished in a median of thirteen seconds.

## Measured on the running stack

```
GET /api/videos/{id}/stream            → remux only, height 720
GET /api/videos/{id}/remux             → 200, 102 MB in 40s
    h264 720p + aac, video ends 2254.29s, audio 2253.91s   (full length, both tracks)
GET /api/videos/{id}/remux?height=1080 → 200, 124 MB in 30s, h264 1080p + aac
GET /api/videos/{id}/remux/start?t=600 → {"start":598.541667}
```

The audio that used to stop at 0.79s runs the whole 37 minutes.

## The pin went back to a release (2026-08-20)

This section's first instruction used to be "check for a stable release carrying
the same fix, then move to it". `2026.8.19` is that release — published
2026-08-19T23:48Z, two days after the nightly this was pinned to, carrying the
same YouTube player-client work (`#17261` player client maintenance, `#17185`
client versions, `#17461` drop `android_vr`, `#17462` `web_embedded`
fallbacks).

Both binaries, one sitting, five videos of this library, freshly resolved URLs,
one request each — the same probe as the table above:

| track | nightly 2026.8.17.73947.dev0 | stable 2026.8.19 |
|---|---|---|
| itag 136 — 720p H.264 | 206 head, 206 middle | 206 head, 206 middle |
| itag 137 — 1080p H.264 | 206 head, 206 middle | 206 head, 206 middle |
| itag 140 — AAC audio | 206 head, 206 middle | 206 head, 206 middle |
| itag 18 — 360p progressive | **206 head, 403 middle, 5 of 5** | **not published at all, 5 of 5** |
| `-f 137+140`, first 10s | rc=0, 9s | rc=0, 9s, byte-identical file |

Where a video published no 136/137/140 at all it published none on either
binary — the same videos, the same gaps. Nothing regressed and nothing new
appeared.

**The interesting row is itag 18.** Upstream now declines to offer the format
rather than offering one that dies a megabyte in. That is this document's own
conclusion reached from the other side, and it is the strongest evidence yet
that the withdrawal is deliberate and not a passing refusal wave.

- **What did not change.** The scanner's `This channel does not have a videos
  tab` failures are identical on both binaries; those channels publish no Videos
  tab, and `#17386` is not about them. Recorded so nobody measures it twice.
- **The way back is one variable.** The nightly is still installed beside it
  (`pipx install --suffix=-stable` put the two side by side), so
  `YTDLP_PATH=$HOME/.local/bin/yt-dlp` restores the previous stack exactly.

## HLS, and the device the mux never worked on (2026-08-20)

This document had no HLS section, and the routes had been built and left
unwired pending "proof it plays on the device it was measured for". Here is
that measurement, plus one nobody had asked for.

**iPhone, iOS 18.7, through the app, same video minutes apart:**

| | muxed stream | HLS |
|---|---|---|
| pressing play | **no picture at all** | plays |
| duration | none | 641.8s |
| seeking | — | works, twice |

The mux failing there was not suspected. It works in desktop Chrome, and every
measurement in this document until now was taken in desktop Chrome. What the
server saw was `live mux opened … headBytes=1572864` followed by
`live mux closed bytes=3100144` **168 ms later**, with no
`remux stream ended early` and no `live mux complained` — the client hung up,
and the server had nothing to report.

**And Chrome is the mirror image**, measured the same day: fed
`master.m3u8` it fails with `MEDIA_ERR_SRC_NOT_SUPPORTED` (code 4). Neither
tier covers both devices.

**The capability probe was the trap.** `canPlayType('application/vnd.apple.mpegurl')`:

| | says | does |
|---|---|---|
| Chrome, macOS | `"maybe"` | fails, code 4 |
| Safari, iOS 18.7 | `"maybe"` | plays and seeks |

`web/public/mse-check.html` asked exactly this, and the commit that built the
HLS routes recorded `HLS natively maybe <- "" means no; "maybe" is a yes`. It
is not. The engine is what separates them: iOS has `ManagedMediaSource` and
**no `MediaSource` at all**, so hls.js could never have covered the iPhone —
native HLS there is not a preference, it is the only path.

**Delivery is not the constraint.** Segments average 0.61 MB per 5.39 s of
video — about 113 KB/s to keep up. Timed through the gateway and through the
Vite dev proxy, the path the phone actually takes:

    12–510 ms per segment, for 3–7 s of video each

Twenty to two hundred times faster than realtime. A `stalled` event seen on the
phone during linear playback had no `waiting` beside it: that is the network
going idle once the buffer filled, not a starved picture.

## What to watch

- **The probe.** `verifyURL` reads the head only, which is what let dead
  progressive URLs through. It is not currently wrong for the adaptive tracks —
  they answered consistently at both depths — but it is one measurement away
  from being wrong again.
- **A release is not a promise.** If playback breaks with no change here, roll
  `YTDLP_PATH` back to the nightly and measure before changing anything else.

## Still true from before

- Writing SABR here is not a realistic answer: protobuf request format, UMP
  framing, BotGuard attestation, and a private implementation would break at
  YouTube's next change.
- The bgutil PO token provider is installed and left in place for the day a
  SABR-capable yt-dlp wants it — provider `~/.local/share/bgutil-pot`, plugin
  `~/.config/yt-dlp/plugins/bgutil`.
