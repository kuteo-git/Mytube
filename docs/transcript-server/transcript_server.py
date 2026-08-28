#!/usr/bin/env python3
"""A second door to YouTube's captions, for when the first one is shut.

YouTube rate-limits the caption endpoint by *address*. Measured on this
household's connection on 2026-08-27 it did so for thirteen hours straight,
while videos played normally the whole time — the block is on the house, not on
the request, so nothing the app does to its own requests gets past it.

This runs on **localhost**, beside the rest of the stack. It used to be written
for another machine, on the reasoning that another machine is another address.
That was measured and it is false: running on the Home Assistant box in the same
house was refused with exactly the same 429, in the same minute. A second door
in the same wall is not a second door.

What changes the address is the **proxy**, and the proxy is named per request by
the caller — so there is nothing here to configure and nothing here to keep. See
`X-Transcript-Proxy` below.

    GET /transcript?video_id=<id>&langs=vi,en
    X-Transcript-Proxy: http://user:pass@host:port      (optional)

    200 {"language": "vi", "generated": true, "vtt": "WEBVTT\\n\\n..."}
    200 {"error": "...", "kind": "proxy|upstream"}   — asked properly, could not be answered
    400 {"error": "..."}                             — asked wrongly

Why VTT rather than a list of cues: VTT is what goes on the disk at the other
end. Sending anything else means a second parser over there, and this library
already ships a WebVTTFormatter that writes it in one line.

Why `langs` comes from the caller: "Vietnamese if there is any, else English" is
one rule, and it belongs to the app that shows the subtitles. Two servers
holding it separately is two rules that agree until one of them is changed.

Why the proxy comes from the caller too, and is not read from the environment
here: the app reads its own settings **per request**, deliberately, because a
caption fetch can be started by a retry timer with no request to carry anything
on. A proxy read once at start-up over here would mean saving the settings form
did nothing until somebody restarted a different process — the exact trap
`internal/mediaroot` exists to document. It also leaves this server holding no
credential at all.

Run it:

    pip install youtube-transcript-api
    python3 transcript_server.py

`scripts/dev.sh` does that already, on :8009, and binds to loopback.
"""

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

import requests

from youtube_transcript_api import YouTubeTranscriptApi
from youtube_transcript_api.formatters import WebVTTFormatter
from youtube_transcript_api.proxies import GenericProxyConfig

PORT = int(os.environ.get("TRANSCRIPT_PORT", "8009"))

# Loopback only.
#
# This used to bind 0.0.0.0 and guard the door with a shared secret, because it
# was meant to run on another machine. It does not any more — another machine in
# the same house is the same address, which was the whole point and was measured
# to be false. On loopback there is nobody else to let in, so the key is gone
# rather than kept "just in case": a secret that protects nothing is one more
# thing to configure wrongly.
HOST = os.environ.get("TRANSCRIPT_HOST", "127.0.0.1")

# What to answer when the caller names no preference. The app always names one;
# this is for somebody testing by hand in a browser.
DEFAULT_LANGS = ["vi", "en"]

# The header the caller names a proxy in. See the module docstring for why it is
# not read from this process's environment.
PROXY_HEADER = "X-Transcript-Proxy"

# Where to ask what address a request went out by.
#
# Reported alongside the transcript because the two failures look identical from
# the app: a proxy that is not connected and a proxy that is connected and not
# changing the address both end in captions that do not arrive.
ECHO_URL = "https://ipv4.webshare.io/"


class ProxyUnusable(Exception):
    """The request never reached YouTube.

    Told apart from an upstream refusal deliberately. They want opposite
    responses — one is fixed on a settings screen and the other by waiting or by
    rotating — and from the app they arrive as the same empty answer unless this
    side says which it was.
    """


def outbound_ip(proxy_url: str) -> str:
    """The address a request leaves by, or "" if it cannot be learned.

    Never fatal. This is a courtesy for whoever is reading the answer, and an
    echo service being down says nothing about whether captions work.
    """
    try:
        proxies = {"http": proxy_url, "https": proxy_url} if proxy_url else None
        return requests.get(ECHO_URL, proxies=proxies, timeout=15).text.strip()
    except Exception:
        return ""


def pick_and_fetch(video_id: str, langs: list[str], proxy_url: str) -> dict:
    """The whole of it: list what exists, take the first language asked for.

    One listing and one download. The app's own path does exactly this and is
    refused; the only thing different here is which address it comes from.
    """
    # A fresh instance per request: the library builds a requests.Session and
    # says plainly that it is not thread-safe, and this server is threaded. The
    # proxy changes per request as well, now that it is the caller's to name.
    proxy = (
        GenericProxyConfig(http_url=proxy_url, https_url=proxy_url)
        if proxy_url
        else None
    )
    api = YouTubeTranscriptApi(proxy_config=proxy)

    try:
        listing = api.list(video_id)
    except Exception as e:
        # Which of the two things went wrong is **measured, not inferred from
        # the exception type**, and that correction cost a wrong answer to find.
        #
        # The obvious version of this caught ProxyError/SSLError/ConnectTimeout
        # around the call. It does not work: youtube_transcript_api catches
        # transport failures itself and re-raises them as its own `IpBlocked`.
        # Measured with a deliberately wrong proxy password — the same request
        # surfaced a raw `ProxyError` once and `IpBlocked` the next time, so a
        # proxy nobody could connect to was reported as YouTube refusing the
        # address. That is the worst possible answer: it sends somebody to wait
        # out a block that does not exist while their credential stays wrong.
        #
        # So when a proxy was named and the fetch failed, the proxy is asked
        # whether it carries a request at all. One extra request, only on the
        # failing path.
        if proxy_url and not outbound_ip(proxy_url):
            raise ProxyUnusable(f"{type(e).__name__}: {e}") from e
        raise

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

        query = parse_qs(parsed.query)
        video_id = (query.get("video_id") or [""])[0].strip()
        if not video_id:
            self._send(400, {"error": "video_id is required"})
            return

        raw_langs = (query.get("langs") or [""])[0].strip()
        langs = [x.strip() for x in raw_langs.split(",") if x.strip()] or DEFAULT_LANGS

        # Named by the caller, per request. Absent means "go out by this
        # machine's own address", which is what every install starts as and what
        # YouTube has been refusing here since 2026-08-27.
        proxy_url = self.headers.get(PROXY_HEADER, "").strip()

        try:
            answer = pick_and_fetch(video_id, langs, proxy_url)
            self._send(200, answer)
        except ProxyUnusable as e:
            # 200 with an error field, not a 500. The request to *this* server
            # was fine; what failed is the proxy it was told to use.
            #
            # `kind` is the whole reason this exception exists: from the app,
            # a proxy that will not connect and a video with no captions arrive
            # as the same empty answer, and they want opposite responses.
            self._send(200, {"error": str(e), "kind": "proxy"})
        except Exception as e:
            # A video with no captions, none in a language that was asked for,
            # or this address being refused in its turn. The caller reads the
            # message and moves on to its next way of asking.
            self._send(200, {"error": f"{type(e).__name__}: {e}", "kind": "upstream"})


if __name__ == "__main__":
    print(
        f"transcript server on {HOST}:{PORT} — "
        f"the proxy is named per request by the caller, not configured here",
        flush=True,
    )
    ThreadingHTTPServer((HOST, PORT), Handler).serve_forever()
