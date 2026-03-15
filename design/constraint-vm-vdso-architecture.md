# Constraint VM: VDSO-Based Shared Attribute Architecture

Ian Smith, March 2026

## Overview

This document captures the architectural design for running the Mazarin
Constraint VM in a VDSO-based shared memory model. The constraint VM evaluates
UI layout constraints using verified bytecode (borrowing its safety model from
BPF). Rather than placing the attribute graph inside the kernel or inside a
single priest, the graph lives in kernel-managed shared pages mapped into all
priests' address spaces. Reads are performed by VDSO code injected by the
kernel — zero syscall overhead. Writes are kernel-mediated for fault isolation.

The programming model is constraint-driven: visual state is expressed as pure
constraints over shared attributes, the system maintains consistency
automatically through Hudson's lazy incremental evaluation, and priests react to
changes via coalesced dirty notifications delivered through Go channels. No
polling, no frame loops — the constraint graph drives everything.

## Core Architecture

### Shared Pages

The kernel allocates a set of memory pages to hold the attribute graph: value
attributes, constraint attributes, compiled bytecode, and a string/data region.
These pages are mapped into every priest's address space at a known fixed
virtual address. The kernel is the authority on these pages; priests access them
through VDSO code.

### VDSO for Reads (Hot Path)

The kernel injects VDSO code into each priest's address space. When a priest
needs to read an attribute value (position, size, label, etc.), it calls into
the VDSO. The VDSO implements the constraint VM interpreter and the lazy
evaluation algorithm directly: if the attribute is clean, return the cached
value; if dirty, run the verified bytecode program against its dependencies and
cache the result. No syscall, no kernel entry. This is the hot path and it must
be fast.

This follows the classic VDSO pattern — `gettimeofday()` reads
kernel-maintained time data from userspace without a syscall. Attribute reads
work the same way.

### VDSO for Writes (Cold Path, Kernel-Mediated)

Writes to value attributes (e.g., user input changes a slider value) require
kernel mediation to prevent a buggy or malicious priest from corrupting shared
state. Two approaches are under consideration:

**Short syscall approach**: The VDSO detects it needs to write, issues a
lightweight syscall with (offset, value, size), and the kernel validates and
performs the write. Simple, portable across architectures, and the write path is
cold anyway.

**Page-fault approach**: The shared pages are mapped read-only to userspace. When
the VDSO attempts a write, a data access exception occurs. The kernel's fault
handler checks whether the faulting PC (ELR) falls within a known VDSO address
range. If yes, the kernel performs the write on behalf of the VDSO. If no, it is
a real fault. This is more transparent but requires the kernel to decode the
faulting store instruction (architecture-specific) or temporarily remap the page
and resume.

The short syscall is simpler and recommended unless there is a specific reason to
prefer the transparent fault path. Writes are infrequent compared to reads.

After a write, the kernel performs the dirty propagation walk: marking dependent
attributes as dirty via depth-first traversal with generation counters to handle
diamond dependencies. If any newly-dirtied attributes are marked as
"eager-notify" (see Dirty Notification section), the kernel enqueues a single
coalesced soft IRQ for the owning priest.

### Trust Model

Mazarin already trusts userspace code to handle syscall delegation and
interrupts. The VDSO is strictly less trust than that — the kernel authored the
code and injected it into the priest's binary. The priest cannot modify the VDSO
code. The kernel-mediated write path ensures that even if a priest has a bug,
it cannot corrupt the shared attribute graph (the pages are read-only to
userspace for the page-fault approach, or writes are validated by the kernel for
the syscall approach).

## Data Representation in Shared Pages

### No Go Types in Shared Pages

The shared pages are mmap'd memory outside the Go heap. The Go GC will not scan
them, will not trace pointers through them, and will not manage their lifetime.
This means **no Go `string`, `[]Value`, `func()`, `interface{}`, or any
pointer-containing type** can live in these pages. Any Go pointer in a shared
page is invisible to the GC — the referent could be collected, creating a
dangling pointer.

### Flat C-Like Representation

All data in the shared pages uses fixed-size, pointer-free structures:

- **Values**: 32-byte fixed-size structs supporting 23 types: primitives (I64,
  F64, Bool, Tribool), strings, time types (Timespec, Timezone, Duration, Date),
  geometry (Point2D, Point3D, PointF2D, PointF3D, Radians, Degrees), networking
  (IPv4, IPv6), and identity (PriestId, MazId). See the Phase 1 tactical plan
  for the complete layout.
- **Strings**: Fixed-size buffer with NUL terminator (256 bytes). This covers
  virtually any widget label or layout key.
- **Collections**: `{element_type uint8, offset uint32, count uint16}` pointing
  into a value array region within the shared pages. Collections of all 19
  scalar types are supported (no nested collections).
- **Attribute nodes**: Fixed-size structs with slot indices (not pointers) to
  reference other nodes, bytecode programs, and string data.
- **Compiled bytecode**: Already a flat array of 128-bit fixed-width
  instructions. Maps naturally into shared pages.
- **Graph edges (dependencies)**: Adjacency lists stored as offset/count pairs
  into an edge array region.

This representation is arguably better for the kernel use case than the current
Go-idiomatic `Value` struct: no GC interaction, no allocation during evaluation,
cache-friendly flat layout, and inherently serializable.

### Client-Side Library

A client-side library (in `mazarin/attr`) hides the ugly type marshaling from
constraint authors. At the API boundary, it converts between Go `string` and the
fixed-size buffer representation, wraps VDSO calls in ergonomic Go functions,
and presents a `Get()`/`Set()` interface. Constraint authors never see the
underlying flat representation.

The library also provides:

- `attr.OnDirty(attrs...)` — returns a `chan struct{}` that receives a
  notification when any of the specified attributes become dirty. Internally,
  this marks the attributes as "eager-notify" in the shared pages and bridges
  the kernel's soft IRQ notification to a Go channel (see Dirty Notification).
- `attr.Kernel("name")` — reference to a kernel-published attribute.
- `attr.Shared("name")` — reference to a priest-published attribute visible to
  all priests.
- Typed constructors: `attr.NewConstraintI64(src, deps...)`,
  `attr.NewConstraintBool(...)`, `attr.NewConstraintStr(...)`.
- Value attributes: `attr.ValueI64(initial)`, `attr.ValueBool(initial)`, etc.
- Dependency references: `attr.DepI64(attr)`, `attr.DepBool(attr)`, etc.

## Dirty Notification and the Reactive Programming Model

### Lazy Evaluation Is the Default

In Hudson's Eval/Vite algorithm, when a value changes, dirty flags propagate
eagerly through the dependency graph (the "Vite" / invalidate pass), but
recomputation happens lazily when someone calls `Get()` (the "Eval" pass). The
beauty is that the invalidation pass is cheap (just setting flag bits) and
evaluation only happens when values are actually needed.

This model is self-sustaining. The constraint system "just works" — no polling,
no event loops, no manual triggering. You declare dependencies, and the system
maintains consistency automatically.

### Eager Notification for Display Updates

Some attributes represent visible state that should trigger a redraw when dirty.
Rather than requiring priests to poll or run a frame loop, the system provides a
notification mechanism:

1. The priest/maz calls `attr.OnDirty(attrs...)` to mark attributes as
   "eager-notify" and receive a Go channel.
2. During the dirty propagation walk (in the kernel, after a `Set()`), if the
   walk reaches an eager-notify attribute, the kernel sets a "notification
   pending" flag for the owning priest.
3. If the flag was not already set (coalescing — avoids flooding the priest with
   redundant notifications), the kernel enqueues a single soft IRQ event for the
   priest.
4. The client library's internal goroutine receives the soft IRQ via
   `WaitSoftIRQBlocking` and sends on the Go channel (non-blocking send —
   further coalescing via Go's channel semantics).
5. The priest's goroutine receives from the channel and calls its redraw
   function, which calls `Get()` on display attributes, triggering lazy
   evaluation via the VDSO.

The priest never sees soft IRQs, dirty flags, or the shared pages. It sees a
channel.

### Change-Gated Propagation

An important optimization: when a constraint is re-evaluated and the result
equals the cached value, dirty flags are **not** propagated to its dependents.
This prevents unnecessary wake-ups.

Without this, any change to a widely-depended-on attribute (time, mouse
position, focus state) would wake ALL consuming priests, even though most would
re-evaluate and get the same result. With change-gated propagation, the dirty
walk stops at the first constraint whose output didn't change.

Hudson's Eval/Vite algorithm supports this naturally: during the "Eval" phase,
if the recomputed value equals the old cached value, dependents are not
re-dirtied. The dirty flags from the "Vite" phase are conservative; the eval
phase prunes them.

### Dirty Propagation Crosses Priest Boundaries

The dirty propagation walk (the "Vite" pass) crosses priest boundaries freely —
it's only setting flag bits in the shared pages. Any VDSO or the kernel can do
this. But **evaluation** (the "Eval" pass) always happens in the owning priest's
context, triggered by that priest's own `Get()` calls.

| Phase | Who does it | Where it runs | Crosses priests? |
|---|---|---|---|
| `Set()` (write) | Kernel or owning priest | Kernel mode (syscall) | N/A |
| Dirty propagation | Whoever called `Set()` | Shared pages (flags only) | Yes |
| Notification | Kernel (during dirty walk) | Kernel sets per-priest flag | Yes |
| Evaluation (`Get()`) | Owning priest only | VDSO in priest context | No |
| Redraw | Owning priest | Priest code | No |

## Input Routing: Layered Attribute Model

### Three Layers

Raw hardware input is published by the kernel. A compositor priest (dapope)
applies routing policy. Application priests consume routed input through pure
constraints. Priests never consume raw kernel input directly for interactive
behavior.

```
Layer 0 (kernel):  mouse.x, mouse.y, mouse.leftDown       (raw hardware state)
Layer 1 (dapope):  focus.priestId, focus.leftDown           (routed to one priest)
Layer 2 (priest):  myPressed = focus.leftDown && focus.priestId == myId
```

### Kernel-Published Input State

The kernel publishes raw input device state as value attributes in the shared
pages:

- `mouse.x` (int64) — current cursor X position
- `mouse.y` (int64) — current cursor Y position
- `mouse.leftDown` (bool) — true while left button is held

These are continuous state, not events. They are updated when input device
interrupts arrive. The kernel's top-half handles the hardware; the bottom-half
updates the shared page attributes via `Set()`, which triggers dirty
propagation.

### Dapope: The Routing Layer

Dapope (the compositor priest) watches raw kernel input and applies routing
policy — specifically, hit-testing to determine which priest's window is under
the cursor. This is the one piece of imperative input handling in the system:

```go
// Dapope watches raw kernel mouse state
go func() {
    wasDown := false
    for range attr.OnDirty(kernelLeftDown) {
        down := kernelLeftDown.Get()
        if down && !wasDown {
            // Mouse just pressed — hit-test to find target priest
            mx, my := kernelMouseX.Get(), kernelMouseY.Get()
            targetId := hitTest(mx, my)  // walk z-ordered window list
            focusPriestId.Set(targetId)  // set priestId FIRST
            focusLeftDown.Set(true)      // THEN set leftDown
        } else if !down && wasDown {
            focusLeftDown.Set(false)     // release: clear leftDown first
        }
        wasDown = down
    }
}()
```

Note the ordering: on press, set `priestId` before `leftDown`. Priests react to
`leftDown` transitions; by the time they see it, `priestId` is already correct.
On release, clear `leftDown` first. This avoids a window where a priest sees
`leftDown=true` with a stale `priestId`.

Dapope publishes `focus.priestId` and `focus.leftDown` as shared attributes
visible to all priests.

### Application Priests: Pure Constraint Consumption

Each priest knows its own priest ID (assigned by the kernel at launch). The
priest writes a constraint that filters routed input:

```go
myId := attr.ValueI64(int64(priestId))
focusDown := attr.DepBool(attr.Shared("focus.leftDown"))
focusId   := attr.DepI64(attr.Shared("focus.priestId"))

// Pure constraint: am I the one being addressed?
myPressed := attr.NewConstraintBool(`
    func compute(down bool, focusId, myId int64) bool {
        return down && focusId == myId
    }
`, focusDown, focusId, myId)
```

Only the addressed priest's `myPressed` evaluates to true. With change-gated
propagation, all other priests evaluate `myPressed` → `false` (unchanged), and
their downstream display attributes stay clean — no redraw, no wasted work.

## Programming Model: Concrete Examples

### Button with Toggle (Clock App Format Switch)

Visual state is pure constraints. The only imperative code is the state
transition (toggle), which is inherently non-functional — it depends on history.

```go
// Button geometry
buttonX, buttonY := attr.ValueI64(300), attr.ValueI64(200)
buttonW, buttonH := attr.ValueI64(120), attr.ValueI64(40)

// Mouse state (from dapope's routed input, not raw kernel)
mouseX   := attr.DepI64(attr.Kernel("mouse.x"))
mouseY   := attr.DepI64(attr.Kernel("mouse.y"))
focusDown := attr.DepBool(attr.Shared("focus.leftDown"))
focusId   := attr.DepI64(attr.Shared("focus.priestId"))
myId     := attr.ValueI64(int64(priestId))

// Pure constraint: is the mouse over this button?
mouseOver := attr.NewConstraintBool(`
    func compute(mx, my, bx, by, bw, bh int64) bool {
        return mx >= bx && mx < bx+bw && my >= by && my < by+bh
    }
`, mouseX, mouseY, buttonX, buttonY, buttonW, buttonH)

// Pure constraint: am I the addressed priest and is button pressed?
myPressed := attr.NewConstraintBool(`
    func compute(down bool, focusId, myId int64) bool {
        return down && focusId == myId
    }
`, focusDown, focusId, myId)

// Pure constraint: is THIS BUTTON being pressed?
isPressed := attr.NewConstraintBool(`
    func compute(myPressed, mouseOver bool) bool {
        return myPressed && mouseOver
    }
`, myPressed, mouseOver)

// Pure constraint: button color
buttonColor := attr.NewConstraintI64(`
    func compute(pressed, over bool) int64 {
        if pressed { return 0x4444AA }
        if over    { return 0x6666CC }
        return 0x8888EE
    }
`, isPressed, mouseOver)
```

Note: if the user presses the button, holds, and drags off, `mouseOver` becomes
false → `isPressed` becomes false → button un-highlights. Drag back on while
holding → re-highlights. Correct button behavior, entirely from constraints. No
mouse grab needed.

The toggle (12-hour ↔ 24-hour) is the irreducible imperative piece:

```go
is12Hour := attr.ValueBool(true)

go func() {
    wasPressed := false
    for range attr.OnDirty(isPressed) {
        pressed := isPressed.Get()
        if wasPressed && !pressed && mouseOver.Get() {
            is12Hour.Set(!is12Hour.Get())  // toggle on release-over-button
        }
        wasPressed = pressed
    }
}()
```

Setting `is12Hour` triggers dirty propagation through all clock display
constraints. The clock app's redraw goroutine wakes and redraws.

### Multi-Clock Display (7 Timezones)

Each timezone is a constraint depending on the kernel's `time.utc_seconds`:

```go
func newClockDisplay(utc attr.Attr, offsetHours int64,
                     is12Hour attr.Attr) *attr.ConstraintStr {
    return attr.NewConstraintStr(fmt.Sprintf(`
        func compute(utc int64, twelveHour bool) string {
            seconds := utc + %d
            hours := (seconds / 3600) %% 24
            mins  := (seconds %% 3600) / 60
            if twelveHour {
                suffix := "AM"
                if hours >= 12 { suffix = "PM" }
                hours = hours %% 12
                if hours == 0 { hours = 12 }
                return str_concat(formatNum(hours),
                    str_concat(":", str_concat(formatPadded(mins),
                    str_concat(" ", suffix))))
            }
            return str_concat(formatPadded(hours),
                str_concat(":", formatPadded(mins)))
        }
    `, offsetHours*3600), utc, is12Hour)
}
```

### Complete Clock App

Two goroutines total: one for the button click detection, one for redraw. No
event loop. No polling. The constraint graph connects kernel-published time and
mouse state to display output through pure constraints, with one small
imperative handler for the toggle.

```go
func MazarinMain() {
    // Kernel-published state
    utc      := attr.DepI64(attr.Kernel("time.utc_seconds"))
    mouseX   := attr.DepI64(attr.Kernel("mouse.x"))
    mouseY   := attr.DepI64(attr.Kernel("mouse.y"))
    focusDown := attr.DepBool(attr.Shared("focus.leftDown"))
    focusId   := attr.DepI64(attr.Shared("focus.priestId"))

    myId := attr.ValueI64(int64(priestId))

    // App state
    is12Hour := attr.ValueBool(true)

    // Button (visual state is pure constraints, toggle is imperative)
    // ... (as above)

    // Clock displays
    clocks := []struct{ city string; offset int64 }{
        {"Tokyo", 9}, {"Los Angeles", -8}, {"New York", -5},
        {"London", 0}, {"Paris", 1}, {"Kyiv", 2}, {"Moscow", 3},
    }
    displays := make([]*attr.ConstraintStr, len(clocks))
    displayAttrs := make([]attr.Attr, len(clocks)+1)
    for i, c := range clocks {
        displays[i] = newClockDisplay(utc, c.offset, is12Hour)
        displayAttrs[i] = displays[i]
    }
    displayAttrs[len(clocks)] = buttonColor

    // Redraw goroutine — the only "loop"
    go func() {
        for range attr.OnDirty(displayAttrs...) {
            drawButton("12/24", buttonColor.Get())
            for i, c := range clocks {
                drawClockFace(c.city, displays[i].Get())
            }
        }
    }()
}
```

### How the Clock App Handles Time Updates

The kernel marks `time.utc_seconds` dirty once per second. Dirty propagation
marks all 7 timezone constraints dirty + their display dependents. One coalesced
notification goes to the clock priest. The goroutine wakes, calls `Get()` on
each clock (VDSO evaluates: one addition each), redraws 7 clock faces, goes
back to sleep. The notification rate is bounded by the priest's redraw speed,
not the kernel's tick rate — if the priest is still drawing when the next dirty
notification arrives, it coalesces naturally.

### How the Button Toggle Propagates

When `is12Hour` is toggled by the button handler, dirty propagation marks all 7
clock display constraints dirty (they all depend on `is12Hour`). The redraw
goroutine wakes, calls `Get()` on each clock, each re-evaluates with the new
format flag, and all 7 clocks redraw in the new format. The button handler
doesn't know about the clocks. The clocks don't know about the button. They are
connected only through the `is12Hour` attribute.

## Kernel-Published Attributes

The kernel publishes attributes into the shared pages. This is analogous to
Linux's `/proc` and `/sys` filesystems — kernel state exposed as readable
entities — but without the overhead of VFS, dentries, string parsing, and
open/read/close syscalls.

Examples:

- **Time**: `time.utc_seconds` (int64) — updated once per second. Lazy: if no
  priest reads it, the only cost is a dirty flag write.
- **Input devices**: `mouse.x`, `mouse.y`, `mouse.leftDown` — updated on device
  interrupts.
- **System state**: CPU load, memory pressure, interrupt counts, disk I/O stats.
- **Device state**: VirtIO device status, block device availability.

This is `/proc` done right: lazy evaluation means unused attributes cost
nothing, and the VDSO read path means no syscall overhead for hot attributes.
Because of the laziness of Hudson's algorithm, when clients don't request a
value, the fact that it is changing costs nothing beyond the dirty mark.

### Kernel Write Mechanics

When the kernel updates an attribute (e.g., on a timer tick or device
interrupt), it writes directly to the shared pages — no syscall, no VDSO. The
kernel is in kernel mode and owns the pages:

1. Increment the attribute's seqlock counter (odd) with store-release.
2. Store the new value.
3. Increment the seqlock counter (even) with store-release.
4. Walk dependents in the shared pages, set their dirty flags.
5. If any newly-dirtied attribute is eager-notify, enqueue a coalesced soft IRQ
   for the owning priest.

Steps 1-3 are trivial. Step 4 is cheap (just flag bits). Step 5 is at most one
soft IRQ per priest per dirty walk. The kernel never evaluates constraints — it
only writes values and marks things dirty. All evaluation happens in priest
context via the VDSO.

## Bytecode Loading via ELF Sections

At build time, the constraint compiler (using `go/parser` and `go/types`) emits
verified bytecode into a `.constraint` section in the priest or .maz ELF binary.
This follows the BPF model — BPF programs are loaded from ELF sections (the
`.bpf` sections that libbpf extracts).

At load time, the kernel:

1. Extracts the `.constraint` section from the ELF.
2. Re-verifies the bytecode (single-pass abstract interpreter — cheap). The
   kernel cannot trust userspace's claim that a program is safe.
3. Copies the verified bytecode into the shared pages.
4. Registers the constraint attributes and their dependency edges in the shared
   graph.

The compilation boundary is clean: `go/parser` and `go/types` (enormous standard
library packages) live in the build toolchain only. The kernel contains only the
verifier and interpreter, both small.

## Synchronization: Two-Layer Approach for Multi-Core

### The Problem

The VDSO runs in userspace with async preemption enabled. VDSO operations like
constraint evaluation, dirty propagation walks, and multi-word value updates
must be consistent with respect to other priests reading the shared pages — both
on the same core (preemption) and on other cores (concurrent access). A priest
can be preempted at any point by the timer IRQ or by SIGURG (Go async
preemption), and on multi-core, another priest on another core may be reading or
evaluating the same shared data simultaneously.

### Layer 1: Deferred Preemption (vdsoCritical)

Mazarin already has this pattern. The timer IRQ top-half checks `svcDepth` — if
non-zero, preemption is deferred. It checks `SPSR.M[0]` — if EL1h, never
preempt. These are "don't preempt me right now" flags that the kernel trusts
because it controls the code that sets them.

The VDSO is also code the kernel controls. The same pattern applies:

1. The shared pages contain a per-priest `vdsoCritical` counter at a known
   offset.
2. The VDSO increments it before entering a critical section (dirty walk,
   constraint evaluation, multi-word value update).
3. The VDSO decrements it when done.
4. The timer IRQ preemption check adds one condition: if `vdsoCritical != 0`
   for the current thread, defer preemption.
5. Go's async preemption (SIGURG delivery) also checks `vdsoCritical` before
   delivering preemption signals, same as it defers preemption during SVC
   handling.

This is necessary on multi-core to prevent a priest from being preempted while
holding an inter-core lock (which would cause all other cores to spin
wastefully). It is the same as Linux's `preempt_disable()` before
`spin_lock()`.

The constraint VM's fuel limit guarantees the critical section is bounded — the
verifier already proves termination. The preemption hold is provably short.

### Layer 2: Per-Attribute Seqlocks (Inter-Core Consistency)

Deferred preemption prevents same-core preemption but says nothing about other
cores. On multi-core, an actual synchronization protocol is needed between
cores. Each attribute in the shared pages carries a sequence counter (seqlock):

**Writer** (VDSO evaluating a dirty constraint, or kernel write path):

1. Set `vdsoCritical` (defer preemption).
2. Increment the attribute's sequence counter to odd (write in progress).
   Use a **store-release** (`STLR` on ARM64, `fence w,rw` + store on RISC-V)
   so the counter update is visible before the data writes.
3. Write the new value.
4. Increment the sequence counter to even (write complete).
   Use a **store-release** so all data writes are visible before the counter
   update.
5. Clear `vdsoCritical`.

**Reader** (VDSO on another core reading an attribute):

1. **Load-acquire** (`LDAR` on ARM64, load + `fence r,rw` on RISC-V) the
   sequence counter. If odd, a write is in progress — spin briefly or retry.
2. Read the value.
3. **Load-acquire** the sequence counter again. If it changed, the value may
   be inconsistent — retry from step 1.

Readers never block, never take a lock, never risk deadlock. The acquire/release
semantics ensure that when a reader sees an even counter, all associated data
writes are visible. On x86_64, the TSO memory model provides store-store
ordering for free, so the writer can use plain stores; readers still need the
counter check.

### Why Acquire/Release Matters (Not Just Atomicity)

Aligned 64-bit reads and writes are atomic on all three architectures (ARM64,
x86_64, RISC-V) — a reader will never see a torn value (half old, half new).
But atomicity is not the same as visibility and ordering.

An attribute has at least two related memory locations: the dirty flag and the
cached value. On ARM64 and RISC-V (weakly ordered memory models), without
acquire/release:

1. Core 0 stores a new value, then clears the dirty flag.
2. Core 1 sees the dirty flag as "clean" (the flag store propagated first) but
   reads the **old** value (the value store is still in core 0's store buffer).

The value is a valid, non-torn int64, but it's the *wrong* int64 — the stale
one before the update. The seqlock with acquire/release on the counter prevents
this: when the reader sees an even counter via load-acquire, all stores that
happened before the writer's store-release are guaranteed visible.

### Edge Cases

- **Priest crashes inside critical section**: The `vdsoCritical` counter stays
  non-zero and the seqlock counter stays odd. The kernel resets both as part of
  priest teardown.
- **Nesting**: `vdsoCritical` is a counter, not a flag, so nesting works.
- **Single-core**: The seqlock is unnecessary overhead on single-core (deferred
  preemption alone is sufficient), but it is cheap and correct, so there is no
  need for a separate single-core code path.

### Alternatives Considered and Rejected

- **Move constraint processing to kernel**: Avoids the problem but defeats the
  purpose of the VDSO architecture. The whole point is to keep evaluation in
  userspace.
- **Restartable sequences (rseq)**: Linux's solution for preemption-safe
  userspace critical sections. If preempted mid-section, the kernel restarts
  from the beginning. Constraint evaluation is idempotent so this would work,
  but it requires new kernel machinery and abort/retry logic in the VDSO.
  More complex than deferred preemption for no clear benefit.
- **Spinlocks without deferred preemption**: Dangerous. If a priest is
  preempted while holding a spinlock, all other cores spin until the holder is
  rescheduled. Combining `vdsoCritical` + seqlocks avoids this entirely.
- **Hardware transactional memory**: ARM64 TME, x86_64 TSX. Too
  hardware-specific and unreliable (Intel has been disabling TSX; RISC-V has
  no standard TM).

For the write path (which goes through the kernel via short syscall), normal
kernel-side locking (spinlocks, atomics) is appropriate since the kernel
controls preemption directly.

## Attribute Lifecycle and Cleanup

### Ownership Model

Every attribute slot in the shared pages stores an owner (priest ID). The kernel
is the authority on ownership. Kernel-published attributes are owned by a
reserved kernel pseudo-ID and are never cleaned up.

During normal operation, no reference counting happens — zero overhead on the
hot path. The dependency edges in the graph tell the kernel who depends on whom.
The graph *is* the reference information; no separate refcount is needed.

### When a Priest Dies

A priest may own value attributes or constraint attributes that other priests
depend on. When it dies, the kernel walks the shared pages during priest
teardown (which is already a kernel operation — releasing pages, closing
handles, resetting `vdsoCritical`). For each attribute owned by the dead priest:

- **No dependents**: Free the slot immediately. Reclaim the space in the shared
  pages.
- **Has dependents**: Tombstone the attribute — replace its value with an
  "unknown" sentinel and propagate dirty through the dependency graph. Constraint
  programs must handle the sentinel. The VM already supports a tribool type
  (true/false/unknown) that serves this purpose.

The walk is O(n) in total attribute slots but the constant is tiny — it's a scan
over a compact, cache-friendly array in the shared pages. The cost is paid only
at priest death, which is rare, not on every dependency operation.

### Tombstone Propagation

When a tombstoned attribute's dependents are next evaluated (lazily, via the
VDSO), they see "unknown" for the dead dependency. The constraint program can
propagate unknown (the default for any operation involving an unknown operand) or
handle it explicitly. This gives downstream priests a clean signal that a
dependency is gone, rather than silently using stale data.

### Diamond Dependencies

Priest A publishes a value, priest B has a constraint depending on it, priest C
depends on both A's value and B's constraint. If B dies, C's constraint has a
dangling dependency. The tombstone approach handles this cleanly — B's node
becomes "unknown", C's constraint evaluates with one unknown input, and the
result propagates as the constraint program dictates.

Cycles between value attributes owned by different priests are not a problem
because constraints are one-way (dependency edges are acyclic — the attribute
library detects cycles during evaluation).

### Why Not Reference Counting

Reference counting adds an atomic increment/decrement on a shared cache line
for every dependency registration and deregistration. On multi-core, this causes
cache line bouncing on the hot path. The ownership + kernel-walk approach pays
zero cost during normal operation and concentrates cleanup work at priest death,
which is infrequent and already involves kernel-side teardown.

## Interrupt Handling: Existing Top-Half / Bottom-Half Architecture

Mazarin's existing interrupt architecture is retained. The kernel runs fast,
minimal top-halves in interrupt context (IRQs disabled): ACK the hardware,
enqueue an event or set a flag, and return. Bottom-half processing happens in
normal thread context via `WaitSoftIRQBlocking` — a priest's event loop thread
sleeps until events arrive, then processes them with full access to the Go
runtime (allocation, goroutines, syscalls).

This is the same split that L4 microkernels use (and that Linux uses for
hardirq/softirq), and it has proven robust in Mazarin across ARM64, x86_64, and
RISC-V. The top-half is trusted kernel code; the bottom-half is priest code
running at normal priority with preemption enabled.

### L4's Approach and Why We Follow It

L4 microkernels do not run user code with interrupts disabled. When a hardware
interrupt fires, the kernel does the bare minimum (ACK, send IPC notification to
the user-level driver thread), then returns. The driver thread runs at highest
priority in normal thread context — never in interrupt context. L4/Fiasco
experimented with making the kernel fully preemptible (interrupts enabled
everywhere) for real-time latency, but found the complexity unmanageable and
reverted to the traditional approach: kernel runs with interrupts disabled
except at a limited number of explicit preemption points.

Mazarin follows the same model. The kernel's top-half is short, runs with
interrupts disabled, and does no complex processing. All complex processing
(device I/O, UI event handling, constraint evaluation) happens in priest threads
in normal context.

### Future Consideration: VDSO Top-Halves

The VDSO mechanism is potentially generalizable to interrupt handling. The
constraint VM's safety properties — bounded execution (fuel), static
verification, no allocation, totality (no panics) — are what top-halves
require: finish fast, don't block, don't allocate. A VDSO top-half could run
verified bytecode in the priest's address space with interrupts masked, under a
tight fuel limit (perhaps 1,000 instructions).

However, given the L4 experience and the proven robustness of the existing
top-half/bottom-half split, this is not a priority. The current architecture
already achieves low interrupt latency. VDSO top-halves would be an
optimization for specific cases where the context-switch overhead of the
bottom-half path is measurably too high — a situation that has not yet arisen.

Open questions if this is ever pursued:

- **Exception return target**: The kernel's IRQ handler would need to return
  into the VDSO code with interrupts still masked. The priest is running in a
  special mode — not normal userspace execution. How does this interact with
  Go's async preemption and signal delivery?
- **Fuel limit tuning**: What is the right fuel budget for a top-half? It must
  be small enough that worst-case execution fits within interrupt latency
  requirements.
- **Scope**: Which interrupts benefit from userspace top-halves? Timer is
  probably kernel-only. Device interrupts (VirtIO, input) are the natural
  candidates since priests already handle them via delegation.

## Open Questions

1. **Fixed string buffer size**: 256 bytes or 512 bytes? Larger is more
   flexible but wastes space in the shared pages for short strings (most layout
   labels are under 32 bytes). A tiered approach (short strings inline, long
   strings in overflow region) adds complexity.

2. **Shared page sizing**: How many pages? Static allocation or growable? What
   is the expected attribute count for a full UI (hundreds? thousands?)?

3. **VDSO code generation**: Is the VDSO hand-written assembly, compiled Go
   with restrictions, or itself VM bytecode? If it contains the constraint VM
   interpreter, that's a non-trivial amount of code to inject.

4. **Multi-architecture VDSO**: The VDSO must be generated per-architecture
   (ARM64, x86_64, RISC-V). The constraint VM interpreter is written in Go,
   which is portable, but injecting it as a VDSO requires either
   cross-compilation at kernel build time or a portable bytecode format for the
   interpreter itself.

5. **Interaction with .maz loading**: How does the `.constraint` ELF section
   interact with the existing .maz PIE loading infrastructure? Can the
   constraint section be part of a .maz binary, or only part of a priest?

6. **Hit-test policy**: How does dapope determine z-order and window bounds for
   hit-testing? Are window bounds published as attributes (allowing
   constraint-based layout to feed into input routing), or are they managed
   separately?

7. **Drag operations**: The button example doesn't need mouse grab, but sliders
   and scroll bars do. How is "this priest owns the mouse until release"
   expressed? Likely via dapope latching `focus.priestId` on press and not
   re-hit-testing until release.

## References

1. Hudson, S.E. Incremental Attribute Evaluation: A Flexible Algorithm for
   Lazy Update. ACM Transactions on Programming Languages and Systems 13, 3
   (July 1991), 315-341.
2. Hudson, S.E. and Smith, I. Ultra-Lightweight Constraints. Proc. UIST '96,
   ACM Press (1996), 147-155.
3. McCanne, S. and Jacobson, V. The BSD Packet Filter: A New Architecture for
   User-level Packet Capture. Proc. Winter 1993 USENIX Conference, USENIX
   Association (1993), 259-270.
