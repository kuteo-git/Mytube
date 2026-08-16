# YouTube accounts: cookie login, per household member

## Context

The library's material comes from `topics.yaml` (6 sources) and whatever channels
have been subscribed to inside the app (87). Everything the household actually
follows on YouTube — real subscriptions, playlists, liked videos, watch later —
is invisible to it, and re-entering it by hand is why `topics.yaml` has 6 lines.

The ask: paste a YouTube account's cookies, and let the scanner read that
account's own feeds. Then the same for each family member, each with their own
account, so their subscriptions and likes land under *their* user.

This also replaces something just removed. `ExpandLibrary`'s search-by-topic-name
layer was deleted last week after it imported 621 channels nobody chose; an
account's own subscription feed is the opposite kind of source — everything in it
was chosen by a person.

**What already exists and must be reused:**

- `services/ingest/internal/adapter/ytdlp/session.go` — cookie plumbing is
  already built: `YTDLP_COOKIES` points at a Netscape file, `newCommand(purpose)`
  attaches it. Its `purpose` split is the key existing decision (see Risk 1).
- `catalog.subscriptions` is already `(user_id, channel_id)`. `catalog.reactions`
  and `catalog.watch_later` are already per-user. Imported data lands in tables
  that already exist — no new per-user schema.
- `catalog.videos.discovered_via` (migration 0012) already records provenance.
- Gateway settings pattern: `services/gateway/internal/api/feed_mix.go` —
  JSON under `configDir`, unreadable file falls back to defaults rather than
  erroring.
- Settings UI pattern: `SettingsSection` (+ `headless` for the phone's own
  screen), `SettingsSectionPage.tsx`, and the `PHONE_PREFS` list in
  `SettingsPage.tsx:96`.

**Confirmed available** in yt-dlp 2026.07.04 (`--extractor-descriptions`):
`:ytsubs`, `:ytfav`, `:ytwatchlater`, `:ythis`, `:ytnotif` (all "requires
cookies"), `:ytrec`, and `youtube:tab` for a channel's playlists.

## Risks, and what the design does about them

**1. This contradicts a deliberate existing rule.** `session.go` says, in the
code: *"The scanner alone walks 93 sources every hour. Attaching an account to
that traffic is the fastest way to lose the account, so it is deliberately not
done."* Subscriptions and playlists are listings, so the rule as written forbids
exactly this.

The rule is right about *volume*, not about *listings*. Reading one account's
own subscription feed once an hour is what a logged-in browser does anyway. So
the rule is refined rather than broken: a third `purposeAccount` carries cookies;
`purposeListing` — the 93-source walk — still carries none. **The anonymous
scanner must never be given an account.**

**2. A cookies.txt is full Google account access**, not YouTube access. Anyone
who reads that file is signed in as that person, everywhere. And this stores one
per family member.

- Files live in `configDir/cookies/`, mode `0600`, directory `0700`.
- **Write-only across the API.** No endpoint ever returns cookie content — the
  status endpoint reports state and last-scan time only. There is no read path
  to abuse and none to accidentally log.
- Never logged, never in an error message, never in a scan record.

**3. The paste crosses the LAN.** §3 says media URLs are unprotected because the
LAN is trusted — but "trusted with video" and "trusted with a Google session"
are different. The paste endpoint **refuses plaintext HTTP** and is served only
through the Caddy TLS front §2 already requires for the TV.

**4. Automated access with an account risks the account, not just the address.**
§8's risk 6 is about IP blocks; this raises it to bans. Hence one pass per hour
for all accounts, a hard cap on requests per pass, and an account that errors
twice in a row is marked expired and **stops being used** until re-pasted.

## Design

### First: the app cannot tell anybody apart yet

This has to come before the cookies, because everything below depends on it and
it does not exist.

`gateway.userID(r)` reads `X-User-Id` and falls back to `DEV_USER_ID`
(`main.go:63`, currently `u_luc`). **The web app has never sent that header.**
So every browser in the house is `u_luc`, every subscription is `u_luc`'s, and
`recsys.signals` holds exactly one user id across 39,583 rows. There is no
`identity` service — §3 lists one, and the build status quietly does not.

Without fixing that, "A's cookies on machine A" cannot mean anything: A's
subscriptions would import into the same account as B's.

**A profile picker, not a login.** §2 says 2–5 people in one household and no
public sign-up; §3 says media URLs are unprotected because the LAN is trusted.
Passwords would be a second trust model bolted beside that one, protecting a
library whose video files anyone on the LAN can already fetch by URL.

- First load with no profile chosen → a picker: household members, plus "Add
  someone".
- The choice is written to `localStorage` and sent as `X-User-Id` on **every**
  request, in the one place `ui/` is allowed to touch HTTP — the repository
  layer (§5: `ui/` never calls `fetch`).
- Switching profile is a menu item, not a logout. There is no session to end.
- `DEV_USER_ID` stays as the fallback for a browser that has not chosen, so
  nothing that works today stops working.

**The device is not the identity.** Machine A can be used by B; A and B can both
open the app on the TV. The profile is the answer to "who is watching", and it
travels with the browser, not the hardware.

**Stated plainly because it is a real limit:** with no password, anything on the
LAN can claim to be anyone by setting a header. That is the same trust the media
routes already make. It is a household, not a public service — but it means
"user A's data" is a separation of *convenience*, not of *security*, and the
cookie files are the one thing in the system where that distinction bites. They
are protected by file mode on the server, never by who claims to be whom.

### The flow, end to end

1. **Machine A**, first open → picker → "Luc" → `localStorage: u_luc`.
2. Settings → **YouTube account** → paste cookie A → validated → written to
   `cookies/u_luc.txt` (0600). Nothing else on the machine changes.
3. The hourly account pass runs for **every** account it has cookies for, on the
   server, regardless of who is browsing. A's subscriptions upsert into
   `catalog.subscriptions` under `u_luc`; A's likes into `catalog.reactions`
   under `u_luc`.
4. **Machine B** → picker → "Vợ" → `u_vo` → pastes cookie B → `cookies/u_vo.txt`.
   Same pass, different rows.
5. Home on machine A ranks against `u_luc`'s profile — subscriptions, likes,
   watch history, channel rotation. Machine B gets `u_vo`'s. Both read the same
   library of videos.

**Shared vs private, decided once:**

| | |
|---|---|
| Shared | the video library, files on disk, the feed mix (§6: "one mix for the household") |
| Per user | subscriptions, likes, watch later, watch history, feed ranking, cookies |

The tables for all of the per-user half already exist and are already keyed by
`user_id`. That is what makes this small: nothing new is stored per user except
the cookie file itself.

### Storage — ingest owns it

Ingest is the only thing that runs yt-dlp, so it owns the cookies; the gateway
stays the only REST speaker and proxies. This keeps §3's boundary intact.

```
<configDir>/cookies/<userID>.txt      0600, Netscape format
<configDir>/youtube-accounts.json     registry: label, addedAt, lastScanAt,
                                      lastResult, state
```

The registry holds no secrets — it is metadata about files. State is
`OK | EXPIRED | NEVER_SET`.

### Validation on paste

Rejected before anything is written:
- first line must be `# HTTP Cookie File` or `# Netscape HTTP Cookie File`
  (yt-dlp's own requirement)
- must contain at least one `.youtube.com` entry
- must parse as tab-separated Netscape rows

A file that fails validation is never written, so a bad paste cannot take down
the pass that was working — `session.go` already learned this for the missing-file
case ("a cookies file that is not there is worse than none").

### What gets imported, and where it lands

Per account, per pass, in this order:

| Source | Lands in | Provenance |
|---|---|---|
| `:ytsubs` | `catalog.subscriptions` for that user, videos into the library | `SOURCE` |
| account's playlists (`youtube:tab`) | videos into the library | `SOURCE` |
| `:ytfav` | `catalog.reactions` LIKE for that user | `SOURCE` |
| `:ytwatchlater` | `catalog.watch_later` for that user | `SOURCE` |
| `:ytrec` | videos only | `YOUTUBE_REC` |

`:ythis` (watch history) is **not** imported: it is the most personal of these
and the catalogue already keeps its own history from actual playback here.

**`:ytrec` is deliberately fenced.** §6's whole claim is that every score can be
explained; YouTube's ordering cannot be, so it is imported as *material* and
never as ranking. Tagged `YOUTUBE_REC`, it is allowed in the `discovery` bucket
only — the same rule `RELATED` already gets in `explain.go`. Requires extending
the `discovered_via` CHECK.

Subscriptions are per-user by nature: importing A's subscriptions must not
subscribe B. The videos are shared (that is what a household library is); the
relationships are not.

### Scanning

One pass an hour, all accounts together, **separate from the anonymous scanner**
so it can be stopped on its own the day an account looks watched. Bounded like
every other pass here (§8): a request cap per account per pass, serial with a
delay between accounts, and it stops on the first authentication failure for
that account rather than working through the list.

`ACCOUNT_SCAN_INTERVAL` (1h, zero disables), matching the existing scanner.

### Expiry

Cookies expire; this is when, not if. Two consecutive authentication failures
mark the account `EXPIRED`, and an expired account is skipped entirely — a dead
cookie must not be replayed hourly into a ban.

The web app polls account status and shows a **top banner** when the current
user's account is expired, with a button to the paste screen. Banner lives beside
`TopBar` in `AppShell.tsx`.

### The paste screen

Route `/settings/youtube-account`, added to `PHONE_PREFS` and to the desktop
Settings page, built from `SettingsSection` like every other panel.

Copy names **[Get cookies.txt LOCALLY](https://github.com/kairi003/Get-cookies.txt-LOCALLY)** — the extension
yt-dlp's own FAQ recommends — and carries the warning that the similarly named
**"Get cookies.txt"** (without LOCALLY) was reported as malware and pulled from
the Chrome Web Store. Anyone following these instructions is about to hand an
extension their Google session; naming the wrong one is the worst outcome this
screen can produce.

Shows: current state, when it last scanned, what the last pass found. Never the
cookies.

## Files

**New**
- `web/src/features/identity/` — the profile picker, feature-sliced like the
  rest: `domain/profile.ts`, `application/use-profile.ts`,
  `infrastructure/profile-storage.ts`, `ui/ProfilePicker.tsx`. The header is
  attached in the shared fetch wrapper the repositories already go through, so
  no `ui/` file learns about HTTP.
- `services/gateway/internal/api/profiles.go` — the household list
  (`data/profiles.json`, same pattern as `feed_mix.go`)
- `services/ingest/internal/domain/account.go` — `Account`, `AccountStore` port
- `services/ingest/internal/adapter/accountfile/store.go` — the file store
- `services/ingest/internal/usecase/accounts.go` — the pass
- `services/catalog/migrations/0013_discovered_via_youtube_rec.sql`
- `web/src/features/settings/ui/YouTubeAccountSettings.tsx`
- `web/src/features/settings/ui/CookieExpiryBanner.tsx`

**Changed**
- `session.go` — `purposeAccount`, and `newCommand` takes the cookie path per
  call rather than reading one global
- `proto/ingest/v1/ingest.proto` — `SetAccountCookies`, `ListAccounts`,
  `RemoveAccount`, `ScanAccounts`
- `services/ingest/internal/usecase/scanner.go` — the account pass alongside
  the existing one
- `services/gateway/internal/api/router.go` — `/api/settings/youtube-account`
  (GET/PUT/DELETE), plaintext refused
- `services/recsys/internal/usecase/explain.go` — `YOUTUBE_REC` joins `RELATED`
  in the discovery-only rule
- `web/src/pages/SettingsPage.tsx`, `SettingsSectionPage.tsx`, `app/routes.tsx`,
  `app/AppShell.tsx`
- `CLAUDE.md` — §3 (accounts), §5 (the banner), §8 (the ban risk)

## Verification

0. **The identity step, first and on its own.** Pick a profile on one browser and
   a different one on a second; confirm `/api/feed` returns different orderings,
   that subscribing on one does not appear on the other, and that a browser which
   has chosen nothing still behaves exactly as it does today. None of the cookie
   work is worth testing until two browsers can be told apart.
1. `make check` and `go test ./...`, `npm test` in `web/`.
2. Unit: Netscape validation accepts a real export and rejects a JSON one, a
   truncated one, and one with no `.youtube.com` row. Cookie content never
   appears in any RPC response or log line — assert on the marshalled response.
3. Unit: `newCommand(purposeListing)` attaches **no** cookies even when an
   account exists. This is the rule that protects the account, so it is the test
   that must not be deleted.
4. Unit: an account failing authentication twice is marked `EXPIRED` and is
   skipped by the next pass.
5. Live, with a real export from `Get cookies.txt LOCALLY`: paste it, run
   `ScanAccounts` manually, and confirm `catalog.subscriptions` for `u_luc` grows
   to match the real account, that liked videos appear in `catalog.reactions`,
   and that `:ytrec` imports land with `discovered_via = 'YOUTUBE_REC'` and
   appear only in the discovery bucket via `/api/feed/explain`.
6. `ls -l` the cookie directory: `0700`, files `0600`.
7. Break it on purpose: corrupt a cookie file, confirm the account goes
   `EXPIRED`, the banner appears, and the anonymous scanner keeps working
   untouched.
