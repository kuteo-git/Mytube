"""Tests for castbridge: which speakers are offered, what a status says, and
that each route reaches the speaker it names. No speaker is touched.

    .venv-castbridge/bin/python -m pytest services/castbridge
"""
import json
import threading
import urllib.request
from http.server import ThreadingHTTPServer
from types import SimpleNamespace

import castbridge


def test_a_television_is_not_offered():
    assert castbridge.listable("audio")
    assert castbridge.listable("group")
    assert not castbridge.listable("cast")


def test_a_status_has_every_field_whatever_is_missing():
    assert castbridge.status_view("", None, None) == {
        "player_state": "UNKNOWN", "content_id": "", "app_id": "", "current_time": 0.0,
        "duration": 0.0, "idle_reason": "", "volume_level": 0.0,
    }


def test_a_status_carries_the_media():
    media = SimpleNamespace(player_state="IDLE", content_id="http://m/a", adjusted_current_time=12.5,
                            duration=184.2, idle_reason="FINISHED")
    got = castbridge.status_view("CC1AD845", media, 0.3)
    assert got["player_state"] == "IDLE" and got["idle_reason"] == "FINISHED"
    assert got["content_id"] == "http://m/a" and got["current_time"] == 12.5 and got["volume_level"] == 0.3


class FakeSpeakers:
    def __init__(self):
        self.calls = []

    def devices(self):
        return [{"uuid": "u1", "name": "Mom speaker", "model": "Google Nest Mini", "cast_type": "audio", "host": "h"}]

    def status(self, uuid):
        if uuid != "u1":
            raise KeyError(uuid)
        return castbridge.status_view("CC1AD845", None, 0)

    def __getattr__(self, action):
        def record(uuid, body=None):
            if uuid != "u1":
                raise KeyError(uuid)
            self.calls.append((action, uuid, body))
        return record


def serve(speakers):
    server = ThreadingHTTPServer(("127.0.0.1", 0), castbridge.make_handler(speakers))
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, f"http://127.0.0.1:{server.server_address[1]}"


def call(base, method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"} if data else {})
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())


def test_routes_reach_the_named_speaker():
    speakers = FakeSpeakers()
    server, base = serve(speakers)
    try:
        assert call(base, "GET", "/health") == (200, {"service": "local-mytube-castbridge"})
        assert call(base, "GET", "/devices")[1][0]["uuid"] == "u1"
        load = {"url": "http://mac/api/videos/a/audio", "content_type": "audio/mp4", "title": "A", "start_s": 90}
        assert call(base, "POST", "/devices/u1/load", load)[0] == 200
        assert call(base, "POST", "/devices/u1/pause")[0] == 200
        assert call(base, "POST", "/devices/u1/seek", {"s": 60})[0] == 200
        assert call(base, "POST", "/devices/u1/volume", {"level": 0})[0] == 200
        assert call(base, "GET", "/devices/u1/status")[1]["player_state"] == "UNKNOWN"
        assert speakers.calls == [
            ("load", "u1", load), ("pause", "u1", None), ("seek", "u1", {"s": 60}), ("volume", "u1", {"level": 0}),
        ]
    finally:
        server.shutdown()


def test_an_unknown_speaker_is_not_found():
    server, base = serve(FakeSpeakers())
    try:
        assert call(base, "POST", "/devices/nope/pause")[0] == 404
        assert call(base, "GET", "/devices/nope/status")[0] == 404
        assert call(base, "GET", "/nothing")[0] == 404
    finally:
        server.shutdown()


def test_load_asks_for_a_buffered_stream_from_the_start_given():
    played = {}
    mc = SimpleNamespace(play_media=lambda url, ct, **kw: played.update(url=url, ct=ct, **kw),
                         block_until_active=lambda timeout: None)
    speakers = castbridge.Speakers.__new__(castbridge.Speakers)
    speakers.cast = lambda uuid: SimpleNamespace(media_controller=mc)
    speakers.load("u1", {"url": "http://m/a", "content_type": "audio/mp4", "title": "A", "start_s": 90})
    assert played["stream_type"] == "BUFFERED", "a LIVE stream cannot be seeked on a Nest"
    assert played["current_time"] == 90.0 and played["url"] == "http://m/a" and played["ct"] == "audio/mp4"


def test_volume_is_clamped():
    levels = []
    rc = SimpleNamespace(set_volume=levels.append)
    speakers = castbridge.Speakers.__new__(castbridge.Speakers)
    speakers.cast = lambda uuid: SimpleNamespace(socket_client=SimpleNamespace(receiver_controller=rc))
    speakers.volume("u1", {"level": 1.7})
    speakers.volume("u1", {"level": -1})
    assert levels == [1.0, 0.0]
