# Continuation prompt — kmazarin x86_64 nosplit stack overflow

> **NOTE 2026-05-01**: bug-B work is paused; the prior bug-B continuation prompt is preserved at the bottom of this file under "PAUSED BUG-B CONTEXT" so it can be resumed once x86_64 builds again. Resume bug-B by promoting this prompt back to the top once the linker error below is fixed.

## Status

`$GO tool task kmazarin:x86_64` fails at link time. ARM64 builds and boots cleanly through `[mail] cache ready` (post-Gap-2 + cleanup). x86_64 has been silently broken since some time after 2026-04-24 (the date on the stale `build/kmazarin-amd64.elf` in master). The agent that landed Gap 2 only smoke-tested ARM64; the regression has been hiding in plain sight.

This blocks x86_64 boot smoke verification, which in turn blocks click-test sweeps and any cross-architecture validation of the mazlink work.

## Symptom

```
$ $GO tool task kmazarin:x86_64
... (compile completes successfully) ...
main.syscallEntry: nosplit stack over 792 byte limit
                                                    56 bytes over limit
[...]
main.isrDev46: nosplit stack over 792 byte limit
                                                    152 bytes over limit
                                                    grows 8 bytes, calls runtime.morestack<0>
                                                    grows 160 bytes, calls runtime.panicBounds64<1>
                                                    grows 40 bytes, calls runtime.panicBounds<1>
/opt/homebrew/Cellar/go/1.26.2/libexec/pkg/tool/darwin_arm64/link: too many errors
task: Failed to run task "kmazarin:x86_64": exit status 1
```

The link error reports the call chain: `isrDev46` (NOSPLIT entry stub) → ... → `runtime.panicBounds64` and `runtime.morestack` are pulling 160 + 40 + 8 bytes into the chain. ARM64 builds fine because its entry stubs in `kmazarin/kmazarin/exceptions_arm64.s` have a different layout that doesn't pull these in.

## What's affected

x86_64-only NOSPLIT entry stubs:
- `main.syscallEntry` — x86_64 SYSCALL instruction handler.
- `main.isrDev32..isrDev47` — IOAPIC device interrupt entry stubs (16 of them) in `kmazarin/kmazarin/exceptions_amd64.s`. Each is `NOSPLIT|NOFRAME, $0`, pushes a vector number, jumps to `common_exception_entry`.

The chain from there reaches Go code that calls a function which panics on bounds — `runtime.panicBounds64` materialised by recent Go runtime versions inside the NOSPLIT chain.

ARM64's `exceptions_arm64.s` uses a different dispatch (`vector_table` macro-driven) and doesn't trip the limit.

## Confirmed pre-existing at master

Reproduced both in worktree (post-rebase) and at master HEAD `/Users/iansmith/mazzy@4842c90`. Same error. Stale `build/kmazarin-amd64.elf` in master is dated **Apr 24 12:00** — that's when x86_64 last built successfully. Something in Go's runtime or in the kernel sources changed between then and now.

## Suspected root cause

A recent Go 1.26.x update made `runtime.panicBounds64` (and probably `runtime.panicBounds`) reachable through the NOSPLIT chain from `common_exception_entry` with larger stack footprints than before. The Go linker's NOSPLIT computation walks the call graph and sums max stack usage; a small change in `panicBounds64` can cascade.

This is likely a reversion of older Go behavior, or a new `// go:nosplit` annotation somewhere that pulled functions into the chain.

## Setup

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
export LOUIS14_DIR=/Users/iansmith/louis14
export FONTS_DIR=/Users/iansmith/louis14/fonts
```

## Investigation steps

### Step 1: capture the full call chain

The link error lists the chain in a tree-like format (deepest indent = leaf). Capture the full output:

```bash
$GO tool task kmazarin:x86_64 2>&1 | tee /tmp/kmazarin-x86_64-link-error.log
$GO tool safe-serial-read /tmp/kmazarin-x86_64-link-error.log | head -200
```

Walk the chain for `isrDev46` and `syscallEntry` — note every function on the path and its stack-growth contribution. The chain probably looks like:

```
isrDev46 (NOSPLIT, 0 bytes, JMP)
  → common_exception_entry (NOSPLIT, X bytes)
    → some Go dispatch function (must also be NOSPLIT to be in this chain)
      → runtime.panicBounds64 (NOSPLIT, 160 bytes — the new offender)
```

### Step 2: identify the Go runtime change

`runtime.panicBounds64` is in `runtime/panic.go` (or similar). Check:

```bash
ls /opt/homebrew/Cellar/go/1.26.2/libexec/src/runtime/panic*.go
grep -n "panicBounds64\|panicBounds\b" /opt/homebrew/Cellar/go/1.26.2/libexec/src/runtime/panic*.go | head -20
```

Compare against an older Go installation if available (e.g., `/Users/iansmith/sdk/go1.25.5/` or wherever the user's older Go lives — see `bin/old.go.1.25.5/` mentioned in memory). Diff `panic*.go` between the two and find what grew.

If the offending function is bigger because it calls something else, follow that thread.

### Step 3: pick the fix

**Option A (preferred): runtime-patches overlay**. Add `runtime-patches/runtime/<file>.go` that overrides the offending function with a NOSPLIT-friendly version. The kmazarin Taskfile already wires `runtime-patches/` into the kmazarin build (see `RUNTIME_PATCHES_DIR` in `Taskfile.yml`). Lowest risk, scoped to kmazarin x86_64.

Concretely, the override would be `//go:nosplit`-tagged versions of the panic helpers that call out to a regular (split-able) function for the actual panic, keeping the NOSPLIT chain small.

**Option B: refactor the entry stubs**. In `kmazarin/kmazarin/exceptions_amd64.s`, change the NOSPLIT chain to do less work — push to a queue, raise a soft IRQ, and let the actual dispatch happen on a normal goroutine stack. Higher cost — touches assembly that already works on ARM64.

**Option C: bump the NOSPLIT stack limit in mazlink**. Add a flag (or hardcoded patch) to `cmd/link/internal/ld/stkcheck.go` raising the limit from 792. Risky — the limit exists because IST/exception stacks have a finite size and growing them silently is not safe.

### Step 4: test

```bash
$GO tool task kmazarin:x86_64                # must succeed
$GO tool task run-x86_64 TIMEOUT=600         # x86_64 TCG is ~10x slower than ARM64 HVF; give it 10 minutes
$GO tool safe-serial-read /tmp/diplomat-serial.log | grep -E "\[mail\] cache ready|panic|fatal|EXIT_GROUP"
```

Look for `[mail] cache ready` and absence of new panics.

Then a 5×TIMEOUT=600 sweep to compare bug-B rate against ARM64 baseline.

## Reminders / non-negotiables

- Use `$GO tool task <target>`; never `go build`/`go vet`/`go test` directly.
- Use `$GO tool safe-serial-read` for `/tmp/diplomat-*.log` — never `cat` or `Read` raw.
- **NEVER set `asyncpreemptoff=1`**, **NEVER set `GOGC=off`**, **always keep `GODEBUG=gccheckmark=1`** (CLAUDE.md mandatory rules).
- Worktree work happens in this branch; don't touch `/Users/iansmith/mazzy` (master) or `/Users/iansmith/louis14` directly.
- Always `git push origin <branch>` explicitly when pushing.

## Done when

- `$GO tool task kmazarin:x86_64` builds clean.
- `$GO tool task run-x86_64 TIMEOUT=600` reaches `[mail] cache ready`.
- A 5×600s x86_64 sweep shows no new failures vs ARM64 baseline.
- The fix is committed via `runtime-patches/` (Option A) or via a focused mazlink/kmazarin asm change (Option B/C), with tracking-file updates.

After this lands: bug-B work resumes (see PAUSED BUG-B CONTEXT below).

---

# PAUSED BUG-B CONTEXT (resume after x86_64 fixed)

The bug-B family of kernel runtime panics fires at/after `[mail] cache ready` on ARM64 HVF (~1-3 in 10 60s sweeps). Three known signatures:

1. **`fatal error: missing deferreturn`** — most common. Stack: `runtime.(*_panic).initOpenCodedDefers` → `runtime.(*_panic).nextFrame.func1` → `runtime.systemstack_switch` → `runtime.(*_panic).start`. Fires on the main goroutine of a launched shepherd. The string is the panic-during-panic message; the original cause is hidden under it.
2. **`runtime.(*mheap).alloc` MemStat overflow** — internal Go runtime heap-accounting invariant violation. Decoded stack bytes contained "MemStat overflow".
3. **mspan corruption signatures** (historical): `freeIndex is not valid`, `sweep increased allocation count`, `nelems=341 nalloc=4024` — GC sweep walking corrupted mspan structs in mail-app's Go heap.

All fire after `[mail] cache ready, initial rebalance first=-1 last=-1 vis=0`. May be a single underlying memory corruption manifesting in different goroutine state, or separate bugs.

### Ruled out

- Buddy double-free / RefCount underflow / unmapLoop hang (commit `ca7f5f6`).
- H-T2 stale PTE in another shepherd's PT memory (`612ed58` Option B, 5×180s, 0 hits).
- H-T1 missing trailing TLB flush at SyscallMunmap (Option A, no-op revert).
- H-T3a kernel write between BuddyFreeTyped and reuse (`c4684ad` free-canary, 5×180s with crash, 0 hits).
- Page-cache audit Stage 2 protocol invariants I1-I5 hold in mainline.
- Page-cache Suspect 5 sysMmapPageFlush over-flush (Stage 3 probe, 0 fires).
- Page-cache Suspect 1 same-VA OVERWRITE gap (Stage 3 probe, 0 fires).
- VA-collision (GG-sweep 10×180s: all `inIPC=132 outIPC=0`, no out-of-IPC SharePages targets).

### Best lead

GG9 GC SIGSEGV had `X8 = " failed "` ASCII in a register-sized field — text overwrote a pointer. Points at heap corruption with a log-string payload.

### Next step (when resuming)

Add `runtime-patches/runtime/traceback.go` guards (KMAZARIN-style) to capture the GG9-class SIGSEGV (`unwinder.resolveInternal` NIL deref). **Prerequisite**: wire `runtime-patches/` into the shepherd build. Today it's only consumed by the kmazarin (kernel) build — add a new `gen-overlay -type shepherd-runtime-patches` target plus an `-overlay=` flag in `maz/shepherd/Taskfile.yml`. Maybe 30 lines of plumbing.

If shepherd overlays show that bug-B is pure heap corruption, switch to:
- **Option B (VirtIO DMA target-PA audit)**: maildb reads BBolt pages from disk via VirtIO block. Audit `kmazarin/kvirtio/block*.go` for DMA target buffer lifecycle.
- **Option C (heap-corruption forensics)**: patch `runtime.(*sweepLocked).sweep` to dump raw mspan bytes when corruption is detected; the byte values should identify the source.

### Pointers

- Investigation history: `task_plan.md` ARCHIVED bug-B sections (extensive).
- Memory: `memory/MEMORY.md` Active Bugs + `memory/bug_b_va_collision_refuted.md`.
- Mazlink Gap 1+Gap 2 unblocking: see `memory/shepherd_overlay_dynlink_experiment.md` (RESOLVED).
- Kernel-side instrumentation toggles: `kmazarin/kmem/stale_pte_check.go`, `kmazarin/kmem/free_canary.go`, `kmazarin/ksyscall/mailbox.go` (VA probe).
