"""castbridge — the Google Nest speakers, over HTTP, for the cast service.

The cast service (Go) owns sessions and decides what plays; this only finds the
speakers on the LAN and passes its orders to them. The Cast protocol has no Go
library worth trusting, and pychromecast is the one the Home Assistant world
keeps working.

    GET  /health                       -> {"service": "local-mytube-castbridge"}
    GET  /devices                      -> [{uuid, name, model, cast_type, host}]
    POST /devices/{uuid}/load          {url, content_type, title, thumbnail, start_s}
    POST /devices/{uuid}/pause|play|stop
    POST /devices/{uuid}/seek          {s}
    POST /devices/{uuid}/volume        {level 0..1}
    GET  /devices/{uuid}/status        -> {player_state, content_id, app_id,
                                           current_time, duration, idle_reason,
                                           volume_level}

Listens on 127.0.0.1 only: nothing but the cast service on this Mac should be
able to make a speaker in the house play.
"""

import json
import logging
import os
import sys
import threading
import time
import uuid as uuidlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SERVICE = "local-mytube-castbridge"
HOST = "127.0.0.1"
PORT = int(os.environ.get("CASTBRIDGE_PORT", "8187"))

# A speaker that has stopped announcing itself is kept this long before it is
# no longer offered, so a Wi-Fi blip does not end a session.
GONE_AFTER_S = 60

logging.basicConfig(
    stream=sys.stdout,
    level=logging.INFO,
    format="%(asctime)s - castbridge - %(levelname)s - %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
log = logging.getLogger("castbridge")


def listable(cast_type):
    """Speakers and groups of them. A Chromecast with a screen (`cast`) is a
    television, which the household does not cast sound to."""
    return cast_type in ("audio", "group")


def device_view(info):
    return {
        "uuid": str(info.uuid),
        "name": info.friendly_name,
        "model": info.model_name,
        "cast_type": info.cast_type,
        "host": info.host,
    }


def status_view(app_id, media_status, volume_level):
    """One answer about a speaker, in the shape the Go driver reads. Every field
    is present: absence is decided here, once."""
    s = media_status
    return {
        "player_state": (s.player_state if s else None) or "UNKNOWN",
        "content_id": (s.content_id if s else None) or "",
        "app_id": app_id or "",
        "current_time": float((s.adjusted_current_time if s else 0) or 0),
        "duration": float((s.duration if s else 0) or 0),
        "idle_reason": (s.idle_reason if s else None) or "",
        "volume_level": float(volume_level or 0),
    }


class Speakers:
    """What has been discovered, and a connection to each speaker used."""

    def __init__(self):
        import pychromecast
        import zeroconf
        from pychromecast.discovery import CastBrowser, SimpleCastListener

        self._pc = pychromecast
        self._lock = threading.Lock()
        self._gone_at = {}  # uuid -> time it stopped announcing itself
        self._casts = {}  # uuid -> connected Chromecast
        # A Zeroconf of our own, given explicitly: CastBrowser browses mDNS only
        # when it holds one, and without it discovery finds nothing and says
        # nothing.
        self._browser = CastBrowser(
            SimpleCastListener(add_callback=self._seen, remove_callback=self._gone, update_callback=self._seen),
            zeroconf.Zeroconf(),
        )
        self._browser.start_discovery()

    def _seen(self, uuid, _service):
        with self._lock:
            self._gone_at.pop(uuid, None)

    def _gone(self, uuid, _service, info):
        log.info("speaker gone: %s", info.friendly_name)
        with self._lock:
            self._gone_at[uuid] = time.monotonic()

    def devices(self):
        now = time.monotonic()
        with self._lock:
            gone = dict(self._gone_at)
        out = []
        for uuid, info in list(self._browser.devices.items()):
            if uuid in gone and now - gone[uuid] > GONE_AFTER_S:
                continue
            if listable(info.cast_type):
                out.append(device_view(info))
        return sorted(out, key=lambda d: d["name"])

    def cast(self, uuid_text):
        uuid = uuidlib.UUID(uuid_text)
        with self._lock:
            cast = self._casts.get(uuid)
        if cast is not None and cast.socket_client.is_connected:
            return cast
        info = self._browser.devices.get(uuid)
        if info is None or not listable(info.cast_type):
            raise KeyError(uuid_text)
        cast = self._pc.get_chromecast_from_cast_info(info, self._browser.zc, tries=2, timeout=5)
        cast.wait(timeout=10)
        with self._lock:
            self._casts[uuid] = cast
        log.info("connected: %s", info.friendly_name)
        return cast

    def load(self, uuid, body):
        cast = self.cast(uuid)
        mc = cast.media_controller
        # BUFFERED, not pychromecast's default LIVE: this is a file with an end,
        # and a speaker told it is live will not seek in it.
        mc.play_media(
            body["url"],
            body.get("content_type") or "audio/mp4",
            title=body.get("title") or None,
            thumb=body.get("thumbnail") or None,
            current_time=float(body.get("start_s") or 0),
            stream_type="BUFFERED",
        )
        mc.block_until_active(timeout=10)

    def pause(self, uuid):
        self.cast(uuid).media_controller.pause()

    def play(self, uuid):
        self.cast(uuid).media_controller.play()

    def stop(self, uuid):
        self.cast(uuid).media_controller.stop()

    def seek(self, uuid, body):
        self.cast(uuid).media_controller.seek(float(body["s"]))

    def volume(self, uuid, body):
        level = min(max(float(body["level"]), 0.0), 1.0)
        self.cast(uuid).socket_client.receiver_controller.set_volume(level)

    def status(self, uuid):
        cast = self.cast(uuid)
        mc = cast.media_controller
        app_id = cast.app_id
        media = mc.status if app_id == "CC1AD845" else None
        return status_view(app_id, media, cast.status.volume_level if cast.status else 0)


def make_handler(speakers):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, fmt, *args):
            # One line per request through our own logger, not the stdlib's
            # separate stderr format that logview cannot read.
            log.debug(fmt, *args)

        def _send(self, status, payload):
            body = json.dumps(payload).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def _body(self):
            length = int(self.headers.get("Content-Length") or 0)
            return json.loads(self.rfile.read(length) or b"{}") if length else {}

        def _route(self, method):
            parts = [p for p in self.path.split("?")[0].split("/") if p]
            try:
                if method == "GET" and parts == ["health"]:
                    return self._send(200, {"service": SERVICE})
                if method == "GET" and parts == ["devices"]:
                    return self._send(200, speakers.devices())
                if len(parts) == 3 and parts[0] == "devices":
                    uuid, action = parts[1], parts[2]
                    if method == "GET" and action == "status":
                        return self._send(200, speakers.status(uuid))
                    if method == "POST" and action in ("pause", "play", "stop"):
                        getattr(speakers, action)(uuid)
                        log.info("%s %s", action, uuid)
                        return self._send(200, {"ok": True})
                    if method == "POST" and action in ("load", "seek", "volume"):
                        body = self._body()
                        getattr(speakers, action)(uuid, body)
                        log.info("%s %s %s", action, uuid, {k: v for k, v in body.items() if k != "thumbnail"})
                        return self._send(200, {"ok": True})
                return self._send(404, {"error": "not found"})
            except (KeyError, ValueError) as e:
                log.warning("%s %s: unknown speaker or bad request: %s", method, self.path, e)
                return self._send(404, {"error": "unknown speaker"})
            except Exception as e:
                log.error("%s %s: %s", method, self.path, e)
                return self._send(502, {"error": str(e)})

        def do_GET(self):
            self._route("GET")

        def do_POST(self):
            self._route("POST")

    return Handler


def main():
    speakers = Speakers()
    log.info("castbridge listening on %s:%d", HOST, PORT)
    ThreadingHTTPServer((HOST, PORT), make_handler(speakers)).serve_forever()


if __name__ == "__main__":
    main()
