# YouTube stopped handing over the bytes

**Status: blocked upstream. Nothing in this repository can fix it.**
Last measured 2026-08-18.

## What happens

Pressing play on a video that is not already on disk gives a player that will
not start, and the download queued behind it fails. The ingest log carries
`HTTP Error 403: Forbidden` for the transfer.

## What was measured

24 videos from this library, downloaded one at a time with yt-dlp:

| | |
|---|---|
| downloaded | **1** |
| refused with 403 | **23** |

Stable per video. The same videos fail on every retry and the same ones succeed,
minutes or hours apart, so this is not one of googlevideo's passing refusal
waves.

**It is not an IP ban.** Metadata resolves perfectly — titles, durations, the
full list of 23–27 formats, all of it. A ban would take that too. A handful of
videos also still download, which a ban would not allow.

**The format list gives no warning.** A video that downloads and a video that
refuses look identical beforehand: same number of formats, same `ANDROID_VR`
client, same itag 140 audio track. The difference only appears when the bytes
are asked for.

## Why

yt-dlp says it plainly in its own debug output:

```
[youtube] Detected experiment to bind GVS PO Token to video ID for web_safari client
[youtube] Some web_safari client https formats have been skipped as they are
          missing a URL. YouTube is forcing SABR streaming for this client.
```

See [yt-dlp#12482](https://github.com/yt-dlp/yt-dlp/issues/12482).

YouTube is moving playback onto **SABR**, its own streaming protocol, and
withdrawing the per-format URLs that anything outside its player used to fetch.
The URLs that still appear serve roughly the first two megabytes and then refuse
the rest.

**The trap:** the client that still yields URLs (`android_vr`) does not take a
PO token, and the client that takes one (`web_safari`) yields no URLs. There is
no combination of the two that works.

## One cause, not four bugs

This explains everything chased during the week of 2026-08-16:

- a live mux whose audio stopped at 0.79s while the picture ran on — ffmpeg had
  read one 16 KB buffer and could not get the next
- resolved URLs "born dead", refusing every request
- a depth limit around 2 MiB on URLs that did serve
- HLS playing from the start but failing on a far seek

Several fixes written before this was understood treat symptoms. They are not
harmful, but they are not the cure either.

## Everything tried

All measured, none worked.

| attempt | result |
|---|---|
| Six InnerTube clients (`android`, `ios`, `web`, `tv_embedded`, `web_embedded`, `mweb`) | no URLs at all — SABR only |
| YouTube.js | no URLs for any client, including `android_vr` — strictly less than yt-dlp |
| ytdlp-nodejs | wraps the same yt-dlp binary, so it fails identically |
| Invidious | a realtime proxy, a different model; its useful half (a manifest) is built here already |
| bgutil PO token provider | installed and generating tokens — still 403 |
| Request headers exactly as yt-dlp sends them | identical results with and without |
| `&range=` query instead of the `Range` header | 403 |
| One long open-ended read instead of many bounded ones | 403 immediately |
| HLS instead of a server-side mux | same bytes, same refusal |

The PO token provider is installed and left in place, because it is what a
future SABR-capable yt-dlp will want:

- provider: `~/.local/share/bgutil-pot`
- plugin: `~/.config/yt-dlp/plugins/bgutil`

## What still works

- **Every video already on disk**, in full, with seeking.
- Feed, ranking, search, history, watch progress.
- Scanning and metadata: the library keeps learning about new videos. It simply
  cannot fetch their bytes.

## What to do

```sh
brew upgrade yt-dlp
yt-dlp -f bestaudio -o /tmp/x.m4a "https://www.youtube.com/watch?v=<any-video>"
```

Every few days. A file appearing means SABR support has landed; at that point
the HLS work on `tier-ladder-smoothness` can be tested and wired in.

Writing SABR here is not a realistic answer. It is a protobuf request format, a
UMP-framed response, and a BotGuard attestation token — the yt-dlp project is
working through it with many contributors, and a private implementation would
break at YouTube's next change.
