# Mazlink Gap 2 — Handle `-dynlink` on host (BuildModeExe + LinkInternal)

**Goal**: Allow the shepherd to build with `-gcflags=all=-dynlink -asmflags=all=-dynlink` instead of the 370-file `cmd/gen-ast-stubs -mode=shepherd` overlay. Once Gap 2 lands, the entire shepherd-overlay generation pipeline can be deleted: `-dynlink` makes the compiler keep all cross-package functions as separately-callable symbols (replacing Job 1, the `//go:noinline` injection); Gap 1 already replaced Job 2 (the keepalive force-reference).

**Why this matters**: with the overlay gone, the shepherd's `runtime/` files are no longer auto-generated. Manual `runtime-patches/`-style overlays — including the bug-B forensic guards in `runtime/traceback.go` (the GG9-class SIGSEGV with `X8 = " failed "` corruption) — can be applied to the shepherd via the same mechanism the kernel uses today.

**Prerequisite**: Gap 1 should land first. This plan assumes `runtime.MazKeepAliveSymbols` and `mazarin/mazhost/keepalive.go` are gone.

---

## Background — what specifically broke when the user tried `-dynlink` on 2026-04-30

The experiment swapped, in `maz/shepherd/Taskfile.yml`:
- **out**: `-overlay=build/merged-shepherd-overlay.json`
- **in**: `-gcflags=all=-dynlink -asmflags=all=-dynlink`

The first failure was `undefined: runtime.MazKeepAliveSymbols` (compile-time) — Gap 1 fixes that. After stubbing it out, the second failure surfaced — the real Gap 2:

```
fmt.(*pp).fmtComplex: unknown reloc to type:uint8: 34 (R_ARM64_GOTPCREL)
fmt.(*pp).fmtComplex: unknown reloc to runtime.writeBarrier: 34 (R_ARM64_GOTPCREL)
... [many similar] ...
internal/abi.(*RegArgs).IntRegArgAddr: unknown reloc to type:string: 34 (R_ARM64_GOTPCREL)
/opt/homebrew/Cellar/go/1.26.2/libexec/pkg/tool/darwin_arm64/link: too many errors
```

### Why it broke (full call-path trace)

The error originates at `/opt/homebrew/Cellar/go/1.26.2/libexec/src/cmd/link/internal/ld/data.go:302` (`unknown reloc to %v: %d (%s)`). That's reached when `thearch.Archreloc(...)` returns `ok=false`. Reaching that point means `Adddynrel` was *not* called for the reloc, so the GOTPCREL never got rewritten into a GOT-based pair, so `archreloc` saw an objabi reloc type it doesn't handle in non-external mode.

Walk the path step by step:

1. **Compiler with `-dynlink`** emits `R_ARM64_GOTPCREL` (objabi value 34) for every cross-translation-unit data reference. This includes references to `type:uint8`, `runtime.writeBarrier`, `type:string`, etc. These targets live in `runtime.a` and are **defined** symbols (not SDYNIMPORT) in the shepherd binary.

2. **`dynrelocsym`** (mazlink-patches `cmd/link/internal/ld/data.go:971`, mirrors stock `data.go:964`) is called for every text + data symbol. Its conditions for routing a reloc to `Adddynrel`:
   - `BuildModePIE && LinkInternal` → call Adddynrel for **all** relocs (line 985 in mazlink-patches).
   - **mazlink addition**: `BuildModePlugin && LinkInternal` → call Adddynrel for **all** relocs (line 996 in mazlink-patches).
   - Otherwise: call Adddynrel **only** for `SDYNIMPORT` targets *or* ELF-formatted relocs (`r.Type() >= objabi.ElfRelocOffset`) (line 1008-1015 in mazlink-patches).

   Shepherd is `BuildModeExe + LinkInternal`. Neither PIE nor Plugin. The GOTPCREL targets `type:uint8` are SDATA (not SDYNIMPORT) and the reloc type is `objabi.R_ARM64_GOTPCREL` (a Go-internal type, not `>= ElfRelocOffset`). **Adddynrel is skipped.**

3. **`archreloc`** (mazlink-patches `cmd/link/internal/arm64/asm.go:886`) is called from `data.go:295` during `relocsym`. For internal linking it has explicit cases for `R_ADDRARM64`, `R_ARM64_PCREL_LDST*`, `R_ARM64_TLS_LE`, `R_ARM64_TLS_IE`, `R_CALLARM64`, `R_ARM64_GOT`, `R_ARM64_PCREL` — but **no internal-linking case for `R_ARM64_GOTPCREL`**. Falls through to `return val, 0, false` at line 1218. `data.go` then logs the "unknown reloc" error.

For comparison, **why plugins work**: the mazlink-patched `adddynrel` at `arm64/asm.go:535-576` has a working GOTPCREL handler. It calls `AddGotSym(target, ldr, syms, targ, uint32(elf.R_AARCH64_GLOB_DAT))` which allocates a GOT slot AND emits an `R_AARCH64_GLOB_DAT` dynamic relocation against that slot, then rewrites the original GOTPCREL into two `R_ARM64_GOT` relocs (one for ADRP, one for LDR). At plugin load time, mazdl resolves the GLOB_DAT entry by writing the host's address into the GOT slot. This handler runs because plugin+internal triggers the `BuildModePlugin && LinkInternal` Adddynrel path in `dynrelocsym`.

### Why we can't just enable that path for the host

The shepherd binary is loaded by the **kernel ELF loader**, not by mazdl. The kernel does not process `R_AARCH64_GLOB_DAT` dynamic relocations on the shepherd ELF — it just maps the segments and jumps to entry. So if we emit `GLOB_DAT` for shepherd-internal GOTPCREL references, the GOT slots stay zero at runtime → first dereference SIGSEGVs.

For the shepherd, GOT slots must be filled **at link time** with the static address of the target symbol. `AddGotSym(..., 0)` (zero relocation type) does exactly that: it allocates a GOT slot whose contents are `addr(targ)`, written by the linker during `address`/`reloc` phases. No runtime help required.

---

## What x86_64 looks like (parallel structure)

`mazlink-patches/cmd/link/internal/amd64/asm.go:478-503` has the same handler shape:

```go
case objabi.R_GOTPCREL:
    if target.IsExternal() {
        return true
    }
    su := ldr.MakeSymbolUpdater(s)
    if target.IsElf() {
        ld.AddGotSym(target, ldr, syms, targ, uint32(elf.R_X86_64_GLOB_DAT))
    } else {
        ld.AddGotSym(target, ldr, syms, targ, 0)
    }
    su.SetRelocSym(rIdx, syms.GOT)
    su.SetRelocAdd(rIdx, r.Add()+int64(ldr.SymGot(targ)))
    return true
```

Same problem: requests GLOB_DAT for ELF, which is wrong for a host-mode shepherd binary. Same fix: route through `AddGotSym(..., 0)` when the binary is a host (no dynamic linker at runtime).

---

## Step-by-step plan

### Step 0 — Set environment

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
```

Confirm Gap 1 has landed (`grep -r MazKeepAlive` should return nothing). If not, stop and finish Gap 1 first.

### Step 1 — Reproduce the failure on a clean baseline

Before changing anything, capture the exact error so post-fix you can diff it.

1. In `maz/shepherd/Taskfile.yml`:
   - Comment out `-overlay={{.ROOT_DIR}}/{{.MERGED_SHEPHERD_OVERLAY_JSON}}` (line 27 for ARM64, line 56 for x86_64).
   - Add `"-gcflags=all=-dynlink" "-asmflags=all=-dynlink"` to the build command line.
   - Drop `-tags mazhost` (the keepalive.go file is gone after Gap 1, so no longer needed). Verify no other file under `mazarin/mazhost/` still uses the tag.
2. Run:
   ```bash
   $GO tool task shepherd:arm64 2>&1 | tee /tmp/gap2-baseline-arm64.log
   $GO tool task shepherd:x86_64 2>&1 | tee /tmp/gap2-baseline-amd64.log
   ```
3. Confirm the log shows `unknown reloc … 34 (R_ARM64_GOTPCREL)` (ARM64) and an analogous x86_64 error (`R_GOTPCREL`, objabi value 29 — see `cmd/internal/objabi/reloctype.go:111`). Save the first 50 lines for later comparison.
4. Revert the Taskfile changes for now (or stash them — you'll re-apply them in Step 4). The intermediate linker work in Steps 2–3 should be developed without the shepherd build broken.

### Step 2 — Linker fix for ARM64

#### 2a — Route GOTPCREL through Adddynrel for host builds

In `mazlink-patches/cmd/link/internal/ld/data.go`, in `dynrelocsym` (currently around line 971-1015), add a third hook **after** the existing `BuildModePlugin && LinkInternal` block:

```go
// mazlink: host with -dlopen-host-exports compiled with -gcflags=-dynlink
// emits GOT-based relocs (R_ARM64_GOTPCREL / R_GOTPCREL) for cross-TU
// references the same way plugin builds do. The host has no dynamic
// linker at runtime, so we still route through Adddynrel to give the
// arch-specific handler a chance to allocate static GOT slots and rewrite
// the relocs into R_ARM64_GOT / R_PCREL form. The GOT slots themselves
// will be filled at link time with the target's static address (see the
// arch Adddynrel changes — host mode requests AddGotSym(...,0), not
// AddGotSym(..., R_*_GLOB_DAT)).
if *flagDlopenHostExports != "" && ctxt.LinkMode == LinkInternal {
    thearch.Adddynrel(target, ldr, syms, s, r, ri)
    continue
}
```

The flag `flagDlopenHostExports` is already declared in `mazdl.go:56` and visible from `data.go` because both files are in package `ld`.

#### 2b — Make Adddynrel's GOTPCREL handler do the right thing for host mode

In `mazlink-patches/cmd/link/internal/arm64/asm.go`, at the `case objabi.R_ARM64_GOTPCREL:` block (currently line 535-576), replace the `if target.IsElf()` branch with logic that distinguishes:

- **plugin-mode target** (`*flagDlopenHostPackages != ""`) → keep current behaviour: `AddGotSym(target, ldr, syms, targ, uint32(elf.R_AARCH64_GLOB_DAT))`. The dynamic loader (mazdl) will fill the slot at plugin-load time.
- **host-mode target** (`*flagDlopenHostExports != ""`) → use `AddGotSym(target, ldr, syms, targ, 0)`. The slot is filled at link time with the target's static address.
- **anything else** with ELF + non-SDYNIMPORT target — defensive: also use `AddGotSym(..., 0)`. The existing path's GLOB_DAT request only made sense for plugin mode.

Concrete diff:

```go
case objabi.R_ARM64_GOTPCREL:
    if target.IsExternal() {
        return true
    }
    if r.Add() != 0 {
        ldr.Errorf(s, "R_ARM64_GOTPCREL with non-zero addend (%v)", r.Add())
    }
    // Pick the right GOT-slot relocation policy:
    //   - Plugin (-dlopen-host-packages): GLOB_DAT, mazdl fills the slot at load time.
    //   - Host (-dlopen-host-exports) or non-dynlink internal: 0, linker fills statically.
    var gotReloc uint32 = 0
    if target.IsElf() && *flagDlopenHostPackages != "" {
        gotReloc = uint32(elf.R_AARCH64_GLOB_DAT)
    }
    ld.AddGotSym(target, ldr, syms, targ, gotReloc)
    rOff := r.Off()
    su := ldr.MakeSymbolUpdater(s)
    su.SetRelocType(rIdx, objabi.R_ARM64_GOT)
    su.SetRelocSiz(rIdx, 4)
    su.SetRelocSym(rIdx, syms.GOT)
    su.SetRelocAdd(rIdx, int64(ldr.SymGot(targ)))
    r2, _ := su.AddRel(objabi.R_ARM64_GOT)
    r2.SetSiz(4)
    r2.SetOff(rOff + 4)
    r2.SetSym(syms.GOT)
    r2.SetAdd(int64(ldr.SymGot(targ)))
    return true
```

This keeps plugin behaviour byte-identical (verify by building a plugin and diffing the .got/.rela.dyn against pre-change). The new behaviour activates only when `-dlopen-host-exports` is in effect.

#### 2c — Confirm `archreloc` handles the rewritten R_ARM64_GOT for host mode

After 2b, the original GOTPCREL has been rewritten to two `R_ARM64_GOT` relocs. The R_ARM64_GOT case at `arm64/asm.go:1108-1131` handles ADRP + LDR instruction patching using `ldr.SymAddr(rs)` — where `rs` is the GOT symbol. For a statically-filled GOT slot, `SymAddr(GOT) + SymGot(targ)` resolves to the address of the slot itself, which is what ADRP+LDR needs. **No archreloc change required for host mode** — the existing R_ARM64_GOT handler is correct.

The slot's *content* (the address of `targ` itself) is written by the GOT-emission pass (`data.go` `addgotsym` family). For host mode, since we passed reloc=0 in 2b, the slot is filled directly with `addr(targ)` at link-output time.

#### 2d — Build mazlink and verify it still compiles

```bash
rm -f bin/mazlink
$GO tool task mazlink-build
```

Should succeed cleanly. If it fails, you have a syntax/type error in 2a or 2b — fix before proceeding.

### Step 3 — Linker fix for x86_64

The x86_64 changes mirror ARM64:

#### 3a — `data.go` already covers it via the same hook from Step 2a (data.go is shared across arches; the new hook fires for both).

#### 3b — `mazlink-patches/cmd/link/internal/amd64/asm.go` `case objabi.R_GOTPCREL:` (line 478-503): same shape change as 2b — gate `R_X86_64_GLOB_DAT` on `*flagDlopenHostPackages != ""`, default to 0.

```go
case objabi.R_GOTPCREL:
    if target.IsExternal() {
        return true
    }
    su := ldr.MakeSymbolUpdater(s)
    var gotReloc uint32 = 0
    if target.IsElf() && *flagDlopenHostPackages != "" {
        gotReloc = uint32(elf.R_X86_64_GLOB_DAT)
    }
    ld.AddGotSym(target, ldr, syms, targ, gotReloc)
    su.SetRelocSym(rIdx, syms.GOT)
    su.SetRelocAdd(rIdx, r.Add()+int64(ldr.SymGot(targ)))
    return true
```

The amd64 GOTPCREL → PC-relative-into-GOT rewrite is one reloc (no ADRP/LDR pair). The R_PCREL case in `archreloc` already handles the result.

#### 3c — Verify amd64 archreloc has no GOTPCREL gap

Walk through `mazlink-patches/cmd/link/internal/amd64/asm.go` `archreloc` (search for "func archreloc"). After Adddynrel's transform, the reloc is now an `R_PCREL` against `syms.GOT` with the slot offset baked into `Add`. Stock archreloc handles `R_PCREL` normally. No extra change needed.

### Step 4 — Switch shepherd to use `-dynlink` and remove the overlay

Now flip the build to use the new path.

1. **`maz/shepherd/Taskfile.yml`**:
   - Remove `-overlay={{.ROOT_DIR}}/{{.MERGED_SHEPHERD_OVERLAY_JSON}}` (line 27 ARM64, line 56 x86_64).
   - Remove `-tags mazhost` (Gap 1 deleted the only file that used the tag; see verification below).
   - Add `"-gcflags=all=-dynlink" "-asmflags=all=-dynlink"` to the build command line, grouped inside the same quoted string as the existing flags or separate — your call but match the existing style of the surrounding flags.
   - Update the dependency lists: drop `':merged-shepherd-overlay'` from the `deps:` array, drop `'{{.MERGED_SHEPHERD_OVERLAY_JSON}}'` and `'{{.SHEPHERD_OVERLAY_DIR}}/**/*'` from `sources:`, drop `'{{.MERGED_SHEPHERD_OVERLAY_AMD64_JSON}}'` / `'{{.SHEPHERD_OVERLAY_AMD64_DIR}}/**/*'` from the x86_64 task.

2. **Confirm `-tags mazhost` is unused**: `grep -rn "//go:build mazhost\|// +build mazhost\|-tags mazhost" mazarin/ maz/`. If anything remains besides the shepherd Taskfile change you just made, decide whether to keep the tag (if the build constraint still has a real purpose) or drop the tag entirely. If keeping, leave the Taskfile flag in. If dropping, also strip the build constraints.

3. **Build & smoke**:
   ```bash
   rm -f build/shepherd-overlay*.json build/merged-shepherd-overlay*.json
   $GO tool task                            # ARM64 default
   $GO tool task shepherd:x86_64
   $GO tool task run-arm64-hvf TIMEOUT=60 > /tmp/gap2-postfix-arm64.out 2>&1
   $GO tool safe-serial-read /tmp/diplomat-arm64-serial.log | grep -E "\[mail\] cache ready|panic|fatal|unresolved" | head -50
   $GO tool task run-x86_64 TIMEOUT=60 > /tmp/gap2-postfix-amd64.out 2>&1
   $GO tool safe-serial-read /tmp/diplomat-serial.log | grep -E "\[mail\] cache ready|panic|fatal|unresolved" | head -50
   ```

4. **5×60s ARM64 HVF sweep** to compare bug-B rate against pre-Gap-2 baseline. Expectation: rate is the same or lower (lower because the heap layout and code patterns might shift slightly, but no causal change). If rate goes UP, stop and investigate — you may have introduced a subtle instruction-encoding or GOT-slot bug that corrupts memory.

5. **Plugin smoke** — the same change must not break plugin loading. Boot a workload that exercises plugin loading (rachel/fontsvc, fs, linux). All `[plugin loaded]`-style telemetry should still appear and plugins should resolve their host imports. The plugin path's GOTPCREL handling is unchanged because `*flagDlopenHostPackages != ""` for plugins, so the gate selects GLOB_DAT just like before.

### Step 5 — Delete the shepherd overlay generator

Once Step 4 is stable:

1. **`cmd/gen-ast-stubs/main.go`** — drop everything under `runShepherdMode` and the `-mode=shepherd` branch. Keep `runThinMode` (still used for `.maz` plugin builds via the `THIN_OVERLAY_*` Taskfile targets). The `keepAliveEntry` / `processPackageForShepherd` / `appendMazKeepAliveSymbolsFunc` were already deleted in Gap 1; this step removes the `-mode=shepherd` plumbing and the `-mode` flag's `shepherd` case. Update the help text and `--mode` doc lines accordingly.

2. **`Taskfile.yml`** — delete:
   - `shepherd-overlay` task (lines 453-464)
   - `shepherd-overlay-amd64` task (lines 466-477)
   - `merged-shepherd-overlay` task (lines 479-491)
   - `merged-shepherd-overlay-amd64` task (lines 493-505)
   - The corresponding `SHEPHERD_OVERLAY_*` and `MERGED_SHEPHERD_OVERLAY_*` variable declarations (lines 210-217).

3. **`mazarin/mazhost/`** — if the package now has no remaining files using the `mazhost` tag, the package may still exist for `LoadMazBootstrap` etc.; leave the rest alone. Verify the directory still has a meaningful purpose:
   ```bash
   ls mazarin/mazhost/
   grep -l "package mazhost" mazarin/mazhost/*.go
   ```

4. **Rebuild from scratch**:
   ```bash
   $GO tool task clean
   $GO tool task
   $GO tool task shepherd:x86_64
   ```

5. **Final smoke** — full ARM64 HVF + x86_64 boot, look for `[mail] cache ready`, plugin-load telemetry, no new errors.

6. **Commit** in two or three focused commits:
   - `mazlink: route -dynlink GOTPCREL through Adddynrel for host builds (Gap 2 linker)` (steps 2-3).
   - `shepherd: build with -gcflags=-dynlink, drop overlay (Gap 2 build)` (step 4).
   - `cmd/gen-ast-stubs: drop -mode=shepherd; delete shepherd-overlay tasks` (step 5).

### Step 6 — Update tracking files and unblock bug-B forensics

1. **`task_plan.md`** — move Gap 2 from TOP OF STACK to ARCHIVED with a summary; resurrect the bug-B section at TOP OF STACK now that shepherd-side runtime patching is unblocked.

2. **`progress.md`** — log the fix, the verification result, the commit set.

3. **`memory/MEMORY.md` + `memory/shepherd_overlay_dynlink_experiment.md`** — flip the latter to "RESOLVED" with the actual mechanism that made it work (the host-vs-plugin GOT slot policy distinction). This is the kind of subtle two-mode behaviour that's easy to forget; record it explicitly.

4. **`next_session_prompt.md`** — refresh to reflect that bug-B forensics can now be added to a `runtime-patches/runtime/traceback.go` overlay that the shepherd build picks up. (Whether that's via the existing `runtime-patches/` mechanism or a new `shepherd-runtime-patches/` directory is a separate decision; flag it explicitly so the next session decides.)

---

## Pitfalls / non-negotiables

- **Plugin path must not regress.** The cleanest verification: build a plugin (`maz/rachel`, `maz/fs`, etc.) before any change, capture `objdump -R` of the resulting `.so` (or equivalent for kmazarin's plugin format). Build again after the change. The dynamic relocations should be byte-identical because the gate `*flagDlopenHostPackages != ""` activates only the new branch for host builds.
- **GOT slot static fill correctness.** When `AddGotSym(..., 0)` is used in host mode, verify that `addr(targ)` is actually written into the slot. Inspect with:
  ```bash
  bin/target-readelf -r build/shepherd.elf | head -40       # should show no R_*_GLOB_DAT for host-internal targets
  bin/target-objdump -s -j .got build/shepherd.elf | head -30  # GOT contents — should be non-zero static addresses
  ```
- **Don't disable async preemption or the GC.** `GODEBUG=gccheckmark=1` stays on. (`asyncpreemptoff` and `GOGC=off` are forbidden — see `CLAUDE.md`.)
- **Use the safe serial reader** for all `/tmp/diplomat-*.log` access.
- **`$GO tool task` for builds** — never `go build` / `go vet` / `go test` directly.
- **`git push origin <branch>` explicitly** — no implicit push.
- **Never run two non-trivial builds in parallel via `go build`** — Taskfile coordinates dependencies; respect it.
- **flagDlopenHostExports / flagDlopenHostPackages are pointer-typed** (`*string`). Always dereference with `*` in checks. Do not capture them in long-lived structs — they're set by `flag.Parse()` and read each access.

## Done when

- Shepherd builds with `-gcflags=all=-dynlink -asmflags=all=-dynlink`, no `-overlay=`, no `-tags mazhost` (or with the tag for unrelated reasons only).
- ARM64 HVF and x86_64 both boot through `[mail] cache ready` cleanly.
- Plugin-load smoke passes: rachel, fs, linux all load and resolve host imports.
- 5×60s ARM64 HVF sweep shows bug-B rate within noise of pre-Gap-2 baseline.
- `cmd/gen-ast-stubs/-mode=shepherd` and the `shepherd-overlay*` Taskfile machinery are deleted.
- `objdump -R build/shepherd.elf` shows zero `R_AARCH64_GLOB_DAT` / `R_X86_64_GLOB_DAT` entries (host has no dynamic linker; static fill replaced them).
- Tracking files updated; bug-B chase resumes as TOP OF STACK in `task_plan.md`.
