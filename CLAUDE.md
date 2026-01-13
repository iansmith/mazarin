# Mazzy Architecture - Essential Guide

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

If Go 1.25.5 is not in your PATH, you can:

1. Install it and add to PATH
2. Override per-command: `GO=/path/to/go1.25.5 scripts/build`
3. Override per-command: `make GO=/path/to/go1.25.5 cardinal`

### Installing QEMU 10.2+

If qemu-system-aarch64 is not in your PATH, you can:

1. Install it and add to PATH
2. Override per-command: `QEMU=/path/to/qemu-system-aarch64 scripts/run`

## Build & Run

**First time setup:** Build the helper scripts (compiled Go binaries):
```bash
make GO=/path/to/go1.25.5 host-tools
```

**Then use the scripts:**
```bash
scripts/build          # Build cardinal and kmazarin
scripts/build clean    # Clean build
scripts/run            # Start QEMU (waits 3s, shows last 500 chars)
scripts/run 10         # Start QEMU with 10s wait
scripts/stop           # Stop any running QEMU instances

# Or using make directly
make GO=/path/to/go1.25.5 cardinal
```

### Complete Development Workflow

```bash
# 1. Stop any running QEMU
scripts/stop

# 2. Build
scripts/build clean    # or just: scripts/build

# 3. Run
scripts/run 5          # Wait 5 seconds before showing output
```

Output is written to `/tmp/cardinal-serial.log`.

**CRITICAL: Serial Log Safety**
- The serial log contains terminal control sequences that can freeze your terminal
- NEVER: `cat /tmp/cardinal-serial.log` or Read the raw file directly
- ALWAYS: Filter control characters first:
  ```bash
  tail -f /tmp/cardinal-serial.log | tr -d '\000-\010\013-\037\177-\377'
  ```
- The `scripts/run` script automatically applies safe filtering

**CRITICAL: QEMU Output Buffering**
- NO: `| tee`, `| tail`, `> file`, `< /dev/null` piped to QEMU - causes buffering issues
- Use file-based serial output with TCP monitor (see below)

### QEMU Monitor Access

The scripts/run script starts QEMU with a TCP monitor on port 4444.

**Query QEMU monitor:**
```bash
# Using netcat
echo "info registers" | nc 127.0.0.1 4444
```

**Key monitor commands:**
- `info registers` - Show CPU registers
- `x/20i 0xADDRESS` - Disassemble at address (use literal address, not `$pc`)

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

### ✅ Working
- Cardinal boots, MMU, UART
- ELF loader handles Go's negative offsets
- Kmazarin loads and starts executing
- Exception handling (SVC syscalls, data abort page faults)
- Syscall dispatch (clone, mmap, futex, etc.)
- Demand paging for kmazarin memory
- Thread creation and context switches
- Page allocation and mapping
- Stack setup (argc/argv/envp/auxv)

### 🔄 In Progress
- Full Go runtime initialization in kmazarin

## Git Practices

**Always** use explicit push: `git push origin <branch-name>`

## Philosophy

Cardinal = GRUB + minimal Linux shim. Once kmazarin runs with full Go runtime, it's the real kernel.
