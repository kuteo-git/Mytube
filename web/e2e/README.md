# Driving the real player

`live-captions.mjs` opens a broadcast in Chromium against the gateway on
:8180 and asserts that its captions can be turned on and that cues arrive.

```sh
node e2e/live-captions.mjs [videoId]     # default: GotlA1KKWoo, CNN Headlines
```

Two things it needs, and both were found the hard way:

- **The household gate.** `/watch/<id>` renders the profile picker until
  somebody is chosen, and nothing under it exists — no `<video>` at all. The
  choice is `yt-profile-id-v1` in `localStorage`, seeded in an init script so
  the run starts on the page under test rather than clicking through a screen
  that is not what is being measured.
- **A broadcast that actually publishes captions.** It varies by stream:
  measured against YouTube, CNN Headlines and Al Jazeera English carry an `en`
  automatic track, Sky News carries none, and a 24/7 music stream refuses its
  metadata. A stream without one makes this script red for the right reason
  and the wrong test.

It asserts on `textTracks`, not on pixels: cues render into the browser's own
shadow DOM and are not in the page, so `mode === 'showing'` with a non-empty
`cues` list is the strongest statement available from outside.

**Proven to fail.** Built from `0ab1207~1` it reports
`no Settings button — the subtitle rows are unreachable`; built from the fix
it reports a showing `en` track with cues. A loop nobody has watched go red is
a loop nobody should believe.
