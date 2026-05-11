# Mazlink Gap 1 — Drop the keepalive force-reference mechanism

**Goal**: Remove `runtime.MazKeepAliveSymbols` and `mazarin/mazhost/keepalive.go`. The shepherd should still build and boot. The shepherd-overlay (`cmd/gen-ast-stubs -mode=shepherd`) loses one of its two jobs (keepalive generation); the other job (`//go:noinline` injection) stays for now and is addressed by Gap 2.

**Why this is unblocking**: until the keepalive infrastructure is gone, gen-ast-stubs writes a per-sub-package `MazKeepAliveSymbols()` function into the first source file of every sub-package — including `runtime/traceback.go` if it's the first file. Manual edits to those files (needed for bug-B shepherd-side forensics) get clobbered each rebuild. Shrinking gen-ast-stubs's output footprint is the precondition for Gap 2's full overlay deletion.

---

## Background you need before touching anything

### How the host-export pipeline works today

The shepherd is built as `BuildModeExe + LinkInternal + -dlopen-host-exports=mazlink-patches/policy/dlopen-host-packages.txt`. Inside mazlink (`mazlink-patches/cmd/link/internal/ld/`):

1. `loadlib` ends with the mazlink hook at `lib.go:705-738`. For host builds it calls `emitHostExportsDynsym(ctxt, policy)` (`mazdl.go:282`).
2. `emitHostExportsDynsym` walks every loaded symbol (`for i := 1; i < ldr.NSym(); i++`), keeps the ones whose owning package matches the policy, and for each survivor:
   - calls `ldr.SetAttrCgoExportDynamic(i, true)`
   - appends to `ctxt.dynexp`
3. After loadlib, stock `main.go` calls `inittasks` then `deadcode`.
4. Stock `deadcode.init()` at `deadcode.go:113-118` walks `ctxt.dynexp` and calls `d.mark(s, 0)` on each — i.e. dynexp entries are roots.
5. `d.flood()` propagates reachability through their relocations. Anything they reference transitively becomes reachable.
6. `addexport()` (run later, from `main.go:419`) drains `ctxt.dynexp` and panics if any entry is not reachable (`go.go:440-442`).

So in principle, every policy-matched defined symbol that is loaded into the linker becomes both (a) a deadcode root and (b) a `.dynsym` GLOBAL DEFINED entry.

### What `MazKeepAliveSymbols` does today

`cmd/gen-ast-stubs/main.go` `runShepherdMode` produces a 370-file overlay. Two jobs:

- **Job 1 — `//go:noinline`**: every Go-bodied function in `runtime` and sub-packages that passes `shouldStub()` (skip `init`, `main`, generics, asm-only) gets `//go:noinline`. Without this, the compiler can inline `runtime.foo` into a shepherd caller, after which `runtime.foo` may not be emitted as a callable symbol at all — and a plugin's UNDEF dynsym for `runtime.foo` cannot bind. **Keep this for Gap 1; Gap 2 replaces it via `-dynlink`.**

- **Job 2 — `MazKeepAliveSymbols`**: `appendMazKeepAliveSymbolsFunc` (`cmd/gen-ast-stubs/main.go:466`) appends to the first overlay file of each sub-package:
  ```go
  var MazKeptSymbols [N]interface{}
  //go:noinline
  func MazKeepAliveSymbols() {
      MazKeptSymbols[0] = funcA
      MazKeptSymbols[1] = recv.MethodB
      ...
  }
  ```
  `mazarin/mazhost/keepalive.go` references `runtime.MazKeepAliveSymbols` from a `//go:noinline init()`, which in turn references its own `runtime/internal/atomic.MazKeepAliveSymbols`, etc., flooding reachability through every stubbable function in the policy packages.

### Hypothesis

Job 2 is **redundant** with the dynexp + deadcode-roots mechanism. `emitHostExportsDynsym` already walks all loaded symbols, picks the policy-matched defined ones, and adds them to `ctxt.dynexp`. Stock deadcode marks dynexp entries as roots. So every policy-matched symbol that the loader knows about is already pinned, regardless of whether `MazKeepAliveSymbols` references it.

### Possible reasons the hypothesis could be wrong

If the verification step below shows breakage, one of these is the cause:

1. **Symbol-not-loaded edge case**: a runtime function exists in `runtime.a` but the loader's `NSym()` walk doesn't see it because the package archive wasn't fully indexed. (Unlikely — Go's linker reads all symbols from imported package archives — but possible if there are unindexed symbols, e.g. from unreferenced files.)
2. **Filter mismatch**: `emitHostExportsDynsym` skips symbols whose name starts with `type:.` or contains `.func`, and only keeps `IsText`/`IsData`/`IsRODATA`. The keepalive list, by contrast, references functions identified by gen-ast-stubs's source-level walk. If there's a function the keepalive includes that emitHostExportsDynsym filters out, that function would survive only via the keepalive path.
3. **DCE before loadlib end**: if some symbol gets dropped before `emitHostExportsDynsym` runs, dynexp won't see it. But the mazlink hook is at the very end of `loadlib`, after all object loading; this should not happen.

The fallback (explicit pre-deadcode pinning) covers all three cases.

---

## Step-by-step plan

### Step 0 — Set environment

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
```

Always invoke builds via `$GO tool task <target>` — never `go build` directly (per `CLAUDE.md` and `feedback_use_taskfile_build.md`).

### Step 1 — Verify the hypothesis (BEFORE making structural changes)

The cheapest way to test "is keepalive redundant?" is to make `runtime.MazKeepAliveSymbols` exist but be empty, and confirm shepherd still builds and boots.

1. Edit `cmd/gen-ast-stubs/main.go` `appendMazKeepAliveSymbolsFunc` (around line 466-503) to emit an empty function body — keep the declaration so `mazarin/mazhost/keepalive.go` still compiles, but do **not** emit any `MazKeptSymbols[i] = …` lines. Replace the for-loop body so the generated function is:
   ```go
   var MazKeptSymbols [0]interface{}
   //go:noinline
   func MazKeepAliveSymbols() {}
   ```
2. Force overlay regeneration:
   ```bash
   rm -f build/shepherd-overlay.json build/shepherd-overlay-amd64.json \
         build/merged-shepherd-overlay.json build/merged-shepherd-overlay-amd64.json
   $GO tool task shepherd:arm64
   $GO tool task shepherd:x86_64
   ```
3. Boot ARM64:
   ```bash
   $GO tool task run-arm64-hvf TIMEOUT=30 > /tmp/gap1-verify.out 2>&1
   $GO tool safe-serial-read /tmp/diplomat-arm64-serial.log | grep -E "\[mail\] cache ready|panic|missing deferreturn|mheap|fatal|unresolved" | head -50
   ```
   - **Expected if hypothesis is correct**: same boot trajectory as mainline. The system should reach `[mail] cache ready, initial rebalance first=-1 last=-1 vis=0` with no new panics. Bug-B may still fire at the existing 1-3/10 rate; that's pre-existing and not caused by this change.
   - **Expected if hypothesis is wrong**: undefined-symbol error at link time (e.g. `runtime.foo: undefined symbol`), or boot-time SIGSEGV from a plugin trying to bind to a missing host symbol, or "dynexp entry not reachable" panic.

4. Record the result. If verified, proceed to Step 2A. **If the hypothesis is falsified — stop. Do not proceed to Step 2B without discussing with the user first.** Capture the exact failure (link error, runtime panic, or sweep regression), the symbol(s) that disappeared, and a short note on what filter `emitHostExportsDynsym` applied that the keepalive caught. Surface this to the user and wait for direction before adding the explicit pinning pass — the user wants to see the falsification evidence and weigh in on the fix shape before code lands.

### Step 2A — Hypothesis verified: clean removal

Delete the keepalive infrastructure:

1. **`cmd/gen-ast-stubs/main.go`** — strip the keepalive emission entirely:
   - Remove the `keepAliveEntry` struct (line 152).
   - Remove `subPkgInfo`, `subPackages`, the `getCompiled` helper if only used for keepalive, and the loop that appends `entries` (lines 228-291).
   - Remove the keepalive-emission block (line 305-318).
   - Remove `appendMazKeepAliveSymbolsFunc` (line 466-503).
   - Update `processPackageForShepherd` so its only remaining job is writing `//go:noinline`-modified files into the overlay map. The function should now return `(noinlineCount int, fileCount int, err error)` with no keepalive-specific bookkeeping.
   - Verify the announcement line at `runShepherdMode` (around line 185) still makes sense; reword if the `keep-alive` count was part of it.

2. **`mazarin/mazhost/keepalive.go`** — delete the file outright.

3. **`mazarin/mazhost/doc.go`** — remove the paragraph about "symbols injected by the shepherd overlay (e.g., runtime.MazKeepAliveSymbols)". The build-tag note about `mazhost` may still apply if the package has other tag-gated files; check `find mazarin/mazhost -name '*.go' -exec grep -l 'mazhost' {} \;` and trim the doc to match what's actually still tag-gated.

4. **Build-graph cleanup** in `Taskfile.yml`:
   - The `shepherd-overlay` and `shepherd-overlay-amd64` targets (lines 453-477) still need to run because of Job 1 (noinline). Their `desc:` lines mention "//go:noinline + keep-alive"; trim the wording to match what's left.
   - No source/dependency changes needed — `cmd/gen-ast-stubs/**/*.go` is the only listed input and that's still right.

5. **Smoke test**:
   ```bash
   rm -f build/shepherd-overlay*.json build/merged-shepherd-overlay*.json
   $GO tool task                      # builds default ARM64 path (kmazarin + shepherd)
   $GO tool task shepherd:x86_64
   $GO tool task run-arm64-hvf TIMEOUT=60 > /tmp/gap1-postclean.out 2>&1
   $GO tool safe-serial-read /tmp/diplomat-arm64-serial.log | tail -30
   ```
   Confirm the shepherd boots through `[mail] cache ready` as it did before the change.

6. **Done check**: run a 5×60s ARM64 HVF sweep and compare bug-B hit rate to baseline. The keepalive removal should be neutral. If it changes the rate (either direction), stop and report — that means the change had an unexpected effect on link-output layout.

7. **Commit** with a small focused diff. Suggested message: `mazlink: drop runtime.MazKeepAliveSymbols force-reference (redundant with dynexp+deadcode roots)`.

### Step 2B — Hypothesis falsified: explicit pre-deadcode pinning (DO NOT EXECUTE WITHOUT USER APPROVAL)

**Gate**: this step requires explicit user approval before any code changes. Step 1 only authorises proof-of-falsification; the fix shape is the user's call. The text below is reference material for the eventual conversation, not a license to implement.

If Step 1 shows breakage, the dynexp mechanism isn't catching everything the keepalive currently catches. The likely shape of the fix is an explicit pinning pass:

1. **Add a new function in `mazlink-patches/cmd/link/internal/ld/mazdl.go`** (next to `emitHostExportsDynsym`):

   ```go
   // pinHostPolicySymbols walks every loaded symbol and, for each whose owning
   // package matches the host-exports policy, marks it reachable so that the
   // subsequent deadcode pass cannot drop it. This complements
   // emitHostExportsDynsym (which adds the same set to ctxt.dynexp): explicit
   // pinning catches symbols that emitHostExportsDynsym filters out (e.g.
   // closures, hashed-type descriptors) but which the runtime needs at load
   // time when a plugin binds against the host.
   //
   // Must run at the end of loadlib, before deadcode. Replaces the
   // runtime.MazKeepAliveSymbols force-reference mechanism that lived in the
   // shepherd-overlay.
   func pinHostPolicySymbols(ctxt *Link, p *HostPolicy) (pinned int) {
       ldr := ctxt.loader
       for i := loader.Sym(1); i < loader.Sym(ldr.NSym()); i++ {
           if !p.Matches(ldr.SymPkg(i)) {
               continue
           }
           st := ldr.SymType(i)
           if !(st.IsText() || st.IsData() || st.IsRODATA()) {
               continue
           }
           if !ldr.AttrReachable(i) {
               ldr.SetAttrReachable(i, true)
               pinned++
           }
       }
       return pinned
   }
   ```

   Note: `SetAttrReachable(s, true)` alone is not sufficient — deadcode's flood pass propagates reachability from a *worklist*, not from the bit. But we're calling this BEFORE deadcode, so deadcode's `init()` will see these symbols as already-reachable; its `mark()` no-ops on already-reachable symbols; instead we need them in the worklist. The cleanest way is to add them to `ctxt.dynexp` (which deadcode `init()` floods from). That's already what `emitHostExportsDynsym` does, with stricter filtering.

   So actually, the right shape for the new pass is: same iteration, but **with looser filters** than `emitHostExportsDynsym` (no `type:.` / `.func` / closure exclusion), appending to `ctxt.dynexp`. Or, separately, call `d.mark(i, 0)` — but `deadcodePass` is internal to the deadcode file.

   Concretely:

   ```go
   func pinHostPolicySymbols(ctxt *Link, p *HostPolicy) (pinned int) {
       ldr := ctxt.loader
       for i := loader.Sym(1); i < loader.Sym(ldr.NSym()); i++ {
           if !p.Matches(ldr.SymPkg(i)) {
               continue
           }
           st := ldr.SymType(i)
           if !(st.IsText() || st.IsData() || st.IsRODATA()) {
               continue
           }
           // Already-reachable symbols are no-ops; AlreadyAddedToDynexp covers
           // the emitHostExportsDynsym set; this catches the rest (closures,
           // hashed type descriptors, anything with a body the policy needs to
           // keep alive).
           if alreadyInDynexp(ctxt, i) {
               continue
           }
           ctxt.dynexp = append(ctxt.dynexp, i)
           ldr.SetAttrCgoExportDynamic(i, true)
           pinned++
       }
       return pinned
   }
   ```

   `alreadyInDynexp` is a small helper that builds a `map[loader.Sym]bool` from the current `ctxt.dynexp` and checks membership. (Avoid O(N²) — call it once at the start of `pinHostPolicySymbols` to materialise the set.)

2. **Wire it into `lib.go`**: in the `if *flagDlopenHostExports != ""` block at `lib.go:725-738`, after `emitHostExportsDynsym`, add:

   ```go
   p2 := pinHostPolicySymbols(ctxt, policy)
   if ctxt.Debugvlog > 0 {
       ctxt.Logf("mazdl: pinned %d additional host-policy symbols (closures/hashed types) before deadcode\n", p2)
   }
   ```

3. **Then perform the same cleanup as Step 2A** — delete `mazhost/keepalive.go`, strip keepalive emission from gen-ast-stubs, update doc.go.

4. **Smoke test** as in Step 2A.

5. **Commit** with a longer message documenting why the hypothesis was falsified, what the explicit pin catches, and how to detect regressions.

### Step 3 — Document outcome in tracking files

Update these files in `/Users/iansmith/mazzy/`:

- **`task_plan.md`** — move the Gap 1 section from "TOP OF STACK" to ARCHIVED with a one-paragraph summary of which path (2A or 2B) was taken and why.
- **`progress.md`** — log the work, the verification result, and the final diff scope.
- **`memory/MEMORY.md`** — update the "Active Work" pointer for `shepherd_overlay_dynlink_experiment.md` to note that Gap 1 has landed and what Gap 2 still owes.
- **`memory/shepherd_overlay_dynlink_experiment.md`** itself — append a "Gap 1 closed" section recording the resolution.

### Step 4 — Stop here

Do **not** start Gap 2 in the same session. Gap 2 is a much larger linker change and deserves its own session with its own verification baseline. The next-session prompt for bug-B forensics (`next_session_prompt.md`) is unblocked partway by Gap 1 alone — it's now possible to add `runtime-patches/runtime/traceback.go` -style overlays for the kernel runtime, but the shepherd's `traceback.go` is still gen-ast-stubs's territory. Document this state and stop.

---

## Pitfalls / non-negotiables

- **Never run `go build` or `go vet` directly** — `$GO tool task <target>` only.
- **`asyncpreemptoff=1` and `GOGC=off` must NOT appear** in any modification of `kmazarin/ksyscall/launch.go` or `diplomat/main/startup_env.go` (per `CLAUDE.md`). You should not be touching these files in Gap 1, but if you do, leave the GC config alone.
- **`GODEBUG=gccheckmark=1`** must remain set on both kernel and shepherds.
- **Don't touch `~/louis14`** — secondary worktree, ask the user before any edit there.
- **Use the safe serial reader** — never `cat` or `Read` `/tmp/diplomat-*.log` directly; always `$GO tool safe-serial-read`.
- **Always `git push origin <branch>` explicitly** — no implicit push.

## Done when

- The shepherd builds without `runtime.MazKeepAliveSymbols` referenced anywhere (`grep -r MazKeepAlive` returns nothing under `cmd/`, `mazarin/`, `runtime-patches/`, build artefacts).
- ARM64 HVF boots through `[mail] cache ready` at the same rate as the pre-change baseline.
- A 5×60s sweep shows no new failure modes introduced (bug-B rate within noise of baseline).
- Tracking files (task_plan, progress, memory) are updated.
- A focused commit lands on master with the cleanup.
