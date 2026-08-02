# Miniplayer — Technical Handoff

**Date:** 2026-08-02
**Branch:** `fix/nine-bugs`
**Status:** Implemented, unit tests green (71/71). Manual acceptance pending.

---

## 1. What this is

A player that keeps playing. Navigate away from `/watch`, scroll past the player,
or drag it down on a phone, and the video carries on in a smaller frame; go back
and it resumes in place. Desktop and mobile shells both, modelled on youtube.com
and the YouTube app respectively.

## 2. The rule everything else follows

**The Player is mounted once and its DOM is never re-parented.**

An earlier implementation portal'd the Player between the watch page and a corner
container. Changing a portal's container does not move the DOM: React tears the
subtree out of the old container and builds a fresh one in the new — `<video>`
included. A rebuilt `<video>` has no buffer, no `currentTime` and no play state,
while the old one keeps playing, detached and invisible. That is the whole of the
"sound but no picture" bug, and no amount of animation work could have addressed
it.

So a single host element in `AppShell` holds the Player forever, and only the
host's position and size change.

Two consequences worth stating, because both were bugs before:

- **Refs that matter are callback refs.** The watch page's slot is read to
  position the host. A plain `ref` is assigned during commit, after render, and
  changing it re-renders nothing — so the render that needed the slot read
  `null`. `slotRef` is a callback ref that sets state.
- **Mode is derived, never stored.** `deriveMode(hasState, isWatch, pinnedMini)`.
  The old `flipping` state had to be exited by hand, and an animation that failed
  to announce its own end stranded the player in it.

## 3. Coordinate spaces

| Mode | Position | Where |
|---|---|---|
| desktop full | `absolute` | slot rect in **document** coordinates |
| desktop mini | `fixed` | 400×225, 16px from the bottom-right |
| mobile full | `fixed` | pinned under the top bar, full width, 16:9 |
| mobile mini | `fixed` | full-width 72px bar, resting on the bottom nav |
| dragging | `fixed` | `lerpRect(full, mini, progress)`, transition off |

Desktop full-size is `absolute` in **document** space on purpose: the browser
then scrolls it with the page itself. There is no scroll listener anywhere in
this feature, so there is nothing that can lag a frame behind the page it sits
in — the classic failure of anything `fixed` pretending not to be.

**The bridging frame.** CSS cannot transition across a change of `position`;
going straight from `absolute` to `fixed` makes the element jump with no
animation. So one frame is committed first that is already in the destination
space but shows identical pixels (`bridgePlacement`, `needsBridge`), and only the
frame after that animates.

## 4. What puts it into the miniplayer

- **Leaving `/watch`** — both shells. Nothing is torn down; the route changes and
  the host moves.
- **Scrolling past the player** — desktop only, via `IntersectionObserver` on the
  slot. The browser computes the crossing, so no per-frame work.
- **Dragging down** — mobile only. Axis-locked: sideways is scrubbing, upward is
  nothing, and under 10px is a tap. Commits on 35% of the player's height **or**
  0.5px/ms of velocity, because a short fast flick is how people actually dismiss
  things and distance alone springs all of those back.

### Getting back out of it

Expanding does three things, unconditionally: navigate if not already on
`/watch`, `restore()`, and scroll to the top. Not one of the three is optional.

- Navigating alone was a bug: asking the router for the page it is already on is
  not a navigation, so the button did nothing once you had scrolled down.
- Unpinning alone is worse than that bug — the player returns to a slot that is
  scrolled off screen and keeps playing out of sight.
- Scrolling alone works only on desktop, where the observer notices the slot and
  unpins. Mobile runs no observer at all, so nothing would undo a pin the gesture
  made.

The scroll is **instant, deliberately**. The bridging frame reads
`window.scrollY`; a smooth scroll has it read a number still in motion, so the
player jumps and then animates. Synchronous scrolling leaves a single movement.

### Closing it

On the watch page, ✕ means "put this small window away", not "stop watching" —
the video still has a home on the page. So `dismiss()` returns it to its slot,
**without** scrolling (the viewer is reading further down; moving them answers a
question they did not ask) and **pauses** it, because an unseen player that is
still audible is the very fault this feature exists to remove.

It also sets a dismissal that blocks re-pinning until the slot comes back into
view (`resolvePin`). Without it, scrolling one more line brings straight back what
was just closed, and the button is all but dead. The flag expires on the viewer
looking at the player again rather than on a timer, so it cannot get stuck.

Off the watch page ✕ still destroys the player: there is no slot to go back to.

## 4b. Layering

The player host is **content that outstayed its page**, not a layer over the app,
so navigation chrome sits above it.

| z | What |
|---|---|
| 50 | bottom nav, watch-page drawer |
| 40 | top bar (and therefore search suggestions), drawer scrim, card ⋮ menu |
| **30** | **player host** |
| 20 | sidebar rail |

Two traps this fixes. The host used to be `z-40` like the drawer, and being later
in the DOM it won every tie — the miniplayer covered the drawer it was supposed to
sit beneath, scrim included. And `TopBar` is `sticky` with a z-index, which makes
it a stacking context: the search suggestions inside it are capped at whatever the
header is worth, so they were losing to the player at `z-40` no matter what their
own z-index said.

## 4c. One movement, not two

Two things used to make the trip between corner and frame arrive in stages.

- **The content margin animated on navigation.** Leaving a page with the rail
  showing hides it, and `main` had `transition-[margin]`, so the slot was still
  sliding left while the player was flying towards it. The player chased a moving
  target, which reads as up-then-across. The margin now animates only when the
  viewer actually collapses the rail (`slideMargin`), never on a route change.
- **Full size was answered before the slot was measured.** `placementFor` returned
  the empty rect, sending the player to the top-left at zero size and then out to
  the real slot. It now stays in the corner until there is a measurement, which
  costs a frame and animates once.

## 5. Files

| File | Status | Purpose |
|---|---|---|
| `features/watch/application/player-geometry.ts` | NEW | rects, lerp, gesture rules — pure, no DOM |
| `features/watch/application/player-host.ts` | NEW | placement, mode derivation, bridging — pure |
| `features/watch/application/player-context.tsx` | REWRITTEN | provider: measurement, observers, drag, mode |
| `app/AppShell.tsx` | REWRITTEN | `PlayerHost`, mobile shell wiring |
| `features/watch/ui/Player.tsx` | MODIFIED | `variant: 'full' \| 'mini' \| 'bar'`, drag handlers |
| `pages/WatchPage.tsx` | MODIFIED | slot only, mobile full-bleed, no teardown |
| `features/navigation/ui/BottomNav.tsx` | NEW | mobile navigation, five real routes |

Removed: `FlipPlaceholder`, `createPortal`, `flipFrom`/`flipTo`/`finishFlip`/
`captureSlotRect`, and the `flipping` mode.

## 6. Tests

`cd web && npm test` — 82 passing.

`vitest` was **not installed** before this work despite a test file importing it,
so nothing in `web/` had ever run. `narration.test.ts` (31 tests) had been sitting
unexecuted too; it passes.

The load-bearing test is `player-continuity.test.tsx`: it holds a reference to the
`<video>` node, navigates, and asserts it is **the same object**, still connected.
It was confirmed to fail when a remount is deliberately forced — a test that only
checked "a video exists" would have passed against the broken version, which is
how that version came to be handed over as working.

`player-expand.test.tsx` covers the scroll-to-corner path and the way back. It
relies on the drivable `IntersectionObserver` in `src/test/setup.ts`: jsdom cannot
decide whether anything intersects, so the choice was between a no-op — leaving
the entire scroll-to-miniplayer path untestable, which is exactly the path that
broke — and letting tests say when the crossing happens. Removing `restore()` from
`onExpand` was confirmed to turn it red.

jsdom still cannot test the animation itself: no layout engine, so
`getBoundingClientRect()` returns zeroes. That is why the geometry is pure
functions — tested for real — and the motion is checked by hand. Tests that assert
placement have to let the bridging frame retire first (`settle()`), or they read
the halfway frame and call it the result.

## 7. Manual acceptance

Desktop (≥700px): scroll past the player → folds into the corner with no gap in
the audio · scroll back → returns to place at the same second · **expand while
still on `/watch`** → page jumps to the top and the player animates back into its
frame · leave for Home → mini · click it → back to `/watch` **at the top of the
page**, same second, no reload, and it does not immediately re-pin · **✕ while on
`/watch`** → miniplayer goes, audio stops, page does not move; scroll up and the
player is in its slot, paused · scrolling down again does **not** bring it back
until you have scrolled up to it once · ✕ on any other page closes it outright ·
resizing the sidebar keeps it tracking.

Mobile (390×844): bottom nav shows five items, none dead · sidebar rail gone ·
player pinned under the top bar with the description scrolling beneath · drag down
→ shrinks **with the finger**, not after release · small slow drag springs back,
quick flick commits · bar sits above the nav, not over it · **tapping the bar**
returns it to the pinned position — this is the case that needs `restore()`, since
no observer runs here to undo the pin · ✕ on the bar while on `/watch` returns it
to its slot, paused · dragging on the seek bar still scrubs and does not minimise.

## 8. Known limits

- Returning to `/watch` re-measures the slot on mount; a very slow layout (fonts,
  images above the player) could place the host a frame late. Not observed.
- The mobile bar shows the channel name only when `channelTitle` is supplied by
  the activating page.
