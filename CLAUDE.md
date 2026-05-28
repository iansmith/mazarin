# Mazzy

**Diplomat** (UEFI bootloader) + **Kmazarin** (Go kernel) + **Mazarin** (userspace).
Supported architectures: ARM64 (HVF, primary) and x86_64. RISC-V removed 2026-04-24.

The Go binary is unmodified — the project keeps a full Go runtime in the kernel.

---

# Universal Project Rules

These rules apply across all of Ian's projects unless this CLAUDE.md explicitly overrides them.

## 1. Pre-commit

- **ALWAYS run `/simplify` on uncommitted changes before every commit.** No exceptions on size — a one-line change can introduce a duplicate constant, touch the wrong file, or violate a project rule, all of which `/simplify` catches cheaply. Apply real findings inline before committing.
- Run the project's build + targeted tests (the package or area you touched) before commit. Run the full suite only when touching shared/cross-cutting code.
- Commit, then push — only after the above are clean. **If the project has multiple remotes, push to all of them.**

## 2. Tests

- **Tests-first for new behavior AND for fixes.** For new behavior, write the test describing the desired contract; confirm it's red **for the right reason** before implementing. For bug fixes, write a test that reproduces the bug — it must be red before the fix and green after. Trivial tweaks, copy changes, and pure refactors are exempt.
- **A failing test is signal, not chore.** Investigate the root cause before changing anything. Never delete a test, narrow an assertion, call `Skip()`, or cite an unverified "flake" to silence it. "Known flake" is a label, not an explanation.

(Test scope before commit is covered by §1. Project-specific guidance on test runtime and scoping lives in each project's CLAUDE.md.)

## 3. Git

- **NEVER squash-merge or rebase-merge.** Use `gh pr merge --merge` (real merge commit). Squash and rebase lose fixup context and break `git bisect`.
- Always include the explicit branch name in `git push origin <branch>`.
- Never `git push --force`, `git reset --hard`, `git commit --no-verify`, `git push --no-verify`, or `gh pr merge --admin` unless the user explicitly asks. When a hook or check fails, fix the underlying issue, don't bypass.
- Create new commits rather than amending. The single exception: amending one fresh commit on a solo branch before anyone has pulled it.

## 4. Refactoring scope

- **Dedupe is in scope.** If you find 2+ near-identical code paths while working on a change, extract the helper and migrate the duplicates in the same PR.
- **Structural changes are out of scope without discussion.** Renaming exported symbols, altering public signatures, moving files, or reshaping module boundaries must be raised separately.
- When extending an existing system, study its types and patterns first. Mirror existing vocabulary; don't invent parallel terms for the same concept.
- Foundational correctness over quick wins. "Nearly passing" is failing. When working through a category of failures, **don't declare done by cherry-picking the easy cases** — solve the problem completely.

## 5. Source of truth

- **One definition per value.** No duplicate constants, aliases, or parallel names. If something needs renaming, update every reference — never add an alias.
- Never edit generated files by hand. Edit the source and regenerate.

## 6. Agents and worktrees

### Coordinator rules — how to behave when running agents

- Commit and push before launching worktree agents — worktrees start from HEAD, not the working directory.
- **Aim for fine-grained milestones** — frequent enough that progress is visible (rough target: a check-in every few minutes of work), but not so frequent that the output becomes noise. Every 10 seconds is too often; every 20 minutes is too long.
- **Aim for parallelism that won't cause merge-back conflicts on the base branch.** If the work can't be cleanly parallelized, consider whether sequential agent offload is actually worth the overhead — small tasks belong on your own plate; genuinely large offloads (long builds, multi-file refactors you'd otherwise wait on) can still be a win even when sequential.
- **Never use `open` to display files unless the user explicitly asks.** Disruptive even from the main session.

### Agent instructions — what to include in every agent prompt

- **Run on a separate branch in a separate directory.** Before working, prepare the directory if the project requires it — e.g., symlink large, rarely-changing directories that aren't under git control from the worktree to their original location, so the agent has its dependencies without duplicating them.
- **Commit only to your worktree's branch.** Never touch `main`/`master` or other shared branches from a worktree.
- **Commit and report at every milestone, not just at the end.**
- **Never use `open` to display files** (disrupts the user's screen).
- **Restate the relevant project rules verbatim in the prompt.** Agents start with no prior context and won't follow rules they don't see.

## 7. Environment

- Never modify PATH manually. If the project has special path or environment requirements, ask the user the first time, then save them to memory for that project so subsequent sessions pick them up automatically.

## 8. Documentation directory layout (universal)

- `docs/` is **gitignored** — used for personal notes, scratch work, drafts. Not committed.
- `design/` is **tracked**, but you do **not** add files to it without explicit user confirmation. Design docs are deliberate artifacts.
- Files specific to a particular ticket (continuation prompts, mid-flight notes, ticket-local plans) go into the **ticket's local storage directory** (`~/.claude/ticket-active/<TICKET>/`), not into `docs/` or `design/`.

## 9. CodeRabbit (universal)

- Every project should have at least one remote that can be used with CodeRabbit. `/simplify`'s pre-commit role is to preempt CodeRabbit findings, not to substitute for the actual review.
- When the project has multiple remotes, **prefer the GitHub remote** for CodeRabbit. **CodeRabbit does not work on Bitbucket**; if Bitbucket is the only remote, factor that into the review plan separately.

## 10. Adding a new rule — where it lives

- **Project-specific operational tip or bug record** → `feedback_*.md` in this project's memory dir; index it in `MEMORY.md`. Default home for new learnings.
- **Project-specific rule every session must follow** → the project-specific section of this `CLAUDE.md`. Delete the memory file if it would duplicate.
- **Universal rule applying to every project of Ian's** → propose adding to the universal §1-§10 block of all four projects' `CLAUDE.md` files identically. Don't drift one project's universal block.

Promotion is one-way: memory → project-specific → universal. Rules go up when they prove durable.

---

# Mazzy-Specific Declarations

## Pre-commit (overrides universal §1)

- **Do NOT commit by default.** After each meaningful chunk of edits, surface what changed and ASK whether to commit or continue. Self-driven commits bloat history, fragment diffs, and remove the user's review checkpoint. Leave the working tree dirty unless told otherwise.
- **Rare exceptions where committing inline IS correct:**
  - You're inside a structured workflow skill that owns commit semantics — `/ticket-pr` (full simplify + commit + PR + CodeRabbit), `/ticket-plan` Phase 0 RED-test commit (locks the spec before implementation).
  - The user just said "commit X".
  - You're rolling back a destructive change you just made.
  - Otherwise: stop and ask.
- **Ticket-anchored work commits via `/ticket-pr`** — it bundles simplify + commit + PR + CodeRabbit polling in one pass. Self-driven Phase-by-Phase commits ahead of `/ticket-pr` defeat its design and clutter the history with intermediate states the reviewer doesn't need to see.
- When committing IS the right call, universal §1's sub-rules still apply: run `simplify` (or `Agent(code-simplifier)`) on staged-or-working changes first, then build + targeted tests, then commit + push. Never use `pre-commit-review` as a stand-in for simplify.

## Environment (set every session)

```bash
export GOTOOLCHAIN=auto                                          # overrides any stale go1.24.x pin
export GO=/opt/homebrew/bin/go                                   # stock Homebrew Go 1.26.2 — NO patched GOROOT
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
export QEMU_X86_64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
```

- The user's shell PATH omits `/opt/homebrew/bin`. Use full `/opt/homebrew/...` paths for Homebrew tools (`gdb`, `lldb`, etc.); bare names fail with "command not found".
- **No patched GOROOT.** All runtime customization lives in `runtime-patches/` and is applied via overlay manifests. Never copy GOROOT to `bin/old.go.*` and patch in place.

## Build & Run

All builds via `$GO tool task <target>`. **NEVER bare `go build`, `go vet`, or `go test`** — even for "does this compile" checks. Bare commands skip the userspace overlay, CGO_ENABLED, GOARCH, build tags, and `gen-overlay` step the Taskfile sets, producing spurious errors and stray binaries in the repo root.

```bash
$GO tool task                          # build ARM64 (default)
$GO tool task run-arm64-hvf            # run under HVF
$GO tool task run-x86_64               # x86_64 QEMU
$GO tool task stop-arm64               # stop ARM64 (monitor port 4446)
$GO tool task stop-x86_64              # stop x86_64 (monitor port 4445)
$GO tool task --list                   # discover targets
```

- **Always `run-arm64-hvf`, never `run-arm64`.** TCG is ~100× slower; iterative work is impossible on it.
- **When no timeout given, use `TIMEOUT=0`** and run in background — the user kills QEMU themselves.
- **No POSIX shell (sed/awk/etc.) in the build pipeline.** Non-Unix targets have no shell. Implement transforms in Go tool code (e.g., `gen-ast-stubs`).
- **Stale binary?** Don't `touch`/`rm`/`clean` to force-rebuild. Trace the Taskfile sources/generates/deps chain backward to the broken link and fix it — otherwise the next session hits the same trap.

QEMU monitor: `echo "info registers" | nc 127.0.0.1 4446` (4445 for x86_64).

## Testing (clarification on universal §2)

This is an OS project. Conventional unit tests are often impossible or very difficult to write for kernel and syscall behavior. **Test programs** — most notably `maz/xfertest/` — are the encouraged substitute: they boot under QEMU and exercise behavior end-to-end. The red-first rule still applies: add a failing xfertest stage before the fix lands, watch it fail for the right reason, then implement.

## Tools that hang on raw output

`/tmp/diplomat-arm64-serial.log` and `/tmp/diplomat-serial.log` can contain millions of characters per line and terminal control codes. **NEVER** `Read`, `cat`, `head`, or `tail` them. **ALWAYS**: `$GO tool safe-serial-read <path>`.

## Generated files / runtime overlays

- `runtime-patches/*` — Go-runtime overlay files. **Rebase surgically** with `Read` + `Edit`, anchoring on stable surrounding lines. Never `cp <stock>/<file> runtime-patches/<file>` and re-insert the delta — that hides our mazzy-specific change inside a wholesale upstream churn diff and makes review impossible.

## Project structure

Single module `mazzy`. Sources: `cmd/` (build tools), `diplomat/` (UEFI bootloader, multi-arch), `kmazarin/` (Go kernel, multi-arch), `maz/` (userspace programs), `mazarin/` (shared userspace libraries), `shared/` (cross-cutting packages).

## Sibling project: louis14

- Treat `~/louis14` as **read-only by default**. Read freely; never `Edit`/`Write` without naming the file and intent in chat and getting explicit per-file go-ahead first. Concurrent development happens there; silent edits cause merge conflicts.
- After editing louis14 via `go.mod` replace, `rm build/<shepherd>.elf` to force shepherd rebuild — the Taskfile doesn't detect replace-source changes.

## Ticket workflow (MAZ on Linear)

Both `task_plan.md` and the Linear ticket description MUST use the literal `## Definition of Done` h2 header (not `## DoD`, not `## Acceptance criteria`). `/ticket-archive` and `/ticket-merge` key off the exact string; the archive's description-push silently rewrites a mismatched local header.

## Cross-arch binary utilities

`bin/target-{objdump,readelf,nm,objcopy,addr2line,gdb}` — for inspecting ARM64 ELFs from the macOS host. Example: `bin/target-objdump -d build/kmazarin.elf | less`.

---

# Kernel & Runtime Rules

## Runtime environment — MANDATORY (kernel AND shepherds)

These are hard, non-negotiable. Fix the root cause; don't disable scheduling or GC as a workaround.

- **`asyncpreemptoff`** must NOT be set anywhere — not GODEBUG, not anywhere. Async preemption is required for correct Go scheduling.
- **`GOGC`** must NOT be `"off"`. Use a low value like `"5"` if you need to reduce frequency, but never disable it.
- **`GODEBUG=gccheckmark=1`** must always be set (kernel and shepherds). Enables GC checkmark verification — validates that concurrent mark found all reachable objects.

```go
// kernel: diplomat/main/startup_env.go
s := "GODEBUG=gccheckmark=1"          // NO asyncpreemptoff!

// shepherds: kmazarin/ksyscall/launch.go
penv.SetEnv("GODEBUG", "gccheckmark=1")
penv.SetEnv("GOGC", "5")
```

## Architectural invariants

- **Stock Linux ABI + stock Go runtime invariants are non-negotiable.** Don't swap `Syscall` ↔ `RawSyscall` to paper over a bug — that's architectural. Discuss before any change.
- **No `GOMAXPROCS=1` assumptions.** Design for multicore: false-sharing, lock-free correctness, etc.
- **P-starvation is a false diagnosis.** Go's P recovery works; find the real cause.
- **Don't work around IPC deadlocks.** If `fmt.Printf` deadlocks on an IPC cycle, do NOT swap to `sys.UartWriteString` or other bypass. Map the cycle (who calls whom, which lock/ring is held), fix it structurally, discuss before implementing.
- **`klog` not `serial` for kernel debug output.** Use `klog.Logf`/`klog.Errf`. `serial.*` and `Criticalf` only for panic paths.

## Debug output safety (ARM64 assembly)

**NEVER write directly to UART in assembly.** Always use the safe macros:

- `UART_PUTC_SAFE` — single character
- `print_hex64` — hex values
- `uartPutsDirect` — strings
- `DEBUG_SAVE_REGS` / `DEBUG_RESTORE_REGS` — preserve X0–X15

Caller must save parameter registers before calling debug functions.

## nosplit discipline

`//go:nosplit` is about preventing morestack from firing inside IRQ-off regions, not just the 792 B budget. Heap-allocating scratch buffers does NOT make a splittable function safe to call from an IRQ-off context — morestack itself acquires runtime locks and can trigger GC work. Asm-implies-nosplit is a transitivity rule.

## ARM64 exception return (ELR_EL1)

| Exception | ELR_EL1 contains |
|---|---|
| SVC | PC+4 (next instruction) |
| Data / Instruction Abort | Faulting instruction |

**SVC returns to next instruction — do NOT add 4 to ELR on return.**

## Go ELF `-T` flag behavior

Go's `-T 0x41800000` creates:
- First LOAD segment at `0x417F0000` (64 KB before requested)
- `.text` at `0x41800000` (as requested)
- 64 KB header region (zero-filled, no file data)
- File offset appears "negative": `0xffffffffffff1000 = -0xF000`

**Loader must** zero-fill the 64 KB header region and copy `.text` from file offset `0x1000`.

---

# Userspace (Mazarin) Conventions

- **"interactor" not "widget".** Never use "widget" in code or discussion.
- **No `PreferredWidth`/`PreferredHeight`.** Sizes come from the constraint system.

---

# Architecture Reference

## Memory layout

Diplomat allocates pages via UEFI, sets up page tables, and passes the memory map to kmazarin via auxv entries (`AT_FRAME_POOL_START`, `AT_UNIFIED_POOL_START`, etc.). See `shared/constants/auxv.go` for the full list.

## ARM64 dual-stack architecture

Two stacks, both at EL1 privilege:

1. **SP_EL0 (g0 stack)** — normal kernel code (EL1t mode)
2. **SP_EL1 (exception stack)** — exception handlers (EL1h mode)

**Boot sequence:**
```asm
// IN EL1h mode - SP_EL1 is active
mov sp, x0          // Set SP_EL1 (current stack) - use mov, not msr!
msr SP_EL0, x0      // Set SP_EL0 (inactive) - safe to use msr
msr SPSel, xzr      // Switch to EL1t mode (use SP_EL0)
```

- Use `mov sp, x0` for the **active** stack.
- Use `msr SP_ELx, x0` for the **inactive** stack.
- Both stacks **must be mapped before MMU enable.**

## Go ARM64 ABI — assembly to Go calls

Go has two ABIs:
- **ABI0** — stack-based (stable, for cross-package asm callers)
- **ABIInternal** — register-based (R0–R15 for args)

### Tail-call stub pattern (the only working shape)

When external assembly needs to call a Go function:

**1. Forward declaration** in `asm_decl.go`:
```go
func FunctionName(arg0, arg1 uint64, ...) (ret0 int64, ret1 bool)
```

**2. Tail-call stub** in `abi_stubs_arm64.s`:
```asm
TEXT ·FunctionName(SB), NOSPLIT, $0-<framesize>
    JMP ·functionNameInternal(SB)
```

**3. Go implementation** (lowercase, unexported):
```go
func functionNameInternal(arg0, arg1 uint64, ...) (ret0 int64, ret1 bool) {
    // actual implementation
}
```

**Why this works:** the stub JMPs without adding a new frame; Go generates an `.abi0` wrapper for the lowercase impl, which reads args from the original RSP offsets.

### Assembly caller pattern

```asm
SUB $<framesize>, RSP      // Allocate: 8 + (args*8) + returns, 16-aligned
MOVD R0, 8(RSP)            // arg[0] at RSP+8
MOVD R1, 16(RSP)           // arg[1] at RSP+16
...
CALL main·FunctionName(SB)
MOVD <retoffset>(RSP), R0  // Read returns
ADD $<framesize>, RSP
```

### Critical: avoid nested wrappers

**NEVER** have a Go function call another Go function when both are called from assembly. Go wraps both with `.abi0`, the second wrapper reads garbage (first puts args in registers, second expects stack). The tail-call stub pattern avoids this.

---

# Current Status

## x86_64 (diplomat + kmazarin) — FULLY WORKING

- Diplomat UEFI boots, loads kmazarin ELF, jumps to kernel.
- Interrupts enabled (APIC timer, IDT with correct CS selector).
- Two clone threads (sysmon + templateThread) created successfully.
- VirtIO GPU display fully working (1920×1080 framebuffer).
- VirtIO PCI block driver implemented (needs testing with disk).

## ARM64 (diplomat + kmazarin) — IN PROGRESS

- Diplomat UEFI builds and loads kmazarin.
- Kmazarin kernel fully working (syscalls, threading, demand paging).
- VirtIO PCI block driver ready (shared code with x86_64).
- Diplomat ARM64 UEFI boot path needs completion.

---

# Philosophy

Diplomat = GRUB/UEFI loader. Kmazarin = the real kernel (full Go runtime, multi-arch).
