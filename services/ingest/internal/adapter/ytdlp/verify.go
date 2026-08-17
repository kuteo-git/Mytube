package ytdlp

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// A signed URL is worth nothing until something has fetched a byte from it.
//
// Measured over 24 fresh resolves of two videos, on the same minute, from the
// same address, with the same credentials: **17 of them were refused** — 403,
// or a redirect to a host that then answers 403 — to any ordinary HTTP client,
// while the remaining 7 answered 206. Same video, same code path, seconds
// apart. yt-dlp's own transfer succeeded throughout, which is why the download
// always lands while the player shows a format error, and why this looked from
// the sofa like the player being broken.
//
// The refusal is not a property of the address, the credentials, or the
// headers, all of which were tested and cleared:
//
//   - User-Agent: the gateway's Chrome string, yt-dlp's own, and none at all
//     were 206/206/206 on a good URL and 403 on a bad one. Not the UA.
//   - Credentials: resolving through the service (cookies) and anonymously both
//     produced good and bad URLs, in both directions, minutes apart.
//   - Player client: mweb was refused carrying the *good* marker, so the
//     marker is not a client setting either.
//
// It is a property of the individual URL, decided when it is issued. The
// player response carries an experiment flag — `fexp=…51946838` on every URL
// that was refused, `…51946837` on every URL that answered, 24 of 24 — which is
// YouTube moving progressive playback behind its own delivery protocol. That
// number is not what is tested here: it is an opaque experiment id that will be
// renumbered, and a rule written against it would go quietly wrong on the day
// it changes. What is tested is the only thing that actually matters, asked of
// upstream directly: does this URL serve a byte?
//
// **The probe must ask for what the reader will ask for, not for one byte.**
// A single byte was the first attempt at this and it under-reports: an adaptive
// audio URL answered `bytes=0-0` with 206 and the 2 MiB range ffmpeg went on to
// send with 403, on the same URL in the same second. A probe that passes and is
// then followed by a refusal is worse than no probe, because it launders a dead
// URL as a checked one.
//
// So the range here is the same megabyte that `httpRequestSizeBytes` gives
// ffmpeg and `instantChunkBytes` gives the gateway's proxy. Whatever those
// become, this follows them.
const probeRangeHeader = "bytes=0-1048575"

// How long the probe may take before the URL is treated as no good. Generous
// enough for a first connection to a fresh googlevideo host, short enough that
// three of them in a row do not become the startup delay this tier exists to
// avoid.
const probeTimeout = 6 * time.Second

// verifier asks upstream whether a URL will really serve bytes.
//
// **Redirects are followed**, because every real reader follows them: ffmpeg,
// the browser, and the gateway's own proxy. This first refused them, on the
// reasoning that a refused URL answers 302 to a host that then answers 403 —
// which is true, and is not the same as a redirect being a refusal. A perfectly
// good URL also redirects, to a host that serves: measured on one video's two
// tracks, `curl` without `-L` gave 302 and 206, and with `-L` gave 206 and 206.
//
// Refusing the redirect made the probe reject a URL that worked, three resolves
// running, and answer the player with a 502 for a video whose download
// succeeded eight seconds later. A probe that is stricter than the thing it
// stands in for does not prevent failures, it invents them.
var probeClient = &http.Client{Timeout: probeTimeout}

// verifyURL reports nil when the URL served a byte, and an error naming what
// upstream said otherwise.
func verifyURL(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", probeRangeHeader)

	resp, err := probeClient.Do(req)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("probe: upstream answered %d", resp.StatusCode)
	}
	return nil
}

// How many times a resolve is repeated in the hope of a URL that answers.
//
// Each attempt is a full metadata fetch, which is the expensive kind §8 risk 6
// counts, so this is a small number rather than a loop that keeps going until
// it wins. It is also not the safety net it might look like: on the afternoon
// this was measured the good rate moved between roughly a third and nothing at
// all, so three attempts is worth having and is not a guarantee. When they all
// fail the tier is simply not offered, which is the honest answer and the one
// the player already knows how to show — a download that always works, landing
// in seconds, with a progress bar in front of it.
const resolveAttempts = 3
