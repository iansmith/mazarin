# Plan — maildb console output + scrollback console interactor

Two related changes. Task 1 is the small wiring change that makes maildb's
status messages land where they belong. Task 2 is a substantial console
rewrite (mirrors the grid) so the user can scroll back to see startup
errors that fly past at boot.

Branch: `feature/mail-dumb`. Active GC-crash work in `task_plan.md` is
unrelated; do not touch it.

## Confirmed decisions (locked-in)

| # | Decision |
|---|---|
| 1 | Color stderr/error lines red — `StatusLine{Text, IsError}` struct on the channel. |
| 2 | `mlogInfo` drops on full channel. Bump channel buffer depth to **256** (from 16) to make drops rare. |
| 3 | Main ring buffer = **2000 lines**. |
| 4 | Slot interactors = **`DynamicLabel`** in the constraint network. Drop the draw-only `consoleLine` helper. |
| 5 | Two-tier buffer: 2000 main + 500 pause-overflow. While paused, new lines go to the overflow buffer; main is frozen. When overflow fills, eviction resumes against main (user's view shifts — accepted tradeoff). When user returns to tail, overflow drains into main. |
| 6 | Grep / filter: out of v1. |
| 7 | Click-drag selection + clipboard copy: out of v1, but **near-term follow-up** — keep the design hospitable to it (per-line hit-testing, per-line selection state). |

---

## Task 1 — Direct maildb output to its own console

### Today

`maildb` shepherd makes ~162 `fmt.Printf`/`Println` calls. They go to
the global Go stdout, which is captured by the linux shepherd's line
accumulator and printed in the **linux** console (`linux-ui.maz`). They
do NOT appear in maildb's own UI (`mail-ui.maz`).

`mail-ui.maz` already owns a `std.Console` that drains
`MailDBIO.StatusChannel()` (a `chan string`) and renders each message
in the default text color. Used today only for two lines (`mbox import
error`, etc.). See:

- shepherd side: `maz/maildb/main.go:152-352` (call sites + `notifyStatus`)
- channel: `mazarin/maildbio/maildbio.go`
- UI drain: `maz/mail-ui/main.go:97-110`

### Goal

- Non-error informational output from maildb → its own console only,
  via `StatusChannel`. **No** `fmt.Printf` (so it does not bleed into
  the linux console).
- Errors and warnings → both its own console (so the user sees them in
  context) and `fmt.Printf` (so the linux console + serial log retain
  the diagnostic).

### Approach

Introduce a tiny `mlog` package inside maildb, then sweep call sites.

#### Step 1 — extend `StatusChannel` to carry an "is-error" flag

`StatusChannel` is `chan string` today. Replace with a struct so error
lines render red. In `mazarin/maildbio/maildbio.go`:

```go
type StatusLine struct {
    Text    string
    IsError bool
}
```

Update `StatusChannel()` return type to `<-chan StatusLine`, update
`MailDBIOInit.StatusCh` to `chan StatusLine`, update both producer
(`maz/maildb/main.go` — was `notifyStatus`, now `mlog`) and consumer
(`maz/mail-ui/main.go` drain) to use the struct.

**Bump channel buffer depth to 256** (from current 16). The shepherd
appends quickly during startup; with depth 16 we'd drop visible info
lines every boot. 256 strings × ~150B ≈ 40 KB peak — cheap.

In the `mail-ui` drain:

```go
if s.IsError {
    // DynamicLabel-based console takes color via AddLine.
    console.AddLine(s.Text, console.ErrorColor())
} else {
    console.AddLine(s.Text, pal.Text())
}
```

(After Task 2 lands, the rendering path goes through DynamicLabel
slots, but the `Console.AddLine(text, fg)` API stays the same — the
`fg` color is stored in the line record and applied when bound to a
slot. See Task 2 §B.)

#### Step 2 — add `maz/maildb/mlog.go`

```go
package main

import (
    "fmt"
    "mazzy/mazarin/maildbio"
)

var (
    mlogCh       chan<- maildbio.StatusLine // nil before SetSink
    mlogNotifyCh chan<- struct{}            // nil before SetSink
)

func mlogSetSink(ch chan<- maildbio.StatusLine, notify chan<- struct{}) {
    mlogCh = ch
    mlogNotifyCh = notify
}

// Info: console-only. Drops if channel is full or unset (never blocks,
// never falls back to Printf — the whole point is to keep the linux
// console quiet).
func mlogInfo(format string, args ...any) {
    msg := fmt.Sprintf(format, args...)
    if mlogCh == nil {
        // Pre-injection bootstrap window: drop. (Bootstrap-critical
        // lines should call rawPuts/Printf directly.)
        return
    }
    select {
    case mlogCh <- maildbio.StatusLine{Text: msg}:
    default: // channel full — drop
    }
    select {
    case mlogNotifyCh <- struct{}{}:
    default:
    }
}

// Errorf: both maildb console (red) and stdio (linux console + serial).
func mlogErrorf(format string, args ...any) {
    msg := fmt.Sprintf(format, args...)
    fmt.Println(msg) // keep linux/serial diagnostic
    if mlogCh == nil {
        return
    }
    select {
    case mlogCh <- maildbio.StatusLine{Text: msg, IsError: true}:
    default:
    }
    select {
    case mlogNotifyCh <- struct{}{}:
    default:
    }
}
```

In `main.go`, after `statusCh` and `notifyCh` are created (around
line 192), call `mlogSetSink(statusCh, notifyCh)` once. Existing
`notifyStatus` closure can be deleted in favor of `mlogInfo`/`mlogErrorf`.

#### Step 3 — sweep `maz/maildb/*.go` call sites

Categorize each `fmt.Printf`/`fmt.Println`. Mapping (use the explore
report in Bug-context section as the master list):

- **`main.go`** (20 calls): line 61 → `mlogErrorf` (death); 122, 261,
  279, 320 → `mlogErrorf`; 137, 152, 165, 184, 188, 221, 236, 268, 312,
  317, 346, 349, 352 → `mlogInfo`. Line 188 ("WARNING: mmap coherence
  test FAILED") is a warning — use `mlogErrorf`.
- **`mbox_import.go`** (23 calls): per-line judgment. Anything with
  "error", "fail", or that signals a dropped record → `mlogErrorf`;
  the rest are progress → `mlogInfo`.
- **`mail_handler.go`** (19 calls): mostly errors on dropped requests →
  `mlogErrorf`. Treat every "drop" / "%v error" as `mlogErrorf`.
- **`collection.go`** (1 call): line 361 → `mlogErrorf`.
- **`mmap_test_coherence.go`** (99 calls): **leave untouched.** This is
  a self-contained diagnostic suite that already prefixes every line and
  is run only when the coherence check fires. Keep it on `fmt.Println`.

Acceptance check: `grep -nE 'fmt\.Print(f|ln)?\(' maz/maildb/*.go`
should show only `mmap_test_coherence.go` (and the `mlogErrorf` body
inside `mlog.go`).

#### Step 4 — verify

- Build + run: `$GO tool task run-arm64-hvf TIMEOUT=30`.
- Maildb console (the small "MailDB" window) should now show every
  startup message that previously appeared in the linux console.
- Linux console should still show errors (`death`, `import failed`,
  etc.) but NOT informational lines.
- Existing red-stderr behavior for any explicit `os.Stderr` writes is
  unaffected (no shepherd uses that path today).

---

## Task 2 — Console scrollback (mirror the grid)

### Today

`std.Console` (`mazarin/mancini/std/console.go`) has:

- A fixed `rows` count baked in at construction (12).
- A growable `content []lineData` ring trimmed to `maxBuf = max(rows*10, 200)`.
- `Draw` always shows the **last `rows` entries** (`visibleStart =
  len(c.content) - c.rows`). No scroll offset.
- No input plumbing (no Click, no KeyDown, no scroll-wheel).
- Per-line color already supported (`lineData.color`). Stderr → red is
  set in `HandleByte` when `fd == 2`.
- Publishes `LineHeight`, `CharsPerLine`, `NumLines` as observability
  attrs. `NumLines` is static.

The user wants it to behave like the mail header grid:

- Visible row count is **derived** from height + line height (constraint).
- Row interactors are **fixed slots** that don't change identity; their
  text is rebound from the buffer on each Draw.
- Scrollbar is wired the same way as `GridFrame`'s.
- Scrollbar visibility is a constraint (`Total > Visible`).
- Per-row display modifiers (today: red rows for stderr writes).
- "Collection" model — initially the whole buffer, later a filtered
  sub-view (grep). Collection size and content can change.

Reference patterns:

- `mazarin/mancini/std/grid_table.go` — visible-count math (`computeVisibleCount`),
  `VisibleRowCountAttr`, `TotalRowsAttr`, `ScrollOffsetAttr`,
  `applyScrollToSlots`, `slotPool`, `DamageAll`.
- `mazarin/mancini/std/grid_frame.go` — NeuBox + Scrollbar wiring with
  `scrollNeededAttr` constraint.
- `mazarin/mancini/std/scrollbar.go` — drag → ValueAttr.
- `mazarin/mancini/bind.go` — `GreaterI64Bool`, `NonnegSubI64`,
  `ThumbFracPermille`.

### Architecture

#### A. Collection abstraction (two-tier ring)

Two physical buffers, one logical line sequence.

```go
type consoleCollection struct {
    main         []lineData // 2000-cap FIFO ring — what the user is reading
    mainHead     int        // next-write index in main (mod len(main))
    mainCount    int        // current valid entries in main (≤ len(main))

    pause        []lineData // 500-cap FIFO ring — fills only while paused
    pauseHead    int
    pauseCount   int

    paused       bool       // true while user is scrolled away from tail
    appended     int64      // monotonic counter (every successful append, ever)

    filter func(lineData) bool // nil = "null search" = everything; v1 = always nil
}
```

**Sizing:** main = 2000, pause = 500. Configurable via constructor
args.

**Tail-anchored mode** (`paused == false`): every `Append` goes
straight into `main` (FIFO eviction at cap). `pause` stays empty.

**Paused mode** (`paused == true`):
1. **Pause buffer not full** (`pauseCount < cap`): append to `pause`
   only. Main is frozen. The user's scroll position remains stable
   relative to the entries they're reading.
2. **Pause buffer full**: must evict. Move the oldest `pause` line
   into `main` (FIFO-evicting `main`'s oldest), then write the new
   line into the freed `pause` slot. From the user's perspective,
   `main`'s contents have shifted under them — this is the "scroll
   position messes up dramatically" case. Unavoidable without
   unbounded memory.

**Resume to tail**: drain `pause` into `main` in order, FIFO-evicting
`main` as needed; reset `pauseCount` to 0; `paused = false`.

API:

- `Append(line lineData)` — uses `paused` flag to route. Updates `appended`.
- `Total() int64` — `mainCount + pauseCount` (the user can scroll
  across into the pause buffer to see what arrived since they paused).
- `LineAt(viewIndex int64) (lineData, bool)` — `viewIndex` is in the
  combined logical sequence: `[0, mainCount)` indexes into main
  (oldest first); `[mainCount, mainCount+pauseCount)` indexes into pause.
- `SetPaused(b bool)` — toggled by Console when user scrolls away
  from / back to tail.
- `SetFilter(fn func(lineData) bool)` — **stub for v1, no-op**.
  Reserved for grep.

The `Console` toggles `SetPaused` based on `tailAnchored`. The
collection stays oblivious to scroll math.

#### B. `Console` rewrite

New fields:

```go
ring          *consoleCollection
scrollOffset  int64               // index of first visible line (view-space)
tailAnchored  bool                // if true, sticks to the end on append
visibleCount  int64               // last computed; published

// Slot interactors live in the constraint network as children of the
// Console. Allocated lazily up to a high-water mark so we never destroy
// labels (avoids dangling layout attrs).
slots         []*DynamicLabel
slotsHWM      int                 // high-water count of slots ever allocated
fontSizeURI   string              // bound to theme/app font size

// Constraint attrs (mirrors GridTable)
visibleLineCountAttr *attr.Attribute[int64] // computed from height/lineH each Draw
totalLineCountAttr   *attr.Attribute[int64] // = ring.Total()
scrollOffsetAttr     *attr.Attribute[int64] // shared with scrollbar.ValueAttr
```

Public attribute getters: `VisibleLineCountAttr()`, `TotalLineCountAttr()`,
`ScrollOffsetAttr()` — needed by `ConsoleFrame` to wire the scrollbar.

Constructor change:
```go
NewConsole(name, parent string, theme mancini.Theme, fontSizeURI string,
    cols int, mainCap, pauseCap int) *Console
```

Notes:
- Drop `rows` (visible-row count is derived from height now).
- Drop `*FontConfig` and `fontSize` literal — now constraint-driven via
  `fontSizeURI` (mirrors how `DynamicLabel` and `GridTable` consume it).
- Defaults: `mainCap = 2000`, `pauseCap = 500`.
- Width = `cols * charW` where `charW` is probed once from a
  monospace face at the current font size. Recompute if font size
  changes (subscribe to `fontSizeURI`).
- Height comes from a layout constraint set by parent (or `ConsoleFrame`).
- `cols` is the **clamp** for line length (chars stored per line);
  visible width may be wider/narrower than `cols * charW` and we
  truncate or pad with whitespace as appropriate. Keep current
  truncation behavior.

Stderr/error color: store on the `lineData` (`fg` field, kept). When
binding a slot in Draw, set `slot.Color = lineData.fg` (with a
zero-alpha fallback to palette text). Expose `Console.ErrorColor()`
returning `pal.AnsiColor(1)` for callers (e.g. mail-ui drain) that
want to add error lines explicitly.

#### C. Draw

Mirror `GridTable.Draw`:

1. Read current `w, h` from layout attrs.
2. `visibleCount := h / lineH` (clamp ≥ 0). Publish to `visibleLineCountAttr`.
3. Publish `totalLineCountAttr = ring.Total()`.
4. Read `scrollOffsetAttr` (so scrollbar drag flows in). If
   `tailAnchored`, override to `max(0, total - visibleCount)`. Clamp
   to `[0, max(0, total - visibleCount)]`.
5. **Grow slot pool** if `visibleCount > slotsHWM`: allocate new
   `DynamicLabel` children parented to the Console (or its inner box).
   Mirror `GridTable.buildSlotPool` — never destroy old slots. New
   slots: `NewDynamicLabel(name, console, theme, "", fontSizeURI)`,
   then position imperatively (Y = `i * lineH`, X = 0, Width =
   console width, Height = lineH).
6. **Bind slots** for `i` in `[0, visibleCount)`:
   - `line, ok := ring.LineAt(scrollOffset + i)`
   - `slot.Text = line.text` (or `""` if !ok)
   - `slot.Color = line.fg` (fallback to palette text if zero alpha)
   - mark `slot.FullDamage()` if text or color changed
7. **Hide unused slots** for `i` in `[visibleCount, slotsHWM)`:
   `slot.Visible = false` (or set Text to "" and zero-height) — pick
   whichever pattern GridTable uses for shrunken-pool slots.

Tail-anchor logic:

- After every `Append`, if `tailAnchored`, leave `scrollOffset` as
  derived (no change needed). The collection appends to `main`
  directly (paused == false).
- If user moves scrollbar to the bottom (`scrollOffset + visibleCount
  >= total`): set `tailAnchored = true`, call `ring.SetPaused(false)`
  which **drains pause → main** if there's anything pending.
- Any imperative scroll-up (mouse wheel up, scrollbar drag away from
  bottom) sets `tailAnchored = false` and `ring.SetPaused(true)`.

Damage:
- On `Append` while at-tail: damage all slots (cheap; just overwrites
  text on each via `FullDamage`). Or, smarter: shift slot text down
  by one and only damage the bottom slot. Defer the optimization.
- On scroll: damage all slots (text-rebind on every slot).

#### D. Slot interactors — `DynamicLabel` in the constraint network

The legacy `consoleLine` (draw-only helper) goes away. Each visible row
becomes a `DynamicLabel` child of the Console, parented in the constraint
network with its own `LayoutAttributes`. Pattern follows `GridTable`'s
leaf-label slots: lazily grown high-water pool, imperative Y/Height
based on the current line height, font size driven by a constraint
binding to `fontSizeURI`.

Why this is worth the extra scope:
- Hit-testing comes for free → enables click-drag selection (post-v1).
- Damage tracking is correct without manual rectangles.
- Font size changes propagate via the constraint network (no special
  cases in Draw).
- Per-row decorations (selection highlight, error background tint) get
  to ride the same machinery `GridTable` uses for `RowPercentage`.

**Monospace font:** `DynamicLabel` accepts a theme; pass a theme
configured to load a monospace family for this label name, or extend
`DynamicLabel` with a "Monospace bool" knob if the theme abstraction
doesn't already cover it. (Investigate during implementation; the
`mancini.FontConfig` path the existing `consoleLine` uses suggests we
have a monospace face available — `fonts.LoadFace(false, fontSize)`
loads the regular family, which today is the monospace family for
console use.)

**Per-row error color:** set `slot.Color` directly when binding text
in Draw (DynamicLabel exposes `Color` as a public field — confirmed
in `dynamic_label.go:26`).

**Slot pool growth pattern** (mirrors `GridTable.buildSlotPool` —
read it before coding):
- Never shrink: keep `slotsHWM` monotonic, hide unused slots.
- Allocate slots in `Draw` once layout dimensions are known (font
  size and lineH may not be valid before first layout pass).
- After grow, recompute Y for all slots based on current `lineH`.

#### E. New `console_frame.go`

Mirror `grid_frame.go`. Layout:

```
ConsoleFrame
  ├── _margin (MarginParent)
  └── _box (NeuBox, Inset, Light)
        ├── _console (Console)
        └── _sb (Scrollbar, vertical, right edge)
```

Constraint wiring (numbers in row units, not pixels):

```go
scrollNeededAttr := attr.ConstraintBool(..., mancini.GreaterI64Bool(
    console.TotalLineCountAttr().URI(),
    console.VisibleLineCountAttr().URI()))

scrollMaxAttr := attr.ConstraintI64(..., mancini.NonnegSubI64(
    console.TotalLineCountAttr().URI(),
    console.VisibleLineCountAttr().URI()))

thumbFracAttr := attr.ConstraintI64(..., mancini.ThumbFracPermille(
    console.VisibleLineCountAttr().URI(),
    console.TotalLineCountAttr().URI()))

attr.SwapToConstraint(scrollbar.LayoutAttributes().Visible,
    mancini.EqualBool(scrollNeededAttr.URI()))
scrollbar.ValueAttr           = console.ScrollOffsetAttr()
scrollbar.MaxAttr             = scrollMaxAttr
scrollbar.ThumbFracPermilleAttr = thumbFracAttr
```

Width sub-allocation: console gets `parent_width - sbTrack`,
scrollbar gets `sbTrack`, both pinned to parent's height. Crib from
`GridFrame.scrollbarTrackWidth` and the `_box` child arrangement.

#### F. Input — mouse wheel only for v1

Console must scroll on mouse wheel. Steps:

1. Implement `Scrollable` (or whatever the project's scroll-wheel
   interface is — search `Scroller.go` for the contract). `Scroller` in
   `mazarin/mancini/std/scroller.go` is the reference.
2. On wheel-up: `scrollOffset = max(0, scrollOffset - step)`,
   `tailAnchored = false`.
3. On wheel-down: `scrollOffset = min(scrollMax, scrollOffset + step)`,
   if `scrollOffset + visibleCount >= total` then `tailAnchored = true`.

`step = 3` lines is a fine starting default.

Defer keyboard arrow / PageUp-PageDown to a later pass — not needed for
"see the startup errors". Note as TODO.

### Files to touch

- `mazarin/mancini/std/console.go` — substantial rewrite (per the
  Architecture section above). Drop `consoleLine` type. Constructor
  signature changes (theme + fontSizeURI; mainCap + pauseCap).
- `mazarin/mancini/std/console_frame.go` — **NEW**, mirror
  `grid_frame.go`.
- `maz/linux-ui/main.go:114-115` — switch from `NewConsole` to
  `NewConsoleFrame`. Drop `consoleRows = 12`; let height be derived.
  Pass `theme` + the app-level fontSizeURI. Layout-wise, the input row
  stays on top of the column; the ConsoleFrame fills the rest of the
  height.
- `maz/mail-ui/main.go:88-89` — switch from `NewConsole` to
  `NewConsoleFrame`. Console fills the AppWindow. Update drain to
  pass color via the new `Console.AddLine(text, fg)` (or the
  `StatusLine{IsError}` mapping from Task 1 §1).
- `mazarin/mancini/std/console.go` callers via `NewConsoleWithBox` —
  retire or thin to a wrapper around `NewConsoleFrame`. (`grep -nrI
  NewConsole\| NewConsoleWithBox mazarin maz` first; should be only
  the two .maz UIs.)

### Step-by-step

1. Read `grid_table.go`, `grid_frame.go`, `scroller.go` end-to-end before
   coding — the wiring details are subtle (e.g., `applyScrollToSlots`
   timing relative to `publishScrollAttrs`, when to call `DamageAll`).
2. Implement `consoleCollection` first; unit-testable in isolation
   (append + LineAt + Total). Skip the filter for now.
3. Rewrite `Console` with the new fields and `Draw`. Tail-anchored mode
   should make existing callers behave identically when there's no
   scrollbar interaction.
4. Add `ConsoleFrame`. Wire the scrollbar attributes. Confirm the
   scrollbar **does not appear** until `Total > Visible`.
5. Switch `linux-ui` and `mail-ui` to `NewConsoleFrame`. Run; verify
   text still renders in both windows.
6. Add wheel scrolling. Verify scrollbar thumb tracks. Verify
   tail-anchor re-arms when user wheels back to bottom.
7. Manual test: boot the mail app, watch errors fly past in the maildb
   console, then wheel up to read them.

### Deferred (v1.1 follow-ups)

- **Click-drag text selection + clipboard copy.** User flagged as
  "coming soon." The DynamicLabel slot model gives us hit-testing and
  per-slot mouse events for free. Plan: add a per-line `selectionRange`
  field to `lineData`, an active-drag-state on `Console` to track
  start/end, and a clipboard write on mouse-up.
- **Grep / filter UI.** `consoleCollection.SetFilter` stub is in place;
  add a search input field that updates the filter and watch
  `TotalLineCountAttr` change drive the scrollbar visibility.
- **Keyboard navigation.** Up/Down arrow, PageUp/PageDown, Home/End on
  the Console. Requires console focus model (currently the input field
  always has focus).
- **Pause-buffer overflow indicator.** Visible "—N lines dropped while
  paused—" banner when the pause-overflow forced eviction into main.

### Acceptance test (the user's stated immediate goal)

- Run mail app, watch the maildb console scroll past startup messages.
- After app reaches steady state, mouse-wheel up over the maildb
  console window. The view scrolls back; thumb position reflects offset.
- Any startup line that flashed by — including the red `WARNING: mmap
  coherence test FAILED` if it fires — is visible by scrolling back.
- Wheel down to the bottom; tail-anchor re-arms; new status lines
  appear at the bottom in real time.

---

## Sequencing

Task 1 depends on Task 2 only for the red-error coloring polish. We can
land Task 1 first using the existing `Console.AddLine(text, color)` API —
the maildb console will render red error lines today even without the
rewrite. Then Task 2 lands on top.

Recommended order:

1. **Task 1** end-to-end (mlog package + StatusLine struct + sweep). This
   alone makes the maildb console useful; user can read errors live as
   they happen.
2. **Task 2** (console rewrite + scrollbar). User can now scroll back
   to errors that flew past.
