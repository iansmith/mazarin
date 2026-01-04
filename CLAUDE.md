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
- ONLY works without pipes/redirects: `~/mazzy/bin/qemu-system-aarch64 -M virt,virtualization=off -cpu cortex-a72 -m 8G -kernel build/cardinal.elf -nodefaults -nographic -serial stdio -monitor none`
- ❌ NO: `| tee`, `| tail`, `> file`, `< /dev/null`
- ✅ Use `SEPARATE_OUTPUT=1 NOGRAPHIC=1 tools/run/run-cardinal` for logs at `/tmp/qemu-logs/`

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

## Current Status

### ✅ Working
- Cardinal boots, MMU, UART, basic syscalls
- ELF loader handles Go's negative offsets
- Kmazarin symbols load correctly (using `·` prefix pattern)
- Page allocation and mapping works
- Stack setup (argc/argv/envp/auxv)

### ❌ Blocked
- **MemmoveBytes hangs** when copying kmazarin code (~608KB)
- Even byte-by-byte loop hangs after breadcrumb 'Z'
- Issue: No exception handlers configured - any fault hangs system
- Addresses are valid and aligned, pages are writable
- Simplified 8-byte MOVD version also hangs

## Git Practices

**Always** use explicit push: `git push origin <branch-name>`

## Philosophy

Cardinal = GRUB + minimal Linux shim. Once kmazarin runs with full Go runtime, it's the real kernel.
