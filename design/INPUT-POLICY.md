# Input Policy Architecture

## Background

This design is informed by the subArctic input model (Hudson, Mankoff, Smith,
CHI 2005) and its predecessor Artkit (Henry, Hudson, Newell, UIST 1990). The
central insight of that work is a three-layer separation: **dispatch policies**
decide the routing strategy, **dispatch agents** translate raw events into
high-level protocols via internal FSMs, and **interactors** implement protocol
interfaces to receive translated input.

Mazzy adopts this architecture but splits it across two levels because the
window manager (rachel) and the application shepherds run in separate address
spaces, communicating via ring buffer IPC.

## Two-Level Dispatch

### Level 1: Rachel (Window Manager)

Rachel is not an application. Her job is **inter-application routing**. She
receives all HID events from the kernel via a shared completion ring, makes
WM-level policy decisions, and forwards events to shepherds via ring buffer
IPC messages. Rachel's "high-level output" is always a message pushed to a
shepherd's ring buffer, never a method call on an interactor.

Rachel maintains:

- **AppWindow bounds** for every tracked shepherd (mirrored via constraint
  attributes from each shepherd's `AppWindow/layout/Bounds`).
- **Z-order** of tracked applications (front-to-back list).
- **Focus state**: which shepherd currently has keyboard and mouse focus.

#### Rachel's Dispatch Policies

Rachel has her own policy chain, ordered by priority. Each policy inspects
the event and either handles it (consuming the event) or passes it along.

1. **WM Hotkey Policy** (highest priority)

   Rachel consumes certain key events herself. These are system-level
   operations that no application should see:

   - Window cycling (e.g. F1 brings next app to front, updates z-order)
   - Window close / minimize / maximize
   - System menu activation
   - Future: screenshot, screen lock, compositor toggles

   When a hotkey matches, rachel performs the action directly (reorder
   z-list, send focus change messages, etc.) and the event is consumed.
   It is never forwarded to any shepherd.

2. **Focus Change Policy**

   On mouse press, rachel picks against the z-ordered AppWindow bounds
   list (front-to-back). If the press lands on a window that is not
   currently focused:

   - Send `YouLostFocus` to the previously focused shepherd.
   - Raise the clicked window to the front of the z-order.
   - Send `YouHaveFocus` to the newly focused shepherd.
   - Update `focusedSID`.
   - Forward the mouse press to the new focus holder (so it also gets
     the click that caused the focus change).

   If the press lands on the already-focused window, or on no window at
   all, this policy does not consume the event.

3. **Forward Policy** (lowest priority)

   All events not consumed by higher policies are forwarded to the
   currently focused shepherd via its ring buffer. This is the common
   path for keyboard input, mouse clicks within the focused window, and
   mouse movement.

   If no shepherd has focus, the event is discarded.

#### Rachel as a Positional Picker

Rachel's focus change policy is analogous to subArctic's positional dispatch
policy, but operates at window granularity. Her "pick list" is the z-ordered
set of AppWindows. The pick test is a simple rectangle hit test against each
window's bounds, front-to-back. The first hit wins.

This is deliberately simple. Rachel does not know about interactors inside a
window. She does not need to. The complexity of interactor-level picking lives
inside each shepherd.

### Level 2: Inside the Shepherd (subArctic-Style)

Once an event arrives at a shepherd (via ring buffer IPC message), the full
subArctic three-layer architecture applies within the shepherd's address space.

#### Dispatch Policies (intra-shepherd)

The shepherd maintains a prioritized policy chain:

1. **Monitor Policy** (highest priority) -- Delivers events to observers that
   never consume. Used for event tracing, recording, click tracking, animation
   ticks (the 10Hz constraint network signal replaces the old timer library
   for animation pacing).

2. **Focus Policy** -- Delivers events to the interactor that has established
   itself as the input focus for a given protocol. Appropriate for keyboard/text
   input, drag continuations, menu traversal. Focus agents handle the FSM
   state that tracks, e.g., press-move-release for drags. The key property:
   once a drag starts, events go to the drag target regardless of cursor
   position, eliminating the need for X11-style input grabs.

3. **Positional Policy** (lowest priority) -- Delivers events to interactors
   found "under" the cursor via picking. The shepherd walks its interactor
   tree top-down, testing each interactor's `PickedBy(x, y)` method. Picked
   interactors are accumulated front-to-back (reverse draw order); dispatch
   happens front-to-back until an interactor consumes the event.

#### Dispatch Agents

Each policy maintains a prioritized list of dispatch agents. Each agent:

- Handles one specific interaction pattern (drag, click, double-click, text
  entry, etc.)
- Maintains an internal FSM translating raw events into high-level protocol
  method calls
- Communicates with interactors through an **input protocol** interface

Hybrid agents span policies. For example, a press-drag agent accepts the
initial press positionally (which interactor was clicked?), then installs
itself as a focus agent for the drag continuation (move events go to the drag
target regardless of cursor position). This eliminates grabs.

#### Input Protocols

An input protocol is an interface that an interactor implements to receive a
particular type of translated input:

- `Clickable` -- `Click(x, y int32)`
- `Pressable` -- `Press(x, y int32)`, `Release(x, y int32)`
- `Draggable` -- `DragStart(x, y)`, `DragFeedback(x, y)`, `DragEnd(x, y)`
- `TextAcceptor` -- character input, backspace, delete, cursor movement
- `Focusable` -- `FocusEnter()`, `FocusExit()`
- `Pointable` -- hover enter/exit (for tooltips, rollovers)

Protocols driven by the same underlying raw events conflict. An interactor
should not implement both `Clickable` and `DoublClickable` unless the agents
are designed to cooperate.

#### Picking

Picking is a top-down recursive traversal of the interactor tree. Each
interactor implements `PickedBy(x, y int32) bool`, which defaults to a
bounding-box test but can be overridden for non-rectangular shapes. The pick
list is explicit, enabling parent interactors to intercept picking (e.g., a
drag container can insert itself ahead of its children to receive drag events
while children receive clicks).

## Message Types

Rachel communicates with shepherds via the existing `shared/wm` message types:

| Message | Direction | Purpose |
|---------|-----------|---------|
| `MsgAppStart` | shepherd -> rachel | Register with WM |
| `MsgYouHaveFocus` | rachel -> shepherd | You gained focus |
| `MsgYouLostFocus` | rachel -> shepherd | You lost focus |
| `MsgMousePress` | rachel -> shepherd | Mouse button down at (X, Y) |
| `MsgMouseRelease` | rachel -> shepherd | Mouse button up at (X, Y) |
| `MsgMouseMove` | rachel -> shepherd | Mouse moved to (X, Y) |
| `MsgKeyPress` | rachel -> shepherd | Key pressed (evdev code) |
| `MsgKeyRelease` | rachel -> shepherd | Key released (evdev code) |

Mouse coordinates in messages are **screen coordinates**. The shepherd
translates to window-local coordinates by subtracting its AppWindow origin.

## Z-Order Management

Rachel maintains a simple ordered slice of tracked app SIDs, front-to-back.
Operations:

- **Raise to front**: Remove SID from current position, prepend. Triggered
  by focus change (click on unfocused window) or WM hotkey.
- **Pick test**: Iterate front-to-back, first bounds hit wins.
- **App exit**: Remove from z-order, if it was focused then focus passes to
  the new front window (or no focus if empty).

The z-order is independent of the constraint system. Constraints track
geometry (bounds); z-order is pure WM policy.

## Coordinate Spaces

- **Screen coordinates**: Origin at top-left of display. Used by rachel for
  picking and in all WM messages.
- **Window-local coordinates**: Origin at top-left of the AppWindow. Used
  inside the shepherd for interactor picking and input protocol methods. The
  shepherd computes these by subtracting its AppWindow's (X, Y) from the
  screen coordinates in the message.

## Drag Across Window Boundaries

When a drag starts in one window (mouse press), rachel establishes that
window as the drag focus. Subsequent mouse move and release events go to
that window even if the cursor leaves its bounds. This is the WM-level
analogue of the subArctic focus policy eliminating grabs. Rachel tracks
`dragFocusSID` separately from `focusedSID`:

- Mouse press: set `dragFocusSID` to the picked window.
- Mouse move while `dragFocusSID >= 0`: forward to drag focus, not keyboard
  focus.
- Mouse release: clear `dragFocusSID`.

This means a drag that starts in window A and ends in window B sends all
events to A. Window B never sees the drag. Focus does not change during a
drag.

## Future Considerations

- **Modal dialogs**: A shepherd could request that rachel restrict input
  routing to only that shepherd (modal grab at WM level). Rachel would add
  a modal policy above the focus change policy that discards events directed
  at other windows.
- **Window decorations**: Rachel could own title bar / resize handle
  interactors per window. Clicks on decorations would be consumed by rachel
  (move, resize, close) rather than forwarded. This adds a WM-level
  positional policy between hotkey and focus change.
- **Multi-monitor**: Z-order and picking extend naturally to multiple
  displays. Rachel tracks which display each window occupies.
- **Accessibility**: A monitor-level agent in rachel could log all WM-level
  routing decisions for screen reader integration.
