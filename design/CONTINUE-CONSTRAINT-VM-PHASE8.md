# Constraint VM Phase 8 — Interactor Framework

## Context

Phases 1–7 are complete. The constraint VM has:
- Flat shared-page attribute storage with dirty propagation (kernel DFS walk)
- Handle[T] client API with lazy evaluation on Get()
- Cascading constraint chains (evaluate-on-deref)
- Eager dirty notification via WaitDirty (per-priest queue, thread blocking)
- Change-gated propagation (skip if bitwise-equal value)
- handletest priest with 8 passing tests on all 3 architectures
- Kernel-published attributes: time (utc_seconds, utc_nanos), screen (width,
  height), darkMode, input modifiers, system info (ram, cpu, budget), timezone,
  charWidth, charHeight (font metrics, added in this phase)
- clocktest priest: discovers kernel time via deref, WaitDirty loop prints
  HH:MM:SS to serial once per second
- Rectangle type (int32 coords, 16 bytes) with 9 builtins: rect, rect_union,
  rect_intersect, rect_overlaps, rect_contains, rect_empty, rect_area,
  rect_width, rect_height
- Point2D type (int64 coords) with constructors and accessors
- Service discovery: find, deref_*, exists, uri_segment, is_unknown
- Display resolution: 1728×1117 (resource height 2234 for scrolling)
- Fonts on FAT32 disk at `/fonts/` (106 files, cached base image build)
- 120s+ stability on ARM64 TCG, x86_64, RISC-V

**Problem**: All constraint usage so far is textual (clocktest prints to serial).
The constraint system has no connection to visual display. There is no way to
express a UI element's position, size, appearance, or damage state as
constraints. A GUI application would need to manually track what changed and
what to redraw — exactly the imperative mess that constraint-based UI avoids.

**Solution**: Phase 8 adds the interactor framework. An interactor is a UI
element (window, card, label) that publishes a standard set of attributes into
the namespace. Layout (position, size, bounds) is expressed as constraints.
Damage tracking (what needs repainting) is a constraint comparing current visual
state against last-painted state. The library handles all attribute creation,
constraint generation, and damage computation. App authors compose interactors;
the system handles the rest.

**Milestone**: Static UI demo — a window containing a card with a centered
label. The label displays clock time from kernel attributes. Damage tracking
works: when the time changes, only the label region repaints. Uitest coexists
with stdio and dapope on the same 1728×1117 framebuffer, each owning a
hard-coded screen region (stdio left half, uitest right half). No interactivity
(that's Phase 9).

## Existing Infrastructure

Already in place (no changes needed unless noted):

- `mazarin/attr/` — Handle[T] API:
  - `ValueI64/F64/Bool/Str/Composite/Rectangle/Point2D(uri, initial)` — value
    attribute creation
  - `ConstraintI64/F64/Bool/Str/Composite(uri, prog, deps...)` — constraint
    creation. **Programs are immutable after creation** — no replacement API.
  - `Handle[T].Get()` — reads value, evaluates constraint if dirty
  - `Handle[T].Set(v)` — writes value attribute
  - `Handle[T].SetEager(bool)` — enables WaitDirty notification
  - `WaitDirty() []uint16` — blocks until eager attributes become dirty
  - `OnDirty() <-chan []uint16` — channel-based alternative
  - `HandleAny` — type-erased interface (Slot(), URI())
  - `Init()` — reads SharedPageHeader from VA 0x00007FFD00000000
- `mazarin/vm/` — VM runtime:
  - `vm.Program{Code []Inst, Strings []string, NumArgs, ArgTypes, Funcs, Entry}`
  - `vm.Inst{Opcode, Typ, Op1, Op2, Flags uint16, Imm uint64}` — 128-bit
  - `vm.RunWithResolver(prog, resolver)` — executes bytecode
  - `Program.Marshal() []byte` / `UnmarshalProgram([]byte)` — serialization
- `mazarin/vm/compile/` — source compiler:
  - `compile.Compile(src string) (*Result, error)` — compiles restricted Go
    subset to verified `*vm.Program`
  - Accepts: int64, float64, bool, string, collections, if/else, for-range,
    all 31+ builtins, multi-function programs, type conversions
  - Rejects: pointers, goroutines, closures, recursion, unbounded loops
  - Result: `{Program *vm.Program, Func string}`
- `mazarin/vm/flat/` — shared-page types:
  - `FlatValue` (32 bytes), `FlatAttrNode` (128 bytes)
  - Type tags: TypeI64=0x01, TypeBool=0x03, TypeStr=0x05,
    TypePoint2D=0x0A, TypeRectangle=0x15
  - `AttrKindValue=0, AttrKindConstraint=1`
  - `FlagDirty=1<<0, FlagEagerNotify=1<<1`
- `kmazarin/ksyscall/constraint_kernel.go` — kernel attrs:
  - `KernelAttrCreate(uri, valueType) (slot, ok)`
  - `KernelAttrWriteI64/Bool/Str(slot, val)`
  - `PublishKernelAttributes()` — time, screen, darkMode, modifiers
  - `PublishSystemAttributes()` — ram, cpu, budget
  - `PublishBootConfigAttributes()` — timezone, goMemLimitMB, gcPercentage
  - `StartKernelAttrUpdaters()` — timeUpdateLoop goroutine
- `mazarin/sys/` — syscall wrappers:
  - `GetFramebuffer() (*FramebufferInfo, error)` — returns VA, width, height,
    pitch
  - `FlushFramebuffer(x, y, w, h)` — triggers GPU transfer+flush
  - `AttrCreate, AttrWrite, AttrWriteResult, AttrWriteString, AttrAddDep,
    AttrUpdateDeps, AttrSetEager, AttrWaitDirty` — constraint syscalls
- `flock/cmd/stdio/main.go` — framebuffer rendering reference:
  - Embeds `AtkinsonHyperlegibleMono-Regular.otf` via `//go:embed`
  - Parses with `opentype.Parse` + `opentype.NewFace` (16pt, 72 DPI, full hinting)
  - `charW = face.GlyphAdvance('M').Ceil()`
  - `charH = (metrics.Ascent + metrics.Descent).Ceil()`
  - Pre-renders all printable ASCII (32-126) into pixel buffers at init
  - Uses `image.RGBA` wrapping framebuffer memory, direct pixel blitting
  - GPU uses BGRA format — `bgraColor(r,g,b,a)` swaps R↔B so RGBA writes
    produce correct BGRA
- Builtins available (selected):
  - Math: min, max, clamp (int64); minf, maxf, clampf, sqrt, floor, ceil (f64)
  - String: str_len, str_concat, str_contains, str_substr, str_prefix, str_suffix
  - Rect: rect, rect_union, rect_intersect, rect_overlaps, rect_contains,
    rect_empty, rect_area, rect_width, rect_height
  - Point2D: point2d, point2d_x, point2d_y
  - Service: find, deref_i64, deref_str, deref_bool, deref_rect, deref_point2d,
    exists, uri_segment, is_unknown
  - Collections: coll_len, coll_get, coll_take, coll_drop, coll_sort, coll_concat

## Implementation Plan

### Step 1: Build-time constraint compiler tool

**File**: `cmd/compile-constraints/main.go` (new)

A Go tool (installed via `go tool`) that compiles `.constraint` source files
into a Go source file containing `*vm.Program` literals.

**Usage**:
```bash
$GO tool compile-constraints -pkg interactor \
    -o mazarin/interactor/programs_gen.go \
    mazarin/interactor/constraints/*.constraint
```

**Implementation**:
1. For each `.constraint` file, read the source string
2. Call `compile.Compile(src)` to get `*Result`
3. Emit a Go source file with one `var Prog<Name> = &vm.Program{...}` per file
4. The variable name is derived from the filename:
   `bounds_from_ulwh.constraint` → `ProgBoundsFromULWH`
5. The `vm.Program` is emitted as a Go struct literal with `[]vm.Inst` slice,
   `[]string` table, etc. — no serialization/deserialization at runtime

**Template for generated code**:
```go
// Code generated by compile-constraints. DO NOT EDIT.
package interactor

import "mazzy/mazarin/vm"

var ProgBoundsFromULWH = &vm.Program{
    Code: []vm.Inst{
        {Opcode: 0x01, Typ: 0x0A, Op1: 0, ...},
        ...
    },
    Strings:  []string{},
    NumArgs:  3,
    ArgTypes: []uint8{0x0A, 0x01, 0x01},
    Entry:    0,
}
```

**Register as Go tool**: Add to `go.tool` in `go.mod` or use the existing
tool registration pattern (same as `safe-serial-read`, `mkfat32`, etc.).

### Step 2: Constraint source files

**Directory**: `mazarin/interactor/constraints/` (new)

Each file contains one function in the restricted Go subset. The function's
parameter types define the constraint's argument types. The return type defines
the constraint's output type.

**`constant_true.constraint`** — always returns true:
```go
func constant_true() bool {
    return true
}
```

**`constant_empty_str.constraint`** — always returns "":
```go
func constant_empty_str() string {
    return ""
}
```

**`identity_i64.constraint`** — returns arg[0] unchanged:
```go
func identity_i64(val int64) int64 {
    return val
}
```

**`identity_point2d.constraint`** — returns arg[0] unchanged:
```go
func identity_point2d(p point2d) point2d {
    return p
}
```

Note: `point2d` is not a native Go type. The compiler must recognize it as a
builtin composite type. Check whether `compile.Compile` supports composite
types as function parameters. If not, the constraint may need to use
`deref_point2d` to read the dependency rather than receiving it as an argument.
**Fall back to deref-based programs if needed.**

**`bounds_from_ulwh.constraint`** — `rect(ul.x, ul.y, ul.x+w, ul.y+h)`:
```go
func bounds_from_ulwh(ulURI string, wURI string, hURI string) rect {
    ulx := point2d_x(deref_point2d(ulURI))
    uly := point2d_y(deref_point2d(ulURI))
    w := deref_i64(wURI)
    h := deref_i64(hURI)
    return rect(int64(int32(ulx)), int64(int32(uly)),
                int64(int32(ulx+w)), int64(int32(uly+h)))
}
```

Note: rect() takes int32-range values packed into int64 args. The int32 cast
ensures correct range. Check if the compiler supports these casts; if not,
use the values directly (they'll be small enough for screen coordinates).

**`center_in_parent.constraint`** — centered UL point:
```go
func center_in_parent(pulURI string, pwURI string, phURI string,
                      mwURI string, mhURI string) point2d {
    pulx := point2d_x(deref_point2d(pulURI))
    puly := point2d_y(deref_point2d(pulURI))
    pw := deref_i64(pwURI)
    ph := deref_i64(phURI)
    mw := deref_i64(mwURI)
    mh := deref_i64(mhURI)
    x := pulx + (pw - mw) / 2
    y := puly + (ph - mh) / 2
    return point2d(x, y)
}
```

**`offset_point.constraint`** — add origin to parent UL:
```go
func offset_point(pulURI string, originURI string) point2d {
    pulx := point2d_x(deref_point2d(pulURI))
    puly := point2d_y(deref_point2d(pulURI))
    ox := point2d_x(deref_point2d(originURI))
    oy := point2d_y(deref_point2d(originURI))
    return point2d(pulx + ox, puly + oy)
}
```

**`label_width.constraint`** — `charWidth * str_len(content)`:
```go
func label_width(contentURI string, charWidthURI string) int64 {
    content := deref_str(contentURI)
    cw := deref_i64(charWidthURI)
    return str_len(content) * cw
}
```

**`outside_in_dim.constraint`** — `parent_dim - 2*padding`:
```go
func outside_in_dim(parentDimURI string, padding int64) int64 {
    pd := deref_i64(parentDimURI)
    return pd - 2 * padding
}
```

**`leaf_damage_rect.constraint`** — compare visual state vs lastPainted:
```go
func leaf_damage_rect(boundsURI string, lpBoundsURI string,
                      visibleURI string, lpVisibleURI string,
                      contentURI string, lpContentURI string,
                      bgColorURI string, lpBgColorURI string,
                      textColorURI string, lpTextColorURI string) rect {
    bounds := deref_rect(boundsURI)
    lpBounds := deref_rect(lpBoundsURI)

    vis := deref_bool(visibleURI)
    lpVis := deref_bool(lpVisibleURI)
    if vis != lpVis {
        return rect_union(bounds, lpBounds)
    }

    content := deref_str(contentURI)
    lpContent := deref_str(lpContentURI)
    if content != lpContent {
        return rect_union(bounds, lpBounds)
    }

    bgc := deref_i64(bgColorURI)
    lpBgc := deref_i64(lpBgColorURI)
    if bgc != lpBgc {
        return rect_union(bounds, lpBounds)
    }

    tc := deref_i64(textColorURI)
    lpTc := deref_i64(lpTextColorURI)
    if tc != lpTc {
        return rect_union(bounds, lpBounds)
    }

    return rect(0, 0, 0, 0)
}
```

**`parent_damage_rect.constraint`** — own damage + children damage union.
This program needs child damageRect URIs as string arguments. Since the
interactor tree is fixed at construction time, the library passes the child
URIs as string constants when creating the constraint:
```go
func parent_damage_rect(boundsURI string, lpBoundsURI string,
                        visibleURI string, lpVisibleURI string,
                        bgColorURI string, lpBgColorURI string,
                        textColorURI string, lpTextColorURI string,
                        childDamageURI string) rect {
    // Check own visual state
    bounds := deref_rect(boundsURI)
    lpBounds := deref_rect(lpBoundsURI)
    vis := deref_bool(visibleURI)
    lpVis := deref_bool(lpVisibleURI)
    bgc := deref_i64(bgColorURI)
    lpBgc := deref_i64(lpBgColorURI)
    tc := deref_i64(textColorURI)
    lpTc := deref_i64(lpTextColorURI)

    ownDamage := rect(0, 0, 0, 0)
    if vis != lpVis {
        ownDamage = rect_union(bounds, lpBounds)
    }
    if bgc != lpBgc {
        ownDamage = rect_union(bounds, lpBounds)
    }
    if tc != lpTc {
        ownDamage = rect_union(bounds, lpBounds)
    }

    // Union with child damage
    childDmg := deref_rect(childDamageURI)
    if !rect_empty(childDmg) {
        if rect_empty(ownDamage) {
            return childDmg
        }
        return rect_union(ownDamage, childDmg)
    }
    return ownDamage
}
```

Note: This handles one child. For multiple children, either: (a) create a
separate program per parent with N child URIs, or (b) chain parent damage rects
(parent's damage = union of own damage + first child's damage, then that result
is unioned with second child's damage via another constraint). For Phase 8,
the demo has one child per parent (window→card→label), so one-child is fine.

**`clock_format.constraint`** — deref kernel time, format HH:MM:SS:
```go
func clock_format(timeURI string) string {
    sec := deref_i64(timeURI)
    h := (sec / 3600) % 24
    m := (sec / 60) % 60
    s := sec % 60

    // Build "HH:MM:SS" using string concatenation
    // Note: no zero-padding available in VM string ops.
    // If zero-padding is needed, use int_to_str or similar.
    // Fallback: compute each digit separately.
    h1 := h / 10
    h2 := h % 10
    m1 := m / 10
    m2 := m % 10
    s1 := s / 10
    s2 := s % 10
    // This requires int_to_str or digit-to-char conversion.
    // If the VM lacks int_to_str, this constraint must be done in Go.
    ...
}
```

**Clock format fallback**: If the VM's string operations cannot produce
zero-padded digit formatting (no `int_to_str` builtin or similar), use a
Go goroutine instead:
```go
go func() {
    for range attr.OnDirty() {
        sec := timeHandle.Get()
        h, m, s := (sec/3600)%24, (sec/60)%60, sec%60
        timeStrHandle.Set(fmt.Sprintf("%02d:%02d:%02d", h, m, s))
    }
}()
```
This makes `content` a value attribute updated by Go code rather than a
pure VM constraint. Acceptable for Phase 8.

### Step 3: Interactor package — core types

**File**: `mazarin/interactor/interactor.go` (new)

```go
package interactor

import (
    "mazzy/mazarin/attr"
    "mazzy/mazarin/vm"
    "mazzy/mazarin/vm/flat"
)

type Kind uint8

const (
    KindWindow Kind = iota
    KindCard
    KindLabel
)

// Interactor represents a UI element in the constraint attribute namespace.
type Interactor struct {
    ID       string
    Kind     Kind
    Parent   *Interactor
    Children []*Interactor

    // Full standard attribute set — all interactors get all of these.
    // Layout (constraints — read via Get)
    Width      *attr.Handle[int64]
    Height     *attr.Handle[int64]
    UpperLeft  *attr.Handle[vm.Value]  // Point2D
    Bounds     *attr.Handle[vm.Value]  // Rectangle
    Visible    *attr.Handle[bool]
    DamageRect *attr.Handle[vm.Value]  // Rectangle

    // Visual state
    Content   *attr.Handle[string]     // Str
    BgColor   *attr.Handle[int64]      // ARGB as int64
    TextColor *attr.Handle[int64]      // ARGB as int64

    // Geometry value (Set-able)
    OriginPoint *attr.Handle[vm.Value] // Point2D

    // LastPainted mirrors (value attrs — Set by draw loop after painting)
    LPBounds    *attr.Handle[vm.Value] // Rectangle
    LPVisible   *attr.Handle[bool]
    LPContent   *attr.Handle[string]
    LPBgColor   *attr.Handle[int64]
    LPTextColor *attr.Handle[int64]

    // Private
    priestName string
    padding    int64
}
```

Package-level state:

```go
var (
    registry   map[string]*Interactor
    priestName string
    charWidth  int64
    charHeight int64
    screenW    int64
    screenH    int64
)

// Init initializes the interactor library. Must be called after attr.Init().
// Reads charWidth, charHeight, screen width/height from kernel attributes
// via deref. priestName is used for URI construction.
func Init(name string) {
    priestName = name
    registry = make(map[string]*Interactor)

    // Read font metrics from kernel attributes.
    charWidth = readKernelI64("attr:///kernel/int64/screen/charWidth")
    charHeight = readKernelI64("attr:///kernel/int64/screen/charHeight")
    screenW = readKernelI64("attr:///kernel/int64/screen/width")
    screenH = readKernelI64("attr:///kernel/int64/screen/height")
}

// readKernelI64 creates a temporary deref constraint, reads the value, and
// returns it. Used for one-time reads of kernel attributes at init.
func readKernelI64(uri string) int64 { ... }
```

**URI construction**: Each attribute is published under the priest's namespace:
```
attr:///priest/<priestName>/int64/<id>/width
attr:///priest/<priestName>/int64/<id>/height
attr:///priest/<priestName>/point2d/<id>/upperLeft
attr:///priest/<priestName>/rect/<id>/bounds
attr:///priest/<priestName>/bool/<id>/visible
attr:///priest/<priestName>/rect/<id>/damageRect
attr:///priest/<priestName>/str/<id>/content
attr:///priest/<priestName>/int64/<id>/bgColor
attr:///priest/<priestName>/int64/<id>/textColor
attr:///priest/<priestName>/point2d/<id>/originPoint
attr:///priest/<priestName>/rect/<id>/lpBounds
attr:///priest/<priestName>/bool/<id>/lpVisible
attr:///priest/<priestName>/str/<id>/lpContent
attr:///priest/<priestName>/int64/<id>/lpBgColor
attr:///priest/<priestName>/int64/<id>/lpTextColor
```

Helper:
```go
func (i *Interactor) uri(typePath, attrName string) string {
    return "attr:///priest/" + i.priestName + "/" + typePath + "/" + i.ID + "/" + attrName
}
```

### Step 4: Interactor constructors

**File**: `mazarin/interactor/window.go` (new)

`NewWindow(id string, width, height int64, bgColor int64) *Interactor`:

Creates all standard attributes. Constraint programs use deref with URI
strings to read dependencies (since programs are pre-compiled at build time
and cannot be parameterized with slot numbers — they use URIs instead).

Value attributes created:
- `originPoint` — Point2D(0, 0) for windows
- `lpBounds` — empty rect
- `lpVisible` — false
- `lpContent` — ""
- `lpBgColor` — 0
- `lpTextColor` — 0

Constraint attributes created:
- `width` — `ProgIdentityI64` with a value attr holding the fixed width as dep
  (or: create a value attr `attr:///priest/.../int64/<id>/fixedWidth` = width,
  then the width constraint derefs it. This indirection exists because programs
  are pre-compiled and can't embed constants — constants come through as value
  attributes that the program reads via deref.)
- `height` — same pattern
- `upperLeft` — `ProgIdentityPoint2D` reading originPoint via deref
- `bounds` — `ProgBoundsFromULWH` with URI strings for upperLeft, width, height
- `visible` — `ProgConstantTrue`
- `bgColor` — identity reading a value attr holding the color constant
- `textColor` — identity reading a value attr (0 for window)
- `content` — `ProgConstantEmptyStr`
- `damageRect` — `ProgLeafDamageRect` (or parent variant) with URIs for all
  visual state + lastPainted attrs

**Parameterization pattern**: Since constraint programs are pre-compiled
bytecode, they cannot embed literal constants. All parameterization flows
through deref:

1. Create a value attribute for the parameter:
   `fixedWidth := attr.ValueI64(uri("int64", "fixedWidth"), width)`
2. Create the constraint using a deref-based program:
   `ProgIdentityI64` reads one URI argument → `deref_i64(uri)` → returns it
3. Pass the value attr's URI as a string argument to the constraint

This is the fundamental pattern: **value attrs hold constants, constraint
programs read them via deref**. The deref infrastructure handles dependency
tracking automatically.

**File**: `mazarin/interactor/card.go` (new)

`NewCard(id string, parent *Interactor, padding int64, bgColor int64) *Interactor`:
- Same value attributes as window
- Sets `padding` field
- Constraint `width` uses `ProgOutsideInDim` reading parent's width URI
  and a value attr holding the padding constant
- Constraint `height` uses `ProgOutsideInDim` reading parent's height URI
- Constraint `upperLeft` uses `ProgCenterInParent` reading parent's UL, parent
  width/height, own width/height URIs
- Adds self to `parent.Children`

**File**: `mazarin/interactor/label.go` (new)

`NewLabel(id string, parent *Interactor, textColor int64) *Interactor`:
- Same value attributes as window/card
- Constraint `width` uses `ProgLabelWidth` reading content URI and kernel
  charWidth URI
- Constraint `height` — identity reading kernel charHeight
- Constraint `upperLeft` uses `ProgOffsetPoint` reading parent's UL and own
  originPoint
- **Content is NOT created here** — deferred to `BindContent()`
- The width constraint uses `deref_str(contentURI)` which returns "" until
  the content constraint is created. Once created, the deref dependency is
  automatically tracked, and width re-evaluates.
- Adds self to `parent.Children`

**File**: `mazarin/interactor/bind.go` (new)

```go
// BindContent creates the content constraint attribute for a label.
// Must be called after NewLabel and before the draw loop starts.
// The program must return a string.
func (i *Interactor) BindContent(prog *vm.Program, deps ...attr.HandleAny) {
    contentURI := i.uri("str", "content")
    i.Content = attr.ConstraintStr(contentURI, prog, deps...)
}

// SetContentValue creates a string value attribute for content (instead of
// a constraint). The caller updates it manually. Used when the content
// formatting is done in Go rather than in a VM program.
func (i *Interactor) SetContentValue(initial string) {
    contentURI := i.uri("str", "content")
    i.Content = attr.ValueStr(contentURI, initial)
}
```

### Step 5: Font metrics kernel attributes

**File**: `kmazarin/ksyscall/constraint_kernel.go`

Add two new slot variables:
```go
var (
    slotCharWidth  uint16
    slotCharHeight uint16
)
```

Add to `PublishKernelAttributes()`, after darkMode:
```go
slotCharWidth, ok = KernelAttrCreate(
    "attr:///kernel/int64/screen/charWidth", flat.TypeI64)
if !ok {
    serial.RawUARTPuts("[attr] FAIL: kernel/int64/screen/charWidth\r\n")
    return
}
slotCharHeight, ok = KernelAttrCreate(
    "attr:///kernel/int64/screen/charHeight", flat.TypeI64)
if !ok {
    serial.RawUARTPuts("[attr] FAIL: kernel/int64/screen/charHeight\r\n")
    return
}
```

Write the values. These must match AtkinsonHyperlegibleMono-Regular.otf at
16pt, 72 DPI, full hinting. Determine the actual values by checking stdio's
serial output — it prints `[stdio] Font: %dx%d px`. Hard-code those values:
```go
KernelAttrWriteI64(slotCharWidth, <charW>)   // from stdio log
KernelAttrWriteI64(slotCharHeight, <charH>)  // from stdio log
```

Update the log message to include charWidth/charHeight:
```go
serial.RawUARTPuts("[attr] kernel attributes published (time, screen, darkMode, modifiers, charMetrics)\r\n")
```

### Step 6: Interactor tree walk and draw support

**File**: `mazarin/interactor/draw.go` (new)

The draw support layer reads constraint values and renders to the framebuffer.
Priest-side code only. Uses the same rendering techniques as stdio.

```go
type DrawContext struct {
    fb       *sys.FramebufferInfo
    im       *image.RGBA      // wraps framebuffer memory
    glyphs   [95][]byte       // pre-rendered ASCII 32-126
    charW    int
    charH    int
    ascent   int
    // Screen region owned by this priest
    regionX  int
    regionY  int
    regionW  int
    regionH  int
}
```

**`NewDrawContext(regionX, regionY, regionW, regionH int) *DrawContext`**:
1. Call `sys.GetFramebuffer()` to get VA, width, height, pitch
2. Wrap framebuffer as `image.RGBA` (same as stdio):
   ```go
   fbPix := unsafe.Slice((*byte)(unsafe.Pointer(fb.Addr)),
       int(fb.Pitch)*int(fb.Height))
   im := &image.RGBA{Pix: fbPix, Stride: int(fb.Pitch),
       Rect: image.Rect(0, 0, int(fb.Width), int(fb.Height))}
   ```
3. Parse embedded font, create face (16pt, 72 DPI, full hinting)
4. Pre-render ASCII glyphs (same `renderGlyphSet` pattern as stdio)
5. Store region bounds

**`DrawTree(root *Interactor, clipRect [4]int32)`**:
1. Read `root.Visible.Get()` — skip if false
2. Read `root.Bounds.Get()` — extract rect coords
3. Compute intersection with clipRect and priest's region — skip if empty
4. Dispatch by Kind:
   - KindWindow: fill background with `root.BgColor.Get()` (BGRA pixel fill)
   - KindCard: fill background with `root.BgColor.Get()`
   - KindLabel: blit text from `root.Content.Get()` using pre-rendered glyphs,
     color from `root.TextColor.Get()`
5. Recurse into `root.Children`
6. Call `updateLastPainted(root)` — sets all LP* values to current state

**`updateLastPainted(i *Interactor)`**:
```go
i.LPBounds.Set(i.Bounds.Get())
i.LPVisible.Set(i.Visible.Get())
i.LPContent.Set(i.Content.Get())
i.LPBgColor.Set(i.BgColor.Get())
i.LPTextColor.Set(i.TextColor.Get())
```
This triggers dirty propagation through damageRect, which re-evaluates and
returns EMPTY_RECT (current matches lastPainted). Change-gated propagation
stops the walk.

**Rendering details**:
- GPU uses BGRA format. Use `bgraColor(r,g,b,a)` (swap R↔B) for all colors
- Rect fill: iterate rows, write uint32 pixels via unsafe.Pointer
- Text: blit pre-rendered glyph pixel buffers (same as stdio's `blitChar`)
- After drawing, call `sys.FlushFramebuffer(x, y, w, h)` for dirty region
- Clip all coordinates to the priest's screen region — never write outside it

### Step 7: Demo priest — uitest

**File**: `flock/cmd/uitest/main.go` (new)

Copy `AtkinsonHyperlegibleMono-Regular.otf` into `flock/cmd/uitest/` for
the `//go:embed` directive. (Same file as stdio uses.)

```go
package main

import (
    "embed"
    "fmt"

    "mazzy/mazarin/attr"
    "mazzy/mazarin/interactor"
    "mazzy/mazarin/sys"
    "mazzy/mazarin/vm/flat"
)

//go:embed AtkinsonHyperlegibleMono-Regular.otf
var fontData []byte

const (
    // Right half of 1728×1117 display
    regionX = 864
    regionY = 0
    regionW = 864
    regionH = 1117
)

func main() {
    sys.UartWriteString("[uitest] main() entered\n")

    // 1. Initialize constraint system and interactor library.
    attr.Init()
    interactor.Init("uitest")

    // 2. Create interactor tree.
    window := interactor.NewWindow("win", 400, 200, 0xFF2D2D2D)
    card := interactor.NewCard("card", window, 16, 0xFF3C3C3C)
    label := interactor.NewLabel("clock", card, 0xFFFFFFFF)

    // 3. Bind label content to kernel time.
    //    Option A: Use pre-compiled VM constraint (if string formatting works).
    //    Option B: Use Go goroutine for formatting (simpler, guaranteed).
    //    Using Option B for Phase 8:
    label.SetContentValue("00:00:00")

    // Create a deref handle for kernel time
    timeProg := ... // ProgIdentityI64 dereffing kernel time URI
    timeSec := attr.ConstraintI64(
        "attr:///priest/uitest/int64/time_sec", timeProg)
    timeSec.SetEager(true)

    go func() {
        for range attr.OnDirty() {
            sec := timeSec.Get()
            h, m, s := (sec/3600)%24, (sec/60)%60, sec%60
            label.Content.Set(fmt.Sprintf("%02d:%02d:%02d", h, m, s))
        }
    }()

    // 4. Mark the window's damageRect as eager.
    window.DamageRect.SetEager(true)

    // 5. Create draw context with screen region.
    dc := interactor.NewDrawContext(regionX, regionY, regionW, regionH)

    // 6. Initial full draw.
    dc.DrawTree(window, [4]int32{
        int32(regionX), int32(regionY),
        int32(regionX + regionW), int32(regionY + regionH),
    })

    fmt.Println("[uitest] entering draw loop")

    // 7. WaitDirty loop — react to changes.
    for {
        attr.WaitDirty()

        dmg := window.DamageRect.Get()
        r := flat.AsRectCoords(dmg) // [x0, y0, x1, y1]
        if r[0] == r[2] && r[1] == r[3] {
            continue // empty rect
        }

        dc.DrawTree(window, r)
    }
}
```

The uitest priest:
- Draws in the right half of the screen (x=864..1727)
- Coexists with stdio (left half) and dapope (input handling)
- Updates the clock label once per second via kernel time attribute
- Damage tracking repaints only the label region on each update

### Step 8: Build integration

**File**: `Taskfile.yml`

Add `compile-constraints` task:
```yaml
compile-constraints:
  desc: Compile constraint source files to Go bytecode
  sources:
    - 'mazarin/interactor/constraints/*.constraint'
    - 'cmd/compile-constraints/*.go'
  generates:
    - 'mazarin/interactor/programs_gen.go'
  cmds:
    - '{{.GO}} tool compile-constraints -pkg interactor
       -o mazarin/interactor/programs_gen.go
       mazarin/interactor/constraints/*.constraint'
```

Add `uitest` build target for all 3 architectures, following the handletest
pattern:
- Build `flock/cmd/uitest` as a priest ELF
- Apply userspace overlay (or merged priest overlay if it imports mazhost)
- Include in disk image
- Depends on `compile-constraints`

**File**: `config/kmazarin-arm64.toml` (and x86_64, riscv64)

Add uitest to the existing boot sequence (after clocktest):
```toml
[[priest]]
name = "uitest"
path = "/uitest.elf"
```

## Verification

1. Build all 3 architectures (no compile errors)
2. `compile-constraints` produces `programs_gen.go` without errors
3. Run handletest on all 3 — all 8 tests still pass (no regression)
4. Run on ARM64 TCG:
   - Stdio's serial console renders in the left half of the screen
   - Uitest's window renders in the right half (x=864+)
   - Window background (dark gray, 400×200 px) visible
   - Card background (slightly lighter) centered in window
   - Label shows "HH:MM:SS" centered in card
   - Time updates once per second — only the label region redraws
   - 60s stability — no panics, both stdio and uitest update independently
   - No pixel bleed between left/right halves
5. Run on x86_64 — same visual verification
6. Run on RISC-V — same visual verification
7. Verify damage tracking via serial breadcrumbs: after each time update,
   print damageRect coords — should be non-empty, then EMPTY_RECT after draw
8. Verify clocktest still prints time to serial (no regression)

## Syscall summary

No new syscalls. Phase 8 uses the existing Phase 3-7 syscall infrastructure.
The interactor library creates attributes via SysAttrCreate / SysAttrWrite /
SysAttrWriteResult. The only kernel change is publishing charWidth/charHeight
attributes (Step 5).

## Files changed (estimated)

### New files
- `cmd/compile-constraints/main.go` — build-time constraint compiler tool
- `mazarin/interactor/interactor.go` — core types, Init, registry
- `mazarin/interactor/window.go` — NewWindow
- `mazarin/interactor/card.go` — NewCard
- `mazarin/interactor/label.go` — NewLabel
- `mazarin/interactor/bind.go` — BindContent, SetContentValue
- `mazarin/interactor/draw.go` — DrawContext, DrawTree, rendering
- `mazarin/interactor/constraints/*.constraint` — ~10 constraint source files
- `mazarin/interactor/programs_gen.go` — generated bytecode (build step)
- `flock/cmd/uitest/main.go` — demo priest
- `flock/cmd/uitest/AtkinsonHyperlegibleMono-Regular.otf` — embedded font

### Modified files
- `kmazarin/ksyscall/constraint_kernel.go` — add charWidth/charHeight slots
  and writes in PublishKernelAttributes
- `Taskfile.yml` — compile-constraints task, uitest build tasks (3 archs),
  uitest in disk images
- `config/kmazarin-arm64.toml` — add uitest to boot
- `config/kmazarin-x86_64.toml` — add uitest to boot
- `config/kmazarin-riscv64.toml` — add uitest to boot

### Not modified
- `kmazarin/ksyscall/constraint_syscall.go` — no new syscalls
- `mazarin/attr/` — no changes (used as-is)
- `mazarin/vm/` — no changes (compiler used as library by build tool)
- `mazarin/vm/compile/` — no changes (used as library by build tool)
- `flock/cmd/dapope/` — not modified (still runs, owns input)
- `flock/cmd/stdio/` — not modified (still runs, owns left half)

## Design decisions

### Screen regions (hard-coded, no protocol)
Uitest and stdio coexist on the 1728×1117 framebuffer. Regions are hard-coded
at compile time: stdio draws at x=pad (40px), uitest draws at x=864+. Dapope
handles input events. No runtime coordination — each priest clips drawing to
its region. This is simple and sufficient for the demo. Dynamic screen region
assignment (via kernel attributes or a window manager) is Phase 9+.

### Full attribute set per interactor
Every interactor creates all ~15 standard attributes regardless of kind. A
Window has a `content` attribute (always ""), a Label has `lpTextColor`
(always tracked). This is uniform, easy to introspect, and avoids per-kind
branching in generic code (tree walks, damage propagation). The cost is ~5
extra attributes per interactor.

### Build-time constraint compilation
Constraint programs are `.constraint` source files compiled by
`cmd/compile-constraints` into `*vm.Program` Go literals. No parser or
type-checker in the runtime binary. Programs are pre-verified at build time.
The compile-constraints tool uses the existing `compile.Compile()` API.

### Deref-based parameterization
Pre-compiled constraint programs cannot embed literal constants (the bytecode
is fixed at build time). All parameterization flows through deref: create a
value attribute holding the constant, and the constraint reads it via
`deref_i64(uri)` or similar. This adds value attributes for constants
(window width, padding, etc.) but keeps the build-time compiler simple.

### Programs are immutable after creation
The current attr API does not support replacing a constraint's program after
creation. This means labels use a two-phase approach: `NewLabel()` creates
all attributes except `content`, then `BindContent()` or `SetContentValue()`
creates the content attribute. The width constraint uses `deref_str(contentURI)`
which returns "" until content exists; once created, the deref dependency is
automatically tracked and width re-evaluates.

### Go goroutine for clock formatting
The VM's string builtins (`str_concat`, `str_substr`) do not include
`int_to_str` or zero-padded formatting. Rather than adding new builtins in
Phase 8, the clock time is formatted in Go (`fmt.Sprintf("%02d:%02d:%02d")`)
and written to a string value attribute. The label's width constraint still
reacts automatically when content changes. Pure VM string formatting can be
added as a builtin in a later phase.

### Outside-in layout
Cards use outside-in layout: `card.width = parent.width - 2*padding`. The
window is fixed-size. The card fills the window minus padding. The label is
sized by its content. This is the simplest layout model for the demo.

### LastPainted drives damage clearing
After drawing, `updateLastPainted()` sets all LP* value attributes to match
current visual state. This causes damageRect constraints to re-evaluate: since
current equals lastPainted, the result is EMPTY_RECT. Change-gated propagation
stops the dirty walk at damageRect. Damage clears up the tree automatically.

### One frame behind on first render
On startup, all lastPainted values are zero/empty/false. DamageRect
immediately evaluates to non-empty. The first WaitDirty wakes immediately.
The draw loop renders the full window and updates lastPainted. From then on,
only actual changes trigger redraws.
