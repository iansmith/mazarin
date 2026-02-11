# x86_64 GDT Implementation and Boot Investigation - Continuation Prompt

**Date**: 2026-02-11
**Status**: GDT implementation complete, kmazarin entry crash under investigation
**Branch**: feature/port-x86-64-riscv

## Overview

This document describes the current state of x86_64 ring 3 userspace support implementation and the boot crash investigation that needs to continue.

## What Was Implemented

### Standard x86_64 GDT Layout

Replaced UEFI GDT inheritance with a fresh standard GDT layout following x86_64 kernel conventions:

```
GDT Layout (diplomat creates, kmazarin uses):
  0x00: Null descriptor
  0x08: Ring 0 code (kernel CS)
  0x10: Ring 0 data (kernel DS/SS/ES)
  0x18: Ring 3 code (user CS)
  0x20: Ring 3 data (user DS/SS/ES)
  0x28: TSS descriptor (16 bytes)
```

**Selector values with RPL bits**:
- Kernel: CS=0x08, SS=0x10
- User: CS=0x1B (0x18|RPL=3), SS=0x23 (0x20|RPL=3)

### Implementation Details

**1. diplomat/main/uefi_calls_amd64.s:138-230** - `jumpToKmazarinWithStack`
- Creates fresh GDT in diplomat's data section
- Writes standard descriptors (Ring 0 code/data, Ring 3 code/data, TSS)
- Uses far return (RETFQ) to reload CS from UEFI's 0x38 to 0x08
- Reloads all segment registers (DS, ES, SS to 0x10, FS/GS to 0)
- Configures SYSCALL MSRs with CS=0x08

**Far return implementation** (RIP-relative addressing via CALL trick):
```asm
BYTE $0xE8; BYTE $0x00; BYTE $0x00; BYTE $0x00; BYTE $0x00  // CALL $+5
POPQ AX
ADDQ $9, AX  // Skip 9 bytes to cs_reloaded
PUSHQ $0x08  // New CS selector
PUSHQ AX     // Return address
BYTE $0x48; BYTE $0xCB  // RETFQ
cs_reloaded:
```

**2. diplomat/main/kernelvm_amd64.go:126,230** - IDT setup
- Changed from reading current CS to hardcoded 0x08
- Applied in `InstallFaultHandler` and `updateIDTWithKmazarinISRs`
- Critical: IDT populated before CS reload but installed after

**3. kmazarin/kmazarin/thread_context_amd64.go:15-37** - Updated selector constants
```go
const (
    kernelCS = 0x08 // Ring 0 code
    kernelSS = 0x10 // Ring 0 data
    userCS   = 0x1B // Ring 3 code (0x18 | RPL=3)
    userSS   = 0x23 // Ring 3 data (0x20 | RPL=3)
)
```

**4. kmazarin/kmazarin/syscall_amd64.go:25,99-123** - SYSCALL configuration
- Changed `syscallCS` from 0x0080 to 0x0008
- Updated TSS descriptor location from GDT 0xA0 to 0x28
- Updated GDT limit from 0xAF to 0x37
- Updated `loadTR` selector from 0xA0 to 0x28

**5. kmazarin/kmazarin/exceptions_amd64.s:50-51** - Syscall entry fake frame
```asm
PUSHQ $0x23  // SS (Ring 3 data: GDT 0x20 | RPL=3)
PUSHQ $0x1B  // CS (Ring 3 code: GDT 0x18 | RPL=3)
```

### Commits
- `2c8f5c5` - WIP: Implement standard x86_64 GDT layout with Ring 0/3 support
- `f8c7e94` - Add run-x86_64 task alias for easier x86_64 QEMU invocation

## Current Problem: Kmazarin Entry Crash

### Symptoms

**Serial log** (`/tmp/diplomat-serial.log`):
```
Diplomat UEFI Bootloader
DBG: before InitializeSpans
DBG: spans OK
Block device ready
Mounting FAT32...
FAT32 mounted OK
Kernel file found
ELF: entry=0x43873740 phdrs=0x6
ELF: virt=0x437FF000-0x43B55F00
ELF: allocating memory...
ELF: zeroing 0x4000000 @ 0x77E00000
ELF: loading segments
  seg[0x2] off=0x0 dest=0x77E00000 fsz=0xB9BF1 msz=0xB9BF1
  seg[0x2] done
  seg[0x3] off=0xBA000 dest=0x77EBA000 fsz=0xFD978 msz=0xFD978
  seg[0x3] done
  seg[0x4] off=0x1B8000 dest=0x77FB8000 fsz=0xB080 msz=0x19EF00
  seg[0x4] done
ELF: symtab at off=0x283CA8 0xC75 syms, strtab at off=0x2967A0
[blank line, then nothing]
```

**QEMU monitor** (port 4445):
- System triple-faults and reboots repeatedly
- Crash occurs immediately after symbol resolution completes
- No output from kmazarin (not even first instruction)

### Investigation Findings

**Diplomat boots successfully**:
- Standard GDT created and loaded correctly
- Far return reloaded CS to 0x08
- All segment registers reloaded (DS, ES, SS to 0x10, FS/GS to 0)
- IDT installed with CS=0x08 entries
- ELF loaded to memory correctly (3 segments)
- Symbol table processed successfully

**Crash location**:
- Occurs after "Jumping to kmazarin..." (diplomat's last message before jump)
- Before any kmazarin output
- This indicates the crash happens in kmazarin's entry point assembly

**Debug breadcrumbs in diplomat/main/elf_loader.go** (TEMPORARY):
- Lines 233-245: Added debugPortOut calls ('V', 'U', 'T', 'Y', 'X', 'W', 'Z', '1', '2')
- These confirmed symbol processing completes successfully
- **TODO**: Remove these temporary debug breadcrumbs once investigation complete

### Root Cause Hypothesis

The crash happens **immediately at kmazarin's entry point** (`_rt0_amd64_linux` in Go runtime). Possible causes:

1. **Stack alignment issue**: Diplomat may be passing misaligned RSP
2. **Register initialization**: Kmazarin entry may expect certain registers set
3. **Go runtime overlay incompatibility**: Runtime patches may assume UEFI selectors
4. **Segment register assumptions**: Entry code may make assumptions about segment state

## Next Steps for Investigation

### 1. Add Breadcrumb at Kmazarin Entry (CRITICAL)

**File**: `kmazarin/kmazarin/abi_stubs_amd64.s` or wherever `_rt0_amd64_linux` is defined

Add as **first instruction** in entry point:
```asm
TEXT _rt0_amd64_linux(SB), NOSPLIT, $-8
    MOVB $'K', DX
    MOVW $0x3F8, DX  // COM1
    OUTB AX, DX      // Output 'K' to serial
    // ... rest of entry code
```

This will confirm if kmazarin entry is reached at all.

### 2. Check Runtime Overlay Compatibility

**File**: `runtime-patches/sys_linux_amd64.s`

Check if the Go runtime overlay makes assumptions about:
- CS/SS selector values (may hardcode 0x38/0x30)
- Segment descriptor layout
- SYSCALL MSR values

### 3. Verify Stack Alignment

**File**: `diplomat/main/uefi_calls_amd64.s:228-230`

Current jump code:
```asm
MOVQ R8, SP                     // Set stack pointer
MOVQ AX, BX                     // Copy entry point to BX
JMP BX                          // Jump to kmazarin entry
```

Check:
- Is RSP 16-byte aligned? (x86_64 ABI requirement)
- Does kmazarin expect arguments on stack?
- Should we clear frame pointer (RBP)?

### 4. Check for Segment Register Assumptions

**Files to examine**:
- `kmazarin/kmazarin/abi_stubs_amd64.s` - Entry point assembly
- `kmazarin/kmazarin/exceptions_amd64.s` - Exception handlers
- `kmazarin/kmazarin/syscall_amd64.s` - System call entry

Look for:
- Hardcoded segment selector values
- Assumptions about GDT layout
- FS/GS base register usage (TLS setup)

### 5. Compare with ARM64 Entry

**File**: `kmazarin/kmazarin/abi_stubs_arm64.s`

ARM64 entry works correctly. Compare to understand:
- What registers does diplomat pass?
- What initial state does kmazarin expect?
- Is there equivalent setup missing in AMD64?

### 6. Check Diplomat's Final State

**File**: `diplomat/main/uefi_calls_amd64.s:138-230`

Before jumping to kmazarin, verify:
- [ ] CS = 0x08
- [ ] SS = 0x10
- [ ] DS = 0x10
- [ ] ES = 0x10
- [ ] FS = 0x00
- [ ] GS = 0x00
- [ ] RSP = valid stack address
- [ ] RSP 16-byte aligned
- [ ] RBP = 0 (or valid frame pointer)
- [ ] RFLAGS = reasonable (interrupts disabled initially?)

## Test Commands

**Build and run with 10s timeout**:
```bash
export GOTOOLCHAIN=auto
export GO=/opt/homebrew/Cellar/go/1.25.5/libexec/bin/go
export QEMU=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64
export QEMU_AMD64=/opt/homebrew/Cellar/qemu/10.2.0/bin/qemu-system-x86_64

$GO tool task run-x86_64 TIMEOUT=10
```

**View serial log safely**:
```bash
$GO tool safe-serial-read /tmp/diplomat-serial.log
```

**QEMU monitor commands**:
```bash
# View registers at crash
echo "info registers" | nc 127.0.0.1 4445

# View GDT
echo "info gdt" | nc 127.0.0.1 4445

# View IDT
echo "info idt" | nc 127.0.0.1 4445

# Stop QEMU
echo "quit" | nc 127.0.0.1 4445
```

## References

### Key Files
- `diplomat/main/uefi_calls_amd64.s` - GDT creation and jump to kmazarin
- `diplomat/main/kernelvm_amd64.go` - IDT setup
- `diplomat/main/elf_loader.go` - ELF loading (has temporary debug breadcrumbs)
- `kmazarin/kmazarin/thread_context_amd64.go` - Selector constants
- `kmazarin/kmazarin/syscall_amd64.go` - SYSCALL MSRs and TSS
- `kmazarin/kmazarin/exceptions_amd64.s` - Exception and syscall entry
- `kmazarin/kmazarin/abi_stubs_amd64.s` - Entry point (likely location)
- `runtime-patches/sys_linux_amd64.s` - Go runtime overlay

### Memory Notes from MEMORY.md

**x86_64 TLS Corruption on Context Switch**:
- Clone child writes g to shared TLS slot
- Context switch must restore g to FS_BASE-8
- All exception handler Go functions need `//go:nosplit`

**x86_64 IDT CS Selector**:
- UEFI GDT uses CS=0x38
- Must read actual CS and use in IDT entries
- Now using CS=0x08 (standard layout)

**GDT and Segmentation**:
- Standard x86_64 kernel GDT layout implemented
- Ring 0 (kernel): CS=0x08, SS=0x10
- Ring 3 (user): CS=0x1B, SS=0x23
- TSS at GDT offset 0x28 (was 0xA0)

## Goal

Get kmazarin to boot successfully with the new standard GDT layout, enabling proper Ring 3 userspace support on x86_64. Once this crash is resolved, we can verify:
- SYSCALL/SYSRET work correctly with new selectors
- Context switch preserves CS/SS properly
- Ring 3 userspace programs can execute

## Related Work

No specific plan file exists for this work, but related to:
- Multi-architecture port (x86_64, ARM64, RISC-V)
- Userspace support (Ring 3 execution)
- System call implementation (SYSCALL/SYSRET)

Previous working state: ARM64 kmazarin boots successfully with proper privilege separation.
Target: Match ARM64 functionality on x86_64.
