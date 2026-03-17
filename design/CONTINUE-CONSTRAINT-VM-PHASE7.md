# Constraint VM Phase 7 — Kernel-Published Attributes

## Context

Phases 1–6 are complete. The constraint VM has:
- Flat shared-page attribute storage with dirty propagation (kernel DFS walk)
- Handle[T] client API with lazy evaluation on Get()
- Cascading constraint chains (evaluate-on-deref)
- Eager dirty notification via WaitDirty (per-priest queue, thread blocking)
- Change-gated propagation (skip if bitwise-equal value)
- handletest priest with 8 passing tests on all 3 architectures

**Problem**: All attributes so far are created by userspace priests. The kernel
has no presence in the attribute namespace. A priest wanting the current time,
mouse position, or screen dimensions has no way to discover or react to these
kernel-owned values through the constraint system. A clock app would have to
poll via syscalls instead of using the reactive OnDirty mechanism.

**Solution**: Phase 7 adds kernel-published attributes. The kernel creates
attributes under `attr:///kernel/...` during boot and updates them from safe
(non-nosplit) context — a dedicated kernel goroutine for time, and the input
event bottom-half path for mouse state. Priests discover and depend on these
attributes using the existing deref/find/WaitDirty infrastructure. A new
`clocktest` priest demonstrates the full end-to-end pipeline.

## Existing Infrastructure

Already in place (no changes needed):
- `ktime.GetTime()` — returns `(seconds, nanoseconds)` from RTC + timer ticks
- `gpu.GetWidth()`, `gpu.GetHeight()` — screen dimensions from VirtIO GPU init
- Mouse events flow through soft IRQ ring buffers (top-half pushes, userspace drains)
- `KernelIdleLoop` (thread 0) and `StartBottomHalfProcessors()` — patterns for kernel goroutines
- `FlagEagerNotify` + `WaitDirty` — Phase 6 notification infrastructure
- `enqueueNotification` + `wakeDirtyNotifyThread` — per-priest notification delivery

## Implementation Plan

### Step 1: Kernel-internal attribute creation API

Currently `SyscallAttrCreate` rejects `kernel/...` URIs (`return -1 // EPERM`).
The kernel needs an internal function to create attributes under this namespace
without going through the syscall path.

**File**: `kmazarin/ksyscall/constraint_kernel.go` (new)

Add `KernelAttrCreate(uri string, valueType uint8) (uint16, bool)`:
- Validates URI starts with `attr:///kernel/`
- Allocates a node slot via `attrMgr.allocNode()`
- Sets `Owner = 0` (kernel-owned)
- Sets `Kind = AttrKindValue`
- Inserts into namespace trie
- Returns `(slot, true)` on success

Add `KernelAttrWriteI64(slot uint16, val int64)`:
- Builds a FlatValue for int64
- Does seqlock write + change-gated dirty propagation
- Safe to call from normal Go context (not nosplit)

Add `KernelAttrWriteBool(slot uint16, val bool)`:
- Same pattern for bool type

These are kernel-internal functions (not syscalls). They bypass ownership
checks since the kernel is the authority.

### Step 2: Boot-time attribute publishing

**File**: `kmazarin/ksyscall/constraint_kernel.go`

Add `PublishKernelAttributes()`:
- Called from kernel init after `InitKernelAttrManager()`
- Creates these attributes:

```
attr:///kernel/int64/time/utc_seconds     — wall-clock seconds since epoch
attr:///kernel/int64/time/utc_nanos       — nanoseconds within current second
attr:///kernel/int64/screen/width         — display width in pixels
attr:///kernel/int64/screen/height        — display height in pixels
attr:///kernel/int64/mouse/x              — accumulated mouse X position
attr:///kernel/int64/mouse/y              — accumulated mouse Y position
attr:///kernel/bool/mouse/leftDown        — left mouse button state
attr:///kernel/bool/darkMode              — system-wide dark mode toggle
```

Screen dimensions are written once at publish time (they don't change).
darkMode starts as false. Time and mouse attributes are updated dynamically
by subsequent steps.

Store the slot numbers in package-level variables for fast access by the
update goroutines:

```go
var (
    slotTimeSeconds uint16
    slotTimeNanos   uint16
    slotScreenW     uint16
    slotScreenH     uint16
    slotMouseX      uint16
    slotMouseY      uint16
    slotMouseLeft   uint16
    slotDarkMode    uint16
)
```

### Step 3: Time update goroutine

**File**: `kmazarin/ksyscall/constraint_kernel.go`

Add `StartKernelAttrUpdaters()`:
- Called after `PublishKernelAttributes()` from the main kernel init path
- Spawns a goroutine that updates time attributes once per second

The goroutine uses a simple loop. It cannot use `time.Sleep` (the Go runtime's
timer depends on kernel infrastructure that may not support it for kernel
threads). Instead, it uses the existing `WaitSoftIRQ`-style blocking with a
timer deadline, or a simpler approach: it calls `enableIRQsAndWait()` in a
loop, checking elapsed time via `ktime.GetTime()`. When the second changes,
it writes the new value.

**Simpler approach**: Use a channel + deadline. The kernel's deadline queue
(`AddDeadlineStatic`) supports one-shot wakeups. Register a 1-second deadline
that sends on a channel. The goroutine waits on the channel, updates the time
attribute, re-registers the deadline.

**Simplest approach** (recommended for Phase 7): Poll in a tight yield loop.
The goroutine calls `runtime.Gosched()` between checks. This is not ideal
long-term but avoids new blocking infrastructure for a single goroutine.

```go
func timeUpdateLoop() {
    var lastSec int64
    for {
        sec, nanos := ktime.GetTime()
        if sec != lastSec {
            KernelAttrWriteI64(slotTimeSeconds, sec)
            KernelAttrWriteI64(slotTimeNanos, int64(nanos))
            lastSec = sec
        }
        runtime_Gosched() // yield to other goroutines
    }
}
```

Note: `runtime_Gosched` is accessed via go:linkname (the kernel binary has
the Go runtime linked in). This goroutine runs on the kernel's M/P and
yields cooperatively.

### Step 4: Mouse position tracking (kernel-side)

**File**: `kmazarin/ksyscall/constraint_kernel.go`

The kernel currently passes mouse events (REL_X, REL_Y, BTN_LEFT) directly
to userspace via soft IRQ ring buffers. For kernel-published attributes, we
need the kernel to also track the accumulated mouse position.

**Approach**: Hook into the input event top-half or bottom-half path. When
a mouse event is pushed to the ring buffer, also update kernel-side accumulators.
Then, a bottom-half or the time update goroutine writes the accumulated position
to the kernel attributes.

**Top-half accumulation (nosplit-safe)**:
- Add atomic accumulators: `var mouseAccumX, mouseAccumY int64`
- Add atomic flag: `var mouseAccumDirty uint32`
- In the mouse event push path (NonTimerIRQTopHalf or the VirtIO input handler),
  after pushing to the ring, also:
  ```go
  atomic.AddInt64(&mouseAccumX, int64(int32(ev.Value)))  // for REL_X
  atomic.AddInt64(&mouseAccumY, int64(int32(ev.Value)))  // for REL_Y
  atomic.StoreUint32(&mouseAccumDirty, 1)
  ```
- Button state: `atomic.StoreUint32(&mouseLeftDown, value)`

**Bottom-half flush**: The time update goroutine (or a separate mouse goroutine)
checks `mouseAccumDirty` and writes the kernel attributes:
```go
if atomic.CompareAndSwapUint32(&mouseAccumDirty, 1, 0) {
    x := atomic.LoadInt64(&mouseAccumX)
    y := atomic.LoadInt64(&mouseAccumY)
    KernelAttrWriteI64(slotMouseX, x)
    KernelAttrWriteI64(slotMouseY, y)
    left := atomic.LoadUint32(&mouseLeftDown) != 0
    KernelAttrWriteBool(slotMouseLeft, left)
}
```

The dirty propagation + eager notification runs in this goroutine's context
(normal Go, not nosplit), which is safe.

**Alternative (simpler for Phase 7)**: Skip kernel-side mouse tracking for now.
Focus on time + screen + darkMode. Mouse attributes can be added in a follow-up.
The clocktest demo only needs time.

### Step 5: Wire into kernel boot sequence

**File**: `kmazarin/kmazarin/main.go` (or wherever simpleMain/kernel init lives)

After `InitKernelAttrManager()` succeeds, call:
```go
ksyscall.PublishKernelAttributes()
ksyscall.StartKernelAttrUpdaters()
```

This needs a linkname bridge or direct function call depending on package
boundaries. The pattern follows `StartBottomHalfProcessors()`.

### Step 6: clocktest priest

**File**: `flock/cmd/clocktest/main.go` (new)

A minimal priest that demonstrates the full constraint pipeline:
1. Initialize the attribute library (`attr.Init()`)
2. Create a constraint that reads `attr:///kernel/int64/time/utc_seconds`
   and formats it as HH:MM:SS (using modular arithmetic in the VM bytecode,
   or a simpler approach: read the raw seconds and format in Go)
3. Mark the constraint (or the time value handle) as eager
4. Enter a loop calling `WaitDirty()` → on wake, `Get()` the constraint →
   print the formatted time to serial

**Simpler approach (recommended)**: Don't use a VM constraint for formatting.
Instead:
1. Create a local Handle that derefs `attr:///kernel/int64/time/utc_seconds`
   via a trivial identity constraint (or use Find/deref directly)
2. Mark it eager
3. WaitDirty loop → read seconds → format in Go → print

```go
func main() {
    attr.Init()

    // Discover the kernel time attribute via deref.
    timeSec := attr.ConstraintI64(
        "attr:///priest/clocktest/time_sec",
        identityProg("attr:///kernel/int64/time/utc_seconds"),
        /* no local deps — deref discovers the kernel attr dynamically */
    )
    timeSec.SetEager(true)

    fmt.Println("clocktest: waiting for time updates...")
    for {
        slots := attr.WaitDirty()
        if slots == nil {
            continue // overflow, re-scan
        }
        sec := timeSec.Get()
        h := (sec / 3600) % 24
        m := (sec / 60) % 60
        s := sec % 60
        fmt.Printf("clocktest: %02d:%02d:%02d (epoch=%d)\n", h, m, s, sec)
    }
}
```

### Step 7: Build integration

**File**: `Taskfile.yml`

Add `clocktest` to the priest build targets (following the handletest pattern):
- Build `flock/cmd/clocktest` as a priest ELF
- Apply overlays (userspace + priest)
- Include in disk image
- Add to TOML boot config (`config/kmazarin-*.toml`)

**File**: `config/kmazarin-arm64.toml` (and x86_64, riscv64 variants)

Add clocktest to the boot sequence:
```toml
[[priest]]
name = "clocktest"
path = "/clocktest.elf"
```

## Verification

1. Build all 3 architectures
2. Run on ARM64 TCG — verify clocktest prints time once per second
3. Run on x86_64 — same verification
4. Run on RISC-V — same verification
5. Verify handletest still passes all 8 tests (no regression)
6. 60s stability test — clocktest prints ~60 time lines, no panics

## Syscall number summary

No new syscalls. Phase 7 uses kernel-internal APIs (not syscalls) for
attribute creation and writing. Priests use the existing Phase 3-6 syscalls
to discover and read kernel-published attributes.

## Files changed (estimated)

### New files
- `kmazarin/ksyscall/constraint_kernel.go` — kernel-internal attribute API,
  PublishKernelAttributes, time update goroutine
- `flock/cmd/clocktest/main.go` — clock demo priest

### Modified files
- `kmazarin/kmazarin/main.go` — call PublishKernelAttributes + StartKernelAttrUpdaters
  (or a bridge file if needed)
- `Taskfile.yml` — build clocktest
- `config/kmazarin-arm64.toml` — add clocktest to boot
- `config/kmazarin-x86_64.toml` — add clocktest to boot
- `config/kmazarin-riscv64.toml` — add clocktest to boot

### Possibly modified (for mouse tracking, if included)
- `kmazarin/kmazarin/bottom_half.go` — mouse event accumulation
- `kmazarin/device/virtio/input/input.go` — atomic accumulators in event path

## Design decisions

### Kernel attribute Owner field
Kernel-owned attributes use `Owner = 0`. This is already the convention
(`FlatAttrNode.Owner` comment says "0 = kernel"). Priests cannot write to
kernel-owned attributes (ownership check in SyscallAttrWrite rejects).

### Time update frequency
Once per second for `utc_seconds`. The goroutine checks `ktime.GetTime()`
and only writes when the second changes. Change-gating prevents spurious
dirty propagation if the goroutine runs multiple times within the same second.

### Why not update from timer IRQ
The timer IRQ top-half is nosplit and runs with IRQs masked. Dirty propagation
involves walking the dependency graph (potentially touching many nodes) which
is not nosplit-safe. The goroutine approach runs in normal Go context where
all operations are safe.

### Mouse tracking scope
Mouse position tracking requires kernel-side accumulators updated from the
input event path. This is straightforward but adds complexity to the nosplit
top-half. For Phase 7, mouse attributes can be deferred — the clocktest demo
only needs time. Mouse tracking can be added as a Phase 7B follow-up or
folded into Phase 8 (interactor framework) where it's actually needed.

### darkMode toggle
Published as `attr:///kernel/bool/darkMode`, initially false. For Phase 7,
it's a static attribute. A keyboard shortcut toggle (e.g., F5 in dapope)
can be added later. The constraint infrastructure already supports it — any
priest with a constraint depending on darkMode will be notified when it changes.
