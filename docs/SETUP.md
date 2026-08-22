# Setup

From an empty machine to a working library. Every command here has been run;
the database section in particular was verified by building the whole schema
from an empty database and counting what came out.

If you would rather have an AI assistant do this for you, hand it this file and
say *"follow docs/SETUP.md step by step, run the checks, and stop at the first
one that fails"*. Each step ends in something to verify, which is what makes
that work.

**Vietnamese: [SETUP.vi.md](SETUP.vi.md)**

---

## What you are installing

Six processes. Four are Go services, one is a Vite dev server, and two are
optional Python sidecars for translation and speech.

| Port | Process | Needed for |
|------|---------|------------|
| 5173 | Vite (web) | the app in a browser |
| 8180 | gateway | **the only port the browser talks to** |
| 8181 | catalog | videos, channels, history |
| 8182 | recsys | the home feed ranking |
| 8183 | ingest | yt-dlp: searching, downloading |
| 8184 | logview | every service's log on one page |
| 8005 | translate | subtitle translation — optional |
| 8002 | speech | reading subtitles aloud — optional |

The library works without 8005 and 8002. You lose translation and narration,
nothing else.

---

## 1. Tools

macOS with Homebrew is what this has been run on. Linux works; the paths below
change.

```bash
brew install go node postgresql@17 ffmpeg
brew services start postgresql@17
```

`yt-dlp` is pinned deliberately, not tracked — a nightly that upgrades itself is
a stack that breaks on a morning nobody changed anything:

```bash
brew install pipx && pipx ensurepath
pipx install "yt-dlp==2026.8.19"
```

### Check

```bash
go version        # go1.26 or later
node -v           # v22 or later
psql --version    # 17.x
ffmpeg -version   # 8.x
yt-dlp --version  # 2026.08.19
```

If `psql` is not found, Homebrew keeps it off the PATH:

```bash
export PATH="/opt/homebrew/opt/postgresql@17/bin:$PATH"
```

---

## 2. Database

One Postgres instance, one database, and **one schema and one role per
service**. The boundary between services is enforced by database permissions
rather than by convention: `catalog_svc` physically cannot read ingest's tables.

```bash
psql -d postgres -f db/bootstrap.sql
```

That creates the database `localyoutube`, four roles, four schemas, and the
`unaccent` extension.

> **The database must be called `localyoutube`.** `bootstrap.sql` names it, and
> so do the connection strings the services default to. Changing it means
> setting `CATALOG_DATABASE_URL`, `RECSYS_DATABASE_URL` and
> `INGEST_DATABASE_URL` yourself.

Then every migration, in order, each as its own service's role:

```bash
for f in services/catalog/migrations/0*.sql; do
  PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -v ON_ERROR_STOP=1 -q -f "$f"
done
for f in services/recsys/migrations/0*.sql; do
  PGPASSWORD=recsys_dev  psql -h localhost -U recsys_svc  -d localyoutube -v ON_ERROR_STOP=1 -q -f "$f"
done
for f in services/ingest/migrations/0*.sql; do
  PGPASSWORD=ingest_dev  psql -h localhost -U ingest_svc  -d localyoutube -v ON_ERROR_STOP=1 -q -f "$f"
done
```

There is no migration runner. They are plain files applied by hand, they are
**not re-runnable**, and `ON_ERROR_STOP=1` is what stops a failure halfway
through from looking like success.

### Check

```bash
psql -d localyoutube -tAc \
  "select count(*) from information_schema.tables
   where table_schema in ('catalog','ingest','recsys')"
```

**17.** Anything less means a migration failed; scroll back for the first error
rather than the last.

---

## 3. Where the library lives

Downloaded videos go to one directory. It can be an external drive — this
deployment uses one — and it does not have to exist before you start, but it
does have to exist before anything downloads.

```bash
mkdir -p ~/Videos/local-youtube
```

The path is a **setting**, not just an environment variable: the Storage page
writes `data/storage.json`, and **that file wins over the environment**. Set the
environment for the first run and change it in the app afterwards.

```bash
export MEDIA_ROOT="$HOME/Videos/local-youtube"
```

`scripts/dev.sh` exports its own default (`/Volumes/Data2/Youtube`), so either
edit that line or set the variable before running it.

---

## 4. Run it

```bash
./scripts/dev.sh
```

It builds the four Go services, starts them, starts Vite, and prints one line
per port saying `up` or `DOWN`. It refuses to start on a port something else is
already holding, rather than half-starting.

### Check

Open **http://localhost:5173**. You should get an empty home page — empty is
correct, nothing has been ingested yet.

```bash
curl -s localhost:8180/api/feed | head -c 60      # {"videos":[], ...}
```

Logs are at `$TMPDIR/local-youtube/`, and all of them on one page at
**http://localhost:8184**.

---

## 5. Get some videos in

Two ways in, and they answer different questions.

**One video, right now** — paste a YouTube URL into the search box. Pasted links
are fetched rather than searched.

**A library that fills itself** — `topics.yaml` at the repository root is the
curated source list. It ships with six sources; every entry is a channel or
playlist URL ending in `/videos`:

```yaml
sources:
  - https://www.youtube.com/@mkbhd/videos
```

The scanner reads it hourly. To scan immediately, use **Activity → Scan now**.

> Scanning uses flat listings, which is the cheap kind of request. Downloads
> only start when you press play on something.

---

## 6. Optional: translation

Reads English subtitles and writes Vietnamese ones. It needs somewhere to send
the text — anything that speaks OpenAI's **chat completions** API: OpenAI
itself, OpenRouter, a local runner, anything that copies the shape.

The sidecar needs two packages and nothing else. The virtualenv is named
`.venv-nllb` for historical reasons; there is no model to download and nothing
heavy to install:

```bash
python3 -m venv .venv-nllb
./.venv-nllb/bin/pip install fastapi uvicorn
```

Then in the app: **Settings → Translation**, fill in the base URL, the API key
and a model, and press **Test**. It translates one fixed sentence so that
testing one model against another compares like with like.

The `/v1` in the base URL is optional — both forms work.

---

## 7. Optional: narration

Reads the Vietnamese subtitles aloud over the video.

The app speaks **OpenAI's audio API** and nothing else, so the endpoint is a URL
you choose. Three things will work:

- **OpenAI** — base URL `https://api.openai.com/v1`, a key, model
  `gpt-4o-mini-tts`, and one of their voice names.
- **Any OpenAI-compatible speech service** you already run.
- **VieNeu-TTS**, which is what this deployment uses for Vietnamese. It is a
  separate project and is **not in this repository**; `scripts/dev.sh` starts it
  from `$TTS_SERVER` if that file exists and says so if it does not.

Then: **Settings → Narration**, fill in the URL, and press **Test** — it returns
the clip so you can hear it, because an endpoint can answer 200 and perfectly
formed silence.

Until a URL is set the narration switch in the player is disabled and says
where to go. That is deliberate: a switch that turns on and produces nothing
sends you looking at the volume, the subtitles and the video before you think of
an empty text field.

---

## 8. Optional: your YouTube account

Brings your own subscriptions, playlists and liked videos into the library.

**Settings → YouTube account** names the extension to export cookies with and
warns about the one with a similar name that was pulled from the Chrome Web
Store as malware. Read that screen rather than this paragraph — it is the one
place where getting it wrong hands somebody a live Google session.

Cookies are stored as files with mode 0600 and are never returned by any route.

---

## When something does not work

| What you see | Where to look |
|---|---|
| A port says `DOWN` | `$TMPDIR/local-youtube/<service>.log`, or http://localhost:8184 |
| `no schema has been selected` | migrations ran before `db/bootstrap.sql` |
| `permission denied to create extension` | `db/bootstrap.sql` was not run as a superuser |
| Home is empty after a scan | Activity shows what the scan found. `topics.yaml` sources must end in `/videos` |
| A video will not play | Activity shows the download; §4 of [CLAUDE.md](../CLAUDE.md) explains the tiers |
| Narration switch is greyed out | no speech URL set — Settings → Narration |
| `narration will be silent` at startup | the speech server is a separate project; see §7 |

**Nothing here is fatal to the library.** Translation, narration and the account
import are each one feature; the library is the app.
