# mazdl Phase 4 — Continuation Prompt

Use this doc to resume Phase 4 of `design/MAZARIN-DLOPEN.md` (userspace Go
plugin dlopen). Read this file **and** `design/MAZARIN-DLOPEN.md` §9 Phase 4
before editing code.

## Exit criterion (unchanged)

`smoke/host-mazdl /tmp/plugin.maz` prints:
```
hello from mazlink plugin
bump: 1
stress: ok (100 iters)
```

## The bug we just found (2026-04-18)

Root cause of the SIGILL we've been chasing is now understood.

**File note:** `memory/mazlink_funcval_dead_reloc_bug.md` is the canonical
description; this section is a shorter recap.

### Symptom
Plugin init calls `runtime.mapassign_faststr` through the PLT → host entry
at 0x27340 runs fine for ~0x74 bytes → `blr x2` at host+0x273B0 jumps to
`plugin+0x7DBF0` (zero-filled .text padding) → SIGILL.

Registers at SIGILL (no patches, clean run):
```
pc  = 0xffff5eae3bf0   <- plugin+0x7DBF0 (zero padding, udf #0)
lr  = 0x273b4          <- host+0x273B4, right after `blr x2`
```

Dropping an in-process `udf #0` at PLT entry 0x7E05C (the `br x17`) showed
`x17 = 0x27340` — so PLT resolution is **correct**. The crash is downstream.

### What x2 is
host-mazdl disassembly around 0x273A0:
```
273a0: ldr x26, [x0, #72]      ; x26 = maptype[72]  (Hasher field = *funcval)
273a4: ldr x2,  [x26]           ; x2  = funcval.fn  (the function PC)
273ac: add x0, sp, #0xb0
273b0: blr x2                   ; call that PC       <-- jumps into the void
```

`abi.SwissMapType.Hasher` at offset 72 is `*funcval`. The plugin's maptype
points at a funcval in its own `.data.rel.ro`:

```
0x1580d0  runtime.strhash·f  (D/local, 8 bytes)
  contents:               f0db0700 00000000   = 0x7DBF0
  .rela entry at 0x1580d0: R_AARCH64_RELATIVE addend=0x7DBF0
```

Neighboring funcvals have the same shape:
```
0x1580c0  runtime.memhash64·f     -> 0x7DBE0 (in dead text gap)
0x1580c8  runtime.nilinterhash·f  -> 0x7D490 (in dead text gap)
0x1580d0  runtime.strhash·f       -> 0x7DBF0 (in dead text gap)
```

The plugin's real .text runs up to `go:link.addmoduledata` at 0x7BC60,
followed by zero padding until `runtime.etext` at 0x7DC90. `0x7DBF0` is in
that zero-padding gap — the "placeholder" address where mazlink would have
put `runtime.strhash` if the plugin had shipped a copy.

### The real fix (mazlink-side, option A)
When a funcval's underlying function is on the `-dlopen-host-packages`
policy, mazlink should NOT emit the funcval with a RELATIVE reloc to a
placeholder address. Two variants:

1. **Strip the funcval entirely.** Emit every reference to it (e.g.
   `maptype.Hasher = &runtime.strhash·f`) as a GLOB_DAT/ABS64 against the
   host's UNDEF symbol `runtime.strhash·f` (host already exports these as
   `OBJECT GLOBAL DEFAULT` — confirmed in host-mazdl dynsym).
2. **Keep the local funcval, fix its .fn reloc.** Change the RELATIVE reloc
   at `0x1580d0` into a GLOB_DAT/ABS64 against the host's FUNC symbol
   `runtime.strhash`.

Variant 1 is cleaner and costs less memory; variant 2 is a smaller patch.
Both require editing mazlink where it decides reloc types for discarded
host-policy symbols (see `mazlink-patches/cmd/link/internal/...`).

### Unblocking the smoke (option B)
If the mazlink fix is too invasive right now, we can patch mazdl to rewrite
these funcvals at load time:

1. After `applySymbolRelocs`, read the plugin's `.symtab` (via
   `elf.File.Symbols()`).
2. For each symbol `name` ending in `·f` (Unicode middle-dot + `f`) of type
   `STT_OBJECT`, size 8:
   - Derive the host symbol name `strings.TrimSuffix(name, "·f")`.
   - Look it up in `globalSyms`.
   - If found, overwrite the 8 bytes at `relocBase + sym.Value` with the
     host function's address.
3. The segment containing `.data.rel.ro` is already RW at that point (Step
   6 → Step 7 happens after this), so no mprotect dance needed if we do it
   between applySymbolRelocs and the mprotect loop (before line 166 in
   `mazdl/open.go`).

Be aware: there may be other classes of host-policy data objects that
were embedded locally (not just `·f` funcvals). If more show up after this
fix, widen the sweep. But for this smoke test, `·f` is the only class that
matters — the crashing pointer is a funcval.

## Current state of the repo

**Branch**: `feature/mail-dumb` (yes, the name is unrelated to this work;
Phase 4 is sharing this branch.)

**Modified but NOT committed** (left intentionally in this state for
continuation):
- `mazarin/mazdl/open.go` — has **diagnostic Fprintf's** at afterReloc,
  afterMprotect, prePluginInit, postOpen. No live `udf #0` patch (the two
  test patches have both been reverted). Keep or clean up at your
  discretion.
- `mazarin/mazdl/plugin_inittasks.go` — has a single diagnostic Fprintf
  just before `runtimeDoInit(initTasks)`; safe to remove.
- `mazarin/mazdl/host_register.go` — has diagnostic Fprintfs dumping
  loadBase, `runtime.mapassign_faststr` st_value, and the first 6 lines of
  `/proc/self/maps`. Safe to remove.
- `mazarin/mazdl/reloc_arm64.go` — has a JUMP_SLOT diagnostic for the
  `mapassign_faststr` / `mapinitnoop` symbols. Safe to remove.

**Do not forget to remove these diagnostics before committing the Phase 4
fix.**

## Facts you don't need to re-discover

- Host `/tmp/host-mazdl` builds as **ET_EXEC non-PIE** with text at
  `0x10000-0x13B000`. `hostLoadBase` correctly returns 0 in that case, so
  dynsym `st_value` IS the absolute address.
- `runtime.mapassign_faststr` lives at host address 0x27340.
- Plugin PT_LOADs and their perms:
  ```
  vaddr=0x10000  len=0x6e7c0  R-X
  vaddr=0x80000  len=0xa8beb  R--
  vaddr=0x130000 len=0x2dff0  RW-
  vaddr=0x160000 len=0x9ba0   RW-   (<- .got.plt lives here)
  ```
- Funcval table lives in `.data.rel.ro` around 0x158000 — covered by the
  `vaddr=0x130000` RW segment.
- PLT entry for `runtime.mapassign_faststr` is at plugin offset 0x7E050:
  ```
  10 07 00 d0   adrp  x16, page(got)
  11 b2 41 f9   ldr   x17, [x16, #offset]   ; offset 0x360 -> 0x160360
  10 82 0d 91   add   x16, x16, #offset
  20 02 1f d6   br    x17
  ```
  GOT.PLT slot 0x160360 is correctly populated with 0x27340 by JUMP_SLOT
  reloc.
- Host is a Go 1.26.2 dynamically-linked ARM64 binary; plugin is Go 1.26.2
  built with mazgo + mazlink. Neither uses cgo.

## How to run the smoke test

```bash
export GOTOOLCHAIN=auto \
       GO=/opt/homebrew/bin/go \
       QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 \
       QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64

$GO tool task mazlink-smoke
```

Runs via Docker (`mazlink-smoke:latest`, alpine + go1.26.2). Container mounts
`$(pwd):/work`. Script: `smoke/run-smoke.sh`. It builds plugin.so (reference),
host (stdlib plugin), plugin.maz (candidate), host-probe (phase 3 check) and
host-mazdl (phase 4 check), then runs `/tmp/host-mazdl /tmp/plugin.maz`.

Candidate artifacts land in `build/` on the host side: `build/plugin.maz`,
`build/host-mazdl` — useful with `bin/target-objdump`, `bin/target-nm`,
`bin/target-readelf` for post-mortem inspection. The Docker run is
ephemeral (`--rm`), so only these copies persist.

## Recommended next step

Implement **option B** first (loader-side rewrite in mazdl). Ship the smoke
test green. Then circle back to mazlink for **option A** and remove the
loader-side patch once the real fix lands. That sequence keeps us moving on
Phase 4 while respecting that mazlink is delicate.

Concrete sketch for option B in `mazarin/mazdl/open.go`, inserted between
current line 160 (end of `afterReloc` diag) and line 162 (Step 7 comment):

```go
// Rewrite runtime funcvals that mazlink emitted with a dead RELATIVE
// reloc to the host-exported function address. See
// memory/mazlink_funcval_dead_reloc_bug.md.
symtab, err := f.Symbols()
if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
    return nil, errorf("Open", filename, "", "Symbols: %v", err)
}
for _, s := range symtab {
    if elf.ST_TYPE(s.Info) != elf.STT_OBJECT || s.Size != 8 {
        continue
    }
    // "·" is U+00B7 MIDDLE DOT, 2 bytes in UTF-8.
    const fv = "\u00b7f"
    if !strings.HasSuffix(s.Name, fv) {
        continue
    }
    base := strings.TrimSuffix(s.Name, fv)
    e, ok := globalSyms[base]
    if !ok {
        continue
    }
    *(*uint64)(unsafe.Pointer(relocBase + uintptr(s.Value))) = uint64(e.addr)
}
```

Verify with a second run of the smoke test.

## After smoke goes green

Clean up:
- Remove every `fmt.Fprintf(os.Stderr, "mazdl ...")` diagnostic added during
  this investigation.
- Remove the `fmt` / `os` imports if nothing else in the file needs them.
- Confirm Phase 4 exits #2, #3, #4 (singleton goroutines, single heap,
  1000-iter stress) still pass.
- Extend to amd64 (Phase 4a) — asm.go's amd64 counterpart needs the same
  treatment.

## Pointers

- `design/MAZARIN-DLOPEN.md` — canonical design doc for this phase.
- `memory/mazlink_funcval_dead_reloc_bug.md` — this bug's detail.
- `memory/mazlink_phase2_complete.md` — phase 2 state.
- `mazlink-patches/cmd/link/internal/arm64/asm.go` — PLT/GOT generation;
  the eventual home of option A.
- `mazlink-patches/policy/dlopen-host-packages.txt` — which packages are
  host-resident. Do **not** broaden without discussion.
- `smoke/run-smoke.sh` — the inside-container driver.
- `Taskfile.yml` target `mazlink-smoke` — wraps the Docker run.
