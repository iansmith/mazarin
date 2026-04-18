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

### The Rachel Channel

Before we look at the code, it helps to understand how a shepherd
communicates with rachel (the window manager).

Shepherds and rachel talk through a **shared-memory ring buffer**.
Each side allocates a ring buffer in its own address space and hands
the other side the address via `sys.MailboxSend`.  Messages are
fixed-size (128 bytes) and copied into the ring with `rb.Push` /
`rb.Pop`.  There are no byte streams — this is a high-speed,
structured message channel.

The two directions use different notification codes:

| Direction | Code | Meaning |
|-----------|------|---------|
| shepherd → rachel | `wm.WMNotify` | "I have a message for the WM" |
| rachel → shepherd | `wm.ShepherdNotify` | "I have a message for you" |

The first `int64` of every message is a type discriminator.  A
shepherd sends `MsgAppStart` ("I exist, here is my SID").  rachel
replies with `MsgYouHaveFocus` or `MsgYouLostFocus` as focus changes,
and forwards mouse events as `MsgMousePress` / `MsgMouseRelease` /
`MsgMouseMove`.

Font requests (`FontNotify` / `FontResponse`) share the same mailbox
but use different codes, so the receive loop can distinguish them.

A shepherd must also publish a `Ready` flag
(`attr.ValueBool(wm.ReadyURI(...), true)`) so rachel knows when its
constraint attributes are valid.  rachel ignores a shepherd until
Ready is true.

### The Code

Create `maz/demo/main.go`:

```go
package main

import (
    "fmt"
    "os"
    "unsafe"

    "mazzy/mazarin/attr"
    "mazzy/mazarin/mancini"
    "mazzy/mazarin/mancini/std"
    "mazzy/mazarin/ringbuf"
    "mazzy/mazarin/sys"
    mfont "mazzy/shared/font"
    "mazzy/shared/wm"
)

// app is the root interactor — set during buildUI, read by the
// mailbox receiver when rachel sends focus messages.
var app *std.AppWindow

func main() {
    // ── 1. Initialize the constraint system ──
    attr.Init()
    mancini.Init()
    waitForServices() // blocks until fs, rachel (WM), and linux are ready

    // ── 2. Set up fonts and theme ──
    rachelSID := sys.MustGetShepherdByName("rachel")
    env := mancini.NewFontEnv(rachelSID, mfont.DefaultMono)
    pal := mancini.DefaultPalette()
    theme := env.DefaultTheme()

    // Start the mailbox receiver before any font requests — it
    // handles both FontResponse (from fontsvc) and ShepherdNotify
    // (focus messages from rachel) on the same channel.
    go mailboxRecvLoop(env)

    // ── 3. Build the interactor tree ──
    app, alpha, beta, gamma, sibling := buildUI(pal, env.Fonts(), theme)

    // Wire each label's text to display its own Y position.
    for _, lbl := range []*std.Label{alpha, beta, gamma, sibling} {
        lh := lbl.GetLayout()
        lbl.TextFunc = func() string {
            return fmt.Sprintf("Y = %d", lh.Y.Get())
        }
    }

    // ── 4. Framebuffer and initial position ──
    //
    // AppWindow creates a fresh gg.Context each draw pass from
    // the framebuffer image, so no stale clip or transform state
    // leaks between frames.
    drawCtx := mancini.NewFramebufferContext()
    app.SetFramebuffer(drawCtx)
    screenW, screenH := screenDimensions()
    appLH := app.GetLayout()
    centerWindow(appLH, screenW, screenH)

    // ── 5. Make the root damage rect eager ──
    //
    // WaitDirty only wakes when an eager attribute is dirty.  The
    // AppWindow's DamageRect is the root of the damage tree — every
    // child's damage propagates upward through the parent damage
    // constraints.  Making it eager means the main loop wakes whenever
    // anything in the window needs repainting: a focus change, a
    // constraint update, a goroutine calling FullDamage, etc.
    appLH.Damage.DamageRect.SetEager(true)

    // ── 6. Announce to rachel ──
    _ = appLH.Bounds.Get()
    attr.ValueBool(wm.ReadyURI(attr.SID()), true)
    announceToWM(rachelSID)

    // ── 7. Main loop ──
    for {
        x, y := appLH.X.Get(), appLH.Y.Get()
        w, h := appLH.Width.Get(), appLH.Height.Get()

        app.Draw(app, x, y, w, h)
        drawCtx.Flush(int32(x), int32(y), int32(x+w), int32(y+h))

        attr.WaitDirty()
    }
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
func buildUI(pal mancini.Palette, fonts *mancini.FontConfig,
    theme mancini.Theme) (*std.AppWindow, *std.Label, *std.Label, *std.Label, *std.Label) {

    gt := std.NewGradientTitle(pal, fonts, "Constraint Demo", 22, 8)
    app = std.NewAppWindow(nil, pal, fonts,
        "Constraint Demo", 26, 500, gt.TitleDraw)

    row := std.NewRow("main_row", "AppWindow", pal, 0, mancini.AxisMinimum, 8)
    row.SetSpacing(20)

    col := std.NewColumn("demo_col", "main_row", pal, 9999, mancini.AxisMinimum, 4, false)
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

    return app, alpha, beta, gamma, sibling
}

// waitForServices blocks until the required shepherds are ready.
// rachel (window manager) is always needed.  fs and linux are only
// required here because Go's time/tzdata package may load timezone
// descriptions from files — a standard library decision, not ours.
// Most shepherds only need to wait for rachel.
func waitForServices() {
    for _, name := range []string{"fs", "rachel", "linux"} {
        if err := sys.WaitForShepherdReady(name, 10); err != nil {
            panic("[demo] " + name + ": " + err.Error())
        }
    }
}

// screenDimensions reads the screen width and height from kernel
// constraint attributes.
func screenDimensions() (int, int) {
    wProg := mancini.BindStrings(mancini.ProgIdentityI64,
        "_source_", "attr:///kernel/int64/screen/width")
    w := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_w"), wProg)
    hProg := mancini.BindStrings(mancini.ProgIdentityI64,
        "_source_", "attr:///kernel/int64/screen/height")
    h := attr.ConstraintI64(attr.ShepherdURI("int64", "screen_h"), hProg)
    return int(w.Get()), int(h.Get())
}

// centerWindow positions the AppWindow at the center of the screen.
func centerWindow(appLH *mancini.LayoutAttributes, screenW, screenH int) {
    w := appLH.Width.Get()
    h := appLH.Height.Get()
    appLH.X.Set(int64(screenW)/2 - w/2)
    appLH.Y.Set(int64(screenH)/2 - h/2)
}

// announceToWM sends MsgAppStart to rachel via the ring buffer
// channel.  rachel adds this shepherd to its tracking list and
// will later send MsgYouHaveFocus when the shepherd gains focus.
func announceToWM(rachelSID int) {
    rb, err := ringbuf.New(rachelSID, 0, wm.SizeWMMessage, wm.DefaultSlotCount)
    if err != nil {
        return
    }
    var msg wm.AppStartMsg
    msg.Type = wm.MsgAppStart
    msg.SID = int64(os.Getpid())
    rb.Push(unsafe.Pointer(&msg))
    sys.MailboxSend(rachelSID, wm.WMNotify, rb.Addr())
}

// mailboxRecvLoop receives notifications on the shared mailbox.
// FontResponse comes from fontsvc (glyph cache replies).
// ShepherdNotify comes from rachel (focus, mouse events).
func mailboxRecvLoop(env *mancini.FontEnv) {
    for {
        notif, err := sys.MailboxRecv()
        if err != nil {
            continue
        }
        switch notif.Code {
        case wm.FontResponse:
            env.HandleNotification(notif)
        case wm.ShepherdNotify:
            handleWMMessages(notif)
        }
    }
}

// handleWMMessages drains the ring buffer from rachel, processing
// focus messages.  When the demo gains focus, it sets the AppWindow
// to focused (which changes the neumorphic depth from Flush to
// Raised) and calls FullDamage to trigger a repaint.
func handleWMMessages(notif sys.MailboxNotification) {
    rb := ringbuf.Open(uintptr(notif.RingAddr))
    var raw [wm.SizeWMMessage]byte
    for rb.Pop(unsafe.Pointer(&raw[0])) {
        msgType := *(*int64)(unsafe.Pointer(&raw[0]))
        switch msgType {
        case wm.MsgYouHaveFocus:
            app.Focused = true
            app.FullDamage()
        case wm.MsgYouLostFocus:
            app.Focused = false
            app.FullDamage()
        }
    }
}
```

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

Edit `config/startup.arm64.toml` (and the amd64/riscv64 variants) to
include your new shepherd:

```toml
[[shepherd]]
name = "rachel"
path = "/rachel.elf"

[[shepherd]]
name = "linux"
path = "/linux.elf"

[[shepherd]]
name = "clocks"
path = "/clocks.elf"

[[shepherd]]
name = "demo"
path = "/demo.elf"
```

Shepherds launch in the order listed.  rachel and linux must come first
(they provide the window manager and I/O delegation), but your demo can
come after clocks or replace it.

## Step 5: Build and Run

```bash
export GOTOOLCHAIN=auto
export GO=/Users/iansmith/sdk/go1.25.5/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

# Build everything including the demo
$GO tool task run TIMEOUT=30
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
