# Constraint VM Phase 9 — Interactive Interactors

## Context

Phases 1–8 are complete. The constraint VM has:

- Flat shared-page attribute storage with dirty propagation (kernel DFS walk)
- Handle[T] client API with lazy evaluation on Get()
- Cascading constraint chains (evaluate-on-deref)
- Eager dirty notification via WaitDirty (per-priest queue, thread blocking)
- Change-gated propagation (skip if bitwise-equal value)
- Pre-compiled constraint programs with BindStrings() template substitution
- Rectangle type (int32 coords) with 9 builtins: rect, rect_union, etc.
- Point2D type (int64 coords) with constructors and accessors
- Service discovery: find, deref_*, exists, uri_segment, is_unknown
- Kernel-published attributes: time (utc_seconds, utc_nanos), screen (width,
  height, charWidth, charHeight), darkMode, input/modifiers, system info
- Interactor framework: Window, Card, Label with constraint-based layout,
  damage tracking (lastPainted mirrors), and efficient redraw
- DrawContext with pre-rendered glyph atlas, clipped tree rendering, GPU flush
- uitest priest: window→card→label tree, clock display, right-half screen region
- stdio priest: left-half screen region, neumorphic card with serial console
- Input event API: input.Keyboard() and input.Mouse() channel-based event
  delivery with KeyEvent/MouseEvent structs, soft IRQ 28/29
- HID modifier bitmask published as kernel attribute (nosplit IRQ → atomic →
  timeUpdateLoop → KernelAttrWriteI64)
- 120s+ stability on ARM64 TCG/HVF, x86_64, RISC-V

**Problem**: The UI is read-only. Interactors display constraint-derived state
but cannot respond to user input. There is no button, no text field, no focus
model, no way to route keyboard/mouse events to the correct interactor. The
constraint system computes layout and damage but has no concept of interactive
state (hovered, pressed, focused). Building even a simple interactive app
requires dropping to imperative channel-based event handling with no connection
to the constraint namespace.

**Solution**: Phase 9 adds interactive interactors. We publish mouse position
and key events as kernel attributes, add Button and TextInput interactor types,
introduce a focus model, and wire hit-testing into the constraint namespace.
Interactive state (hovered, pressed, focused, caret position) lives as value
attributes that goroutines update from input events. Constraints react to these
states for visual feedback (hover highlight, press animation, cursor blink).

**Milestone**: A simple interactive demo — uitest shows a window with a clock
label, a "Toggle Dark Mode" button, and a single-line text input. Clicking the
button toggles `attr:///kernel/bool/darkMode` (the existing kernel attribute),
which changes the window/card/label background colors via constraints. Typing
in the text input updates a label that mirrors the input. Tab switches focus
between button and text input. All visual updates flow through constraints and
damage tracking — no imperative redraw calls.

**Design principles**:
- Interactive state is **value attributes** updated by goroutines, not constraint
  outputs. Constraints are pure functions; side effects (state changes from
  clicks) happen in Go goroutines that Set() attribute values.
- The constraint system reacts to interactive state changes the same way it
  reacts to kernel time changes — dirty propagation, lazy eval, damage rect.
- Hit-testing is done by a goroutine that reads mouse position and compares
  against interactor bounds. It doesn't need a special builtin — rect_contains
  with Point2D→int32 conversion suffices, or just plain Go comparisons.
- Focus is a single value attribute per priest (the ID of the focused
  interactor). Keyboard events are routed to the focused interactor's handler
  goroutine.

## Existing Infrastructure

Already in place (no changes needed unless noted):

### Kernel attributes (kmazarin/ksyscall/constraint_kernel.go)
- `attr:///kernel/int64/input/modifiers` — keyboard modifier bitmask
- `attr:///kernel/bool/darkMode` — dark mode toggle (currently unused)
- TopHalfUpdateModifiers() — nosplit IRQ handler for modifier state
- timeUpdateLoop() — flushes modifier state to attribute

### Input event API (mazarin/input/input.go)
- `input.Keyboard()` — returns `<-chan KeyEvent`
- `input.Mouse()` — returns `<-chan MouseEvent`
- KeyEvent: Key, Code, Pressed, Repeat, Char, Action
- MouseEvent: DX, DY, Wheel, Button, Pressed

### Interactor framework (mazarin/interactor/)
- Window, Card, Label constructors
- BindStrings template substitution for constraint programs
- DrawContext with glyph atlas and clipped tree rendering
- Damage tracking via lastPainted mirrors

### Constraint programs (mazarin/interactor/constraints/)
- bounds_from_ulwh, center_in_parent, offset_point
- label_width, outside_in_dim
- leaf_damage_rect, parent_damage_rect
- constant_true, constant_empty_str, identity_i64

### VM builtins (mazarin/vm/builtin.go)
- rect_contains(rect, rect) — rectangle containment test
- rect_overlaps(rect, rect) — rectangle overlap test
- All deref_* builtins for dynamic attribute lookup

## Implementation Steps

### Step 1: Kernel mouse position attributes

**File**: `kmazarin/ksyscall/constraint_kernel.go`

Add mouse position as kernel-published attributes. The kernel already has mouse
event delivery via VirtIO input → soft IRQ. We need to accumulate absolute
position from relative motion deltas and publish it.

```
attr:///kernel/int64/input/mouse_x    — absolute X position (pixels)
attr:///kernel/int64/input/mouse_y    — absolute Y position (pixels)
attr:///kernel/int64/input/mouse_btn  — button bitmask (bit 0=left, 1=right, 2=middle)
```

Add slot variables:
```go
var (
    slotMouseX   uint16
    slotMouseY   uint16
    slotMouseBtn uint16
)
```

Add atomic state variables (updated from nosplit IRQ context, same pattern as
modifierState/modifierDirty):
```go
var mouseAbsX   int64  // accumulated absolute X
var mouseAbsY   int64  // accumulated absolute Y
var mouseBtnMask uint64 // button bitmask
var mouseDirty  uint32 // set atomically by top-half
```

Add `TopHalfUpdateMouse(evType, code, value uint32)` — nosplit function called
from the existing NonTimerIRQTopHalf for mouse events:
- EV_REL + REL_X: `atomic.AddInt64(&mouseAbsX, int64(int32(value)))` + clamp to [0, screenW)
- EV_REL + REL_Y: `atomic.AddInt64(&mouseAbsY, int64(int32(value)))` + clamp to [0, screenH)
- EV_REL + REL_WHEEL: (skip for now, or store separately)
- EV_KEY + BTN_LEFT/RIGHT/MIDDLE: set/clear bit in mouseBtnMask via CAS
- Set mouseDirty=1 on any change

In `PublishKernelAttributes()`, create the three mouse attributes.
In `timeUpdateLoop()`, flush mouse state (same pattern as modifiers):
```go
if atomic.CompareAndSwapUint32(&mouseDirty, 1, 0) {
    KernelAttrWriteI64(slotMouseX, atomic.LoadInt64(&mouseAbsX))
    KernelAttrWriteI64(slotMouseY, atomic.LoadInt64(&mouseAbsY))
    KernelAttrWriteI64(slotMouseBtn, int64(atomic.LoadUint64(&mouseBtnMask)))
}
```

**Note**: The screen dimensions for clamping (screenW, screenH) are available
from the gpu package (`gpu.GetWidth()`, `gpu.GetHeight()`).

**Note**: Check how the existing NonTimerIRQTopHalf dispatches mouse events. It
may already call into a function that delivers to userspace soft IRQ channels.
The new TopHalfUpdateMouse should be called *in addition* to that delivery, so
both the attribute system and the channel API see mouse events.

### Step 2: Kernel key event attribute

**File**: `kmazarin/ksyscall/constraint_kernel.go`

Add a last-key-pressed attribute. This is a single int64 that holds the most
recent key code (positive = press, negative = release). It changes on every
key event, so constraints that deref it will re-evaluate on every keystroke.

```
attr:///kernel/int64/input/last_key   — last keycode (>0 press, <0 release)
```

This is intentionally simple. Complex key event routing (character translation,
repeat detection) stays in the input.Keyboard() channel API. The attribute is
for constraints that need to react to "any key pressed" (e.g., cursor blink
reset).

Add `TopHalfUpdateLastKey(code uint16, value uint32)` — nosplit, called from
NonTimerIRQTopHalf for EV_KEY events:
```go
var lastKeyCode int64
var lastKeyDirty uint32

func TopHalfUpdateLastKey(code uint16, value uint32) {
    if value == 0 {
        atomic.StoreInt64(&lastKeyCode, -int64(code))
    } else {
        atomic.StoreInt64(&lastKeyCode, int64(code))
    }
    atomic.StoreUint32(&lastKeyDirty, 1)
}
```

Flush in timeUpdateLoop() like modifiers.

### Step 3: Button interactor type

**File**: `mazarin/interactor/button.go` (new)

A Button is a leaf interactor (like Label) with additional interactive state:

```go
type Interactor struct {
    // ... existing fields ...

    // Interactive state (value attrs, Set by event goroutines)
    Hovered  *attr.Handle[bool]   // true when mouse is inside bounds
    Pressed  *attr.Handle[bool]   // true when mouse button down inside
    Focused  *attr.Handle[bool]   // true when this element has keyboard focus
    OnClick  func()               // callback invoked on click (press+release inside)
}
```

Add these fields to the existing Interactor struct (not a subtype — all
interactors can potentially be interactive, but only Button/TextInput wire them).

**NewButton(id string, parent *Interactor, label string, bgColor int64, onClick func()) *Interactor**:
- Kind = KindButton (new constant)
- Fixed height = charHeightV + 2*buttonPaddingV (e.g., 8px vertical padding)
- Width = charWidthV * len(label) + 2*buttonPaddingH (e.g., 16px horizontal)
- Centered in parent (same as Label)
- Content = label text (value attr, not constraint)
- Hovered = ValueBool(uri, false)
- Pressed = ValueBool(uri, false)
- Focused = ValueBool(uri, false)
- OnClick stored in struct

**Damage tracking**: Extend leaf_damage_rect.constraint to also compare
Hovered, Pressed, Focused states (if they're wired). Or create a new
`button_damage_rect.constraint` that adds those comparisons.

**Visual rendering in DrawContext.drawNode()**:
- KindButton: draw background (BgColor or hoveredBgColor if Hovered, or
  pressedBgColor if Pressed), then draw text centered.
- Colors: derive from BgColor. Hovered = BgColor lightened by 10%.
  Pressed = BgColor darkened by 10%. Or use separate constraint attributes.

### Step 4: Focus model

**File**: `mazarin/interactor/focus.go` (new)

Focus is tracked per-priest as a value attribute:
```
attr:///priest/{name}/str/focus/current — ID of the focused interactor (empty = none)
```

```go
var focusAttr *attr.Handle[string]

func InitFocus() {
    focusAttr = attr.ValueStr("attr:///priest/"+pName+"/str/focus/current", "")
}

func SetFocus(id string) {
    // Unfocus previous
    if prev := focusAttr.Get(); prev != "" {
        if p, ok := registry[prev]; ok && p.Focused != nil {
            p.Focused.Set(false)
        }
    }
    focusAttr.Set(id)
    if next, ok := registry[id]; ok && next.Focused != nil {
        next.Focused.Set(true)
    }
}

func FocusedID() string {
    return focusAttr.Get()
}
```

**Tab order**: Iterate registry in insertion order (or maintain a separate
focusOrder slice). Tab key advances focus; Shift+Tab reverses.

### Step 5: TextInput interactor type

**File**: `mazarin/interactor/textinput.go` (new)

A TextInput is a leaf interactor that accepts keyboard input:

```go
const KindTextInput Kind = 4  // after KindButton = 3

func NewTextInput(id string, parent *Interactor, placeholder string, textColor int64) *Interactor
```

Additional value attributes:
- `EditBuffer` *attr.Handle[string] — current text content
- `CursorPos` *attr.Handle[int64] — caret position (character index)
- `Placeholder` string — placeholder text (displayed when EditBuffer empty)
- Hovered, Pressed, Focused (same as Button)

Width: fixed or parent-derived (outside-in minus padding).
Height: charHeightV + 2*inputPaddingV.

**Rendering**:
- Background: darker than card (input well)
- Text: EditBuffer content or placeholder in dimmed color
- Cursor: vertical bar at CursorPos * charW (blink via constraint on time?)
- Focused ring: 1px border when focused

### Step 6: Event dispatch goroutine

**File**: `mazarin/interactor/events.go` (new)

A single goroutine per priest that reads input events and updates interactive
state attributes. This bridges the imperative input.Keyboard()/Mouse() channels
to the constraint namespace.

```go
func StartEventLoop(root *Interactor) {
    kbdCh, _ := input.Keyboard()
    mouseCh, _ := input.Mouse()

    // Track absolute mouse position locally (for hit-testing)
    var mouseX, mouseY int

    go func() {
        for {
            select {
            case key := <-kbdCh:
                handleKey(key)
            case mouse := <-mouseCh:
                mouseX += mouse.DX
                mouseY -= mouse.DY // screen Y is inverted from mouse delta
                // Clamp to screen bounds
                handleMouse(root, mouseX, mouseY, mouse)
            }
        }
    }()
}
```

**handleMouse**: Walk interactor tree, test point-in-bounds for each
interactive element. Update Hovered attribute. On button press inside bounds,
set Pressed. On release inside bounds (while Pressed), invoke OnClick.

**handleKey**:
- Tab / Shift+Tab: advance/reverse focus
- If focused element is TextInput: insert Char, handle backspace/delete/arrows
- If focused element is Button: Enter/Space = click
- Modifier-only keys: ignored (handled by kernel attribute)

### Step 7: Dark mode constraint programs

**File**: `mazarin/interactor/constraints/dark_mode_bg.constraint` (new)

A constraint that derives background color from the darkMode kernel attribute:

```go
func dark_mode_bg() int64 {
    dark := deref_bool("_0_")        // attr:///kernel/bool/darkMode
    lightColor := deref_i64("_1_")   // light mode color
    darkColor := deref_i64("_2_")    // dark mode color
    if dark {
        return darkColor
    }
    return lightColor
}
```

This allows interactors to declare: "my background is 0xFF2D2D2D in light mode,
0xFF1A1A1A in dark mode" — the constraint automatically switches when the
kernel darkMode attribute changes.

Similarly for text color:
```go
func dark_mode_text() int64 {
    dark := deref_bool("_0_")
    lightColor := deref_i64("_1_")
    darkColor := deref_i64("_2_")
    if dark {
        return darkColor
    }
    return lightColor
}
```

### Step 8: Extend DrawContext for new types

**File**: `mazarin/interactor/draw.go`

Add rendering for KindButton and KindTextInput in drawNode():

```go
case KindButton:
    // Background varies with interactive state.
    bg := i.BgColor.Get()
    if i.Pressed != nil && i.Pressed.Get() {
        bg = darken(bg, 20)
    } else if i.Hovered != nil && i.Hovered.Get() {
        bg = lighten(bg, 15)
    }
    dc.fillRect(cx0, cy0, cx1, cy1, bg)
    dc.drawText(i, bx0, by0, cx0, cy0, cx1, cy1)
    // Focused ring.
    if i.Focused != nil && i.Focused.Get() {
        dc.drawFocusRing(cx0, cy0, cx1, cy1)
    }

case KindTextInput:
    // Input well background.
    dc.fillRect(cx0, cy0, cx1, cy1, i.BgColor.Get())
    // Text or placeholder.
    content := i.Content.Get()
    if len(content) == 0 && i.Placeholder != "" {
        // Draw placeholder in dimmed color.
        dc.drawTextStr(i.Placeholder, bx0, by0, cx0, cy0, cx1, cy1, dimColor(i.TextColor.Get()))
    } else {
        dc.drawText(i, bx0, by0, cx0, cy0, cx1, cy1)
    }
    // Cursor.
    if i.Focused != nil && i.Focused.Get() {
        cursorX := bx0 + int32(i.CursorPos.Get())*int32(dc.charW)
        if cursorX >= cx0 && cursorX < cx1 {
            dc.drawCursor(cursorX, by0, cy0, cy1, i.TextColor.Get())
        }
    }
```

Helper functions:
- `darken(argb int64, amount int) int64` — reduce R/G/B by amount
- `lighten(argb int64, amount int) int64` — increase R/G/B by amount
- `dimColor(argb int64) int64` — reduce alpha or lower brightness
- `drawFocusRing(x0, y0, x1, y1)` — 1px accent-colored border
- `drawCursor(x, y0, cy0, cy1, color)` — 2px wide vertical bar

### Step 9: Update uitest demo

**File**: `flock/cmd/uitest/main.go`

Replace the clock-only demo with an interactive demo:

```go
func main() {
    sys.UartWriteString("[uitest] main() entered\n")

    attr.Init()
    interactor.Init("uitest")

    // Window → Card → [clock label, button, text input, echo label]
    window := interactor.NewWindow("win", 400, 300, 0xFF2D2D2D)
    window.OriginPoint.Set(vm.Point2DVal(
        int64(regionX)+(int64(regionW)-400)/2,
        int64(regionY)+(int64(regionH)-300)/2,
    ))

    card := interactor.NewCard("card", window, 16, 0xFF3C3C3C)

    // Clock label (same as before).
    clock := interactor.NewLabel("clock", card, 0xFFFFFFFF)
    clock.SetContentValue("00:00:00")

    // Dark mode toggle button.
    btn := interactor.NewButton("darkbtn", card, "Toggle Dark", 0xFF505050, func() {
        // Toggle kernel darkMode attribute.
        // ... (read current, write !current)
    })

    // Text input.
    input := interactor.NewTextInput("input", card, "Type here...", 0xFFCCCCCC)

    // Echo label — mirrors text input.
    echo := interactor.NewLabel("echo", card, 0xFF88FF88)
    echo.SetContentValue("")

    // Wire echo to input's EditBuffer.
    go func() {
        for range attr.OnDirty() {
            echo.Content.Set(input.EditBuffer.Get())
        }
    }()

    // Clock formatting goroutine (same as Phase 8).
    // ...

    // Finalize damage.
    card.FinalizeDamage()
    window.FinalizeDamage()

    // Start event loop.
    interactor.StartEventLoop(window)

    // Draw context + render loop (same pattern as Phase 8).
    dc := interactor.NewDrawContext(fontData, regionX, regionY, regionW, regionH)
    // ...
}
```

### Step 10: Damage tracking for interactive state

The existing leaf_damage_rect.constraint compares bounds, visible, content,
bgColor, textColor. For interactive elements we also need to detect changes in
Hovered, Pressed, Focused.

**Option A**: Create `button_damage_rect.constraint` that adds these comparisons.
This is cleaner but means more constraint programs.

**Option B**: Add LP (lastPainted) mirrors for Hovered/Pressed/Focused to the
Interactor struct and extend the existing leaf_damage_rect with additional
deref slots. Since BindStrings is variadic, the same constraint can take more
URI parameters.

Recommend **Option A** for clarity. Create:
- `button_damage_rect.constraint` — leaf_damage_rect + hovered/pressed/focused
- `textinput_damage_rect.constraint` — leaf_damage_rect + hovered/focused + editBuffer + cursorPos

### Step 11: Build integration

- Add `button_damage_rect.constraint` and `textinput_damage_rect.constraint` to
  `mazarin/interactor/constraints/`
- Re-run compile-constraints (already wired in Taskfile)
- No new Taskfile tasks needed — uitest build already picks up interactor changes
- No TOML config changes needed — uitest already in boot sequence

## Layout Considerations

Phase 8 used a simple layout: one child per parent, centered. Phase 9 needs
multiple children (clock + button + input + echo in one card). This requires:

**Vertical stacking**: Children laid out top-to-bottom with spacing. The card's
internal layout is:
```
[clock label]      — centered, y = card.upperLeft.y + padding
[button]           — centered, y = clock.y + clock.height + spacing
[text input]       — centered, y = button.y + button.height + spacing
[echo label]       — centered, y = input.y + input.height + spacing
```

This can be expressed as offset_point constraints:
```
button.upperLeft = offset_point(clock.upperLeft, 0, clock.height + spacing)
input.upperLeft  = offset_point(button.upperLeft, 0, button.height + spacing)
echo.upperLeft   = offset_point(input.upperLeft, 0, input.height + spacing)
```

The existing `offset_point.constraint` and `center_in_parent.constraint` handle
the X centering. Vertical stacking needs a new constraint or explicit wiring:

**New constraint**: `stack_below.constraint`
```go
func stack_below() Point2D {
    above_ul := deref_point2d("_0_")  // sibling above, upperLeft
    above_h := deref_i64("_1_")       // sibling above, height
    spacing := deref_i64("_2_")       // vertical gap
    parent_ul := deref_point2d("_3_") // parent upperLeft (for X centering)
    parent_w := deref_i64("_4_")      // parent width
    my_w := deref_i64("_5_")          // my width

    x := point2d_x(parent_ul) + (parent_w - my_w) / 2
    y := point2d_y(above_ul) + above_h + spacing
    return point2d(x, y)
}
```

## Testing Strategy

1. Build and run on ARM64 HVF (fastest iteration cycle)
2. Verify uitest launches without attribute creation errors
3. Verify clock label still updates every second
4. Verify button renders with correct text
5. Verify mouse hover changes button appearance (check serial output for
   Hovered attribute changes)
6. Verify click invokes OnClick (toggle darkMode, check serial for attribute
   change)
7. Verify Tab cycles focus (check serial output for focus changes)
8. Verify text input accepts characters (check serial for EditBuffer changes)
9. Verify echo label mirrors text input
10. Run 120s stability test on all 4 platforms

## Non-Goals for Phase 9

- Scroll views or list boxes (Phase 10)
- Animation or transitions (future)
- Multi-window management or compositor (future)
- Drag and drop (future)
- Complex text editing (selection, copy/paste — future)
- Font selection or size changes (future)
- Layout engines (flexbox, grid — future; explicit constraint wiring is fine)

## Dependencies

- Phase 8 complete (interactor framework, uitest priest, DrawContext)
- VirtIO input events delivered to userspace (already working)
- input.Keyboard() and input.Mouse() channels (already working)
- Kernel mouse event dispatch in NonTimerIRQTopHalf (already working for
  soft IRQ delivery; needs TopHalfUpdateMouse addition for attributes)
