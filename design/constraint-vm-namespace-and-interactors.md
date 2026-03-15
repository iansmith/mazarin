# Constraint VM: Namespace, Service Discovery, and Interactor Framework

Ian Smith, March 2026

See `constraint-vm-vdso-architecture.md` for the core shared-page/VDSO architecture.
See `constraint-vm-strategic-plan.md` for the phased implementation plan.

## Overview

The constraint VM's shared attribute system is not just a UI layout engine — it
is a machine-wide interprocess communication mechanism. Priests and .maz modules
publish attributes into a shared namespace, discover each other's attributes
through pattern matching, and react to changes via dirty propagation — all
without explicit IPC setup, polling, or lifecycle management.

This document covers three interconnected architectural extensions:

1. **URI namespace** — a hierarchical naming scheme for attributes, with a
   kernel-maintained trie in the shared pages
2. **Service discovery** — `find`, `deref`, and `exists` as VM builtins,
   with dynamic dependency tracking (read sets during evaluation)
3. **Interactor framework** — standard attributes for UI elements, damage
   rectangles, and the draw protocol

These extensions are what make the constraint system a complete UI programming
model. The constraint implementation is the UI of mazarin.

## Part 1: The `attr:///` URI Namespace

### URI Structure

Every attribute in the system has a URI:

```
attr:///priest/<priest-name-or-id>/<type>/<path...>
attr:///kernel/<type>/<path...>
```

The triple-slash (no authority component) signals "machine-local."

**Segments:**

| Segment | Meaning |
|---|---|
| `priest` or `kernel` | Ownership domain |
| `<priest-name-or-id>` | Priest filename (e.g., `calendar.elf`) if unique, else numeric ID |
| `<type>` | Value type tag: `int64`, `f64`, `bool`, `tribool`, `str`, `point2d`, `rect`, etc. |
| `<path...>` | Hierarchical name path, application-defined |

**Examples:**

```
attr:///kernel/int64/time/utc_seconds
attr:///kernel/bool/darkMode
attr:///kernel/int64/mouse/x
attr:///priest/calendar.elf/str/currentlyDisplayed/eventTitle
attr:///priest/calendar.elf/timespec/currentlyDisplayed/startTime
attr:///priest/transcript.elf/rect/titleLabel/damageRect
attr:///priest/transcript.elf/str/titleLabel/parent
attr:///priest/dapope/bool/focus/leftDown
attr:///priest/dapope/int64/focus/priestId
```

### Why Type in the URI

The type segment provides early type safety at the naming level. A priest
looking for `attr:///priest/*/int64/eventTitle` gets nothing back if the
calendar published it as `str` — the mismatch is caught at discovery time,
not at runtime evaluation. It also makes the namespace self-documenting: you
can inspect what types are published without reading any code.

### Why Hierarchical Paths

Attributes can be grouped by path. A calendar priest publishing information
about the currently displayed event uses:

```
attr:///priest/calendar.elf/str/currentlyDisplayed/eventTitle
attr:///priest/calendar.elf/timespec/currentlyDisplayed/startTime
attr:///priest/calendar.elf/int64/currentlyDisplayed/duration
```

This enables `exists("attr:///priest/calendar.elf/str/currentlyDisplayed")` to
return true if any children exist under that prefix — the presence of the
calendar's "currently displayed" data is itself a queryable fact.

### Namespace Trie in Shared Pages

The kernel maintains a trie over URI segments in the shared pages. This is a
flat, pointer-free structure readable by VDSO code and writable only by the
kernel.

```go
// TrieNode — one per URI segment. Fixed size, no pointers.
type TrieNode struct {
    Segment      [64]byte   // URI segment, NUL-terminated
    FirstChild   uint16     // index of first child (0 = none)
    NextSibling  uint16     // index of next sibling (0 = none)
    AttrSlot     uint16     // attribute slot if leaf (0xFFFF = not a leaf)
    ChildCount   uint16     // total descendants (for exists)
    SeqCounter   uint32     // seqlock protects mutations
    _pad         [48]byte   // pad to 128 bytes
}
```

Example trie when calendar.elf is running:

```
[root]
  └─ "priest"
       └─ "calendar.elf"
            └─ "str"
            │    └─ "currentlyDisplayed"
            │         └─ "eventTitle"    → slot 42
            └─ "timespec"
                 └─ "currentlyDisplayed"
                      └─ "startTime"    → slot 43
```

The VDSO walks this trie to resolve URIs during `deref` — no syscall needed.
Each node's SeqCounter protects against concurrent kernel mutations (attribute
creation/destruction). The VDSO uses seqlock-read protocol: load-acquire
counter, read, load-acquire again, retry if changed.

The trie is allocated in a dedicated region of the shared pages (see updated
page layout in the strategic plan). Initial capacity: 2048 nodes. Growth is
handled by the kernel expanding the region.

### Query Result Attributes

When a priest registers a `find` pattern, the kernel creates a **query result
attribute** — a collection-typed attribute in the shared pages that the kernel
maintains. When the namespace changes (attributes created or destroyed), the
kernel checks registered patterns and updates matching query result collections,
triggering dirty propagation.

Query patterns are registered:
- Explicitly via `attr.Find()` in the Go client library (issues a syscall)
- Implicitly when the VM executes a `FIND` instruction for the first time
  (the kernel registers the pattern and caches the query result slot index
  in a per-constraint resolution table)

Subsequent `FIND` executions read the cached query result slot directly from
shared pages — pure VDSO, no syscall.

### Resolution Cache

Each constraint has a small resolution table in the shared pages (per-priest
writable scratch page):

```
// Per-constraint resolution entry
type ResolutionEntry struct {
    PatternHash  uint64   // hash of the URI pattern or full URI
    SlotIndex    uint16   // resolved attribute slot (or query result slot)
    _pad         [6]byte
}
```

- `FIND` instructions: pattern hash → query result slot index
- `DEREF` instructions: URI hash → attribute slot index

Cache hits skip both the trie walk and the syscall. Cache misses fall through
to the VDSO trie walk (for `deref`) or a kernel syscall (for first-time `find`
pattern registration).

Invalidation: the kernel bumps a generation counter when it tombstones
attributes. The VDSO checks the generation counter before using cached
resolutions. On mismatch, the cache is flushed.

## Part 2: Service Discovery via VM Builtins

### New VM Instructions

**DUP** — duplicate top of stack:
```
OpDup  uint8 = 0x12  // push stack[sp-1] again
```

### New VM Builtins

**`find(pattern str) → collection of str`**

Matches a URI pattern (with `*` wildcards) against the namespace. Returns a
collection of matching URI prefixes.

During evaluation:
1. VDSO looks up the pattern in the constraint's resolution table → cache hit
   returns the query result slot index
2. Cache miss → VDSO issues `SysAttrRegisterQuery` syscall on first call;
   kernel creates the query result attribute, returns slot index; VDSO caches it
3. Seqlock-read the query result collection from shared pages
4. Add the query result slot to the **read set** (dynamic dependency)

**`deref_<type>(uri str) → value or unknown`**

Typed variants: `deref_i64`, `deref_f64`, `deref_bool`, `deref_str`,
`deref_point2d`, `deref_rect`, `deref_tribool`, etc.

Resolves a URI to an attribute slot and reads the value. Returns `unknown`
(tribool) if the URI doesn't resolve or the type doesn't match.

During evaluation:
1. Check resolution cache → hit: read slot directly (seqlock-protected)
2. Miss: walk the namespace trie in shared pages (VDSO, no syscall)
3. Cache the URI → slot mapping
4. Add the resolved slot to the **read set** (dynamic dependency)

**`exists(prefix str) → bool`**

Returns true if any attributes exist under the given URI prefix. Implemented
as a trie walk: find the prefix node, check `ChildCount > 0`.

The VDSO walks the trie (seqlock-protected reads). The prefix node is added
to the read set — when children are added/removed under that prefix,
`ChildCount` changes, the node's SeqCounter bumps, and the constraint is
dirtied.

**`uri_segment(uri str, index i64) → str`**

Extracts a segment from a URI by index. E.g., `uri_segment("attr:///priest/
calendar.elf/str/currentlyDisplayed/eventTitle", 3)` returns
`"currentlyDisplayed"`. Used to decompose URIs discovered via `find`.

**Rectangle builtins** (see Part 3 for the Rectangle type):

```
rect(x0, y0, x1, y1 i64) → Rectangle
rect_union(a, b Rectangle) → Rectangle
rect_intersect(a, b Rectangle) → Rectangle
rect_overlaps(a, b Rectangle) → bool
rect_contains(a, b Rectangle) → bool
rect_empty(a Rectangle) → bool
rect_area(a Rectangle) → i64
rect_width(a Rectangle) → i64
rect_height(a Rectangle) → i64
```

### Dynamic Dependency Tracking (Approach A)

Hudson's Eval/Vite algorithm naturally supports dependencies discovered during
evaluation. The existing VM assumes static dependencies (declared at constraint
creation). Dynamic dependencies extend this:

**During evaluation**, the VM maintains a read set — an array of attribute slot
indices that were accessed via `find`, `deref`, or `exists`:

```go
type ReadSet struct {
    Slots [MaxReadSetSize]uint16
    Count uint16
}
```

The read set is stored in the per-priest scratch page. After evaluation:

1. Compare the new read set with the constraint's stored dependency edges
2. If identical (common case for stable dependencies): no update needed
3. If different: issue `SysAttrUpdateDeps` syscall to the kernel
4. Kernel atomically updates forward edges (deps) and reverse edges (dependents)

**Correctness argument**: The invalidation pass (Vite) uses reverse edges from
the *previous* evaluation. If a constraint's dependencies change, whatever
*caused* the change was itself in the previous read set (e.g., the `find` query
result). So the constraint is correctly dirtied, re-evaluated, and the new
dependencies are discovered.

**Steady-state cost**: When the dependency graph is stable (services are
running, no priest births/deaths), the read set doesn't change between
evaluations. The comparison detects "same" and skips the syscall. Evaluation
is pure VDSO.

### Walkthrough: Cross-Priest Service Discovery

A "transcript finder" app discovers and mirrors the calendar's event title.

**What the app author writes:**

```go
func MazarinMain() {
    label := interactor.NewLabel("titleLabel", parentCard)
    label.BindContent(MustGetProgram("calendarTitle"))
}
```

The `calendarTitle` program (compiled at build time into `.constraint` ELF
section):

```
// Stack machine bytecode (pseudocode)
CONST_STR    "attr:///priest/*/currentlyDisplayed"
CALL_BUILTIN find, 1             // pop pattern, push collection
DUP                               // keep collection for later
CALL_BUILTIN coll_len, 1         // pop coll, push len
CONST_I64    0
EQ           i64
IF
  CONST_STR  "No calendar available"
  RET        1
END_IF
                                   // original collection still on stack (from DUP)
CALL_BUILTIN coll_first, 1        // pop coll, push first URI
CONST_STR    "/str/eventTitle"
CALL_BUILTIN str_concat, 2        // pop two, push concatenated URI
CALL_BUILTIN deref_str, 1         // pop URI, push value or unknown
STORE        0                     // save to local[0]
LOAD         0
CALL_BUILTIN is_unknown, 1        // pop, push bool
IF
  CONST_STR  "Waiting for calendar..."
  RET        1
END_IF
LOAD         0
RET          1
```

**Timeline — calendar not running:**

1. `find` → query result is empty collection → returns "No calendar available"
2. Read set: `{queryResultSlot}`. One dependency.
3. Cost: 0 syscalls (find from resolution cache), 1 syscall (initial dep update)

**Timeline — calendar starts:**

4. Calendar publishes `attr:///priest/calendar.elf/str/currentlyDisplayed/eventTitle`
5. Kernel updates namespace trie, checks query patterns, updates query result
6. Query result dirty → transcript's constraint dirty → re-evaluates
7. `find` returns non-empty → `deref_str` resolves calendar's title → "Team Standup"
8. Read set: `{queryResultSlot, calendarTitleSlot}`. Deps updated.
9. Cost: 0 syscalls for bytecode, 1 syscall for dep update

**Steady state — calendar changes event:**

10. Calendar `Set(eventTitle, "Architecture Review")` → dirty propagation →
    transcript's constraint → re-evaluates → returns new title
11. Read set unchanged → no dep update syscall
12. **Cost: 0 syscalls. Pure VDSO.**

**Calendar dies:**

13. Kernel tombstones calendar's attributes, removes from trie, updates query
14. Query result → empty → constraint re-evaluates → "No calendar available"
15. Read set: `{queryResultSlot}`. Dep update removes calendar edge.

## Part 3: Interactor Framework and Damage Model

### The Rectangle Type

Added to FlatValue as type tag `0x15`:

```go
TypeRectangle = 0x15  // {X0 int64, Y0 int64, X1 int64, Y1 int64}
```

Fits in the 32-byte Data field (4 × 8 = 32 bytes, using all 24 bytes of Data
plus the 8-byte header — actually needs a layout adjustment since Data is only
24 bytes).

**Design decision**: Rectangle stores `{X0, Y0, X1, Y1}` where X0 <= X1 and
Y0 <= Y1 (upper-left and lower-right). Because this requires 32 bytes of
payload (4 × int64), and FlatValue.Data is 24 bytes, Rectangle is one of
the types that requires the full Data field. Since Point3D and PointF3D
already use all 24 bytes, and Rectangle needs 32, we either:

(a) Expand FlatValue.Data to 32 bytes (making FlatValue 40 bytes total), or
(b) Store Rectangle as two Point2D values using 2 slots, or
(c) Use int32 coordinates (sufficient for pixel-space UI: max 2 billion pixels)

**Recommendation**: option (c) — int32 coordinates. `{X0, Y0, X1, Y1 int32}`
fits in 16 bytes. Screen coordinates never exceed int32 range. This keeps
FlatValue at 32 bytes. If sub-pixel or world-space coordinates are needed
later, a `RectF` type with float64 can be added.

```go
// Rectangle in FlatValue.Data (16 bytes used of 24 available)
// X0 int32 at offset 0
// Y0 int32 at offset 4
// X1 int32 at offset 8
// Y1 int32 at offset 12
```

**EMPTY_RECT sentinel**: `{0, 0, 0, 0}` — zero area. `rect_empty` checks
`X0 >= X1 || Y0 >= Y1`.

### Standard Interactor Attributes

Every interactor publishes a standard set of attributes into the namespace.
These are created by the interactor library (`mazarin/interactor` or similar),
not by app code.

For an interactor with id `lbl1` in priest `transcript.elf`:

**Value attributes** (Set-able):

```
.../point2d/lbl1/originPoint          position in parent's coordinate system
.../str/lbl1/parent                   parent interactor ID ("" for window roots)
.../int64/lbl1/lastPaintedWidth       dapope writes after painting
.../int64/lbl1/lastPaintedHeight      dapope writes after painting
.../point2d/lbl1/lastPaintedUpperLeft dapope writes after painting
.../bool/lbl1/lastPaintedVisible      dapope writes after painting
.../str/lbl1/lastPaintedContent       dapope writes after painting
.../int64/lbl1/lastPaintedBgColor     dapope writes after painting
.../int64/lbl1/lastPaintedTextColor   dapope writes after painting
.../rect/lbl1/lastPaintedBounds       dapope writes (union of UL/LR)
```

**Constraint attributes** (computed, Get-only — Set panics):

```
.../int64/lbl1/width                  f(content, font, parent constraints)
.../int64/lbl1/height                 f(content, font, maxWidth, wrapping)
.../point2d/lbl1/upperLeft            f(parent.upperLeft, originPoint)
.../point2d/lbl1/lowerRight           f(upperLeft, width, height)
.../rect/lbl1/bounds                  f(upperLeft, lowerRight) → Rectangle
.../bool/lbl1/visible                 f(app-specific, e.g., parent.selectedIndex)
.../str/lbl1/content                  f(app-specific, e.g., calendar deref)
.../int64/lbl1/bgColor                f(darkMode, app-specific palette)
.../int64/lbl1/textColor              f(darkMode, app-specific palette)
.../rect/lbl1/damageRect              f(visual attrs, lastPainted, children)
```

### Set() Prohibition on Constrained Attributes

This is a fundamental rule of the constraint system, inherited from Hudson's
original work and standard practice in constraint-based systems:

- **Value attributes**: writable via Set(). No constraint. Source of truth is
  the last Set() call.
- **Constraint attributes**: read-only via Get(). Value comes from evaluating
  bytecode. Calling Set() panics with a clear error message.

This prevents state confusion. `drawingDirty` / `damageRect` cannot be manually
set — it is always computed from truth. The ONLY way `damageRect` becomes
non-empty is when a visual attribute changes. The ONLY way it becomes empty
is when dapope updates `lastPainted*` values after painting.

The existing `attr` package in `mazarin/attr/attribute.go` already distinguishes
value vs constraint. Enforcement at the Set() callsite with panic.

### The damageRect Constraint

Each interactor's `damageRect` is the central mechanism for determining what
needs repainting. It replaces the more complex `drawingDirty` boolean.

**Leaf interactor damageRect** (e.g., label):

```
// Pseudocode for label.damageRect bytecode
func compute(width, lastPaintedWidth, height, lastPaintedHeight int64,
             upperLeft, lastPaintedUpperLeft point2d,
             visible, lastPaintedVisible bool,
             content, lastPaintedContent str,
             bgColor, lastPaintedBgColor int64,
             textColor, lastPaintedTextColor int64,
             bounds, lastPaintedBounds rect) rect {

    if width != lastPaintedWidth ||
       height != lastPaintedHeight ||
       upperLeft != lastPaintedUpperLeft ||
       visible != lastPaintedVisible ||
       content != lastPaintedContent ||
       bgColor != lastPaintedBgColor ||
       textColor != lastPaintedTextColor {
        // Damaged — include both current and previous bounds
        // to handle shrinking (exposed parent region)
        return rect_union(bounds, lastPaintedBounds)
    }
    return EMPTY_RECT
}
```

**Parent interactor damageRect** — varies by parent type (see Damage
Propagation Policies below):

The parent's damageRect depends on its own visual attribute changes AND on
changes to children's bounds and visibility. This is what captures the exposed
region problem: when a child shrinks, the parent's damageRect includes the
region that was previously covered by the child.

The parent discovers children via `find` + `deref` on the `parent` attribute.
This is expressed in the parent's damageRect constraint bytecode. Dynamic
dependency tracking ensures that when children are added/removed, the parent's
damageRect constraint is re-evaluated.

### Damage Propagation Policies

Each parent type has its own damageRect constraint that encodes its damage
policy. These are library-provided constraints, compiled at build time.

**Naive parent** (mark whole self damaged if any child changed):

```
func compute(ownDamageRect rect) rect {
    children := find("attr:///priest/me/str/*/parent")
    for _, c := range children {
        if deref_str(c) != myId { continue }
        childId := uri_segment(c, 3)
        childDamage := deref_rect(childId + "/damageRect")
        if !rect_empty(childDamage) {
            return rect_union(ownDamageRect, myBounds)
        }
    }
    return ownDamageRect
}
```

This is often fast enough in practice. A window with 20 interactors redraws
in microseconds. Start here.

**Precise parent** (tight bounding box of damage):

```
func compute(ownDamageRect rect) rect {
    result := ownDamageRect
    children := find(...)
    for _, c := range children {
        // ... filter to my children ...
        childDamage := deref_rect(childId + "/damageRect")
        if !rect_empty(childDamage) {
            result = rect_union(result, childDamage)
        }
        // Also check if child bounds changed (exposed region)
        childBounds := deref_rect(childId + "/bounds")
        childLastBounds := deref_rect(childId + "/lastPaintedBounds")
        if childBounds != childLastBounds {
            result = rect_union(result, rect_union(childBounds, childLastBounds))
        }
    }
    return result
}
```

**Deck of cards** (ignore non-selected children):

```
func compute(ownDamageRect rect, selectedIndex int64) rect {
    result := ownDamageRect
    children := find(...)
    childIdx := 0
    for _, c := range children {
        // ... filter to my children ...
        if childIdx == selectedIndex {
            childDamage := deref_rect(childId + "/damageRect")
            result = rect_union(result, childDamage)
        }
        childIdx = childIdx + 1
    }
    return result
}
```

Non-selected children's damage is ignored entirely. They can be changing
wildly — their constraints evaluate, their damageRects become non-empty —
but the deck's constraint never reads them, so the deck is not dirtied.

**Row with overlap awareness**:

```
func compute(ownDamageRect rect) rect {
    result := ownDamageRect
    children := find(...)
    for _, c := range children {
        childDamage := deref_rect(childId + "/damageRect")
        if rect_empty(childDamage) { continue }
        // If children overlap due to width crunch, expand damage to neighbors
        for _, neighbor := range children {
            neighborBounds := deref_rect(neighborId + "/bounds")
            if rect_overlaps(childDamage, neighborBounds) {
                result = rect_union(result, neighborBounds)
            }
        }
        result = rect_union(result, childDamage)
    }
    return result
}
```

### The Draw Protocol

Drawing is imperative, tree-ordered, and owned by the priest. The constraint
system provides the damageRect and visibility; the draw protocol provides
sequencing and clipping.

**Key principle**: The damage rect IS the clipping rect. Dapope (for windows)
or the parent interactor sets the clipping rectangle to the computed damageRect
before invoking draw. The interactor draws freely — anything outside the
clipping rect is clipped by the rendering layer. No need to pass damage
rectangles around during drawing.

**Three-phase draw** (per interactor):

```
draw(clipRect):
    if rect_empty(rect_intersect(clipRect, myBounds)):
        return                           // I'm outside the damaged region
    if !visible:
        return

    1. PRE-DRAW:  draw own background, chrome, etc.
    2. CHILDREN:  for each child in z-order:
                      if child.visible:
                          childClip = rect_intersect(clipRect, myBounds)
                          child.draw(childClip)
    3. POST-DRAW: draw decorations over children (borders, shadows)
```

Pre-draw and post-draw allow parents to render both behind and in front of
their children. A parent with fancy borders draws the border in post-draw,
after children have rendered — the border appears on top.

**Who calls draw()**:

- **Dapope** calls draw on window roots. It discovers windows via
  `find("attr:///priest/*/str/*/parent")` filtered for parent="" (root
  interactors). It calls draw on dirty windows with clipRect = the window's
  damageRect.
- **Inside a window**, the priest's interactor library handles the tree walk.
  Each parent recurses to visible children with the clipping rect set to
  `rect_intersect(parentClipRect, parentBounds)`.

The draw call is a delegated operation — dapope sends a "draw your window"
message to the priest via the delegation mechanism. The priest walks its own
tree. Dapope only coordinates cross-priest z-order.

**Deck of cards parent draw**:

```
draw(clipRect):
    preDraw(clipRect)          // draw deck background
    for i, child := range children:
        if i == selectedIndex && child.visible:
            child.draw(rect_intersect(clipRect, myBounds))
    postDraw(clipRect)         // draw deck border
```

Only the selected child is drawn. Other children are skipped regardless of
their damage state.

### After Painting: Clearing Damage

After dapope (or the priest's draw code) paints an interactor, it updates the
`lastPainted*` value attributes:

```go
attr.Set(interactorId, "lastPaintedWidth", currentWidth)
attr.Set(interactorId, "lastPaintedHeight", currentHeight)
attr.Set(interactorId, "lastPaintedUpperLeft", currentUpperLeft)
attr.Set(interactorId, "lastPaintedBounds", currentBounds)
attr.Set(interactorId, "lastPaintedVisible", currentVisible)
attr.Set(interactorId, "lastPaintedContent", currentContent)
attr.Set(interactorId, "lastPaintedBgColor", currentBgColor)
attr.Set(interactorId, "lastPaintedTextColor", currentTextColor)
```

This triggers re-evaluation of the interactor's damageRect constraint. Since
current values now match lastPainted values, damageRect evaluates to EMPTY_RECT.
Change-gated propagation: damageRect went from non-empty to empty → propagates
to parent. Parent's damageRect re-evaluates → if all children are empty and own
damage is empty → EMPTY_RECT. Damage clears up the tree automatically.

## Part 4: Layout Patterns

### Inside-Out vs Outside-In

These are different constraint configurations on the same standard attributes.

**Outside-in** (window is king — children constrain to parent):

```
window.width = 400                                            // value, fixed
card.width   = constraint: min(label.width + 2*padding,
                                deref_i64(parent + "/width"))  // clamp to parent
label.width  = constraint: min(textWidth(content, font),
                                deref_i64(parent + "/innerWidth"))
```

Changes flow inward. The window never becomes dirty from children because its
size is fixed. The card's width is clamped — label text changes don't
propagate past the card's min().

**Inside-out** (content is king — parents expand):

```
label.width  = constraint: textWidth(content, font)           // natural size
card.width   = constraint: label.width + 2*padding            // fit content
window.width = constraint: clamp(card.width + 2*margin, 200, 1024)  // expand, with limits
```

Changes flow outward. When label text gets longer, card expands, window
might expand (until clamped at 1024). Once the clamp is hit, further
expansion stops — change-gated propagation at the clamp boundary.

### The `visible` Attribute

`visible` is a bool constraint attribute. It is the universal show/hide
mechanism:

- **Deck of cards**: `child[i].visible = constraint: i == deck.selectedIndex`
- **Checkbox toggle**: `detailCard.visible = constraint: deref_bool(checkbox + "/checked")`
- **Minimum size**: `widget.visible = constraint: width >= 20 && height >= 14`
- **Row overflow**: `child.visible = constraint: lowerRight.x <= deref_i64(parent + "/width")`

Because visible is a constraint (not a value), it cannot be Set() directly.
To hide a child, change the value attribute that the visible constraint depends
on (e.g., set `selectedIndex`, uncheck the checkbox, resize the window).

When visible transitions true→false, the interactor's damageRect includes the
lastPaintedBounds — ensuring the parent repaints over the now-hidden interactor.

### darkMode and Non-Geometric Changes

Not all drawingDirty changes are geometric. darkMode toggling changes colors
but not sizes:

```
attr:///kernel/bool/darkMode = false

label.bgColor   = constraint: if deref_bool("attr:///kernel/bool/darkMode")
                               then 0x1a1a1a else 0xffffff
label.textColor = constraint: if deref_bool("attr:///kernel/bool/darkMode")
                               then 0xffffff else 0x1a1a1a
```

When darkMode toggles, bgColor and textColor change. The label's damageRect
constraint depends on bgColor and textColor — it detects that they differ from
lastPaintedBgColor and lastPaintedTextColor → damageRect becomes non-empty →
repaint. Size is unchanged. Correct.

The damageRect formula covers ALL visual changes — geometry, color, content,
visibility — because it compares every visual attribute against its lastPainted
counterpart.

### What the App Author Writes

The interactor library handles all the standard attribute publishing, damage
computation, and lastPainted management. The app author writes:

```go
func MazarinMain() {
    window := interactor.NewWindow("transcriptWindow", 400, 600)
    card := interactor.NewCard("card1", window,
        interactor.Padding(10),
        interactor.CenterH(), interactor.CenterV())
    label := interactor.NewLabel("titleLabel", card)

    // Bind content to the calendar discovery constraint
    label.BindContent(MustGetProgram("calendarTitle"))

    // That's it. No OnDirty. No draw loop. No damage tracking.
    // No lifecycle management for the calendar priest.
}
```

`interactor.NewLabel(name, parent)` publishes all standard attributes. The
width/height constraints know font metrics. The card's centering constraints
compute the label's originPoint. The damageRect constraint compares visual attrs
against lastPainted. All library code.

The app has zero imperative code for drawing, damage, or service discovery.

## Part 5: Updated Shared Page Layout

The shared pages now include additional regions:

```
+---------------------------+ offset 0
| Page Header               |
|   magic, version          |
|   slot count, free bitmap |
|   region offsets/sizes    |
|   generation counter      |  ← for resolution cache invalidation
+---------------------------+ offset H
| Attribute Node Array      |
|   [node 0: 128 bytes]     |
|   ...                     |
+---------------------------+ offset A
| Edge Array Region         |
|   [uint16 slot indices]   |
+---------------------------+ offset E
| Bytecode Region           |
|   [Inst: 16 bytes each]   |
+---------------------------+ offset B
| String Data Region        |
|   [256-byte string slots] |
+---------------------------+ offset S
| Collection Data Region    |
|   [FlatValue: 32 bytes]   |
+---------------------------+ offset C
| Namespace Trie Region     |  ← NEW
|   [TrieNode: 128 bytes]   |
+---------------------------+ offset T
| Per-Priest Scratch Pages  |  ← NEW (one per priest, writable by that priest)
|   Resolution caches       |
|   Read sets during eval   |
+---------------------------+ offset P
| Free/growth space         |
+---------------------------+ end of mapped region
```

The namespace trie and per-priest scratch pages are new regions. The trie is
mapped read-only to all priests (kernel-writable). The scratch pages are
per-priest, mapped writable only to the owning priest.

## Part 6: Syscall Interface

New syscalls for the namespace and discovery mechanism:

| Syscall | Purpose |
|---|---|
| `SysAttrCreate(uri, type, kind, bytecode, len)` | Create attribute with URI |
| `SysAttrWrite(uri, value)` | Write to value attribute (panics on constraint) |
| `SysAttrRegisterQuery(pattern)` | Register find pattern, returns query result slot |
| `SysAttrUpdateDeps(slot, readSet, count)` | Update dependency edges after eval |
| `SysAttrResolveURI(uri)` | Resolve URI to slot (fallback if VDSO trie miss) |

Existing syscalls from the strategic plan are unchanged. The URI parameter
replaces the previous name-based lookup.

## Open Questions

1. **Trie vs hash table for namespace**: Trie supports prefix queries (exists)
   and wildcard matching (find) naturally. Hash table is faster for exact
   lookups (deref). Using both (trie for structure, hash at each level for
   fast segment lookup) adds complexity. Start with trie only?

2. **Per-priest scratch page size**: How large? The resolution cache and read
   set are bounded by the fuel limit (can't read more attributes than
   instructions executed). 4KB (one page) may be sufficient.

3. **Interactor ID uniqueness**: Within a priest, interactor IDs must be
   unique. Across priests, they're disambiguated by the priest name in the
   URI. Should the kernel enforce uniqueness, or leave it to the library?

4. **Draw delegation mechanism**: How exactly does dapope tell a priest "draw
   your window"? Via the existing syscall delegation infrastructure? Via a
   new attribute-based signaling mechanism? The delegation infrastructure
   (SyscallDelegate) exists but hasn't been tested for this use case.

5. **Font metrics in constraints**: Label width/height constraints need font
   measurement. Where does the font data live? In the priest's address space
   (accessed during VDSO evaluation)? In shared pages? This is a practical
   blocker for real layout constraints.

6. **Z-order**: How is cross-priest z-order expressed? As kernel-published
   attributes? As a dapope-maintained ordering? As attributes on window
   interactors?

## References

1. Hudson, S.E. Incremental Attribute Evaluation: A Flexible Algorithm for
   Lazy Update. ACM Transactions on Programming Languages and Systems 13, 3
   (July 1991), 315-341.
2. Hudson, S.E. and Smith, I. Ultra-Lightweight Constraints. Proc. UIST '96,
   ACM Press (1996), 147-155.
3. McCanne, S. and Jacobson, V. The BSD Packet Filter: A New Architecture for
   User-level Packet Capture. Proc. Winter 1993 USENIX Conference, USENIX
   Association (1993), 259-270.
4. Linton, M.A., Vlissides, J.M., and Calder, P.R. Composing User Interfaces
   with InterViews. IEEE Computer 22, 2 (February 1989), 8-22.
