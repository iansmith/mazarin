# Mazzy Architecture - Essential Guide

---
## STOP - READ THIS FIRST (Claude Code Safety)

**DO NOT READ serial log files (`/tmp/diplomat-serial.log`, `/tmp/diplomat-arm64-serial.log`, etc.) DIRECTLY!**

These files frequently contain:
- Lines with MILLIONS of characters (infinite loop output)
- Terminal control sequences that freeze tools

**Using Read tool or cat/head/tail on these files WILL HANG YOUR SESSION.**

**ALWAYS use the safe reader:**
```bash
$GO tool safe-serial-read /tmp/diplomat-serial.log
```

This is enforced by hooks in `.claude/settings.local.json` but read this anyway.

---

## Quick Reference - Environment Variables

**ALWAYS set all three variables when building or running:**

```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
```

- **GOTOOLCHAIN=auto** - Required. Ensures the correct Go version is used.
- **GO** - Path to Go binary (>= 1.24 required, 1.25.5 recommended)
- **QEMU** - Path to qemu-system-aarch64 (>= 10.2 required)

**Usage (inline or after export):**
```bash
# Build diplomat + kmazarin for ARM64 (default)
$GO tool task

# Build and run ARM64 diplomat+kmazarin in QEMU (5s default timeout)
$GO tool task run

# Run x86_64 diplomat+kmazarin
$GO tool task run-x86_64

# Run RISC-V diplomat+kmazarin
$GO tool task run-riscv64

# Run with custom timeout
$GO tool task run TIMEOUT=30

# Or export once and use throughout session:
export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
$GO tool task clean
$GO tool task run TIMEOUT=10
```

## Overview

**Diplomat** (UEFI bootloader) + **Kmazarin** (Go kernel)
- Diplomat: Multi-arch UEFI application that loads kmazarin ELF, sets up page tables, and jumps to kernel
- Kmazarin: Unmodified Go binary - full OS kernel with Go runtime
- Supported architectures: ARM64, x86_64, RISC-V

## Prerequisites

**Go Version: >= 1.24 (REQUIRED)**

This project requires Go 1.24 or later. The build will abort with a helpful error if the version is too old.

**QEMU Version: >= 10.2 (REQUIRED)**

QEMU 10.2 or later is required. Earlier versions have issues with the ELF loading.

### Environment Setup

If GO or QEMU are not set, the build will attempt to find them via `which`. If found, a warning is printed. If not found, the build aborts.

**On this system (Homebrew installation):**
```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
```

**Important:** Always set GOTOOLCHAIN=auto. This ensures the correct Go toolchain is used.

## Build & Run

### Project Structure (Single Module)

This project uses a single Go module (`mazzy`). Build is managed by Taskfile (`Taskfile.yml`).

**Source locations:**
- `cmd/` - Build tools (used via `go tool <name>`)
- `diplomat/` - Diplomat UEFI bootloader (multi-arch: ARM64, x86_64, RISC-V)
- `kmazarin/` - Kmazarin kernel (Go kernel, multi-arch)
- `flock/` - Userspace programs (priest, helloworld, etc.)
- `mazarin/` - Shared userspace libraries
- `shared/` - Shared packages

**Task is installed as a Go tool. Run it with `$GO tool task`:**

```bash
# Build
$GO tool task                    # Build diplomat + kmazarin for ARM64 (default)
$GO tool task kmazarin-arm64     # Build kmazarin kernel (ARM64)
$GO tool task kmazarin-x86_64    # Build kmazarin kernel (x86_64)
$GO tool task kmazarin-riscv64   # Build kmazarin kernel (RISC-V)
$GO tool task clean              # Remove build artifacts
$GO tool task --list             # Show all available tasks

# Run (diplomat-based UEFI boot)
$GO tool task run                      # ARM64 diplomat+kmazarin (5s timeout)
$GO tool task run TIMEOUT=30           # ARM64 with 30s timeout
$GO tool task run-x86_64              # x86_64 diplomat+kmazarin (5s timeout)
$GO tool task run-riscv64             # RISC-V diplomat+kmazarin (5s timeout)
$GO tool task stop-arm64               # Stop ARM64 QEMU
$GO tool task stop-x86_64             # Stop x86_64 QEMU
$GO tool task stop-riscv64            # Stop RISC-V QEMU
```

All build and run operations go through `$GO tool task`. See `design/TASK.md` for comprehensive documentation.

### Complete Development Workflow

```bash
# 1. Set environment (once per session)
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

# 2. Build and run (ARM64 — default)
$GO tool task run              # Builds diplomat+kmazarin, runs QEMU for 5s
$GO tool task run TIMEOUT=30   # 30 second run

# 3. Build and run (x86_64)
$GO tool task run-x86_64 TIMEOUT=10

# 4. Build and run (RISC-V)
$GO tool task run-riscv64 TIMEOUT=10

# 5. View output / interact
$GO tool safe-serial-read /tmp/diplomat-arm64-serial.log   # ARM64 log
$GO tool safe-serial-read /tmp/diplomat-serial.log         # x86_64 log
$GO tool safe-serial-read /tmp/diplomat-riscv64-serial.log # RISC-V log
$GO tool task stop-arm64               # Stop ARM64 QEMU (port 4446)
$GO tool task stop-x86_64             # Stop x86_64 QEMU (port 4445)
$GO tool task stop-riscv64            # Stop RISC-V QEMU (port 4447)
```

Output is written to `/tmp/diplomat-serial.log` (x86_64) or `/tmp/diplomat-arm64-serial.log` (ARM64).

**CRITICAL: Serial Log Safety**
- The serial log can contain:
  - Terminal control sequences that freeze your terminal
  - Ridiculously long lines (millions of characters) that crash tools
- NEVER: `cat /tmp/diplomat-serial.log` or Read the raw file directly
- ALWAYS: Use the safe reader tools:
  ```bash
  # Safely view the log (handles long lines + control chars)
  $GO tool safe-serial-read /tmp/diplomat-serial.log
  ```
- The `$GO tool task run` command displays filtered output after the timeout

**CRITICAL: QEMU Output Buffering**
- NO: `| tee`, `| tail`, `> file`, `< /dev/null` piped to QEMU - causes buffering issues
- Use file-based serial output with TCP monitor (see below)

### QEMU Monitor Access

QEMU runs with TCP monitors on different ports per architecture:
- **ARM64**: port 4446
- **x86_64**: port 4445
- **RISC-V**: port 4447

```bash
# ARM64 diplomat+kmazarin
echo "info registers" | nc 127.0.0.1 4446

# x86_64 diplomat+kmazarin
echo "info registers" | nc 127.0.0.1 4445

# RISC-V diplomat+kmazarin
echo "info registers" | nc 127.0.0.1 4447
```

## Binary Utilities (Cross-compilation)

For examining ARM64 binaries on this macOS system, use the `target-*` prefixed utilities in the project's `bin/` directory:

```bash
# Disassemble kmazarin ELF
bin/target-objdump -d build/kmazarin.elf | less

# Disassemble around a specific address
bin/target-objdump -d build/kmazarin.elf | grep -A5 "41812cf"

# Show ELF sections
bin/target-readelf -S build/kmazarin.elf

# Show symbols
bin/target-nm build/kmazarin.elf | grep FunctionName

# Debugging with GDB (if needed)
bin/target-gdb build/kmazarin.elf
```

**Available tools:** `target-objdump`, `target-readelf`, `target-nm`, `target-objcopy`, `target-addr2line`, `target-gdb`, etc.

## Critical Development Rules

### Debug Output Safety

**NEVER write directly to UART in assembly** - always use safe macros:
- `UART_PUTC_SAFE` - single character
- `print_hex64` - hex values
- `uartPutsDirect` - strings
- `DEBUG_SAVE_REGS` / `DEBUG_RESTORE_REGS` - preserve X0-X15

**Caller must save parameter registers** before calling debug functions.

### ARM64 Exception Return (ELR_EL1)

| Exception | ELR_EL1 Contains |
|-----------|------------------|
| SVC | PC+4 (next instruction) |
| Data/Instruction Abort | Faulting instruction |

**SVC returns to next instruction** - do NOT add 4 to ELR on return.

### Go ELF `-T` Flag Behavior

Go's `-T 0x41800000` creates:
- First LOAD segment at `0x417F0000` (64KB before requested)
- .text at `0x41800000` (as requested)
- 64KB header region (zero-filled, no file data)
- File offset appears "negative": `0xffffffffffff1000 = -0xF000`

**Loader must**:
- Zero-fill 64KB header region
- Copy .text from file offset 0x1000

## Memory Layout

Memory layout is managed by diplomat UEFI bootloader and varies by architecture.
Diplomat allocates pages via UEFI, sets up page tables, and passes memory map info
to kmazarin via auxv entries (AT_FRAME_POOL_START, AT_UNIFIED_POOL_START, etc.).
See `shared/constants/auxv.go` for the full list of boot parameters.

## ARM64 Dual-Stack Architecture

**Two stacks, both at EL1 privilege**:

1. **SP_EL0 (g0 stack)** - Normal kernel code (EL1t mode)
2. **SP_EL1 (exception stack)** - Exception handlers (EL1h mode)

**Boot sequence**:
```asm
// IN EL1h mode - SP_EL1 is active
mov sp, x0          // Set SP_EL1 (current stack) - use mov, not msr!
msr SP_EL0, x0      // Set SP_EL0 (inactive) - safe to use msr
msr SPSel, xzr      // Switch to EL1t mode (use SP_EL0)
```

**CRITICAL**:
- Use `mov sp, x0` for **active** stack
- Use `msr SP_ELx, x0` for **inactive** stack
- Both stacks **MUST** be mapped before MMU enable

## Go ARM64 ABI - Assembly to Go Calls

Go has two ABIs:
- **ABI0**: Stack-based (stable, for cross-package assembly callers)
- **ABIInternal**: Register-based (R0-R15 for args)

### Working Pattern: Tail-Call Stub

When external assembly needs to call a Go function, use this three-part pattern:

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

### Why This Works

- External assembly stores args at RSP+8, RSP+16, etc. and calls `main·FunctionName`
- The stub does a tail-call (JMP) - no new stack frame added
- Go generates `.abi0` wrapper for `functionNameInternal` (called from same-package asm)
- The wrapper reads from sp+N+8 after its prologue, which equals the original RSP+8
- Returns are stored at the correct offsets for the caller

### Assembly Caller Pattern

```asm
SUB $<framesize>, RSP      // Allocate: 8 + (args*8) + returns, 16-aligned
MOVD R0, 8(RSP)            // arg[0] at RSP+8
MOVD R1, 16(RSP)           // arg[1] at RSP+16
...
CALL main·FunctionName(SB)
MOVD <retoffset>(RSP), R0  // Read returns
ADD $<framesize>, RSP
```

### Critical: Avoid Nested Wrappers

**NEVER** have a Go function call another Go function when both are called from assembly.
Go wraps both with `.abi0`, causing the second wrapper to read garbage (first put args
in registers, second expects stack). The tail-call stub pattern avoids this.

## Current Status

### x86_64 (diplomat + kmazarin) - FULLY WORKING
- Diplomat UEFI boots, loads kmazarin ELF, jumps to kernel
- Interrupts enabled (APIC timer, IDT with correct CS selector)
- Two clone threads (sysmon + templateThread) created successfully
- VirtIO GPU display fully working (1920x1080 framebuffer)
- VirtIO PCI block driver implemented (needs testing with disk)

### ARM64 (diplomat + kmazarin) - IN PROGRESS
- Diplomat UEFI builds and loads kmazarin
- Kmazarin kernel fully working (syscalls, threading, demand paging)
- VirtIO PCI block driver ready (shared code with x86_64)
- Diplomat ARM64 UEFI boot path needs completion

### RISC-V (diplomat + kmazarin) - FULLY WORKING
- Diplomat loaded via OpenSBI -kernel (UEFI firmware broken on RISC-V)
- Kmazarin kernel fully working (syscalls, threading, demand paging)
- VirtIO GPU, block, keyboard, mouse all working via PLIC interrupts
- Userspace programs (dapope, stdio) run successfully

## Git Practices

**Always** use explicit push: `git push origin <branch-name>`

**docs/ directory**: The `docs/` directory is gitignored and managed outside of git. Do not attempt to commit files in `docs/`. Use the `design/` directory for design documents that should be tracked in git.

## Philosophy

Diplomat = GRUB/UEFI loader. Kmazarin = the real kernel (full Go runtime, multi-arch).
