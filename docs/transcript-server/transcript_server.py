#!/usr/bin/env python3
"""A second door to YouTube's captions, for when the first one is shut.

YouTube rate-limits the caption endpoint by *address*. Measured on this
household's connection on 2026-08-27 it did so for thirteen hours straight,
while videos played normally the whole time — the block is on the house, not on
the request, so nothing the app does to its own requests gets past it.

This runs somewhere else. It is deliberately tiny: one endpoint, no state, no
database, no scheduler. Everything it knows about YouTube comes from
`youtube_transcript_api`, whose whole business is keeping up with the shape of
YouTube's player response.

    GET /transcript?video_id=<id>&langs=vi,en

    200 {"language": "vi", "generated": true, "vtt": "WEBVTT\\n\\n..."}
    200 {"error": "..."}          — asked properly, could not be answered
    400 {"error": "..."}          — asked wrongly
    401 {"error": "..."}          — TRANSCRIPT_API_KEY set and not matched

Why VTT rather than a list of cues: VTT is what goes on the disk at the other
end. Sending anything else means a second parser over there, and this library
already ships a WebVTTFormatter that writes it in one line.

Why `langs` comes from the caller: "Vietnamese if there is any, else English" is
one rule, and it belongs to the app that shows the subtitles. Two servers
holding it separately is two rules that agree until one of them is changed.

Run it:

    pip install youtube-transcript-api
    TRANSCRIPT_API_KEY=optional-shared-secret \
    TRANSCRIPT_PROXY=http://user:pass@rotating-residential:80 \
    python3 transcript_server.py

The proxy is not decoration. YouTube blocks by *public* address, so another
machine in the same house is the same address — measured, and refused with the
same 429 in the same minute. Without a proxy this server helps only if it runs
on a different connection entirely.

Then put http://<this-machine>:8009 into Settings → Transcript in the app.
"""

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

from youtube_transcript_api import YouTubeTranscriptApi
from youtube_transcript_api.formatters import WebVTTFormatter
from youtube_transcript_api.proxies import GenericProxyConfig

PORT = int(os.environ.get("TRANSCRIPT_PORT", "8009"))

# Optional. Empty means anyone on the network may ask, which is the same trust
# model the app itself uses on a LAN — but this machine may not be on the same
# LAN, which is the whole point of it, so there is a bolt on the door.
API_KEY = os.environ.get("TRANSCRIPT_API_KEY", "")

# What to answer when the caller names no preference. The app always names one;
# this is for somebody testing by hand in a browser.
DEFAULT_LANGS = ["vi", "en"]

# A proxy to go out through, and on most home networks this is the whole point.
#
# YouTube blocks by **public** address. Another machine in the same house shares
# the same one — measured: this server, running on the Home Assistant box, was
# refused with exactly the 429 the app had been getting, in the same minute. A
# second door in the same wall is not a second door.
#
# What actually changes the address is a proxy, and it has to be a **rotating
# residential** one: datacenter ranges are blocked wholesale, and a static
# residential IP is one address that gets blocked in its turn.
#
#     TRANSCRIPT_PROXY=http://user:pass@p.example.io:80
#
# Only the caption requests go through it — a few tens of kilobytes per video —
# so the smallest bandwidth plan any provider sells is far more than enough.
PROXY_URL = os.environ.get("TRANSCRIPT_PROXY", "")


def pick_and_fetch(video_id: str, langs: list[str]) -> dict:
    """The whole of it: list what exists, take the first language asked for.

    One listing and one download. The app's own path does exactly this and is
    refused; the only thing different here is which address it comes from.
    """
    # A fresh instance per request: the library builds a requests.Session and
    # says plainly that it is not thread-safe, and this server is threaded.
    proxy = GenericProxyConfig(http_url=PROXY_URL, https_url=PROXY_URL) if PROXY_URL else None
    api = YouTubeTranscriptApi(proxy_config=proxy)
    listing = api.list(video_id)

    # find_transcript walks the languages in the order given and returns the
    # first that exists, preferring a human-made track over a generated one for
    # the same language. Failing that, an automatic one is still worth having.
    transcript = listing.find_transcript(langs)
    fetched = transcript.fetch()

    return {
        "language": transcript.language_code,
        "generated": transcript.is_generated,
        # Timings are the point. A transcript flattened to one string of text
        # cannot be shown as subtitles, read aloud in step with the picture, or
        # seeked to — and that is what the app does with it.
        "vtt": WebVTTFormatter().format_transcript(fetched),
    }


class Handler(BaseHTTPRequestHandler):
    # Quieter default logging; one line per request, not three.
    def log_message(self, fmt, *args):
        print("%s - %s" % (self.address_string(), fmt % args), flush=True)

    def _send(self, status: int, body: dict):
        blob = json.dumps(body, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(blob)))
        self.end_headers()
        self.wfile.write(blob)

    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path.rstrip("/") not in ("/transcript", ""):
            self._send(404, {"error": "unknown path"})
            return

        if API_KEY:
            sent = self.headers.get("Authorization", "")
            if sent != f"Bearer {API_KEY}":
                self._send(401, {"error": "bad or missing key"})
                return

        query = parse_qs(parsed.query)
        video_id = (query.get("video_id") or [""])[0].strip()
        if not video_id:
            self._send(400, {"error": "video_id is required"})
            return

        raw_langs = (query.get("langs") or [""])[0].strip()
        langs = [x.strip() for x in raw_langs.split(",") if x.strip()] or DEFAULT_LANGS

        try:
            self._send(200, pick_and_fetch(video_id, langs))
        except Exception as e:
            # 200 with an error field, not a 500. The request to *this* server
            # was fine; what failed is upstream — a video with no captions, none
            # in a language that was asked for, or this address being refused in
            # its turn. The caller reads the message and moves on to its next
            # way of asking.
            self._send(200, {"error": f"{type(e).__name__}: {e}"})


if __name__ == "__main__":
    print(
        f"transcript server on :{PORT}, "
        f"key {'set' if API_KEY else 'not set'}, "
        f"proxy {'set' if PROXY_URL else 'NOT SET — same address as the caller'}",
        flush=True,
    )
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
