# Mazzy Architecture - Essential Guide

---
## STOP - READ THIS FIRST (Claude Code Safety)

**DO NOT READ `/tmp/cardinal-serial.log` DIRECTLY!**

This file frequently contains:
- Lines with MILLIONS of characters (infinite loop output)
- Terminal control sequences that freeze tools

**Using Read tool or cat/head/tail on this file WILL HANG YOUR SESSION.**

**ALWAYS use the safe reader:**
```bash
go tool safe-serial-read
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

- **GOTOOLCHAIN=auto** - Required. Ensures the correct Go version is used even if the required version changes.
- **GO** - Path to Go 1.25.5 binary (required for build tools and make)
- **QEMU** - Path to qemu-system-aarch64 10.2+ (required for run)

**Usage (inline or after export):**
```bash
# Build (all variables)
GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go go tool build

# Run
QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 go tool run 5

# Or export once and use throughout session:
export GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64
go tool build clean
go tool run 5
```

## Overview

**Cardinal** (bootloader/OS shim) + **Kmazarin** (Go kernel)
- Cardinal: Minimal OS providing syscalls/environment for Go runtime to start
- Kmazarin: Unmodified Go binary - full OS kernel with Go runtime

## Prerequisites

**Go Version: 1.25.5 (REQUIRED)**

This project requires Go 1.25.5 exactly. The build will abort with a helpful error if the wrong version is detected.

**QEMU Version: 10.2+ (REQUIRED)**

QEMU 10.2 or later is required. Earlier versions have issues with the ELF loading.

### Installing Go 1.25.5

If Go 1.25.5 is not in your PATH, you have three options:

1. **Install Go 1.25.5 directly** and add to PATH
2. **Use Homebrew** (recommended on macOS):
   ```bash
   brew install go@1.25.5
   ```
3. **Use GOTOOLCHAIN** (if you have Go 1.24.x or later):
   ```bash
   GOTOOLCHAIN=auto go version  # Automatically downloads Go 1.25.5 when required
   ```

**On this system (Homebrew installation):**
```bash
# Always set GOTOOLCHAIN=auto along with GO:
GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go go tool build
GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go make cardinal
```

**Important:** Always set both GOTOOLCHAIN=auto and GO when running build commands. GOTOOLCHAIN=auto ensures that if the required Go version changes in the future, the build will automatically use the correct version. The build tools check for Go 1.25.5 and will abort with a helpful error if the wrong version is detected.

### Installing QEMU 10.2+

If qemu-system-aarch64 is not in your PATH, you can:

1. Install it and add to PATH
2. Override per-command: `QEMU=/path/to/qemu-system-aarch64 go tool run`

**On this system (Homebrew installation):**
```bash
QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 go tool run
```

## Build & Run

### Project Structure (Single Module)

This project uses a single Go module (`mazzy`). All tools are registered in `go.mod` and can be run with `go tool <name>`:

**Source locations:**
- `cmd/` - Build tools (used via `go tool <name>`)
- `cardinal/` - Cardinal kernel (bootloader/OS shim)
- `kmazarin/` - Kmazarin kernel (Go kernel)
- `flock/` - Userspace programs (priest, helloworld, etc.)
- `mazarin/` - Shared userspace libraries
- `shared/` - Shared packages

**Available tools (via `go tool`):**
- `go tool build` - Build cardinal and kmazarin
- `go tool run` - Start QEMU with built kernel
- `go tool stop` - Stop running QEMU instances
- `go tool safe-serial-read` - Safely read serial log (handles infinite loops)

### Complete Development Workflow

**On this system (with Homebrew paths):**
```bash
# 1. Stop any running QEMU
go tool stop

# 2. Build (always set GOTOOLCHAIN=auto with GO)
GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go go tool build clean

# 3. Run
QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64 go tool run 5
```

**Or export once for the session:**
```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-aarch64

go tool stop
go tool build clean
go tool run 5
```

**Using make:**
```bash
GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go make cardinal
GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go make kmazarin
GOTOOLCHAIN=auto GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go make priest
```

Output is written to `/tmp/cardinal-serial.log`.

**CRITICAL: Serial Log Safety**
- The serial log can contain:
  - Terminal control sequences that freeze your terminal
  - Ridiculously long lines (millions of characters) that crash tools
- NEVER: `cat /tmp/cardinal-serial.log` or Read the raw file directly
- ALWAYS: Use the safe reader tools:
  ```bash
  # Safely view the log (handles long lines + control chars)
  go tool safe-serial-read

  # Or manually filter (only safe for reasonable line lengths):
  tail -f /tmp/cardinal-serial.log | tr -d '\000-\010\013-\037\177-\377'
  ```
- The `go tool run` script automatically applies safe filtering

**CRITICAL: QEMU Output Buffering**
- NO: `| tee`, `| tail`, `> file`, `< /dev/null` piped to QEMU - causes buffering issues
- Use file-based serial output with TCP monitor (see below)

### QEMU Monitor Access

The go tool run script starts QEMU with a TCP monitor on port 4444.

**Query QEMU monitor:**
```bash
# Using netcat
echo "info registers" | nc 127.0.0.1 4444
```

**Key monitor commands:**
- `info registers` - Show CPU registers
- `x/20i 0xADDRESS` - Disassemble at address (use literal address, not `$pc`)

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

## Memory Layout (1GB RAM @ 0x40000000)

```
0x40100000-0x401E2000   Cardinal .text (RO+X)
0x401E2000-0x40567000   Cardinal .rodata (RO)
0x40567000-0x405FE000   Cardinal .data (RW)
0x405FE000-0x406C8000   Cardinal .bss (RW)
0x41000000-0x41800000   Page Tables (8MB, RW)
0x41800000-~0x41A00000  Kmazarin ELF (RO+X)
0x5EFF0000-0x5F000000   g0 Stack / SP_EL0 (64KB, RW)
0x5F000000-0x5F020000   Exception Stack / SP_EL1 (128KB, RW)
```

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

### Working
- Cardinal boots, MMU, UART
- ELF loader handles Go's negative offsets
- Kmazarin loads and starts executing
- Exception handling (SVC syscalls, data abort page faults)
- Syscall dispatch (clone, mmap, futex, etc.)
- Demand paging for kmazarin memory
- Thread creation and context switches
- Page allocation and mapping
- Stack setup (argc/argv/envp/auxv)

### In Progress
- Full Go runtime initialization in kmazarin

## Git Practices

**Always** use explicit push: `git push origin <branch-name>`

**docs/ directory**: The `docs/` directory is gitignored and managed outside of git. Do not attempt to commit files in `docs/`. Use the `design/` directory for design documents that should be tracked in git.

## Philosophy

Cardinal = GRUB + minimal Linux shim. Once kmazarin runs with full Go runtime, it's the real kernel.
