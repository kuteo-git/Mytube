"""Start the transcript server when Home Assistant boots.

Put this in `/config/pyscript/` and the server beside it in
`/config/pyscript/servers/transcript/`.

Pyscript does **not** run the server itself, and cannot: it is a restricted AST
interpreter, and `serve_forever()` never returns. What it does is start a plain
CPython process and get out of the way — the same shape as the pytube server
already running here.

    /config/pyscript/
      transcript_startup.py          <- this file
      servers/transcript/
        transcript_server.py
        requirements.txt

This file is **not valid CPython** and `python3 -m py_compile` will refuse it:
`await` appears inside a plain `def`, and `log` and `@time_trigger` are names
Pyscript injects. That is Pyscript's dialect, not a mistake — the same shape as
the pytube starter already here. Only `transcript_server.py` beside it is
ordinary Python, and that is the file that actually serves.
"""

import os
import subprocess
import sys

import asyncio

__BASE_PATH = "/config/pyscript/servers"
__TRANSCRIPT_PATH = f"{__BASE_PATH}/transcript"
__TRANSCRIPT_FILE = "transcript_server.py"
__REQUIREMENTS_FILE = "requirements.txt"

# Where the app will point at this machine. Kept here as well as in the server
# so the log line says the same number the server binds.
__PORT = "8009"

# Optional shared secret. Empty means anyone who can reach the port may ask —
# which is fine on a home network and not fine if this box has a public address.
__API_KEY = ""

# The proxy this server goes out through, and on a machine in the same house it
# is the whole point of running it at all.
#
# YouTube blocks by *public* address. Measured on 2026-08-28: this server on the
# Home Assistant box was refused with the same 429 the app was getting, in the
# same minute, because both leave by the same router. Leave this empty and this
# machine is the same address wearing a different hat.
#
# Rotating residential, not datacenter and not static: a VPS range is blocked
# wholesale, and one fixed residential address gets blocked in its turn.
__PROXY = ""


def __install_requirements(requirements_file: str):
    """Install what the server needs, into the interpreter that will run it."""
    try:
        if not os.path.exists(requirements_file):
            log.error(f"{requirements_file} not found")
            return False

        cmd = [sys.executable, "-m", "pip", "install", "-r", requirements_file]
        log.info(f"Running: {' '.join(cmd)}")
        result = subprocess.run(cmd, check=True, capture_output=True, text=True)
        log.info(f"Requirements installed: {result.stdout[-400:]}")
        return True

    except subprocess.CalledProcessError as e:
        log.error(f"pip failed ({e.returncode}): {e.stderr}")
        return False
    except Exception as e:
        log.error(f"Unexpected error installing requirements: {e}")
        return False


def __start_server(path: str, file: str, env: dict):
    try:
        # Kill any earlier instance. Restarting Home Assistant does not kill
        # processes it started this way, so without this the second start binds
        # nothing and the first one goes on serving stale code.
        subprocess.run(["pkill", "-f", file], check=False)
        await asyncio.sleep(3)

        subprocess.Popen(
            ["python3", f"{path}/{file}"],
            cwd=path,
            env={**os.environ, **env},
        )
        log.info(f"`{file}` started on port {env.get('TRANSCRIPT_PORT')}")

    except Exception as e:
        log.error(f"Failed to start {file}: {e}")


@time_trigger("startup")
def start_transcript_server_on_boot():
    # Home Assistant is still bringing itself up; nothing here is urgent.
    await asyncio.sleep(120)

    __install_requirements(
        requirements_file=f"{__TRANSCRIPT_PATH}/{__REQUIREMENTS_FILE}"
    )

    __start_server(
        path=__TRANSCRIPT_PATH,
        file=__TRANSCRIPT_FILE,
        env={
            "TRANSCRIPT_PORT": __PORT,
            "TRANSCRIPT_API_KEY": __API_KEY,
            "TRANSCRIPT_PROXY": __PROXY,
        },
    )
