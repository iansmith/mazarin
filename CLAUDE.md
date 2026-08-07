# Mazzy

**Diplomat** (UEFI bootloader) + **Kmazarin** (Go kernel) + **Mazarin** (userspace).
Supported architectures: ARM64 (HVF, primary) and x86_64. RISC-V removed 2026-04-24.

The Go binary is unmodified — the project keeps a full Go runtime in the kernel.

# Mazzy-Specific Declarations

The `universal §N` references below point at `.claude/rules/universal.md` (moved
there in slopstop 4.0.0 from the old root-level `CLAUDE-universal.md`). Project
rules here take precedence over it.

## Pre-commit (overrides universal §1)

- **Do NOT commit by default.** After each meaningful chunk of edits, surface what changed and ASK whether to commit or continue. Self-driven commits bloat history, fragment diffs, and remove the user's review checkpoint. Leave the working tree dirty unless told otherwise.
- **Rare exceptions where committing inline IS correct:**
  - You're inside a structured workflow skill that owns commit semantics (the slopstop ticket workflow's PR step, or its plan step's Phase 0 RED-test commit that locks the spec before implementation).
  - The user just said "commit X".
  - You're rolling back a destructive change you just made.
  - Otherwise: stop and ask.
- **Ticket-anchored work commits through the slopstop ticket workflow**, which bundles simplify + commit + PR + review in one pass. Self-driven phase-by-phase commits ahead of it defeat that design and clutter the history with intermediate states the reviewer doesn't need to see.
- **Command names: check, don't assume.** The old `/ticket-*` commands no longer exist — they were renamed to `slopstop-*` and changed substantially in 4.0.0 (different steps, different arguments). The skills are project-local and version-frozen under `.claude/skills/slopstop-*`, which is why `.gitignore` un-ignores that path. **Read the live skill list rather than invoking from memory**, and ask if the step you want isn't there.
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

Both `task_plan.md` and the Linear ticket description MUST use the literal `## Definition of Done` h2 header (not `## DoD`, not `## Acceptance criteria`). The slopstop archive and merge steps key off the exact string; the archive's description-push silently rewrites a mismatched local header.

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
