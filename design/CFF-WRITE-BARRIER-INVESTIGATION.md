# CFF Growslice Panic — Write-Barrier Investigation (paused 2026-04-17)

Paused to pursue a different solution the user has in mind. This document
captures where the investigation left off so it can be resumed.

## Symptom

fontsvc.maz crashes during CFF glyph rendering in go-text/typesetting. Two
different failure modes seen across runs (non-deterministic — memory
corruption pattern):

- `[signal SIGSEGV: segmentation violation code=0x1 addr=0x70000000000000]`
  at `(*CharstringReader).ensureClosePath` (charstrings.go:163 — the
  `append(out.Segments, ...)` inside ensureClosePath).
- `panic: runtime error: growslice: len out of range` — growslice's internal
  sanity check (`uint(newLen) < uint(oldCap)`) firing, meaning the slice's
  cap field is garbage before growslice is entered.

Crash always happens after loading the Italic font — the Regular font renders
fine first, then GC runs one full cycle (2 writeBarrier transitions), then
Italic rendering crashes.

## Confirmed (not the bug)

- **go-text/typesetting library is fine on stock Go 1.26.2**: standalone test
  at `/tmp/cff_test/main.go` renders both Regular + Italic (362 glyphs each,
  0 panics, max 38 segments). The bug is specific to fontsvc.maz's execution
  environment.
- **RegisterMazWriteBarrier IS being called for fontsvc.maz** (log:
  `[runtime] RegisterMazWriteBarrier: registered .maz writeBarrier at
  0x4bc8960 enabled= false`).
- **syncMazWriteBarriers IS firing at STW exit** (just added instrumentation
  confirmed 2 transitions per GC cycle: true→false).
- **Compiled fontsvc.maz code reads the correct writeBarrier address**:
  disassembled `(*CharstringReader).move` at offset a5354 shows `adrp x27,
  0x263000; ldr w6, [x27, #2400]` — reads `runtime.writeBarrier` at file
  offset 0x263960 which matches the registered VA (+ load base).
- **Body-trampolined runtime asm symbols are patched correctly**:
  `runtime.morestack.abi0`, `runtime.morestack_noctxt.abi0`,
  `runtime.wbBufFlush.abi0` (list at cmd/maz-reloc/main.go:445).
- **Go P-struct offsets (`p_wbBuf+wbBuf_next/end`) identical between host and
  .maz**: both built with Go 1.26.2, so gcWriteBarrier's fixed offsets match.
- **GOEXPERIMENT is `norandomizedheapbase64,nogreenteagc` globally** (set in
  top-level Taskfile.yml).

## Not yet disproven / still suspicious

1. **Timing gap in write-barrier sync**: host `setGCPhase(_GCmark)` flips
   host `writeBarrier.enabled=true` during STW, but our
   `syncMazWriteBarriers` in `startTheWorldWithSema` only runs after
   `worldStarted()` — technically after setGCPhase, but both happen inside
   STW, so .maz code shouldn't execute in the gap. Timing seems correct on
   paper but hasn't been runtime-verified.
2. **[]ot.Segment GC bitmap after typemap merge** — `ot.Segment` is
   `{uint8, [3]{float32, float32}}` = 28 bytes, noscan. In theory this slice's
   backing array is scan-free. But if `buildCompleteTypemap` redirected the
   .maz's `*_type` for `[]ot.Segment` to a host type that differs in Size_ or
   GC bitmap, growslice would compute wrong capacity. (Separate pending task
   — #19.)
3. **Race between growslice return and slice-header store**: fontsvc.maz's
   compiled code writes slice.cap at `a5350 str x2, [x5, #16]` BEFORE the
   slice.array write at `a5360-a536c`. If async preemption + GC interrupts
   here, the stack frame has registers saved but the heap slice header is in
   a transient inconsistent state. Normally write barriers protect this; if
   they don't fire, GC could miss the new array.

## Key facts about architecture

- fontsvc.maz is loaded by rachel (not kmazarin). rachel is the HOST.
- rachel was built with the userspace overlay at
  `mazarin/overlay/userspace/runtime/` (files: maz_moduledata.go, proc.go,
  and others — NO slice.go or mgc.go in that overlay).
- fontsvc.maz was built with the thin-overlay at
  `build/shepherd-overlay/runtime/` (thin stubs for all runtime funcs that
  get JMP-patched at load time). slice.go there has stubs that panic with
  `_thinStubPanic("runtime.growslice")` etc.
- Kernel config at `config/kernel.arm64.toml`: `gc_percentage=10000`,
  `go_mem_limit=24` (I bumped to 256 during testing — didn't change
  behavior, REVERT before resuming).

## Current instrumentation (still in place)

**File: `mazarin/overlay/userspace/runtime/maz_moduledata.go`**

Added `mazWriteBarrierLastVal` + `mazWriteBarrierSyncCount` globals;
`syncMazWriteBarriers` now prints:
```
[runtime] syncMazWriteBarriers: transition enabled=<v> n=<count> calls=<N>
```
on every false↔true transition. Current observation: exactly 2 transitions
fire before crash (one complete GC cycle). Existing runtime behavior is
unchanged.

## Config change still in place — REVERT ON RESUME

`config/kernel.arm64.toml`:
- `go_mem_limit = 256` (was 24). Reset to 24.

## Uncommitted files

```
M CLAUDE.md
M mazarin/overlay/userspace/runtime/maz_moduledata.go     <- instrumentation
M mazarin/overlay/userspace/runtime/syscall_linux.go
M runtime-patches/diplomat-linux/syscall_linux.go
M runtime-patches/syscall/syscall_linux.go
M config/kernel.arm64.toml                                 <- go_mem_limit bump
?? design/                                                 <- this file
```

## If resuming

Next diagnostic step would have been: force `runtime.GC()` to run before
every glyph render in fontsvc, so the GC-correlation hypothesis can be
tested reliably. Or, add slice.go to the userspace runtime overlay with
instrumentation at growslice entry to log the exact (et, oldCap, newLen,
num) values and distinguish garbage inputs from growslice misbehavior.

Also pending (task #19): verify `*_type` pointer used by fontsvc.maz's
compiled `append` call (adrp x4, 0x21c000 + #0x880 = 0x21c880) still points
to an unmodified `[]ot.Segment` type descriptor with `Size_=28`, GCdata nil,
after typelinksinit + buildCompleteTypemap run.
