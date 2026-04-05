# Implementation Plan: Input Row, Selection, Animation

Three tasks: (1) label + row layout for input, (2) text selection,
(3) time-interval attributes + rachel animation protocol.

---

## Task 1: Label + RowFillLastChild Layout for Input

### Goal

Replace the bare SingleLineText with a RowFillLastChild containing a
Label ("Stdio Input") and the SingleLineText. The text input (last
child) gets all remaining horizontal space. The RowFillLastChild's
width is constrained to match the console width (moved from the current
input width constraint).

### 1A. "Stdio Input" Label

- Create a `std.NewLabelNamed("inputLabel", "inputRow", theme, "Stdio Input", fontSize)`
  in `flock/cmd/linux/main.go`.
- Label auto-measures its own width from the text.

### 1B. RowFillLastChild Interactor

**New interactor** in `mazarin/mancini/std/row_fill_last_child.go`.

The existing `std.NewRow` sums children's intrinsic widths. We need a
variant where the last child in the row gets
`rowWidth - sum(other children widths) - spacing`.

RowFillLastChild differs from Row in two ways:
1. Its width is set externally (not computed from children).
2. The last visible child's width is computed as the remaining space.

**Files to create:**
- `mazarin/mancini/std/row_fill_last_child.go` — interactor
- `mazarin/mancini/std/row_fill_last_child_width.vgo` — vgo program for
  computing last child's width
- `mazarin/mancini/std/row_fill_last_child_width.vbc.go` — compiled bytecode

**Constructor:**
```go
func NewRowFillLastChild(myName, parent string, pal Palette, spacing int64) *RowFillLastChild
```

**RowFillLastChild.Draw behavior:**
1. Iterate visible children left-to-right.
2. For each child except the last: use child's intrinsic Width.
3. For the last child: use `rowWidth - usedWidth - spacing*(nChildren-1)`.
4. All children get the row's height (or their intrinsic height,
   whichever is smaller), vertically centered.

**vgo program `rowFillLastChildWidth`:**
```
func rowFillLastChildWidth() int64 {
    rowW := derefI64("_rowWidth_")
    spacing := derefI64("_spacing_")
    margin := derefI64("_hMargin_")
    children := findVisibleChildren("_myName_")
    n := len(children)
    if n < 2 { return rowW - 2*margin }
    used := 0
    for i := 0; i < n-1; i++ {
        used += childWidth(children[i])
    }
    return rowW - used - (n-1)*spacing - 2*margin
}
```

### 1C. Width Constraint Wiring

**Current state** in `flock/cmd/linux/main.go`:
```go
inputLH.Width = attr.ConstraintI64(..., mancini.EqualI64(consoleWidthURI))
```

**New state:**
- The RowFillLastChild's Width gets the console-width equality constraint.
- The SingleLineText's Width becomes a constraint bound to
  `ProgRowFillLastChildWidth`, which reads the row's width and subtracts
  the label's width + spacing.
- The SingleLineText no longer needs `NewLayoutAttributesBase` for custom
  width wiring — the RowFillLastChild handles it.

**Changes to `flock/cmd/linux/main.go`:**
1. Create row: `row := std.NewRowFillLastChild("inputRow", "column", pal, 4)`
2. Set row width constraint:
   `row.GetLayout().Width = attr.ConstraintI64(..., mancini.EqualI64(consoleWidthURI))`
3. Create label:
   `std.NewLabelNamed("inputLabel", "inputRow", theme, "Stdio Input", fontSize)`
4. Create SingleLineText with parent `"inputRow"` instead of `"column"`.
   Its width is computed by the RowFillLastChild's vgo program.
5. `SwapSequence("inputRow", "console")` (instead of `"input"`, `"console"`).

### Implementation Order

1. Write `row_fill_last_child_width.vgo`
2. Compile: `$GO tool compile-constraints -pkg std -vgolib mazarin/vgolib mazarin/mancini/std/row_fill_last_child_width.vgo`
3. Write `row_fill_last_child.go`
4. Update `flock/cmd/linux/main.go`
5. Build and test

---

## Task 2: Text Selection

### Goal

Add selection state to SingleLineText with visual feedback (tinted
background on selected characters), shift+motion bindings, and
typing/delete replaces selection.

### 2A. Selection State and Rendering

**New fields on `SingleLineText`:**
```go
selAnchor int  // rune position where shift was first pressed (-1 = no selection)
selExtent int  // rune position where selection extends to (cursor moves here)
```

Selection range is `[min(selAnchor, selExtent), max(selAnchor, selExtent))`.

**Visual rendering** in Draw():
- After drawing the inset background (step 2) and before drawing text
  (step 7), if there is a selection:
  - Compute pixel X for selection start and end using `MeasureText`.
  - Fill rectangle from selStartPx to selEndPx at text height
    with `pal.SurfaceTint()`.

**Helper methods:**
```go
func (t *SingleLineText) HasSelection() bool
func (t *SingleLineText) SelectionRange() (start, end int) // ordered min,max
func (t *SingleLineText) ClearSelection()
func (t *SingleLineText) SelectedText() string
```

### 2B. Selection Methods

All `Selection*` methods operate on the anchor/extent pair:

```go
// SelectionExtendLeftChar extends selection one rune left.
// If no selection, sets anchor = cursorPos first.
func (t *SingleLineText) SelectionExtendLeftChar()

// SelectionExtendRightChar extends selection one rune right.
func (t *SingleLineText) SelectionExtendRightChar()

// SelectionExtendToBeginning extends selection to beginning of line.
func (t *SingleLineText) SelectionExtendToBeginning()

// SelectionExtendToEnd extends selection to end of line.
func (t *SingleLineText) SelectionExtendToEnd()

// SelectionExtendBackwardWord extends selection to previous word boundary.
func (t *SingleLineText) SelectionExtendBackwardWord()

// SelectionExtendForwardWord extends selection to next word boundary.
func (t *SingleLineText) SelectionExtendForwardWord()

// SelectionAll selects all text (e.g., Ctrl+A with no other modifiers).
func (t *SingleLineText) SelectionAll()
```

**Pattern for extend methods:**
```go
func (t *SingleLineText) SelectionExtendLeftChar() {
    if t.selAnchor < 0 {
        t.selAnchor = t.cursorPos
    }
    if t.cursorPos > 0 {
        t.cursorPos--
        t.selExtent = t.cursorPos
        t.FullDamage()
    }
}
```

### 2C. Shift+Motion Keybindings

Modify `KeyPress()` to check `hid.Shift(mods)` for all cursor motion
actions. When shift is held, call the `SelectionExtend*` variant
instead of the cursor-move variant.

**Cursor motion WITHOUT shift does NOT clear selection.** The cursor
moves independently; the selection remains until explicitly dismissed.

**In the `"left"` case:**
```go
case "left":
    if hid.Shift(mods) {
        if hid.Meta(mods) {
            t.SelectionExtendToBeginning()
        } else if hid.Alt(mods) {
            t.SelectionExtendBackwardWord()
        } else {
            t.SelectionExtendLeftChar()
        }
    } else {
        if hid.Meta(mods) {
            t.CursorMoveBeginningOfLine()
        } else if hid.Alt(mods) {
            t.CursorMoveBackwardWord()
        } else {
            t.CursorMoveLeftChar()
        }
    }
```

Same pattern for `"right"`, `"home"`, `"end"`.

### 2D. Dismissing Selection

Three ways selection is cleared:

1. **Escape** — clears selection, cursor stays at current position.
2. **Typing a character** — deletes selected text, inserts the typed
   character at the selection start, clears selection.
3. **Delete / Backspace** — deletes selected text, clears selection
   (does not delete an additional character).

**Escape handling** (in the `"escape"` case of KeyPress):
```go
case "escape":
    if t.HasSelection() {
        t.ClearSelection()
        t.FullDamage()
    }
    return true
```

**InsertAtCursor:**
```go
func (t *SingleLineText) InsertAtCursor(ch rune) {
    t.ClearAlert()
    if t.HasSelection() {
        t.deleteSelection()  // deletes selected range, moves cursor to start
    }
    // ... existing insert logic
}
```

**DeleteBackward / DeleteForward:**
```go
func (t *SingleLineText) DeleteBackward() {
    if t.HasSelection() {
        t.deleteSelection()
        return
    }
    // ... existing single-char delete logic
}
```

**Private helper:**
```go
func (t *SingleLineText) deleteSelection() {
    start, end := t.SelectionRange()
    text := t.Text()
    bStart := runeByteOffset(text, start)
    bEnd := runeByteOffset(text, end)
    t.cursorPos = start
    t.selAnchor = -1
    t.selExtent = -1
    t.textAttr.Set(text[:bStart] + text[bEnd:])
    t.FullDamage()
}
```

### Files to Modify

- `mazarin/mancini/std/single_line_text.go` — all selection logic + rendering

### Implementation Order

1. Add selAnchor/selExtent fields, initialize to -1
2. Add HasSelection, SelectionRange, ClearSelection, SelectedText, deleteSelection
3. Add SelectionExtend* methods
4. Update Draw() to render selection tint with `pal.SurfaceTint()`
5. Update KeyPress() with shift+motion checks (no selection clear on plain motion)
6. Update Escape to clear selection
7. Update InsertAtCursor, DeleteBackward, DeleteForward to handle selection
8. Build and test

---

## Task 3: Time Interval + Animation Protocol

### 3A. Rachel Time-Interval Attributes

Rachel publishes interval attributes representing the time window
between consecutive ticks. Updated in Go code each tick (no vgo needed).

**New rachel attributes:**
```
attr:///shepherd/{rachelSID}/int64/intervalStart
attr:///shepherd/{rachelSID}/int64/intervalEndOpen
```

Values are UTC epoch nanoseconds. The interval is `[start, endOpen)`.

**Implementation in rachel's main.go:**
```go
// Setup (after time constraints):
prevNanos := int64(0)
intervalStart := attr.ValueI64(
    attr.ShepherdURI("int64", "intervalStart"), 0)
intervalEndOpen := attr.ValueI64(
    attr.ShepherdURI("int64", "intervalEndOpen"), 0)

// In dirty-tick handler (runs ~10Hz from kernel time):
nowNanos := timeNanos.Get()
if prevNanos != 0 {
    intervalStart.Set(prevNanos)
    intervalEndOpen.Set(nowNanos)
}
prevNanos = nowNanos
```

Rachel needs its own time constraint mirroring kernel time nanos,
set eager, driving the dirty channel. Follow the clocks app pattern
(`ProgIdentityI64` bound to `attr:///kernel/int64/time/utc_nanos`,
`SetEager(true)`).

### 3B. Animation Wire Messages

**New message types** in `shared/wm/uring.go`:

```go
MsgTypeAnimationRegister   uint32 = 20  // shepherd → rachel
MsgTypeAnimationRegistered uint32 = 21  // rachel → shepherd
MsgTypeAnimationStart      uint32 = 22  // rachel → shepherd
MsgTypeAnimationUpdate     uint32 = 23  // rachel → shepherd
MsgTypeAnimationFinish     uint32 = 24  // rachel → shepherd
```

**Message structs:**

```go
// AnimationRegister — shepherd → rachel.
// Requests an animation spanning [StartNanos, EndNanos].
// The interval is CLOSED (both endpoints included).
// Nonce is a caller-chosen value that rachel echoes back in
// AnimationRegistered so the caller can correlate the response
// without assuming message ordering.
type AnimationRegister struct {
    StartNanos int64
    EndNanos   int64
    Nonce      uint64  // caller's local animation ID
}

// AnimationRegistered — rachel → shepherd.
// Confirms registration. AnimationID is rachel's global ID;
// Nonce is the value from the AnimationRegister request.
type AnimationRegistered struct {
    AnimationID uint64
    Nonce       uint64  // echoed from AnimationRegister
}

// AnimationStart — rachel → shepherd.
// Sent when the animation's start time is first reached.
type AnimationStart struct {
    AnimationID uint64
    StartNanos  int64
}

// AnimationUpdate — rachel → shepherd.
// Sent each interval tick while the animation is active.
// CoveredStart and CoveredEnd are fractions in [0, 1).
// The end is open: CoveredEnd never reaches 1.0 during updates.
type AnimationUpdate struct {
    AnimationID  uint64
    StartNanos   int64
    EndNanos     int64
    CoveredStart float64
    CoveredEnd   float64
}

// AnimationFinish — rachel → shepherd.
// Sent when the animation's end time has passed.
type AnimationFinish struct {
    AnimationID uint64
    EndNanos    int64
}
```

**Encode/Decode:** Follow existing pattern. AnimationRegister is
shepherd→rachel via `ipc.ProtoWMNotify` (same as AppStart, Blit).
Animation{Registered,Start,Update,Finish} are rachel→shepherd via
`ipc.ProtoShepherdNotify` (same as focus, mouse, key messages).

### 3C. Animatable Interface and AppWindow Mediation

**`Animatable` interface** in `mazarin/mancini/mancini.go`:
```go
// Animatable is implemented by interactors that participate in
// rachel's animation protocol. AppWindow dispatches animation
// messages to registered Animatable interactors.
type Animatable interface {
    AnimationStart(localID uint64, startNanos int64)
    AnimationUpdate(localID uint64, startNanos, endNanos int64, coveredStart, coveredEnd float64)
    AnimationFinish(localID uint64, endNanos int64)
}
```

**AppWindow animation bookkeeping** (new fields on AppWindow or a
helper struct):

```go
// nextLocalID is the counter for local animation IDs.
nextLocalID uint64

// localToAnimatable maps localID → Animatable. Populated when the
// interactor calls RegisterAnimation. The localID is sent as the
// Nonce in AnimationRegister, so rachel echoes it back.
// Concurrent-safe (sync.Map or mutex-guarded map).
localToAnimatable sync.Map  // map[uint64]Animatable

// remoteToLocal maps rachel's global animation ID → localID.
// Populated when AnimationRegistered arrives (Nonce = localID).
// Concurrent-safe.
remoteToLocal sync.Map  // map[uint64]uint64
```

**AppWindow.RegisterAnimation method:**
```go
// RegisterAnimation registers an Animatable for an animation and
// sends AnimationRegister to rachel. The local ID is sent as the
// Nonce field so rachel echoes it back — no ordering assumption.
// Returns the local animation ID.
func (w *AppWindow) RegisterAnimation(a Animatable, startNanos, endNanos int64) uint64 {
    localID := atomic.AddUint64(&w.nextLocalID, 1)
    w.localToAnimatable.Store(localID, a)
    msg := wm.EncodeAnimationRegister(&wm.AnimationRegister{
        StartNanos: startNanos,
        EndNanos:   endNanos,
        Nonce:      localID,
    })
    uring.Send(rachelSID, &msg)
    return localID
}
```

**AppWindow WM message dispatch** (called from the app's main loop
when WM messages arrive):

```go
case wm.AnimationRegistered:
    // Rachel echoes our localID as Nonce. Map her global ID to it.
    localID := msg.Nonce
    w.remoteToLocal.Store(msg.AnimationID, localID)

case wm.AnimationStart:
    localID, ok := w.remoteToLocal.Load(msg.AnimationID)
    if !ok { break }
    a, ok := w.localToAnimatable.Load(localID)
    if !ok { break }
    a.(Animatable).AnimationStart(localID.(uint64), msg.StartNanos)

case wm.AnimationUpdate:
    localID, ok := w.remoteToLocal.Load(msg.AnimationID)
    if !ok { break }
    a, ok := w.localToAnimatable.Load(localID)
    if !ok { break }
    a.(Animatable).AnimationUpdate(localID.(uint64),
        msg.StartNanos, msg.EndNanos, msg.CoveredStart, msg.CoveredEnd)

case wm.AnimationFinish:
    localID, ok := w.remoteToLocal.Load(msg.AnimationID)
    if !ok { break }
    a, ok := w.localToAnimatable.Load(localID)
    if ok {
        a.(Animatable).AnimationFinish(localID.(uint64), msg.EndNanos)
    }
    w.localToAnimatable.Delete(localID)
    w.remoteToLocal.Delete(msg.AnimationID)
```

**No FIFO needed.** The Nonce field in AnimationRegister carries the
localID. Rachel echoes it in AnimationRegistered, so we can correlate
responses without assuming message ordering.

### 3D. SingleLineText as Animatable

**Alert constants:**
```go
const (
    AlertFadeStartMs = 500   // ms after Alert() before fade begins
    AlertFadeEndMs   = 1000  // ms after Alert() when fade completes
)
```

**New fields:**
```go
alertAlpha   uint8   // current border alpha (255 = opaque, 0 = gone)
alertLocalID uint64  // local animation ID (0 = no active animation)
appWindow    *AppWindow // set during construction or via setter
```

**Alert() revised flow:**
1. Set `isAlerting = true`, `alertAlpha = 255`.
2. Compute `now` from kernel time nanos.
3. Call `appWindow.RegisterAnimation(t, now+500ms, now+1000ms)`.
4. Store returned localID in `alertLocalID`.
5. Call `FullDamage()`.

**Animatable implementation:**
```go
func (t *SingleLineText) AnimationStart(localID uint64, startNanos int64) {
    // Alert border already visible at full alpha. No-op.
}

func (t *SingleLineText) AnimationUpdate(localID uint64,
    startNanos, endNanos int64, coveredStart, coveredEnd float64) {
    t.alertAlpha = uint8(255.0 * (1.0 - coveredEnd))
    t.FullDamage()
}

func (t *SingleLineText) AnimationFinish(localID uint64, endNanos int64) {
    t.isAlerting = false
    t.alertAlpha = 0
    t.alertLocalID = 0
    t.FullDamage()
}
```

**Draw changes:**
```go
if t.isAlerting {
    hi := pal.Highlight()
    alpha := t.alertAlpha
    if alpha == 0 { alpha = 255 }  // safety: full alpha if not yet set
    dc.SetColor(color.NRGBA{hi.R, hi.G, hi.B, alpha})
    dc.FillRectangle(fx, fy, fw, fh)
}
```

**Remove old mechanism:**
- Delete `AlertDone chan struct{}` field and channel init.
- Delete `time.AfterFunc` call from `Alert()`.
- Remove `<-input.AlertDone` case from linux main loop.

### 3E. Rachel Animation List

**New file: `flock/cmd/rachel/animation.go`**

```go
type activeAnimation struct {
    id         uint64
    targetSID  int
    startNanos int64
    endNanos   int64
    started    bool  // true after AnimationStart sent
}
```

**Storage:** `animations []activeAnimation`, kept sorted by startNanos
(ascending). `nextAnimID uint64` counter, starts at 1.

**On AnimationRegister received (from wmCh):**
1. Assign `id = nextAnimID; nextAnimID++`.
2. Send `AnimationRegistered{AnimationID: id}` back to sender SID.
3. **Special case — end time in the past** (`endNanos <= now`):
   - Send `AnimationStart{id, startNanos}` to sender.
   - Send `AnimationFinish{id, endNanos}` to sender.
   - Do NOT insert into the sorted list. Discard the ID.
4. **Normal case:** Insert into `animations` sorted by startNanos.

**On each interval tick** (wired to intervalEndOpen dirty notification):

Walk `animations` front-to-back:
```go
now := intervalEndOpen.Get()
intervalStartVal := intervalStart.Get()
i := 0
for i < len(animations) {
    anim := &animations[i]

    if anim.endNanos <= now {
        // Past end — send finish (and start if never sent)
        if !anim.started {
            send AnimationStart{anim.id, anim.startNanos} to anim.targetSID
        }
        send AnimationFinish{anim.id, anim.endNanos} to anim.targetSID
        animations = append(animations[:i], animations[i+1:]...)
        continue
    }

    if anim.startNanos > now {
        break  // remaining animations are in the future
    }

    // Active: startNanos <= now < endNanos
    if !anim.started {
        send AnimationStart{anim.id, anim.startNanos} to anim.targetSID
        anim.started = true
    }
    duration := float64(anim.endNanos - anim.startNanos)
    cs := clamp(float64(intervalStartVal - anim.startNanos) / duration, 0.0, 1.0)
    ce := clamp(float64(now - anim.startNanos) / duration, 0.0, 1.0)
    send AnimationUpdate{anim.id, anim.startNanos, anim.endNanos, cs, ce} to anim.targetSID
    i++
}
```

### Files to Create/Modify

**Create:**
- `flock/cmd/rachel/animation.go` — activeAnimation, sorted list,
  tick handler, register handler

**Modify:**
- `shared/wm/uring.go` — add 5 message types, structs, encode/decode
- `mazarin/mancini/mancini.go` — add `Animatable` interface
- `mazarin/mancini/std/app_window.go` — add RegisterAnimation,
  outstanding/remoteToLocal maps, pending queue, animation dispatch
- `mazarin/mancini/std/single_line_text.go` — implement Animatable,
  replace time.AfterFunc + AlertDone with animation-based alert,
  add alertAlpha field, update Draw for alpha fade
- `flock/cmd/rachel/main.go` — add time constraints, interval attrs,
  wire interval tick to animation walker, handle AnimationRegister
- `flock/cmd/linux/main.go` — remove AlertDone select case, forward
  animation WM messages to AppWindow dispatch

---

## Dependency Order

```
Task 3A (rachel interval attrs)
  then 3B (wire messages)
  then 3C (Animatable + AppWindow mediation)
  then 3E (rachel animation list + tick walker)
  then 3D (SingleLineText implements Animatable)

Task 1 (RowFillLastChild + label + wiring) — independent

Task 2 (selection) — independent
```

**Recommended build order:**
1. Task 1 (layout) — smallest, self-contained
2. Task 2 (selection) — self-contained
3. Task 3 (animation) — largest, most cross-cutting

---

## Resolved Decisions

1. **Interval granularity:** ~10Hz is acceptable for now. If alpha
   fading looks choppy with ~5 frames over 500ms, rachel can switch
   to a higher-frequency local ticker later.

2. **No FIFO / no ordering assumption:** AnimationRegister carries a
   Nonce field (set to the localID). Rachel echoes it back in
   AnimationRegistered. This eliminates the `outstanding` map and the
   FIFO queue — `localToAnimatable` + `remoteToLocal` are the only
   two maps needed.

3. **Time access:** `Alert()` calls `sys.GetTime()` to get current
   nanos. This is a kernel `RawSyscall6` (`mazarin/sys/time.go:21`)
   — fast, P held, no goroutine scheduling overhead. Returns
   `TimeSpec{Seconds, Nanoseconds}` which is converted to epoch
   nanos via `ts.Seconds * 1e9 + ts.Nanoseconds`.
