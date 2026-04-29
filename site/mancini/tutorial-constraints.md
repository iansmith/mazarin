---
layout: default
title: "Tutorial: Constraints in Mancini"
author: iansmith
---

# Tutorial: Constraints in Mancini

This tutorial builds a small application that demonstrates how mancini's
constraint system works.  We will build an AppWindow containing a Row with
two children: a Column of three Labels, and a sibling Label alongside it.
Every label displays its own Y position — computed by the constraint
system — as its text.  The sibling label's height is derived from a
custom constraint that averages the Y positions of the last two labels
in the column.

By the end you will understand:

- What constraints are and how they differ from imperative layout
- How `.vgo` programs are compiled into constraint bytecodes
- How to bind constraint placeholders to real attribute URIs
- How to write a custom constraint
- The lifecycle of a change: from attribute mutation through damage
  propagation to screen repaint
- How to build and run a shepherd on mazarin

## What Are Constraints?

In most UI toolkits, layout is imperative: you compute sizes and positions
in code and assign them.  When something changes — a window resizes, a child
becomes invisible — you either recompute manually or use a layout manager
(like a VBox or grid) that handles it for you.  Layout managers work, but
they are opaque: the rules live inside the manager's implementation, and
you cannot easily express cross-cutting relationships between unrelated
parts of the tree.

Mancini uses a **declarative constraint system** instead.  Each interactor
publishes its layout properties (X, Y, Width, Height, Visible) as
**attributes** in a shared namespace.  Some attributes are plain values
that you set directly.  Others are **constraints** — small programs that
compute their value from other attributes.

For example, a Column's Height is a constraint that sums its visible
children's heights plus spacing.  When a child's height changes or a child
becomes invisible, the Column's height recomputes automatically,
synchronously, and fast — no extra code required on your part.

### The Attribute System

Every attribute has a **URI** like:

```
attr:///shepherd/42/int64/my_label/layout/Height
```

This URI encodes:
- The shepherd (process) that owns it
- The data type (`int64`, `bool`, `str`, `rect`)
- The interactor name (`my_label`)
- The property (`layout/Height`)

Attributes come in two flavors:

- **Value attributes** — set imperatively via `.Set(v)`, read via `.Get()`.
  A label's X and Y are value attributes; the Column's Draw method sets
  them as it positions each child.

- **Constraint attributes** — computed by a `.vgo` program that
  dereferences other attributes.  A Column's Width and Height are
  constraints that discover children and sum their dimensions.

The constraint system is reactive: when an attribute that a constraint
depends on changes, the constraint is marked dirty.  The main loop calls
`attr.WaitDirty()` to sleep until something changes, then redraws.

### The `.vgo` Language

Constraints are written in `.vgo` files — a restricted subset of Go with
a handful of built-in functions:

| Built-in | Description |
|----------|-------------|
| `derefI64("uri")` | Read an `int64` attribute by URI |
| `derefBool("uri")` | Read a `bool` attribute by URI |
| `derefRect("uri")` | Read a `Rect` attribute by URI |
| `findWhere("pattern", value)` | Find all attributes matching a URI pattern whose value equals `value` |
| `uriSegment(uri, n)` | Extract the Nth segment from a URI |
| `collEmpty()`, `collPush(c, v)` | Build a string slice |
| `rectUnion(a, b)` | Compute the bounding rectangle of two rectangles |
| `rectEmpty(r)` | Test whether a rectangle has zero area |
| `rect(x1, y1, x2, y2)` | Construct a rectangle |

A `.vgo` file may contain **multiple functions**.  Helper functions come
first; the **lexically last function** is the entry point whose return
type determines the attribute type (`int64`, `bool`, `Rect`).  This is how
the `children.vgo` library works — it defines helper functions like
`childHeight`, `isChildVisible`, and `findVisibleChildren`, and the
importing `.vgo` file's last function calls them.

Here is the simplest possible constraint — it just forwards another
attribute's value:

```go
// identity_i64.vgo
func identityI64() int64 {
    return derefI64("_source_")
}
```

The string `"_source_"` is a **placeholder**.  At runtime, `BindStrings`
replaces it with a real attribute URI:

```go
prog := mancini.BindStrings(mancini.ProgIdentityI64,
    "_source_", someAttribute.URI())
result := attr.ConstraintI64(myURI, prog)
```

The compiled bytecodes live in generated `.vbc.go` files (e.g.,
`identity_i64.vbc.go`).  You never edit these — they are rebuilt by
`compile-constraints` whenever you change the `.vgo` source.

### Placeholders and Binding

Placeholders are underscore-bracketed names: `_source_`, `_maxHeight_`,
`_myName_`.  The `BindStrings` function replaces them in the program's
string table:

```go
// Replace _a_ and _b_ with real attribute URIs
prog := mancini.BindStrings(myProg,
    "_a_", labelA.GetLayout().Y.URI(),
    "_b_", labelB.GetLayout().Y.URI())
```

For constraints that need to discover children (like Column's height),
`BindStringsChildren` adds the standard child-discovery placeholders
automatically.

## The Application We Are Building

```
AppWindow "Constraint Demo"
 └─ Row (hMargin=8, spacing=20)
     ├─ Column (vMargin=4, spacing=12, maxHeight=9999)
     │   ├─ Label "alpha"   — text shows its own Y position
     │   ├─ Label "beta"    — text shows its own Y position
     │   └─ Label "gamma"   — text shows its own Y position
     └─ Label "sibling"     — text shows its own Y position
                              height = avg(beta.Y, gamma.Y)
```

Each label's text is dynamic: it reads its own Y attribute from the
constraint system and displays `"Y = 147"` (or whatever the computed
value is).  The sibling label's height is a custom constraint that
averages the Y positions of the last two column children.  This is a
deliberately unusual constraint — it makes the sibling's height depend
on where the Column positions beta and gamma, so if the window moves
on screen, the sibling's height changes too.

## Step 1: Write the Custom Constraint

Create a file `maz/demo/average_y.vgo`:

```go
func averageY() int64 {
    y1 := derefI64("_y1_")
    y2 := derefI64("_y2_")
    return (y1 + y2) / 2
}
```

This compiles to `average_y.vbc.go` containing `var ProgAverageY`.
The two placeholders `_y1_` and `_y2_` will be bound to the Y
attributes of the beta and gamma labels.

## Step 2: Write the Shepherd

### How a shepherd talks to rachel

Before we look at the code, it helps to understand how a shepherd
communicates with rachel (the window manager).

Shepherds and rachel talk over **uring IPC** — a kernel-allocated
shared-memory ring of fixed-size 128-byte message slots, with one ring
per shepherd. Messages are typed by a 32-bit `Protocol` discriminator
in the envelope. The relevant protocols for window management are:

| Protocol | Direction | Carries |
|----------|-----------|---------|
| `ipc.ProtoWMNotify` | shepherd → rachel | `AppStart` (window registration), `Blit` (frame ready) |
| `ipc.ProtoShepherdNotify` | rachel → shepherd | `YouHaveFocus`, `YouLostFocus`, mouse / keyboard events, `BackingStoreReady` |
| `ipc.ProtoFontResponse` | fontsvc → shepherd | font cache replies (handled by `mazarin/fontcache`) |

You don't write the wire framing yourself. On the **send** side, the
mancini standard library ships helpers like
[`AppWindow.AnnounceToWM`](https://pkg.go.dev/mazzy/mazarin/mancini/std)
and `AppWindow.SendBlit` that do the encoding plus `uring.Send` for
you. On the **receive** side, you use a `uring.Dispatcher`: register
one decoder per protocol, then call `Start()` and the dispatcher pumps
incoming messages into typed Go channels in a background goroutine.

Every shepherd must also publish a `Ready` flag through
`sys.SetReady(true)` once its uring dispatcher and constraint
attributes are wired up. rachel and other shepherds use
`sys.WaitForShepherdReady` to block until this flips. The three core
services — `fs`, `rachel`, `linux` — are conventionally waited on as a
group via `sys.WaitForCoreServices`.

### The Code

Create `maz/demo/main.go`:

```go
package main

import (
    "fmt"

    "golang.org/x/image/font"

    "mazzy/mazarin/attr"
    "mazzy/mazarin/fontcache"
    "mazzy/mazarin/mancini"
    "mazzy/mazarin/mancini/std"
    mctheme "mazzy/mazarin/mancini/theme"
    "mazzy/mazarin/sys"
    "mazzy/mazarin/uring"
    mfont "mazzy/shared/font"
    "mazzy/shared/ipc"
    "mazzy/shared/wm"
)

// app is the root interactor — set during buildUI, read by the
// uring dispatcher when rachel sends focus messages.
var app *std.AppWindow

// wmCh receives typed WM messages from the uring Dispatcher.
var wmCh = make(chan any, 4)

func main() {
    sys.UartWriteString("[demo] main() entered\n")

    // ── 1. Initialize the constraint system ──
    attr.Init()
    mancini.Init()

    // ── 2. Wait for the core services (fs, rachel, linux) ──
    if err := sys.WaitForCoreServices(20); err != nil {
        panic(fmt.Sprintf("[demo] FATAL: core services: %v", err))
    }

    // ── 3. Set up fonts, palette, and theme ──
    rachelSID := sys.MustGetShepherdByName("rachel")
    fc := fontcache.New(rachelSID)

    // Start the uring dispatcher BEFORE the first OpenFace call —
    // OpenFace blocks waiting for FontResponse, which arrives via the
    // dispatcher.
    startUringDispatcher(fc)

    pal := mctheme.NewDefaultPaletteSwapRB()
    resolver := func(family string, feature mancini.Feature, size int64) font.Face {
        style := mfont.Regular
        if feature == mancini.Bold {
            style = mfont.Bold
        }
        return fc.OpenFaceByName(family, style, size)
    }
    theme := mctheme.NewTheme(pal, mctheme.NewDefaultNeumorphicParams(),
        mfont.DefaultMono, 18, resolver)

    // ── 4. Build the interactor tree ──
    alpha, beta, gamma, sibling := buildUI(pal, theme)

    // Wire each label's text to display its own Y position.
    for _, lbl := range []*std.Label{alpha, beta, gamma, sibling} {
        lh := lbl.GetLayout()
        lbl.TextFunc = func() string {
            return fmt.Sprintf("Y = %d", lh.Y.Get())
        }
    }

    // ── 5. Make the root damage rect eager ──
    //
    // WaitDirty only wakes when an eager attribute is dirty. The
    // AppWindow's DamageRect is the root of the damage tree — every
    // child's damage propagates upward through the parent damage
    // constraints. Making it eager means the main loop wakes whenever
    // anything in the window needs repainting.
    appLH := app.GetLayout()
    appLH.Damage.DamageRect.SetEager(true)

    // ── 6. Announce ourselves to rachel ──
    //
    // AnnounceToWM sends an AppStart message via uring with our
    // desired window size and initial screen position. Rachel
    // allocates a backing store and (in due course) replies with
    // BackingStoreReady on the same dispatcher.
    app.RachelSID = rachelSID
    app.AnnounceToWM(0, 0, 500, 320)

    // Tell other shepherds we are ready.
    sys.SetReady(true)

    // ── 7. Main loop ──
    for {
        x, y := appLH.X.Get(), appLH.Y.Get()
        w, h := appLH.Width.Get(), appLH.Height.Get()
        app.Draw(app, x, y, w, h, app.GetLayout().Bounds.Get())
        app.SendBlit()  // tell rachel "frame ready, please composite"
        attr.WaitDirty()
    }
}

// startUringDispatcher wires our uring ring 0 reader. The dispatcher
// goroutine pumps font replies into fc.ReplyCh and WM notifications
// into wmCh.
func startUringDispatcher(fc *fontcache.FontCache) {
    d := uring.NewDispatcher()
    d.On(ipc.ProtoShepherdNotify, wm.DecodeShepherdNotify, wmCh)
    d.On(ipc.ProtoFontResponse, wm.DecodeFontResponse, fc.ReplyCh)
    d.Start()

    // Drain wmCh in another goroutine — focus events flip the
    // AppWindow's Focused flag and trigger a full-window repaint.
    go func() {
        for msg := range wmCh {
            switch m := msg.(type) {
            case wm.YouHaveFocus:
                app.Focused = true
                app.FullDamage()
            case wm.YouLostFocus:
                app.Focused = false
                app.FullDamage()
            case wm.BackingStoreReady:
                _ = m
                app.FullDamage()
            }
        }
    }()
}

// buildUI constructs the interactor tree and the custom constraint.
//
//   AppWindow "Constraint Demo"
//    └─ Row
//        ├─ Column
//        │   ├─ alpha
//        │   ├─ beta
//        │   └─ gamma
//        └─ sibling (height = avg of beta.Y and gamma.Y)
//
func buildUI(pal mancini.Palette, theme mancini.Theme) (
    *std.Label, *std.Label, *std.Label, *std.Label) {

    app = std.NewAppWindow(pal, "Constraint Demo")
    app.Focused = false // wait for rachel to grant focus

    row := std.NewRow("main_row", "AppWindow", pal, 0, mancini.AxisMinimum, 8)
    row.SetSpacing(20)

    col := std.NewColumn("demo_col", "main_row", pal, 9999, mancini.AxisMinimum, 4, 0, false)
    col.SetSpacing(12)

    fontSize := int64(18)
    alpha := std.NewLabelNamed("alpha", "demo_col", theme, "Y = ?", fontSize)
    beta  := std.NewLabelNamed("beta",  "demo_col", theme, "Y = ?", fontSize)
    gamma := std.NewLabelNamed("gamma", "demo_col", theme, "Y = ?", fontSize)

    // Sibling label with a custom constraint: height = avg(beta.Y, gamma.Y).
    sibLH := mancini.NewLayoutAttributes("sibling", "main_row")
    sibLH.Width.Set(120)
    sibLH.Height = attr.ConstraintI64(
        mancini.LayoutURI("sibling", mancini.DataTypeInt64, mancini.LayoutHeight),
        mancini.BindStrings(ProgAverageY,
            "_y1_", beta.GetLayout().Y.URI(),
            "_y2_", gamma.GetLayout().Y.URI()))
    sibling := std.NewLabel(sibLH, theme, "Y = ?", fontSize)

    return alpha, beta, gamma, sibling
}
```

Notice what is **not** here: no manual `MailboxSend` / `MailboxRecv`,
no ringbuf allocation, no hand-written message framing. The
`AppWindow` helper methods (`AnnounceToWM`, `SendBlit`) and the
`uring.Dispatcher` together cover the WM protocol; `fontcache.New`
plus the dispatcher cover font replies; everything else is layout
constraints and draw.

> **Note — production-grade boot.** The example above is simplified to
> keep the focus on constraints. A real shepherd does a touch more
> after `AnnounceToWM`: it **synchronously waits** for a
> `wm.BackingStoreReady` message from rachel (rachel allocates the
> backing store and tells the shepherd its address, dimensions, and
> insets), then constructs an `image.RGBA` over that shared memory
> and a `DrawContext` for it before drawing. The draw loop renders
> into that backing store, and `SendBlit` tells rachel "frame ready,
> please composite." See `maz/clocks/main.go` for the canonical
> reference. The constraint mechanics shown above are exactly the same
> in either case — only the framebuffer plumbing differs.

### What Is Happening Here?

The key constraint interactions are:

1. **Column sizing** — When we create the Column with `NewColumn`, its
   Width and Height are automatically constraint-computed from its
   children.  The `.vgo` programs `column_height.vgo` and
   `column_width.vgo` discover children via `findVisibleChildren` and
   sum/max their dimensions.

2. **Child positioning** — The Column's `Draw` method imperatively sets
   each child's `X` and `Y` value attributes as it lays them out
   top-to-bottom.  This is what makes `lh.Y.Get()` return different
   values for each label.

3. **Dynamic text** — Each label's `TextFunc` closure calls `lh.Y.Get()`
   on its own layout.  Since `Y` is a value attribute updated by the
   Column during drawing, the label always shows its current vertical
   position.

4. **Custom constraint** — The sibling label's Height is not a plain
   value.  It is a constraint computed by our `average_y.vgo` program:
   `(beta.Y + gamma.Y) / 2`.  Because it depends on Y positions set
   during draw, the sibling's height changes when the window moves.

5. **Row sizing** — The Row's Width and Height are also constraints.
   Its width sums the Column's width plus the sibling's width plus
   spacing; its height takes the maximum.

6. **AppWindow sizing** — The AppWindow is a Decorator.  Its Width and
   Height constraints wrap the Row's dimensions plus decoration insets
   (shadow margin + title bar + padding).  Everything sizes from the
   inside out.

7. **Focus handling** — rachel sends `MsgYouHaveFocus` /
   `MsgYouLostFocus` over the ring buffer channel.  The mailbox
   receiver sets `app.Focused` and calls `app.FullDamage()`.  This
   changes the AppWindow's neumorphic depth (Raised when focused,
   Flush when not) and the full-bounds damage triggers a repaint of
   the window decoration — all without the main loop knowing anything
   about focus.

8. **Eager root** — `WaitDirty` only wakes when an **eager** attribute
   is dirty.  The AppWindow's DamageRect is the root of the damage
   tree: every child's damage propagates upward through parent damage
   constraints.  Making it eager means the main loop wakes whenever
   _anything_ in the window needs repainting — focus, constraint
   updates, explicit `FullDamage` calls.  One eager attribute at the
   root is all you need.

9. **Same-value suppression** — You might worry that the Draw pass
   creates an infinite loop: the Column sets each child's X and Y
   during drawing, which would dirty the damage rect, which would
   wake WaitDirty again.  This does not happen because the kernel's
   `AttrWrite` syscall is **change-gated**: if the new value is
   bitwise equal to the current value, the write is a no-op — no
   dirty propagation, no eager wake.  In steady state, the Column
   writes the same positions every frame, the kernel suppresses them
   all, and the system sleeps until real input arrives.

The entire layout — from individual label heights up through the
AppWindow's shadow-padded bounds — is a connected graph of constraints
and value attributes.  Change one value and the system propagates the
effect.

## Lifecycle of a Change

Now let's trace what happens when something changes at runtime.  Suppose
a background goroutine modifies beta's height:

```go
go func() {
    time.Sleep(5 * time.Second)
    betaLH.Height.Set(40) // double the label height
}()
```

This single `.Set()` call triggers a cascade through the constraint
system, the damage tracking system, and the draw loop.  Here is
every step.

### 1. The Attribute Changes

`betaLH.Height.Set(40)` writes the new value and marks the attribute
dirty.  The attribute system records that beta's Height has changed.

### 2. Dependent Constraints Are Marked Dirty

Any constraint that dereferences beta's Height URI is now stale.
In our application, several constraints depend on it:

- **demo_col's Height** — the Column height constraint sums children's
  heights via `childHeight(seg)`, which dereferences each child's
  Height attribute.  Since beta is a child of demo_col, this
  constraint is now dirty.
- **sibling's Height** — our custom `averageY` constraint dereferences
  beta's Y position.  beta's Y doesn't change yet (it only changes
  during draw), but the Column's Height change will eventually
  reposition gamma, which changes gamma's Y — so the sibling
  becomes transitively dirty.
- **main_row's Height** — the Row's height constraint takes the max
  of its children's heights.  demo_col is a child, so this is dirty.
- **AppWindow's Height** — the Decorator constraint wraps the Row's
  height, so it propagates further.
- **Bounds and BoundsHash** — every interactor whose Height changed
  has a Bounds constraint (`rect(X, Y, X+W, Y+H)`) and a BoundsHash
  that depend on it.  These are also dirty now.

This all happens synchronously: marking one attribute dirty walks the
dependency graph and flags every transitive dependent.  No
constraint _evaluates_ yet — they are just marked stale.

### 3. Damage Rectangles Are Computed

Every interactor has a **damage rectangle** — a constraint of type
`Rect` that compares the interactor's current visual state against its
**last-painted** state.  The damage system maintains a set of
"last-painted" (LP) mirror attributes:

- `LPBounds` — the bounds we painted last time
- `LPVisible` — whether we were visible last time
- `LPBoundsHash` — hash of last-painted bounds
- `LPBgColor`, `LPFgColor` — last-painted colors

The damage rectangle constraint program (`leaf_damage_rect.vgo` for
leaves, `parent_damage_default.vgo` for parents) compares current state
against the LP mirrors:

```go
// leaf_damage_rect.vgo (simplified)
func leafDamageRect() Rect {
    bounds := derefRect("_bounds_")
    lpBounds := derefRect("_lpBounds_")

    boundsHash := derefI64("_boundsHash_")
    lpBoundsHash := derefI64("_lpBoundsHash_")
    if boundsHash != lpBoundsHash {
        return rectUnion(bounds, lpBounds)
    }

    // ... also checks visibility, bg/fg color changes ...

    return rect(0, 0, 0, 0) // no damage
}
```

If _anything_ changed — bounds moved, content changed, visibility
toggled — the damage rectangle is the **union** of the old and new
bounds.  This is critical: you need to repaint both where the
interactor _was_ (to erase it) and where it _is now_ (to draw it).

For **parent** interactors, a default damage constraint
(`parent_damage_default.vgo`) discovers all children via the
constraint network and unions their damage rectangles:

```go
// parent_damage_default.vgo (simplified)
func parentDamageDefault() Rect {
    boundsHash := derefI64("_boundsHash_")
    lpBoundsHash := derefI64("_lpBoundsHash_")
    if boundsHash != lpBoundsHash {
        return derefRect("_bounds_")  // parent moved — full repaint
    }

    if hasNoVisibleChildren("_myName_") {
        return derefRect("_bounds_")  // no visible children — full repaint
    }

    children := findWhere("_childPattern_", "_myName_")
    result := rect(0, 0, 0, 0)
    for _, uri := range children {
        seg := uriSegment(uri, 3)
        if isChildVisible(seg) {
            childDmg := childDmgRect(seg)
            if !rectEmpty(childDmg) {
                if rectEmpty(result) {
                    result = childDmg
                } else {
                    result = rectUnion(result, childDmg)
                }
            }
        }
    }
    return result
}
```

This constraint runs automatically for every parent interactor
(Column, Row, Decorator, AppWindow).  It handles three cases:

- **Parent bounds changed** (BoundsHash differs from last-painted) —
  return the full parent bounds, since the parent itself moved or
  resized and everything it draws needs repainting.
- **No visible children** — return full bounds.  A parent with no
  visible children still needs to repaint its own area (background,
  decoration).
- **Normal case** — union all visible children's damage rectangles.
  Only the regions where children actually changed are marked dirty.

Damage propagates upward through the tree.  beta's bounds changed, so
beta has damage.  demo_col's parent damage constraint discovers
beta's non-empty damage and unions it into its own.  main_row does the
same for demo_col, and the AppWindow for main_row.  At the top of the
tree, the AppWindow's damage rectangle covers everything that needs
repainting.

### First-Frame Damage: FullDamage and Initialize

At construction time, every interactor needs to be painted for the first
time.  There is no "last-painted" state to compare against — the LP
mirrors are initialized to zero/empty.  If the damage constraint ran
naively, it would see matching zeros on both sides and report no damage.

This is solved by `FullDamage()`.  Every interactor's `Initialize`
method (called by all the `std.New…` constructors under the hood) calls
`FullDamage()` as its last step.  `FullDamage` sets the interactor's
DamageRect to its full bounds as a value attribute, ensuring the first
draw pass repaints everything.

For parent interactors (Column, Row, Decorator), `Initialize` also
installs the default parent damage constraint — which **replaces** the
value-attribute DamageRect with a constraint-attribute DamageRect.
From that point on, the parent's damage is computed reactively by
unioning its children's damage.  The initial full-bounds damage from
each child's `FullDamage()` propagates upward through the parent
constraints, so the first frame paints the entire window without any
special-case code.

### Why Damage Matters

Without damage tracking, every change would require repainting the
entire window — expensive when the window is large and only one label
moved.  With damage, the system knows _exactly_ which screen
rectangle is stale.

The best case is the most common one: a label's text changes but
the label has not moved or resized.  The application code calls
`FullDamage()` on the label, which sets the damage rectangle to the
label's own bounds.  Nothing else in the window needs repainting —
one small rectangle redrawn, one small rectangle flushed to the GPU.

In our lifecycle example, beta grew taller — a geometry change, which
is more expensive.  The damage region covers:

- beta's old bounds (to erase the old rendering)
- beta's new bounds (taller)
- gamma's old and new bounds (it shifted down)
- The sibling's old and new bounds (its height changed)

The rest of the Column (alpha) and the rest of the window (title bar,
shadows on the left side) are undamaged and do not need repainting.
Even in this worst case, damage tracking limits the repainted area to
the interactors that actually changed.

### 4. The Main Loop Wakes

Attributes that are marked **eager** (via `.SetEager(true)`) notify
the kernel when they become dirty.  `attr.WaitDirty()` blocks until
at least one eager attribute changes, then returns.

`WaitDirty` is a **kernel syscall**, not a userspace spin loop.
Attributes live on shared pages that the kernel maps into each
shepherd's address space, so one shepherd's `.Set()` can dirty a
constraint in another shepherd's attribute graph.  The kernel tracks
which eager attributes are dirty across all shepherds and wakes the
blocked thread when any of them fire.  This is what makes
cross-shepherd data flow work — for example, the kernel publishes
`attr:///kernel/int64/time/utc_seconds`, and any shepherd can create
a constraint that dereferences it and wake when it changes.

In our demo, the AppWindow's DamageRect is the only eager attribute.
The main loop is:

```go
for {
    x, y := appLH.X.Get(), appLH.Y.Get()
    w, h := appLH.Width.Get(), appLH.Height.Get()

    app.Draw(app, x, y, w, h)
    drawCtx.Flush(int32(x), int32(y), int32(x+w), int32(y+h))

    attr.WaitDirty()  // blocks until AppWindow.DamageRect is dirty
}
```

When the goroutine sets `betaLH.Height.Set(40)`, the dirty mark
propagates through Column height → Row height → AppWindow height →
BoundsHash → DamageRect.  The DamageRect is eager, so `WaitDirty`
wakes and the loop redraws.

### 5. The Draw Pass

The draw pass starts at the AppWindow and descends the tree.  Each
parent is responsible for calling `Draw` on its children:

```
AppWindow.Draw(app, x, y, w, h)
 ├─ Decorator.DecorateIfNeeded(...)  — checks BoundsHash, redraws shadow if needed
 └─ Decorator.Draw(...)              — positions child, propagates DC, calls:
     └─ Row.Draw(row, cx, cy, cw, ch)
         ├─ Column.Draw(col, ...)
         │   ├─ alpha.Draw(alpha, x, y1, w, h)    — sets alpha.Y = y1
         │   ├─ beta.Draw(beta, x, y2, w, 40)     — sets beta.Y = y2
         │   └─ gamma.Draw(gamma, x, y3, w, h)    — sets gamma.Y = y3
         └─ sibling.Draw(sibling, x, sy, w, sh)   — sh is now avg(y2, y3)
```

Notice that drawing **resets value attributes**.  The Column's Draw
sets each child's `X` and `Y`.  This is how the "Y wired to text"
labels get their updated values: the Column positions them, and
their `TextFunc` reads the newly-set Y during their own Draw.

### 6. Damage Snapshot

After an interactor paints itself, it calls `SnapshotDamage()` to
copy its current state into the last-painted mirrors:

```go
lh.SnapshotDamage()              // copies Bounds, Visible, BoundsHash
lh.SnapshotDamageColors(bg, fg)  // copies bg/fg color
```

This makes the damage rectangle for that interactor return
`rect(0, 0, 0, 0)` — no damage — until the next change.
Repainting is the thing that clears the damage.

### 7. Flush

After the full tree draws, `drawCtx.Flush(...)` sends the
updated pixel rectangle to the GPU.  Only the damaged region (or the
full window bounds, depending on the flush call) is transferred.

### The Complete Cycle

```
   .Set(40)
      │
      ▼
   mark dirty ──▶ propagate to dependents
      │
      ▼
   damage rects computed (current vs last-painted)
      │
      ▼
   eager attribute fires ──▶ WaitDirty() wakes
      │
      ▼
   Draw pass (top-down, parents call children)
      │
      ▼
   SnapshotDamage() ──▶ damage rect resets to empty
      │
      ▼
   Flush to GPU
      │
      ▼
   WaitDirty() ──▶ sleep until next change
```

This cycle runs for every change.  A goroutine pokes one value;
the constraint graph propagates the effect; damage is computed;
the damaged area repaints; the snapshot clears the damage; the
system sleeps until the next poke.

## Step 3: Create the Taskfile (mazarin-specific)

> This section and the next are specific to mazarin's build system
> and boot sequence.  If you are using mancini in another context,
> adapt the build steps accordingly.

Create `maz/demo/Taskfile.yml` by copying from clocks:

```yaml
version: '3'

tasks:
  arm64:
    desc: Build demo (constraint tutorial, ARM64)
    deps: [':check-env', ':mazarin:userspace-overlay', ':mancini:compile-constraints']
    sources:
      - 'maz/demo/**/*'
      - 'mazarin/**/*.go'
      - '{{.USERSPACE_OVERLAY_JSON}}'
      - '{{.USERSPACE_OVERLAY_DIR}}/**/*'
    generates:
      - '{{.BUILD_DIR}}/demo.elf'
    cmds:
      - '{{.GO}} tool go-echo "Building demo..."'
      - 'CGO_ENABLED=0 GOARCH={{.TARGET_GOARCH}} GOOS={{.TARGET_GOOS}} {{.GO}} build -overlay={{.ROOT_DIR}}/{{.USERSPACE_OVERLAY_JSON}} {{.GCFLAGS}} -o {{.BUILD_DIR}}/demo.elf ./maz/demo'
      - '{{.GO}} tool go-echo "Demo built at {{.BUILD_DIR}}/demo.elf"'
```

The critical dependency is `:mancini:compile-constraints` — this runs the
`compile-constraints` tool on all `.vgo` files (including your new
`average_y.vgo`) and generates the `.vbc.go` bytecode files before
compilation.

You also need to:

1. Add a `DEMO_ELF` variable in the root `Taskfile.yml`
2. Include the demo task as a subtask
3. Add `demo.elf` to the disk image build step

## Step 4: Add to the Boot Sequence (mazarin-specific)

Edit `config/startup.arm64.toml` (and the amd64 variant) to include
your new shepherd. The startup config only lists **application**
shepherds — fs, rachel, and linux are bootstrapped earlier by the fs
shepherd's `bootSequence`, before this file is read, so do not list
them here.

```toml
[[shepherd]]
name = "demo"
path = "/demo.elf"
```

Shepherds launch in the order listed.  By the time your demo's `main`
runs, fs / rachel / linux are already up — `WaitForCoreServices` will
return immediately.

## Step 5: Build and Run

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

# Build everything (default task)
$GO tool task

# Run under QEMU + HVF (macOS Apple Silicon — the path that gets
# tested every day). Drop `-hvf` for software-emulated ARM64, or
# use `run-x86_64` instead.
$GO tool task run-arm64-hvf TIMEOUT=30
```

## Step 6: Observe the Output

When the demo runs, you will see an AppWindow titled "Constraint Demo"
with a Row containing:

- A **Column** of three labels, each displaying its computed Y position:
  ```
  Y = 183
  Y = 207
  Y = 231
  ```
  The exact numbers depend on the window's position on screen.  The
  Column positions its children top-to-bottom with 12px spacing, and each
  label reads its Y attribute and displays it.

- A **sibling label** next to the column, also showing its Y.  Its height
  is the average of beta's and gamma's Y positions.  As the window moves
  (e.g., rachel repositions it), the Y positions change and the sibling's
  height updates automatically.

If the goroutine from the lifecycle example fires after 5 seconds and
doubles beta's height to 40, you will see gamma shift downward (its Y
increases), the sibling's height change (because gamma's Y changed),
and all four labels update their displayed Y values — all from a single
`.Set()` call.

## Summary

| Concept | How It Works |
|---------|--------------|
| Value attribute | `attr.ValueI64(uri, initialValue)` — set with `.Set()`, read with `.Get()` |
| Constraint attribute | `attr.ConstraintI64(uri, program)` — computed from other attributes |
| `.vgo` program | Restricted Go subset compiled to bytecodes by `compile-constraints` |
| Multiple functions | Helper functions come first; the last function is the entry point |
| Placeholders | `_name_` strings in `.vgo`, replaced by `BindStrings` at runtime |
| Child discovery | `findVisibleChildren("_myName_")` queries the Parent attribute of all interactors |
| FullDamage | Called by `Initialize` — sets DamageRect to full bounds so the first draw paints everything |
| Parent damage | Default constraint unions visible children's damage; returns full bounds when parent moved or has no visible children |
| Damage tracking | Compares current bounds against last-painted mirrors; repaints only the changed region |
| Eager root | Make the AppWindow's DamageRect eager — one attribute wakes the loop for all visual changes |
| Same-value suppression | Kernel skips dirty propagation when a written value is bitwise equal — prevents infinite redraws |
| Reactive loop | `attr.WaitDirty()` blocks until an eager attribute is dirty, then you redraw |

The constraint system replaces imperative layout bookkeeping with
declared relationships.  You say "my height is the sum of my children's
heights" or "my height is the average of those two Y positions" and the
system maintains the relationship, computes what changed, and repaints
only what is necessary.
