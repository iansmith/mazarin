# Mazzy Architecture Documentation

## Overview

Mazzy is a bare-metal ARM64 operating system project consisting of two main components:

1. **Mazboot** - Minimal bootloader + OS shim
2. **Kmazarin** - The actual operating system kernel (written in Go)

## Architectural Principles

### Mazboot: The Bootloader/OS Shim

Mazboot implements **just enough of an operating system** to start kmazarin (our real kernel). Since kmazarin is written in Go, mazboot must provide the minimal OS support that the Go compiler and runtime expect.

**Key responsibilities:**
- Initialize hardware (MMU, UART, timers, etc.)
- Provide minimal syscall support (mmap, openat, read, close, etc.)
- Load kmazarin ELF binary into memory
- Set up the execution environment exactly as Linux would (argc/argv/envp/auxv)
- Jump to kmazarin's entry point (`_rt0_arm64_linux`)

**Important constraints:**
- Mazboot uses **minimal Go runtime** - skips full heap/GC initialization
- Uses simple `kmalloc/kfree` for essential allocations only
- No goroutines or full scheduler in mazboot

### Kmazarin: The Real Kernel

Kmazarin is an **unmodified Go binary** that must start up in "absolutely the normal way" - exactly as if Linux had exec'd it. We cannot modify kmazarin's entry point or skip its initialization.

**Key characteristics:**
- Full Go runtime initialization (heap, GC, scheduler, goroutines)
- Once initialized and running goroutines in `main()`, it will provide OS services
- Will eventually provide syscall/OS support to user-space Go programs
- Acts as the real operating system kernel

## Current Challenge: Starting Kmazarin

### The Problem

When a Go program starts on Linux:
1. Linux kernel loads the ELF binary
2. Sets up initial stack with:
   - argc (argument count)
   - argv (argument vector)
   - envp (environment variables)
   - **auxv (auxiliary vector)** - critical OS information
3. Jumps to program entry point (`_rt0_arm64_linux`)
4. Go runtime reads auxv to get:
   - `AT_PAGESZ` - Physical page size (4096 bytes)
   - `AT_RANDOM` - Random bytes for security
   - `AT_SECURE` - Secure mode flag
5. Runtime uses this to initialize `physPageSize` and other globals
6. Calls `runtime.schedinit()` → `runtime.mallocinit()` to set up heap

### What We Must Implement in Mazboot

To start kmazarin properly, mazboot must:

1. **Study Linux kernel behavior** when exec'ing a Go binary
2. **Replicate the kernel loader**:
   - Load ELF segments into memory at correct addresses
   - Set up initial stack with argc/argv/envp/auxv structure
   - Set registers R0=argc, R1=argv (ARM64 calling convention)
   - Jump to kmazarin's `_rt0_arm64_linux` entry point

3. **Provide syscall support** for kmazarin's startup:
   - `mmap` - Memory allocation (used by heap allocator)
   - `openat` - File operations (for `/proc/self/auxv` fallback)
   - `read` - Read auxiliary vector data
   - `close` - Close file descriptors
   - Others as needed (futex, clock_gettime, etc.)

4. **Provide auxiliary vector** with conservative, correct values:
   - `AT_PAGESZ = 4096` - Physical page size
   - `AT_RANDOM` → pointer to 16 random bytes
   - `AT_SECURE = 0` - Not in secure mode
   - `AT_NULL = 0` - Terminator

## Current Status

### ✅ Completed
- Mazboot initialization (MMU, UART, basic syscalls)
- ELF loading code (`loadAndRunKmazarin()`)
- Auxiliary vector data structure in syscall.go
- `/proc/self/auxv` file descriptor support (FD 4)
- Basic syscall handlers (mmap, openat, read, close)

### ❌ Not Working Yet
- **argc/argv/envp/auxv chain setup** - Not passing auxv to kmazarin properly
- **Initial stack setup** - Not setting up the stack structure before jumping
- **Register initialization** - Not setting R0/R1 before jumping to kmazarin

### Current Error
```
runtime.sysMapOS(0x0, 0x0, {0x0, 0x0})
runtime: mmap(0x0, 0) returned 0x0, 22
fatal error: runtime: cannot map pages in arena address space
```

**Root cause:** kmazarin's runtime calls `sysargs()` which expects argc/argv/auxv chain, doesn't find it, tries to read `/proc/self/auxv` fallback, but crashes before even opening it. The runtime is operating on uninitialized/zero values.

## Next Steps

1. **Implement argc/argv/envp/auxv chain in memory**
   - Allocate stack space for the structure
   - Populate with proper values
   - Format: `[argc][argv...][NULL][envp...][NULL][auxv...]`

2. **Modify jump code** to set registers before jumping:
   - R0 = argc
   - R1 = argv pointer
   - SP = stack pointer to argc

3. **Test** that kmazarin's `sysargs()` successfully reads auxv

4. **Verify** `physPageSize` is set to 4096

5. **Monitor** heap initialization succeeds

## References

- Go runtime source: `/opt/homebrew/Cellar/go/1.25.5/libexec/src/runtime/`
  - `asm_arm64.s` - `rt0_go` startup code
  - `os_linux.go` - `sysargs()` function
  - `proc.go` - `schedinit()` function
  - `malloc.go` - `mallocinit()` function
  - `mem_linux.go` - `sysMapOS()` function

- Linux auxiliary vector format:
  - Array of (tag, value) uint64 pairs
  - Terminated by `AT_NULL` (0, 0)
  - Passed via stack after argc/argv/envp

## Philosophy

**Mazboot is NOT a full OS** - it's the absolute minimum needed to start the real OS (kmazarin). Think of it as:
- GRUB/UEFI (loads the kernel)
- + Minimal Linux kernel shim (provides just enough syscalls for Go runtime init)
- = Mazboot

Once kmazarin is running with full Go runtime initialized, it becomes the real kernel that provides OS services to user programs.

## Physical Memory Layout

Mazzy runs on QEMU with **1GB RAM** starting at physical address `0x40000000`. The memory is organized as follows:

```
Physical Address Range          Size      Purpose                      Mapped?
================================================================================
0x00000000 - 0x08FFFFFF         144 MB    Device Memory (UART, etc.)   ✓ (Device)
0x09000000 - 0x09000FFF         4 KB      UART0 (PL011)                ✓ (Device)
0x3F000000 - 0x3FFFFFFF         16 MB     PCI ECAM (lowmem)            ✓ (Device)

0x40000000 - 0x40100000         1 MB      DTB (Device Tree Blob)       ✓ (RO)
0x40100000 - 0x401E2000         ~920 KB   Mazboot .text (code)         ✓ (RO+X)
0x401E2000 - 0x40567000         ~3.5 MB   Mazboot .rodata              ✓ (RO)
0x40567000 - 0x405FE000         ~604 KB   Mazboot .data                ✓ (RW)
0x405FE000 - 0x406C8000         ~808 KB   Mazboot .bss                 ✓ (RW)
0x406C8000 - 0x41000000         ~3.2 MB   Mazboot heap (kmalloc)       ✓ (RW)

0x41000000 - 0x41800000         8 MB      Page Tables (L0/L1/L2/L3)    ✓ (RW)
0x41800000 - ~0x41A00000        ~2 MB     Kmazarin ELF (loaded)        ✓ (varies)
~0x41A00000 - 0x50000000        ~230 MB   Physical Frame Pool          (demand)

0x50000000 - 0x5EFEFFFF         ~239 MB   Reserved/Unmapped            ✗
0x5EFF0000 - 0x5F000000         64 KB     **g0 Stack (SP_EL0)**        ✓ (RW) ← NEW!
0x5F000000 - 0x5F010000         64 KB     **Exception Stack (SP_EL1)** ✓ (RW) ← NEW!

0x5F010000 - 0x80000000         ~528 MB   Unused RAM                   ✗

0x4010000000 - 0x401FFFFFFF     256 MB    PCI ECAM (highmem)           ✓ (Device)
================================================================================
```

### Critical Regions

#### g0 Stack (SP_EL0) - 0x5EFF0000 - 0x5F000000
- **Size**: 64 KB
- **Purpose**: Normal kernel execution stack (mazboot/kernel code)
- **Register**: SP_EL0 set to `0x5F000000`
- **Mode**: EL1t (SPSel=0, using SP_EL0)
- **Privilege Level**: EL1 (full kernel privileges - NOT EL0 user mode!)
- **Set in**: `boot.s:99` - `msr SP_EL0, x0` where `x0 = 0x5F000000`
- **Activated**: `boot.s:104` - `msr SPSel, xzr` switches to EL1t mode
- **Direction**: Grows downward from `0x5F000000` toward `0x5EFF0000`
- **Attributes**: Normal memory, RW, non-executable
- **Used for**: All normal kernel execution (g0 goroutine, syscalls, etc.)
- **CRITICAL**: Must be mapped in page tables before enabling MMU!

#### Exception Stack (SP_EL1) - 0x5F000000 - 0x5F010000
- **Size**: 64 KB
- **Purpose**: Exception handler stack (IRQ, FIQ, synchronous exceptions)
- **Register**: SP_EL1 set to `0x5F010000`
- **Mode**: EL1h (SPSel=1, using SP_EL1)
- **Privilege Level**: EL1 (full kernel privileges)
- **Set in**: `boot.s:94` - `mov sp, x0` where `x0 = 0x5F010000`
- **Activated**: Automatically when exceptions occur
- **Direction**: Grows downward from `0x5F010000` toward `0x5F000000`
- **Attributes**: Normal memory, RW, non-executable
- **Used for**: Exception handlers only (CPU auto-switches to SP_EL1)
- **CRITICAL**: Must be mapped in page tables before enabling MMU!

**Key Architecture Point**: Both stacks operate at **EL1 privilege level**. The SP_EL0 register is just a name - we use it as a stack pointer while running at EL1 in EL1t mode. No actual EL0 (user mode) execution occurs yet.

#### Page Tables (0x41000000 - 0x41800000)
- **Size**: 8 MB (defined in `linker.ld`)
- **Structure**:
  - L0 table: 4 KB (512 entries @ 512GB each)
  - L1 table: 4 KB (512 entries @ 1GB each)
  - L2 tables: Multiple 4KB tables (512 entries @ 2MB each)
  - L3 tables: Multiple 4KB tables (512 entries @ 4KB each)
- **Attributes**: Normal Cacheable memory (ARM64 page walker is cache-coherent)

#### Physical Frame Pool (~0x41A00000 - 0x50000000)
- **Size**: ~230 MB (varies based on kmazarin size)
- **Purpose**: Demand-paged memory allocation
- **Start**: After kmazarin ELF (determined at runtime)
- **End**: 0x50000000 (end of first 256MB RAM region)
- **Mapping**: Identity-mapped on demand as pages are allocated

### Memory Mapping Notes

1. **Identity Mapping**: Most regions use VA = PA (virtual address equals physical address)
2. **Device Memory**: UART and MMIO regions use Device-nGnRnE attributes for strict ordering
3. **Normal Memory**: Code/data/stack use Normal Cacheable attributes for performance
4. **Execute Permissions**:
   - `.text`: Read-Only + Execute
   - `.rodata`, `.data`, `.bss`, heap, stack, page tables: Execute Never

## ARM64 Dual-Stack Architecture

### Understanding Exception Levels vs Stack Selection

**CRITICAL DISTINCTION**: Exception levels (EL0-EL3) are **privilege levels**, not stack selectors.

**Exception Levels** (privilege):
- **EL0** = User mode (unprivileged)
- **EL1** = Kernel mode (privileged) ← Mazboot operates here
- **EL2** = Hypervisor mode
- **EL3** = Secure monitor mode

**Stack Selection Modes at EL1**:
- **EL1t** (thread) = Execute at **EL1 privilege**, using **SP_EL0** register (SPSel=0)
- **EL1h** (handler) = Execute at **EL1 privilege**, using **SP_EL1** register (SPSel=1)

**KEY INSIGHT**: SP_EL0 is just a register name! When we use SP_EL0 in EL1t mode, we are:
- ✅ Running at **EL1 privilege level** (full kernel mode)
- ✅ Using the **SP_EL0 register** as our stack pointer
- ❌ **NOT running at EL0** (user mode)
- ❌ **NOT in unprivileged mode**

### Dual-Stack Implementation

Mazboot uses **two separate stacks, both at EL1 privilege**:

1. **g0 Stack (SP_EL0, EL1t mode)**:
   - Normal kernel execution (mazboot code, Go runtime, syscalls)
   - Runs in EL1t mode (SPSel=0, using SP_EL0)
   - Full EL1 privileges - can access all system registers

2. **Exception Stack (SP_EL1, EL1h mode)**:
   - Exception handlers (IRQ, FIQ, synchronous exceptions)
   - Activated automatically when exceptions occur
   - CPU switches to EL1h mode (SPSel=1, using SP_EL1)

**Why separate stacks?**
- **Safety**: Exception handlers can't corrupt normal execution stack
- **Isolation**: Stack overflow in normal code won't corrupt exception handlers
- **Standard practice**: This is how Linux and most OSes work

### Boot Sequence (boot.s)

1. **Start at EL2** (QEMU with virtualization=off)
   - Configure HCR_EL2 to allow EL1 to use AArch64
   - Set SPSR_EL2 to `0x3C5` (EL1h mode with DAIF masked)
   - Execute `eret` to drop to EL1

2. **Enter EL1h Mode** (using SP_EL1)
   - CPU now uses SP_EL1 as current stack
   - ⚠️ **CRITICAL**: SP_EL1 is uninitialized (0x0 or garbage)!

3. **Initialize BOTH Stacks IMMEDIATELY** (boot.s:90-104)
   ```asm
   // Set SP_EL1 (exception stack) - we're IN EL1h mode, must use 'mov sp'
   movz x0, #0x5F01, lsl #16    // 0x5F010000 (exception stack top)
   mov sp, x0                   // Set current stack (SP_EL1)

   // Set SP_EL0 (g0 stack) - safe to use 'msr' because we're using SP_EL1
   movz x0, #0x5F00, lsl #16    // 0x5F000000 (g0 stack top)
   msr SP_EL0, x0               // Set g0 stack

   // Switch to EL1t mode - use SP_EL0 for normal execution
   msr SPSel, xzr               // SPSel=0 → EL1t mode, still at EL1 privilege!
   ```

4. **Continue Boot with g0 Stack (SP_EL0)**
   - All normal code executes in EL1t mode on SP_EL0
   - When exceptions occur, CPU auto-switches to EL1h mode on SP_EL1
   - Both modes run at full EL1 privilege

#### Critical Bug and Fix

**Problem**: Initial implementation used `msr SP_EL1, x0` while in EL1h mode
```asm
msr SP_EL1, x0    // ❌ WRONG - trying to modify active stack register!
```

**Symptom**: System hung completely - no breadcrumbs, no execution

**Root Cause**: Cannot use `msr SP_ELx` to modify the currently active stack pointer register. When in EL1h mode (SPSel=1), the active stack is SP_EL1, and attempting to modify it with `msr` causes undefined behavior (system hang on QEMU).

**Solution**: Use `mov sp, x0` to set the current stack pointer
```asm
mov sp, x0        // ✅ CORRECT - sets current SP (which is SP_EL1 in EL1h)
```

**Rule**:
- Use `mov sp, x0` to set the **currently active** stack pointer
- Use `msr SP_ELx, x0` to set the **inactive** stack pointer

#### Memory Mapping (mmu.go)

Both stacks MUST be mapped in page tables before enabling MMU:

```go
// Map g0 stack (SP_EL0) - boot.s:93
stackBottom := uintptr(0x5EFF0000)
stackTop := uintptr(0x5F000000)
mapRegion(stackBottom, stackTop, stackBottom, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)

// Map exception stack (SP_EL1) - boot.s:89
exceptionStackBottom := uintptr(0x5F000000)
exceptionStackTop := uintptr(0x5F010000)
mapRegion(exceptionStackBottom, exceptionStackTop, exceptionStackBottom, PTE_ATTR_NORMAL, PTE_AP_RW_EL1, PTE_EXEC_NEVER)
```

### Stack Layout Diagram

```
High Memory
┌─────────────────────────┐ 0x5F010000 ← SP_EL1 (exception stack top)
│                         │
│   Exception Stack       │  64 KB, grows ↓
│   (SP_EL1)              │  Used by IRQ/FIQ/exception handlers
│                         │
├─────────────────────────┤ 0x5F000000 ← SP_EL0 (g0 stack top)
│                         │
│   g0 Stack              │  64 KB, grows ↓
│   (SP_EL0)              │  Used by normal kernel code
│                         │
└─────────────────────────┘ 0x5EFF0000 (stack bottom)
Low Memory
```

### Testing and Verification

Boot successfully verified with QEMU logging (`-d exec,cpu_reset`):
- CPU reset shows initial state: `PSTATE=400003c5 -Z-- EL1h`
- Execution trace confirms reaching Go code: `0x401d30d0 main.KernelMain`
- Both stacks properly mapped: Console output shows mapping messages
- MMU enables successfully and returns to Go code: "ZXYm" breadcrumb appears

### References

- [ARM Architecture Reference Manual](https://developer.arm.com/documentation/ddi0487/latest) - Exception levels and stack pointer selection
- [Linux kernel arm64: Introduce IRQ stack](https://lwn.net/Articles/657969/) - Rationale for separate IRQ stacks
- [Linux kernel arm64: VMAP_STACK support](https://lwn.net/Articles/730997/) - Advanced stack protection with guard pages
- [ARM Exception Levels Guide](https://learn.arm.com/learning-paths/embedded-and-microcontrollers/bare-metal/exception-levels/) - Switching exception levels

### Exception Handling Strategy

**Current Status**: Exception vectors not yet configured (to be implemented)

**Future Exception Model**:
- **EL1 exceptions**: Handled by EL1 exception vectors (using SP_EL1 exception stack)
- **EL0 exceptions** (when user mode is added):
  - Synchronous exceptions (syscalls, faults) → Trigger EL1 handlers
  - EL1 handler runs at EL1 privilege on SP_EL1 (exception stack)
  - Handler can inspect exception, handle syscall, or kill process
  - **No EL0-level exception handlers** - all exceptions escalate to EL1

This is the standard OS model: user code (EL0) cannot handle its own exceptions - the kernel (EL1) handles everything.

### Stack Layout Diagram

```
High Memory
┌─────────────────────────┐ 0x5F010000 ← SP_EL1 (exception stack top)
│                         │
│   Exception Stack       │  64 KB, grows ↓
│   (SP_EL1, EL1h mode)   │  IRQ/FIQ/exception handlers
│                         │  Full EL1 privilege
│                         │
├─────────────────────────┤ 0x5F000000 ← SP_EL0 (g0 stack top)
│                         │
│   g0 Stack              │  64 KB, grows ↓
│   (SP_EL0, EL1t mode)   │  Normal kernel code
│                         │  Full EL1 privilege
│                         │
└─────────────────────────┘ 0x5EFF0000 (stack bottom)
Low Memory
```

### Recent Fixes

**2025-12-25 (Part 1)**: Added g0 stack mapping (0x5EFF0000-0x5F000000)
- **Problem**: Stack region was not mapped in page tables
- **Symptom**: MMU enable succeeded, but `ret` instruction hung - Go code never executed
- **Root Cause**: Stack operations after MMU enable caused silent failures (unmapped region)
- **Solution**: Added explicit stack region mapping in `initMMU()` before enabling MMU
- **Result**: ✅ MMU enables successfully and returns to Go code!

**2025-12-25 (Part 2)**: Implemented dual-stack architecture (SP_EL0 and SP_EL1)
- **Problem**: Using `msr SP_EL1, x0` while in EL1h mode caused system hang
- **Symptom**: Boot code produced no output at all - complete hang at first stack setup
- **Root Cause**: Cannot modify currently active stack pointer with `msr` instruction
- **Solution**:
  1. Use `mov sp, x0` to set SP_EL1 while in EL1h mode
  2. Use `msr SP_EL0, x0` to set SP_EL0 (safe because it's inactive)
  3. Switch to EL1t mode with `msr SPSel, xzr` to use SP_EL0 for normal code
  4. Map both stack regions in page tables (mmu.go:1169-1188)
- **Architecture Understanding**:
  - EL1t mode uses SP_EL0 **at EL1 privilege** (not EL0 user mode!)
  - Both stacks operate at full EL1 kernel privilege
  - No actual EL0 (user mode) execution yet
  - Future EL0 exceptions will escalate to EL1 handlers
- **Result**: ✅ Boot succeeds, dual-stack configured, exception safety implemented!
