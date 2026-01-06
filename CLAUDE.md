# Mazzy Architecture - Essential Guide

## Overview

**Cardinal** (bootloader/OS shim) + **Kmazarin** (Go kernel)
- Cardinal: Minimal OS providing syscalls/environment for Go runtime to start
- Kmazarin: Unmodified Go binary - full OS kernel with Go runtime

## Build & Run

```bash
make cardinal  # Build
```

**CRITICAL: QEMU Output Buffering**
- ❌ NO: `| tee`, `| tail`, `> file`, `< /dev/null` - causes buffering issues
- ✅ Use file-based serial output with TCP monitor (see below)

### Reliable QEMU Debugging Setup

**IMPORTANT**: The `bochs-display` device is REQUIRED for serial output to work, even in nographic mode!

**Terminal 1 - Run QEMU:**
```bash
rm -f /tmp/cardinal-serial.log
~/mazzy/bin/qemu-system-aarch64 \
    -M virt,virtualization=off \
    -cpu cortex-a72 \
    -m 8G \
    -kernel build/cardinal.elf \
    -nodefaults \
    -device bochs-display \
    -object rng-random,id=rng0,filename=/dev/urandom \
    -device virtio-rng-pci,rng=rng0,disable-legacy=on \
    -display none \
    -serial file:/tmp/cardinal-serial.log \
    -monitor tcp:127.0.0.1:4444,server,nowait \
    -semihosting \
    -no-reboot &
```

**Terminal 2 - Watch serial output:**
```bash
tail -f /tmp/cardinal-serial.log
```

**Terminal 3 - Query QEMU monitor (Python):**
```python
import socket, time
sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
sock.connect(('127.0.0.1', 4444))
sock.settimeout(2)
time.sleep(0.2); sock.recv(4096)  # Drain banner
sock.send(b'info registers\n')
time.sleep(0.5)
print(sock.recv(8192).decode())
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
