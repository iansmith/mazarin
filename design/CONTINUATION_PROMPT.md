# Continuation Prompt for Race Condition Investigation

Copy-paste everything below this line into a new Claude Code session.

---

## Context

Read `design/RACE_CONDITION.md` for the full writeup. Here's the short version:

Kmazarin (the Go kernel in this project) has a **pre-existing race condition** that corrupts Go runtime mspan metadata under heavy keyboard/mouse input load. The crash is:

```
runtime: s.allocCount= 31 s.nelems= 32
fatal error: s.allocCount != s.nelems && freeIndex == s.nelems
```

This was found during memory overhaul Stage 1 (commit aea77cc). Adding ANY extra code to the `BuddyAllocTyped` hot path in `kmazarin/kmem/buddy.go` — even a no-op function call — changes timing enough to trigger the crash within 20-40 seconds of heavy input. Without the extra code, the race window is missed and the system runs indefinitely.

## Your Task

Investigate the race condition and find the root cause. You are doing **research only** — do not write code yet. Read files, search code, analyze control flow, and produce a diagnosis.

## What to Investigate

The crash is an mspan allocCount/freeIndex mismatch, meaning GC mark bits or alloc bits are being corrupted. This is a concurrency issue — something is modifying span metadata without proper synchronization. The extra code in the buddy allocator shifts preemption timing just enough to open the race window.

### Key Areas to Examine

1. **Context switch code** — `kmazarin/kmazarin/threads.go`
   - `doContextSwitchImpl`, `YieldToReadyThread`, `StartFirstThread`
   - How does it save/restore the Go runtime's per-M state (mcache, current g)?
   - Kmazarin multiplexes multiple priest threads on a single M (M0). Does it properly handle `m.mcache` across context switches?

2. **Timer IRQ and preemption** — `kmazarin/kirq/`
   - How does the timer IRQ handler decide to preempt?
   - Can preemption happen while a goroutine is inside `mallocgc` or `mspan.sweep`?
   - Check `TimerIRQHandlerCanPreempt` and `asyncPreempt` injection

3. **Exception handler assembly** — look at the ARM64 exception vectors
   - `kmazarin/kmazarin/exceptions_arm64.s` or similar
   - How is register state saved/restored? Does it properly handle the g pointer in X28?
   - Is there a window where TLS/g is inconsistent?

4. **Go runtime overlays** — check what Go runtime functions are overlayed
   - `build/kmazarin-overlay.json` lists the overlay mappings
   - Overlayed `usleep`, `futex`, `clone` — any that touch allocation?
   - Does the `clone` overlay properly set up mcache for child threads?

5. **The mcache question** — this is probably the most important
   - In normal Go, each M has its own mcache and goroutines on the same M share it
   - Kmazarin's "threads" are more like processes (each with its own address space)
   - When kmazarin switches from priest A's goroutine to priest B's goroutine on the same M, do both see the same mcache? Is that correct?
   - If priest A's goroutine allocates from span S, gets preempted, and priest B's goroutine allocates from the SAME span S (same mcache → same alloc cache), are the span's freeIndex/allocBits being properly synchronized?

6. **Why input matters** — `kmazarin/device/virtio/input/input.go`
   - Input generates PLIC/GIC interrupts → top-half handlers → preemption
   - More interrupts = more context switches = wider race window
   - Check if the input IRQ handler itself does any heap allocation

### Key Files

- `kmazarin/kmazarin/threads.go` — scheduler, context switch, thread creation
- `kmazarin/kmazarin/main.go` — init, interrupt dispatch
- `kmazarin/kirq/` — IRQ handling, timer, preemption
- `kmazarin/kmazarin/exceptions_arm64.s` — exception vectors (ARM64)
- `kmazarin/kmazarin/bottom_half.go` — interrupt bottom-half processing
- `kmazarin/kmem/buddy.go` — the allocation hot path where timing matters
- `design/RACE_CONDITION.md` — full investigation notes

### Build and Test

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

# To reproduce: uncomment SetPageDescriptor in buddy.go line ~261, then:
$GO tool task run TIMEOUT=60
# Type aggressively on keyboard + move mouse in QEMU window
# Crash within 20-40 seconds

# Safe serial log reader (NEVER cat the raw log):
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log
```

### What a Good Answer Looks Like

Identify the specific code path where the race occurs. For example:
- "Timer IRQ fires at PC=X inside mallocgc, asyncPreempt is injected, goroutine yields with span S partially allocated. New goroutine on same M reads span S from mcache, sees stale freeIndex."
- Or: "Context switch in doContextSwitchImpl doesn't save M0.mcache state. When priest B's goroutine runs, it inherits priest A's partially-used span."

The diagnosis should explain: (1) what two things are racing, (2) what specific memory is being corrupted, and (3) why heavy input makes it more likely.
