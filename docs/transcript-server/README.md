# A second door to YouTube's captions

YouTube rate-limits the caption endpoint by **address**. Measured on this
household's connection on 2026-08-27 it did so for thirteen hours straight,
while videos played normally the whole time — the block is on the house, not on
the request, so nothing the app does to its own requests gets past it.

This is a small server to run somewhere else. The app asks it only when its own
attempt was refused; leave Settings → Transcript empty and it is never used.

## The contract

    GET /transcript?video_id=<id>&langs=vi,en

    200 {"language": "vi", "generated": true, "vtt": "WEBVTT\n\n..."}
    200 {"error": "..."}    asked properly, could not be answered
    400 {"error": "..."}    asked wrongly
    401 {"error": "..."}    TRANSCRIPT_API_KEY set and not matched

There is no standard for this. OpenAI's `/v1/audio/transcriptions` is an audio
upload, not a lookup by video id, and every commercial service that does this
has invented its own shape — so this one was defined, in the smallest form that
answers the question.

`vtt` rather than a list of cues, because VTT is what goes on disk at the other
end; anything else means a second parser over there. `langs` comes from the
caller because "Vietnamese if there is any, else English" is one rule and it
belongs to the app that shows the subtitles.

Anything can answer this — it is four lines of routing. `transcript_server.py`
is the version built on `youtube_transcript_api`, whose whole business is
keeping up with the shape of YouTube's player response.

## Running it anywhere

    pip install -r requirements.txt
    TRANSCRIPT_PORT=8185 python3 transcript_server.py

It runs on loopback and there is nothing to configure in the app — the proxy is
named per request by the caller. See **Settings → Proxy**.
Press Test: it reports the language, the number of lines and the first line —
not a verdict, because a server can answer 200 with an empty transcript.

## Running it on Home Assistant with Pyscript

`pyscript_startup.py` starts it at boot. Pyscript does **not** run the server
itself and cannot: it is a restricted AST interpreter and `serve_forever()`
never returns. It starts a plain `python3` process and gets out of the way.

    /config/pyscript/
      transcript_startup.py          <- pyscript_startup.py, renamed
      servers/transcript/
        transcript_server.py
        requirements.txt

Two things worth checking on a Home Assistant OS box, because neither is this
code's to fix:

- **Can the port be reached from the rest of the network?** The core container
  publishes what its own configuration publishes. If another server started
  this way is already reachable, this one will be too.
- **Does `pip install` survive a restart?** On HA OS the container filesystem is
  rebuilt on upgrade, which is why the startup script installs every boot rather
  than assuming.
