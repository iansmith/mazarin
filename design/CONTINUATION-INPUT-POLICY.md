# Continuation Prompt: Input Policy and Window Occlusion

## What Was Done

### Phase 2: Shared-Memory Completion Ring for HID Input (committed)

The kernel IRQ top-half writes HID events directly to a shared completion ring
page owned by rachel (the window manager). Rachel drains the ring when woken
via mailbox notification. The three legacy input goroutines (`keyboardLoop`,
`mouseClickLoop`, `mouseMovementLoop`) are gone. Event classification happens
in userspace in `processInputEvent`.

**Key commits on `fix/rawsyscall-experiment`:**
- `71371bc` — WIP: Shared-memory completion ring for HID input events (Phase 2)
- `46e8173` — Remove legacy input infrastructure: rachel is sole input consumer

### Dead Code Removal (committed in `46e8173`)

Removed 826 lines of legacy input infrastructure:
- `WaitInputEvent` + `SetInputFocus` syscalls and dispatch table slots (45-46)
- Per-shepherd kernel input queues (`inputQueues[32][3]`), `BlockOnInputQueue`, `DrainInputQueue`
- `mazarin/input` gutted — only `KeyEvent`, `Keymap`, `KeyName` remain
- `mazarin/timer` deleted entirely (zero importers)
- `SetTimerDeadline` userspace wrapper removed
- `InputClassCount` constant removed
- `SysSetInputFocus`/`SysWaitInputEvent` constants removed from `shared/mazzy/mazzy.go`

### Input Policy Design Document (committed)

`design/INPUT-POLICY.md` describes the two-level dispatch architecture:

**Level 1 — Rachel (WM-level routing):**
Rachel maintains AppWindow bounds, z-order, and focus state. Her policy chain:
1. WM Hotkey Policy (consumes system keys like F1-to-raise)
2. Focus Change Policy (pick against z-ordered bounds, send YouLostFocus/YouHaveFocus)
3. Forward Policy (pass-through to focused shepherd via ring buffer IPC)

Rachel can consume events herself (e.g., F1 to cycle windows).

**Level 2 — Inside the shepherd (subArctic-style):**
Full three-layer architecture: monitor/focus/positional policies, dispatch
agents with FSMs, input protocol interfaces, explicit pick lists. Based on
the subArctic input model (Hudson, Mankoff, Smith, CHI 2005) and Artkit
(Henry, Hudson, Newell, UIST 1990).

### Current Rachel State

Rachel currently:
- Claims WM role via `RequestWindowManager`
- Receives all HID events via shared completion ring (508 slots, one 4KB page)
- Tracks AppWindow bounds via constraint mirroring (`trackAppBounds`)
- Has a `trackedApps` map (SID -> `trackedApp{sid, bounds, returnRb}`)
- Has a `focusedSID` variable (focus always goes to most recent `MsgAppStart` sender)
- Forwards keyboard events via `forwardKeyboardEvent` (KeyPressMsg/KeyReleaseMsg)
- Forwards mouse events via `forwardMouseEvent` (MousePress/Release/Move)
- Does cursor inversion when hovering over app bounds (`pointInAnyAppBounds`)

Rachel does NOT yet have:
- Z-order tracking
- Focus-change-on-click (clicking unfocused window should change focus)
- WM hotkey handling (F1 to cycle, etc.)
- Occlusion/clip rectangle computation

## What Needs to Be Done Next

### 1. Z-Order Tracking

Rachel needs an ordered slice of SIDs representing the window stack,
front-to-back. Currently `trackedApps` is an unordered map.

**Data structure:**
```go
var zOrder []int // front-to-back; zOrder[0] is the topmost window
```

**Operations:**
- `raiseToFront(sid)` — remove from current position, prepend
- `pickWindow(x, y)` — iterate front-to-back, return first SID whose bounds contain (x,y), or -1
- On `MsgAppStart`: append to front of z-order, grant focus
- On shepherd death: remove from z-order; if it was focused, focus new front (or -1)

### 2. Focus Change on Click

When a mouse press lands on a window that isn't currently focused:
1. `pickWindow(mouseX, mouseY)` to find which window was clicked
2. If the picked window != `focusedSID`:
   - Send `YouLostFocus` to old `focusedSID`
   - `raiseToFront(pickedSID)`
   - Send `YouHaveFocus` to picked window
   - Update `focusedSID`
3. Forward the mouse press to the (now focused) window

If the click lands on empty space (no window hit), optionally clear focus.

### 3. WM Hotkey Handling

Before forwarding keyboard events, rachel checks for WM-consumed hotkeys.
Start simple:
- **F1**: Cycle focus to next window in z-order (rotate front to back)
- Possibly later: F2 for close, Alt-Tab for switcher, etc.

If a hotkey matches, rachel handles it and does NOT forward the key event.

### 4. Occlusion Clip Rectangles (the hard part)

**The problem:** All shepherds draw directly to the shared framebuffer.
There is no compositing step. If window B is behind window A, B must not
paint pixels that A covers. Without clipping, B would overwrite A's content
every draw pass.

**Rachel must compute, for each window, a set of "exclusion rectangles"**
— the regions of that window's bounds that are occluded by windows above it
in z-order. The window must skip drawing in these areas.

#### How Occlusion Works

For each tracked window, rachel computes:
```
exclusionRects[sid] = union of (myBounds ∩ aboveBounds) for each window above me in z-order
```

The topmost window has no exclusion rects (nothing above it). A window at
the bottom of the stack may have many.

This is the classic **visible region** problem from framebuffer-based window
systems (original Mac Toolbox, Windows 3.1 GDI). The usual approach is to
decompose the visible area into a set of non-overlapping rectangles (a "clip
list"). The window's draw pass only paints within those rectangles.

#### How to Communicate Clip Rects to Shepherds

Option A: **Shared attribute** — Rachel publishes a list of exclusion rects
as a constraint attribute per shepherd. The shepherd's draw loop reads this
before each paint pass and clips accordingly. This integrates with the
existing constraint/damage system.

Option B: **WM message** — Rachel sends a new message type
(`MsgUpdateClipRects`) containing the rect list. The shepherd stores it and
uses it during draw. Simpler than constraints but doesn't participate in
damage tracking.

Option C: **Shared page** — Rachel writes clip rects to a shared memory
page per shepherd (like the completion ring pattern). Zero-copy, no
serialization, but requires a new shared page allocation.

#### How AppWindow Enforces Clipping

The `AppWindow.Draw` method (in `mazarin/mancini/std/app_window.go`) already
does clipping via `WithClip` for child overflow. The occlusion clipping would
wrap the entire `Draw` call:

For each exclusion rect, the draw pass must avoid painting those pixels.
The existing `ClippedContext` pattern (save pixels before draw, restore
after) could be extended, but the save/restore approach doesn't scale well
to many exclusion rects.

A better approach: compute the **visible region** as a set of
non-overlapping rectangles, and only draw within those rectangles. The
interactor tree walk would be called once per visible rect, with the
DrawContext clipped to that rect. This is how classic window systems work.

Alternatively, since we have the constraint/damage system, we could:
1. Rachel computes exclusion rects and publishes them
2. Before each draw pass, AppWindow reads its exclusion rects
3. AppWindow saves the framebuffer contents of each exclusion rect
4. Normal draw proceeds (may overwrite occluded areas)
5. AppWindow restores saved content for each exclusion rect

This is simpler to implement (no per-rect tree walk) but costs memory for
the save buffers and does redundant painting in occluded areas.

#### Recomputation Triggers

Exclusion rects change when:
- A window moves (its bounds constraint changes)
- A window is raised/lowered (z-order changes)
- A window appears or disappears

Rachel can detect all of these:
- Bounds changes come through the constraint system (tracked via `trackAppBounds`)
- Z-order changes happen inside rachel
- App start/death happen via mailbox messages

When exclusion rects change, the affected window needs a full repaint of
the newly-exposed area. This is a damage rect that rachel computes and
communicates.

### 5. Coordinate Translation

Mouse coordinates in WM messages are currently screen coordinates. Shepherds
need to translate to window-local:
```go
localX := screenX - appWindowX
localY := screenY - appWindowY
```

Rachel could do this translation before forwarding (she knows the bounds),
or the shepherd can do it (it knows its own origin). The shepherd already
has access to its AppWindow's X,Y via constraints.

Currently rachel forwards raw screen coordinates. This should be decided
and made consistent.

## Key Files

| File | Role |
|------|------|
| `flock/cmd/rachel/main.go` | Window manager — all WM logic lives here |
| `mazarin/mancini/std/app_window.go` | AppWindow interactor — draw + clip |
| `mazarin/mancini/draw_context.go` | ClippedContext for overflow clipping |
| `mazarin/mancini/damage.go` | Damage rect computation via constraints |
| `mazarin/mancini/framebuffer.go` | Framebuffer mapping + flush |
| `shared/wm/message.go` | WM message types (may need new MsgUpdateClipRects) |
| `design/INPUT-POLICY.md` | Input policy architecture document |

## Implementation Order

1. **Z-order slice + raiseToFront + pickWindow** in rachel. Wire into
   existing MsgAppStart handler. This is pure rachel-side, no shepherd
   changes needed. Test: multiple apps should track correctly.

2. **Focus change on click.** In `processInputEvent`, before forwarding a
   mouse press, call `pickWindow`. If hit != focused, do the focus change
   dance. Test: click on unfocused window should switch focus.

3. **WM hotkey interception.** In `processInputEvent`, check keyboard events
   against hotkey table before forwarding. Test: F1 should cycle windows.

4. **Occlusion clip rectangles.** This is the big one. Start with the
   simplest viable approach:
   a. Rachel computes exclusion rects per window when z-order or bounds change
   b. Communicate to shepherds (decide mechanism)
   c. AppWindow enforces clipping during draw
   d. Damage tracking for newly-exposed/newly-hidden areas

   The save/restore approach (option 5 above) is the least invasive to the
   existing draw pipeline and should be tried first.

5. **Coordinate translation.** Decide whether rachel or shepherd translates
   screen-to-local. Implement consistently across mouse press/release/move.

## Architecture Constraints

- No polling or timeouts — all event-driven (mailbox + constraint wakes)
- Rachel is the sole input consumer — she sees everything, forwards selectively
- AppWindows draw directly to the shared framebuffer — no compositor
- The constraint system tracks bounds; z-order is pure WM policy
- The 10Hz constraint network signal (already exists in kernel) drives animation,
  not the deleted timer library
- Keep architecture as similar as possible across RISC-V, ARM64, and x86_64
