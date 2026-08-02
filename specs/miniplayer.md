# Miniplayer Plan

## Context

When navigating from Watch page to other pages (Home, History, Storage...), the video stops because `Player` lives inside `WatchPage` and gets unmounted by React Router's `<Outlet />`. YouTube keeps the video playing in a miniplayer at the bottom-right corner. This plan implements the same behavior.

## Design Decisions (from grilling session)

| Decision | Choice |
|----------|--------|
| When miniplayer appears | Only when navigating away from `/watch` with a playing video |
| Instance count | Single Player instance, survives route transitions |
| Positioning | Player lives in AppShell, portals into WatchPage slot when on `/watch`, renders as fixed miniplayer otherwise |
| WatchPage refactor | WatchPage exposes a slot ref via context; AppShell passes Player to it |
| Animation | FLIP: capture old rect → animate to miniplayer position |
| Miniplayer controls | Close (X) + Expand (back to Watch). Title visible on hover |
| Close behavior | Stop video, clear player state entirely |
| Click card from mini | Navigate to `/watch/{newVideoId}`, replace player content |
| Mode management | AppShell reads route, passes `mode` prop to Player |
| Miniplayer size | 400×225px, bottom: 16px, right: 16px, z-40 |

## Architecture Overview

```
AppShell
├── PlayerContext.Provider
│   ├── TopBar
│   ├── Sidebar
│   ├── <main>
│   │   ├── <div id="player-slot" />  ← Portal target for full-size mode
│   │   └── <Outlet />                ← WatchPage renders title/desc/comments here
│   │       └── WatchPage
│   │           ├── ← Player portals here when mode='full' (via context ref)
│   │           ├── Title, VideoActions, DescriptionBox, CommentSection
│   │           └── UpNextRail / QueueRail (sidebar)
│   └── <div id="miniplayer-slot" />  ← Portal target for mini mode (fixed bottom-right)
└──
```

## Implementation Steps

### Step 1: Create PlayerContext

**New file**: `web/src/features/watch/application/player-context.ts`

```ts
interface PlayerState {
  videoId: string | null
  hue: number
  durationSeconds: number
  initialPositionSeconds: number
  mediaState: MediaState
  subtitles: SubtitleTrack[]
  thumbnailURL?: string
  nextVideoTitle?: string
  onPlayNext?: () => void
}

interface PlayerContextValue {
  state: PlayerState | null
  slotRef: React.RefObject<HTMLDivElement | null>
  activate: (state: PlayerState) => void
  deactivate: () => void
}
```

- `slotRef` — WatchPage renders `<div ref={slotRef} />` where Player should appear.
  AppShell owns the ref; WatchPage reads it from context.
- `activate(state)` — sets the current video; called by WatchPage on mount or when video changes.
- `deactivate()` — clears state, stops playback.

### Step 2: Refactor AppShell (`web/src/app/AppShell.tsx`)

**Changes**:
1. Wrap children in `PlayerContext.Provider`
2. Create `slotRef` for the WatchPage portal target
3. Create `miniRef` for the miniplayer portal target (always mounted in AppShell)
4. Read `isWatch` from `useLocation()`
5. Determine Player mode:
   - `full`: on `/watch` AND `playerState !== null`
   - `mini`: NOT on `/watch` AND `playerState !== null`
   - `hidden`: `playerState === null`
6. Capture FLIP rect before navigation:
   - When `isWatch` changes from true to false and player is active:
     - Read `miniRef.current.getBoundingClientRect()` (target position)
     - Read `slotRef.current?.getBoundingClientRect()` (source position, may be null if already unmounted)
     - Store source rect in state for FLIP animation
7. Render two portal targets:
   ```tsx
   <main>
     {/* Full-size slot: only used as portal target, invisible itself */}
     <div ref={slotRef} className="contents" />
     <Outlet />
   </main>
   {/* Miniplayer slot: fixed bottom-right */}
   <div ref={miniRef} />
   ```
8. Render Player via portal into the correct target based on mode
9. FLIP animation wrapper: a `<div>` that transitions position/size from old rect to miniplayer position using CSS `transition: all 300ms ease-out`

### Step 3: Add `mini` prop to Player (`web/src/features/watch/ui/Player.tsx`)

**New prop**: `mini?: boolean`

When `mini === true`:
- Remove `aspect-video`, `rounded-xl` — size is controlled by the portal container
- Replace the full controls bar with a minimal overlay:
  - Hover: gradient at bottom showing video title (clamped 1 line)
  - Hover: close button (X icon, top-right corner)
  - Hover: expand button (to go back to `/watch/{videoId}`)
- Click on video area: navigate to `/watch/{videoId}`
- Keep both `<video>` elements and tier management intact
- Keep all state (position, volume, captions, quality, narration) — nothing resets

**Props needed from context**:
- `onClose` — calls `deactivate()` in AppShell
- `onExpand` — navigates to `/watch/{videoId}`

### Step 4: Refactor WatchPage (`web/src/pages/WatchPage.tsx`)

**Changes**:
1. Read `slotRef` and `activate` from `PlayerContext`
2. Remove direct `<Player .../>` rendering
3. Instead: call `activate({ videoId, hue, ... })` in a `useEffect` on mount/change
4. Render `<div ref={slotRef} />` where Player used to be
5. Title, Description, Comments, Sidebar remain unchanged below the slot
6. Call `deactivate()` on unmount IF navigating to a non-watch page (not when going to another `/watch/:id`)

### Step 5: Implement FLIP animation

**New file or logic in AppShell**:

1. On navigation away from `/watch` with active player:
   - Capture `miniRef.current.getBoundingClientRect()` → target rect
   - The source rect is captured from `slotRef.current` BEFORE WatchPage unmounts
   - Actually: capture the slot rect in WatchPage's cleanup effect and pass to context

2. After navigation, render a FLIP container:
   ```tsx
   <div
     style={{
       position: 'fixed',
       top: flipFrom.top,
       left: flipFrom.left,
       width: flipFrom.width,
       height: flipFrom.height,
       transition: 'all 300ms cubic-bezier(0.4, 0, 0.2, 1)',
       zIndex: 40,
     }}
     ref={el => {
       if (el && flipFrom) {
         requestAnimationFrame(() => {
           el.style.top = `${flipTo.top}px`
           el.style.left = `${flipTo.left}px`
           el.style.width = `${flipTo.width}px`
           el.style.height = `${flipTo.height}px`
         })
       }
     }}
     onTransitionEnd={() => setFlipFrom(null)}
   >
     {/* Render Player here during animation */}
   </div>
   ```

   After transition ends, render Player directly in the miniplayer slot (no more FLIP wrapper).

### Step 6: Wire up routing edge cases

1. **Close miniplayer** → `deactivate()`, Player unmounts entirely
2. **Expand miniplayer** → `navigate(/watch/{videoId})`, Player transitions from mini → full
3. **Click another video card while miniplayer active** → `navigate(/watch/{newId})`.
   WatchPage calls `activate(newState)`; miniplayer transitions to full for the new video.
4. **Navigate to another `/watch/:id` directly** (e.g., from sidebar while miniplayer is
   showing for a different video) → Player gets new video state, same as #3.
5. **Browser back/forward** → Player survives because it lives above the router tree.

### Step 7: Miniplayer overlay UI

Inside Player, when `mini === true`:

```tsx
{mini && (
  <div className="absolute inset-0 group/mini">
    {/* Click anywhere → go to Watch */}
    <button onClick={onExpand} className="absolute inset-0" aria-label="Expand player" />
    
    {/* Hover: close button */}
    <button
      onClick={(e) => { e.stopPropagation(); onClose(); }}
      className="absolute top-2 right-2 z-10 p-1.5 rounded-full bg-black/70 
                 opacity-0 group-hover/mini:opacity-100 transition-opacity duration-150"
      aria-label="Close player"
    >
      <X size={18} />
    </button>
    
    {/* Hover: expand button */}
    <button
      onClick={onExpand}
      className="absolute top-2 left-2 z-10 p-1.5 rounded-full bg-black/70 
                 opacity-0 group-hover/mini:opacity-100 transition-opacity duration-150"
      aria-label="Expand player"
    >
      <Expand size={18} />
    </button>
    
    {/* Hover: title bar at bottom */}
    <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/80 to-transparent pt-8 pb-2 px-3
                    opacity-0 group-hover/mini:opacity-100 transition-opacity duration-150">
      <p className="text-xs text-white clamp-1">{title}</p>
    </div>
  </div>
)}
```

## Files Modified

| File | Change |
|------|--------|
| `web/src/features/watch/application/player-context.ts` | **NEW** — PlayerContext with slotRef, activate, deactivate |
| `web/src/app/AppShell.tsx` | Major refactor: add context provider, portal targets, FLIP animation, mode management |
| `web/src/features/watch/ui/Player.tsx` | Add `mini` prop and `onClose`/`onExpand` callbacks; conditional rendering for mini overlay |
| `web/src/pages/WatchPage.tsx` | Remove direct Player rendering; use context to activate/portal |
| `web/src/main.tsx` | No changes needed (routing unchanged) |

## Verification

1. **Basic flow**: Open a video → let it play → click Home in sidebar → video continues in miniplayer bottom-right → click miniplayer → back to Watch full-size
2. **Close**: Open video → go to Home → click X on miniplayer → video stops, miniplayer disappears → clicking another video card opens Watch normally
3. **FLIP animation**: The transition from full to mini should animate smoothly over ~300ms
4. **Control persistence**: Volume, mute, captions, quality choice survive the full→mini→full roundtrip
5. **Seek position**: Current playback position is maintained throughout
6. **Download continues**: If a download was in progress, it continues while in miniplayer mode
7. **Responsive**: At breakpoints <700px, miniplayer should scale down or move to avoid covering content
8. **Keyboard**: Space/arrows still work on miniplayer when it has focus
9. **Reduced motion**: When `prefers-reduced-motion: reduce`, FLIP animation is skipped (instant transition)
