# Using it

What each screen is for, and the handful of behaviours that are deliberate but
surprising.

**Vietnamese: [USAGE.vi.md](USAGE.vi.md)**

---

## Getting videos in

**Paste a link.** A YouTube URL in the search box is fetched, not searched. The
catalogue is asked by id first, so a video already in the library opens without
touching the network.

**Search.** Results come back in two groups — *In your library* and *On
YouTube*. Anything from the second group is fetched when you open it.

**Follow a channel.** Subscribe on a channel page and it becomes a live source:
its uploads arrive on their own, and a fast pass checks followed channels every
five minutes for anything published in the last two days.

**Curate `topics.yaml`.** The file at the repository root is the primary source
list, read hourly. This is the file to edit if you want the feed to be about
something in particular.

---

## Playing

Press play and the video starts before it has downloaded. What you are watching
in those first seconds is YouTube's own adaptive tracks, described as a playlist
your browser assembles. When the file lands — a median of **thirteen seconds**
across this deployment's downloads — the player switches to the local copy
without interrupting you.

The label at the top left says which one you are on.

| Control | Note |
|---|---|
| Quality | Auto keeps 720p while streaming. Pinning 1080p is an order, and it stays. |
| Subtitles | Whatever the video published, plus a Vietnamese translation if you turn it on. |
| Read aloud | Reads the Vietnamese subtitles over the video. Needs a speech endpoint — see [SETUP](SETUP.md#7-optional-narration). |
| Sound | Ten-band equaliser and four rooms. Per device, because it corrects for *your* speakers. |
| Autoplay | Stops after three videos nobody touched. |

**Seeking works on every tier.** On a live broadcast the bar covers the rewind
window YouTube publishes — about an hour — and the **LIVE** button returns you to
the edge.

### Things that look like faults and are not

- **Autoplay does not start on iOS.** Safari wants a gesture before it will make
  noise. The first frame sits still rather than the video playing muted.
- **The equaliser does nothing on a phone while a video is still downloading.**
  iOS does not route HLS audio through Web Audio by any route — measured, with
  and without a library. The panel says so while it lasts, and it stops the
  moment the local file takes over.
- **A video says "not downloaded" and plays anyway.** That is the point: the
  copy is for later.

---

## The home feed

Ranked, not chronological, and every part of the ranking can be explained.

**Settings → Home feed** divides most of the page between three shares:

| Share | Means |
|---|---|
| Channels you follow | subscribed |
| More of what you watch | not subscribed, on subjects you return to |
| Something new | outside your usual subjects |

The rest is fixed: videos you are part way through, ones you finished and might
want again, and new uploads from channels you follow. None of those three is a
taste, so none of them is yours to divide.

**A share set to zero is gone, not moved.** Those videos leave the home page
entirely. Search and the channel page still find them.

**A feed can run out**, and when it does it says so and names the setting that
decided it. An empty home page here is an ordinary outcome rather than a fault.

**Chips.** *All* leads, then *Live* when anything you follow is broadcasting,
then the topics that actually have videos in them.

---

## The screens

| Screen | For |
|---|---|
| **Home** | the ranked feed |
| **Subscriptions** | channels you follow; tap one for its page |
| **History** | what you have watched, and how far |
| **Saved** | videos you keep — pinned against the eviction sweep |
| **Watch later** / **Playlists** | a **read-only** copy of your YouTube account |
| **Storage** | disk in use, what is next to be removed, where the library lives |
| **Activity** | the download queue and the scan history |
| **Settings** | feed, translation, narration, account, advanced |

**Watch later and Playlists cannot be edited here.** They are refreshed from
your account on every scan, so anything changed here would be undone by the next
pass. Saved is this library's own list and is yours to change.

**Save and Watch later are different intentions.** Watch later is a note about
what to do next and clears itself once watched. Save keeps the *file* on disk
against the sweep and never clears itself.

---

## Storage and eviction

There is a budget. Above the high-water mark, the **file** of the least recently
watched unpinned video is deleted — its metadata, thumbnail and your history
stay, and the card says *Removed — press to fetch again*.

Saving a video exempts it. Anybody in the house saving it exempts it for
everybody, because the disk is one disk.

**Stream only, keep nothing** (Storage) stops pressing play from scheduling a
download. Subtitles still arrive, files already on disk still play, Retry still
works, and the sweep still runs — somebody may well turn this on *because* the
disk is full.

---

## Several people

Add profiles from the avatar menu. Subscriptions, history, recommendations,
reactions, Watch later and playlists are per person; the video library is the
household's.

This is **convenience, not security**. Anything on the LAN can claim to be
anyone by setting a header, and media URLs are unprotected. The one thing that
is actually protected is the cookie files, by file permissions.

Deleting a profile removes that person's side of the library and nothing else —
the videos and channels stay, because they belong to everybody. The confirmation
shows real counts from the same query that then does the deleting.

---

## Language

The avatar menu switches between **English** and **Tiếng Việt**. It applies
immediately, is remembered per device, and each language is named in its own
words so a wrong press is recoverable.

---

## When something is wrong

**http://localhost:8184** is every service's log on one page, with a live tail
and a filter for errors only. It is a separate process on purpose: a log viewer
inside the gateway goes down with the thing it is there to explain.

`GET /api/feed/explain` returns every video's score component by component, its
slot, its position, and for excluded videos which rule dropped it. Tune the feed
with that rather than by eye.
