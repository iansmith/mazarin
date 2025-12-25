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
