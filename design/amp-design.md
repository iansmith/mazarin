# AMP Kernel Design for Kmazarin

## Overview

This document describes an Asymmetric Multi-Processing (AMP) design for kmazarin that maximizes "Go-ishness" while maintaining memory protection between user and kernel code.

### Design Goals

1. **Go-ish**: Kernel subsystems are goroutines communicating via channels
2. **Memory Protection**: User code (EL0) cannot corrupt kernel state
3. **Unified Code**: Same kernel code works for 1-core and N-core systems
4. **No Visible Locks**: Channel-based APIs, no mutexes in kernel interface

### Core Philosophy

> "Don't communicate by sharing memory; share memory by communicating."

Every kernel subsystem is a goroutine that receives requests on channels and sends responses back. User code cannot touch channels directly - they live in kernel memory.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                    MEMORY LAYOUT                                    │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   ┌─────────────────────────────────────────────────────────────┐  │
│   │  Kernel Memory (EL1 only)                                   │  │
│   │  - Kernel code (.text)                                      │  │
│   │  - Service channels and buffers                             │  │
│   │  - Syscall handler goroutine stacks                         │  │
│   │  - Page tables, scheduler state                             │  │
│   ├─────────────────────────────────────────────────────────────┤  │
│   │  User Memory (EL0 + EL1 accessible)                         │  │
│   │  - User goroutine code                                      │  │
│   │  - User goroutine stacks                                    │  │
│   │  - User heap                                                │  │
│   └─────────────────────────────────────────────────────────────┘  │
│                                                                     │
│   Wild pointers in user code can only corrupt user memory.          │
│   Channels and kernel state are protected by page tables.           │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Kernel Service Architecture

### Service Goroutines

Each kernel subsystem runs as one or more goroutines that own their state exclusively:

```
┌─────────────────────────────────────────────────────────────────────┐
│                 KERNEL SERVICE GOROUTINES                           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   ┌───────────┐    ┌───────────┐    ┌───────────┐    ┌───────────┐ │
│   │  Memory   │    │ Scheduler │    │   Timer   │    │   File    │ │
│   │  Service  │    │  Service  │    │  Service  │    │  Service  │ │
│   │           │    │           │    │           │    │           │ │
│   │ for req   │    │ for req   │    │ for tick  │    │ for req   │ │
│   │  := range │    │  := range │    │  := range │    │  := range │ │
│   │ memChan   │    │ schedChan │    │ timerChan │    │ fileChan  │ │
│   └─────┬─────┘    └─────┬─────┘    └─────┬─────┘    └─────┬─────┘ │
│         │                │                │                │       │
│         └────────────────┴────────────────┴────────────────┘       │
│                              │                                      │
│                        Go Channels                                  │
│                  (the ONLY communication)                           │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Channel Definitions

```go
// Core service channels - all in kernel memory
var (
    memoryServiceChan    chan MemoryRequest
    schedulerServiceChan chan SchedulerRequest
    timerTickChan        chan uint64
    sleepRequestChan     chan SleepRequest
    syscallChan          chan SyscallRequest
)

// Request/Response pattern
type MemoryRequest struct {
    Op       MemOp  // MMAP, MUNMAP, MPROTECT
    Addr     uintptr
    Size     uintptr
    Prot     int
    Response chan<- MemoryResponse
}

type MemoryResponse struct {
    Addr  uintptr
    Error error
}
```

---

## Syscall Handling

This section describes how **EL0 user code** makes syscalls. Note that kernel self-calls
(Go runtime needing mmap, clone, etc.) use a completely different path - see
"The Syscall Overlay" section below.

### User Syscall Flow (EL0 → EL1 → EL0)

When user code running at EL0 executes SVC, hardware traps to EL1:

```
User Process (EL0)                      Kernel (EL1)
──────────────────                      ────────────

mmap(0, 4096, RW)
       │
       ▼
libc syscall stub:
  X8 = SYS_MMAP
  X0-X5 = args
  SVC #0
       │
═══════╪═══════════════════════════════════════════  EL0 → EL1 (hardware)
       │
       ▼
                              svc_entry (assembly):
                                - Save user registers to UserContext
                                - Switch to kernel stack
                                         │
                                         ▼
                              handleSVC(ctx *UserContext):
                                switch ctx.Regs[8] {
                                case SYS_MMAP:
                                    result = doMmap(...)
                                case SYS_WRITE:
                                    result = doWrite(...)
                                ...
                                }
                                ctx.Regs[0] = result
                                         │
                                         ▼
                              svc_exit (assembly):
                                - Restore user registers
                                - ERET
                                         │
═══════════════════════════════════════════════════  EL1 → EL0 (ERET)
       │
       ▼
(result in X0)
continue...
```

### User SVC Handler Implementation

The SVC handler dispatches to the same `doXxx()` functions used by kernel self-calls:

```go
// handleSVC - called from assembly svc_entry after saving context
func handleSVC(ctx *UserContext) {
    syscallNum := ctx.Regs[8]  // X8 contains syscall number

    var result uintptr
    switch syscallNum {
    case SYS_MMAP:
        result = doMmap(ctx.Regs[0], ctx.Regs[1], ctx.Regs[2],
                        ctx.Regs[3], ctx.Regs[4], ctx.Regs[5])
    case SYS_MUNMAP:
        result = doMunmap(ctx.Regs[0], ctx.Regs[1])
    case SYS_WRITE:
        result = doWrite(ctx.Regs[0], ctx.Regs[1], ctx.Regs[2])
    case SYS_READ:
        result = doRead(ctx.Regs[0], ctx.Regs[1], ctx.Regs[2])
    case SYS_CLONE:
        result = doClone(ctx.Regs[0], ctx.Regs[1], ctx.Regs[2],
                         ctx.Regs[3], ctx.Regs[4])
    case SYS_FUTEX:
        result = doFutex(ctx.Regs[0], ctx.Regs[1], ctx.Regs[2],
                         ctx.Regs[3], ctx.Regs[4], ctx.Regs[5])
    case SYS_EXIT_GROUP:
        doExitGroup(ctx.Regs[0])
        // doesn't return
    default:
        result = uintptr(-ENOSYS)
    }

    // Store result in X0 for return to user
    ctx.Regs[0] = uint64(result)
}
```

**Key point**: The `doXxx()` functions are shared between:
1. **Kernel self-calls** via `ksyscall6()` (overlay, no SVC)
2. **User syscalls** via `handleSVC()` (hardware SVC exception)

This means syscall logic is implemented once, with two entry paths.

---

## Single-Core vs Multi-Core

### Single Core (1 CPU)

All goroutines (user syscall handlers, kernel services) run on the same core, multiplexed by the Go scheduler:

```
┌─────────────────────────────────────────────────────────────────────┐
│                     SINGLE CORE                                     │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   Core 0                                                            │
│   ┌─────────────────────────────────────────────────────────────┐  │
│   │                                                             │  │
│   │   EL0: User goroutines                                      │  │
│   │         │                                                   │  │
│   │         │ SVC                                               │  │
│   │         ▼                                                   │  │
│   │   EL1: Syscall handlers ←──┐                               │  │
│   │         │                  │                               │  │
│   │         │ channels         │ Go scheduler                  │  │
│   │         ▼                  │ multiplexes                   │  │
│   │   EL1: Service goroutines ─┘                               │  │
│   │                                                             │  │
│   └─────────────────────────────────────────────────────────────┘  │
│                                                                     │
│   No IPI needed - Go scheduler handles everything                   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Multi-Core AMP (4 CPUs)

Cores 0-2 run user code at EL0, Core 3 runs all kernel goroutines:

```
┌─────────────────────────────────────────────────────────────────────┐
│                     MULTI-CORE AMP                                  │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   User Cores (0, 1, 2)              Kernel Core (3)                 │
│   ────────────────────              ───────────────                 │
│                                                                     │
│   ┌─────────┐                       ┌──────────────────────────┐   │
│   │  EL0    │   SVC                 │        EL1               │   │
│   │  User   │────────┐              │                          │   │
│   │ Goroutn │        │              │  ┌────────────────────┐  │   │
│   └─────────┘        │              │  │  Syscall Handlers  │  │   │
│        ▲             │              │  │    (goroutines)    │  │   │
│        │             ▼              │  └─────────┬──────────┘  │   │
│   ┌─────────┐   ┌─────────┐        │            │             │   │
│   │  EL1    │   │  EL1    │   IPI  │            ▼             │   │
│   │  Stub   │──►│ Mailbox │───────►│  ┌────────────────────┐  │   │
│   │(minimal)│   │  + IPI  │        │  │  Service Channels  │  │   │
│   └─────────┘   └─────────┘        │  └────────────────────┘  │   │
│        │                           │            │             │   │
│        │                           │            ▼             │   │
│   ┌────┴────┐                      │  ┌────────────────────┐  │   │
│   │Response │◄──────── IPI ────────│  │ Memory, Scheduler, │  │   │
│   │  Ready  │                      │  │ Timer Services     │  │   │
│   └─────────┘                      │  └────────────────────┘  │   │
│                                    │                          │   │
│                                    └──────────────────────────┘   │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Code That Differs

| Component | Single Core | Multi-Core |
|-----------|-------------|------------|
| Service goroutines | Same | Same |
| Channel operations | Same | Same |
| Syscall handlers | Same | Same |
| **SVC entry** | Direct switch to handler | IPI to kernel core |
| **Return to user** | Direct ERET | IPI back + ERET |
| **Timer delivery** | Local flag + goroutine | IPI to kernel core |

Only the **entry/exit paths** differ. All Go code is identical.

---

## Timer Interrupt Handling

Timer interrupts require special handling because they're asynchronous hardware events
that must be converted to channel messages without corrupting whatever was interrupted.

### The Challenge

```
Timer can interrupt:

  1. User code at EL0        → Need to possibly preempt
  2. Syscall handler at EL1  → Kernel goroutine running
  3. Service goroutine at EL1 → Kernel goroutine running
  4. Go scheduler itself     → Very delicate!
  5. Idle loop               → Wake up and check deadlines

We need to turn this async event into a channel message
WITHOUT corrupting whatever was interrupted.
```

### Design Decision: Timer Only on Kernel Core

For multi-core, we route timer interrupts only to the kernel core. User cores
have their timer disabled in the GIC. This means:

- Timer code is **identical** for single and multi-core
- No timer forwarding IPIs needed
- Simpler, more reliable

```
Single Core:                    Multi-Core:
────────────                    ───────────
Core 0: Timer IRQ enabled       Core 0,1,2: Timer IRQ disabled
        ↓                       Core 3:     Timer IRQ enabled
        timerBridge goroutine              ↓
        ↓                                  timerBridge goroutine (same!)
        timerTickChan                      ↓
        ↓                                  timerTickChan
        timerService goroutine             ↓
                                           timerService goroutine (same!)
```

### Timer Implementation (Core-Agnostic)

```go
// Timer IRQ handler sets this atomic flag
var timerTickPending uint64

// Bridge goroutine converts hardware event to channel message
// IDENTICAL for single and multi-core!
func timerBridge() {
    for {
        WaitForInterrupt()  // WFI - wakes on any interrupt

        if tick := atomic.SwapUint64(&timerTickPending, 0); tick != 0 {
            timerTickChan <- tick  // Now it's a channel message!
        }
    }
}

// Timer service goroutine - processes ticks and deadlines
// IDENTICAL for single and multi-core!
func timerService() {
    var deadlines []Deadline

    for {
        select {
        case tick := <-timerTickChan:
            // Find all ready deadlines
            var ready []Deadline
            var remaining []Deadline

            for _, d := range deadlines {
                if d.Time <= tick {
                    ready = append(ready, d)
                } else {
                    remaining = append(remaining, d)
                }
            }
            deadlines = remaining

            // Wake each ready thread via their channel
            for _, d := range ready {
                d.WakeChan <- tick
                // Also notify scheduler if needed
                schedulerServiceChan <- SchedulerRequest{
                    Op:       WAKE,
                    ThreadID: d.ThreadID,
                }
            }

        case req := <-sleepRequestChan:
            deadlines = append(deadlines, Deadline{
                Time:     req.WakeTime,
                WakeChan: req.WakeChan,
                ThreadID: req.ThreadID,
            })
        }
    }
}
```

### User Preemption (Time Slicing)

With timer only on kernel core, how do we preempt long-running user code on other cores?

**Options:**
1. Kernel core sends IPI to user core: "preempt now"
2. User code voluntarily yields periodically
3. Accept that user code runs until syscall

For Go code, option (2) is natural - Go inserts preemption points at function calls.
User goroutines will yield when they call functions, which happens frequently.

For compute-bound loops without function calls, option (1) can be added:

```go
// On kernel core, periodically check if user cores need preemption
func preemptionMonitor() {
    for tick := range preemptTickChan {
        for core := range userCores {
            if shouldPreempt(core, tick) {
                SendIPI(core, SGI_PREEMPT)
            }
        }
    }
}
```

---

## Kernel Self-Calls (Runtime Bootstrap)

The Go runtime itself needs OS services (mmap, clone, futex). This creates
a bootstrap problem: channels need memory, but memory allocation needs channels.

### The Bootstrap Problem

```
Go runtime needs:              But that requires:
─────────────────              ─────────────────
Memory (mmap)           →      memoryService goroutine running
Threads (clone)         →      schedulerService goroutine running
Sync (futex)            →      channels working
Channels                →      memory allocation

How do we start the first goroutine?
How do we allocate memory for the first channel?
```

### Solution: Cardinal as Phase 0 + Phased Kmazarin Bootstrap

Cardinal (the bootloader) serves as Phase 0, handling all pre-Go setup:

```
┌─────────────────────────────────────────────────────────────────────┐
│              BOOTSTRAP PHASES                                       │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   Phase 0: Cardinal (Assembly/C, before Go)                         │
│   ─────────────────────────────────────────                         │
│     Cardinal already does:                                          │
│       ✓ Initialize MMU and page tables                              │
│       ✓ Set up GIC (interrupt controller)                           │
│       ✓ Configure memory regions                                    │
│       ✓ Load kmazarin ELF                                           │
│       ✓ Set up initial kernel stack                                 │
│       ✓ Jump to kmazarin entry point                                │
│                                                                     │
│     May need to add:                                                │
│       - Detect core count, pass to kmazarin                         │
│       - Configure timer routing (kernel core only for multi)        │
│       - Hold secondary cores in WFI (multi-core)                    │
│                                                                     │
│   Phase 1: Kmazarin Bootstrap (Minimal Go, No Channels Yet)         │
│   ─────────────────────────────────────────────────────────         │
│     - Go runtime starts on g0                                       │
│     - Runtime calls "mmap" → direct function (not channel)          │
│     - Static bootstrap allocator provides memory                    │
│     - Create service goroutines                                     │
│     - Create channels (now allocator works!)                        │
│                                                                     │
│   Phase 2: Full Go (Channels Active)                                │
│   ────────────────────────────────────                              │
│     - Switch services to channel-based API                          │
│     - Runtime self-calls go through channels                        │
│     - Wake secondary cores (multi-core)                             │
│     - User code can start                                           │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### The Syscall Overlay: Intercepting All Syscalls at the Root

The Go runtime needs OS services (mmap, clone, futex, etc.) to function. Normally these
would be syscalls via SVC, but kmazarin **is** the kernel - there's no OS above us to call.

Rather than intercepting individual syscall functions, we intercept at the **root**: the
`runtime.Syscall6()` family of functions. Every syscall in Go flows through these functions,
so by redirecting them we capture everything with a single overlay.

**Key insight #1**: This is a **link-time** mechanism. The overlay affects how the kmazarin
binary is built. It has zero effect on how user EL0 code works - that's a completely separate
path using hardware SVC exceptions.

**Key insight #2**: Both paths (kernel self-call and user SVC) call the **same underlying
`doXxx()` implementations**. The syscall logic is written once; only the entry path differs.

```
┌─────────────────────────────────────────────────────────────────────┐
│              SYSCALL INTERCEPTION AT THE ROOT                       │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   Go Runtime (standard)              Kmazarin (via overlay)         │
│   ─────────────────────              ──────────────────────         │
│                                                                     │
│   runtime.mmap()                     runtime.mmap()                 │
│         ↓                                  ↓                        │
│   syscall.Syscall6(SYS_MMAP, ...)   syscall.Syscall6(SYS_MMAP, ...) │
│         ↓                                  ↓                        │
│   runtime.Syscall6()                 ksyscall6()  ← overlay redirect│
│         ↓                                  ↓                        │
│   SVC #0 instruction                 switch trap {                  │
│         ↓                            case SYS_MMAP:                 │
│   Linux kernel                           return doMmap(...)         │
│                                      case SYS_CLONE:                │
│                                          return doClone(...)        │
│                                      ...                            │
│                                      }                              │
│                                                                     │
│   NO SVC instruction ever executes in the kmazarin binary.          │
│   All syscalls become direct Go function calls.                     │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Two Entry Paths, Same Implementation

The `doXxx()` functions contain the actual syscall logic. They're called from two paths:

```
┌─────────────────────────────────────────────────────────────────────┐
│              KERNEL SELF-CALL vs USER SYSCALL                       │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   Kernel Self-Call (via overlay)       User EL0 Code (via SVC)      │
│   ──────────────────────────────       ────────────────────────     │
│                                                                     │
│   Go runtime needs memory              User program calls mmap()    │
│         │                                    │                      │
│         ▼                                    ▼                      │
│   syscall.Syscall6(SYS_MMAP,...)       libc: mov x8,#SYS_MMAP       │
│         │                                    svc #0                 │
│         ▼                                    │                      │
│   ksyscall6(SYS_MMAP, ...)             ═════╪═════ EL0 → EL1        │
│         │                                    │                      │
│         │                                    ▼                      │
│         │                              svc_entry:                   │
│         │                                save context               │
│         │                                    │                      │
│         │                                    ▼                      │
│         │                              handleSVC(ctx):              │
│         │                                switch ctx.Regs[8] {       │
│         │                                case SYS_MMAP:             │
│         ▼                                    │                      │
│   ┌─────────────────────────────────────────────┐                   │
│   │              doMmap(...)                    │ ← Same function!  │
│   │   - Allocate physical pages                 │                   │
│   │   - Update page tables                      │                   │
│   │   - Return mapped address                   │                   │
│   └─────────────────────────────────────────────┘                   │
│         │                                    │                      │
│         ▼                                    ▼                      │
│   Return to caller                     ctx.Regs[0] = result         │
│   (just a function return)             ERET back to user            │
│                                                                     │
│   Direct function call             Hardware exception + ERET        │
│   No privilege transition          EL0 → EL1 → EL0 transition       │
│   ~nanoseconds                     ~microseconds                    │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### The ksyscall Implementation

```go
package ksyscall

import "unsafe"

// ksyscall6 - replaces runtime.Syscall6 via overlay
// This is the ONLY interception point needed - all syscalls flow through here.
//
//go:nosplit
func ksyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr) {
    switch trap {
    case SYS_MMAP:
        r1 = doMmap(a1, a2, a3, a4, a5, a6)
    case SYS_MUNMAP:
        r1 = doMunmap(a1, a2)
    case SYS_MPROTECT:
        r1 = doMprotect(a1, a2, a3)
    case SYS_CLONE:
        r1 = doClone(a1, a2, a3, a4, a5)
    case SYS_FUTEX:
        r1 = doFutex(a1, a2, a3, a4, a5, a6)
    case SYS_NANOSLEEP:
        r1 = doNanosleep(a1, a2)
    case SYS_WRITE:
        r1 = doWrite(a1, a2, a3)
    case SYS_READ:
        r1 = doRead(a1, a2, a3)
    case SYS_OPENAT:
        r1 = doOpenat(a1, a2, a3, a4)
    case SYS_CLOSE:
        r1 = doClose(a1)
    case SYS_EXIT_GROUP:
        doExitGroup(a1)
    // ... other syscalls as needed
    default:
        // Unknown syscall - return ENOSYS
        return 0, 0, uintptr(ENOSYS)
    }

    // Convention: negative r1 means error, convert to (0, 0, errno)
    if int64(r1) < 0 {
        return 0, 0, uintptr(-int64(r1))
    }
    return r1, 0, 0
}

// Variants for different argument counts
//go:nosplit
func ksyscall0(trap uintptr) (r1, r2, err uintptr) {
    return ksyscall6(trap, 0, 0, 0, 0, 0, 0)
}

//go:nosplit
func ksyscall1(trap, a1 uintptr) (r1, r2, err uintptr) {
    return ksyscall6(trap, a1, 0, 0, 0, 0, 0)
}

// ... ksyscall2, ksyscall3, etc.
```

### The doXxx() Implementations

These are the actual syscall handlers - shared between kernel self-calls and user SVCs:

```go
package ksyscall

// doMmap - the REAL mmap implementation
// Called from: ksyscall6 (kernel) or handleSVC (user)
//
//go:nosplit
func doMmap(addr, length, prot, flags, fd, offset uintptr) uintptr {
    // During bootstrap, use simple bump allocator
    if !bootstrapComplete {
        return bootstrapMmap(addr, length, prot, flags)
    }

    // Full implementation: allocate pages, update page tables
    return allocateAndMapPages(addr, length, prot, flags, fd, offset)
}

// doClone - create a new thread/process
//go:nosplit
func doClone(flags, stack, parentTid, childTid, tls uintptr) uintptr {
    if !bootstrapComplete {
        // During bootstrap, we're single-threaded
        // Return success but don't actually create thread
        return 0
    }

    // Full implementation: create ThreadContext, add to scheduler
    return createThread(flags, stack, parentTid, childTid, tls)
}

// doFutex - fast userspace mutex
//go:nosplit
func doFutex(addr, op, val, timeout, addr2, val3 uintptr) uintptr {
    if !bootstrapComplete {
        // During bootstrap: WAKE is no-op, WAIT would deadlock
        if op&FUTEX_WAKE != 0 {
            return 0
        }
        throw("futex wait during bootstrap")
    }

    // Full implementation
    return futexOp(addr, op, val, timeout, addr2, val3)
}

// doWrite - write to file descriptor
//go:nosplit
func doWrite(fd, buf, count uintptr) uintptr {
    // For fd=1 or fd=2, write to UART
    if fd == 1 || fd == 2 {
        return uartWrite(buf, count)
    }
    // Other fds: file system (future)
    return uintptr(-EBADF)
}
```

### User SVC Handler

For EL0 user code, the SVC exception handler dispatches to the same `doXxx()` functions:

```go
// handleSVC - called from assembly svc_entry after saving context
func handleSVC(ctx *UserContext) {
    syscallNum := ctx.Regs[8]  // X8 contains syscall number

    var result uintptr
    switch syscallNum {
    case SYS_MMAP:
        result = doMmap(ctx.Regs[0], ctx.Regs[1], ctx.Regs[2],
                        ctx.Regs[3], ctx.Regs[4], ctx.Regs[5])
    case SYS_WRITE:
        result = doWrite(ctx.Regs[0], ctx.Regs[1], ctx.Regs[2])
    case SYS_READ:
        result = doRead(ctx.Regs[0], ctx.Regs[1], ctx.Regs[2])
    // ... same doXxx functions as ksyscall6!
    default:
        result = uintptr(-ENOSYS)
    }

    // Store result in X0 for return to user
    ctx.Regs[0] = uint64(result)
}
```

### The Overlay File

The overlay replaces `runtime/sys_linux_arm64.s` to redirect syscall functions:

```go
// overlay/runtime/syscall_kmazarin.go
package runtime

import _ "unsafe"

// Redirect all syscall entry points to kmazarin's handlers
//go:linkname Syscall  ksyscall.ksyscall3
//go:linkname Syscall6 ksyscall.ksyscall6
//go:linkname RawSyscall ksyscall.ksyscall3
//go:linkname RawSyscall6 ksyscall.ksyscall6
```

Or replace the assembly file entirely with Go stubs that call `ksyscall.ksyscall6()`.

### Why This Design Works

| Aspect | Benefit |
|--------|---------|
| Single interception point | One overlay catches ALL syscalls |
| Syscall number available | Natural dispatch via `switch trap` |
| Shared implementations | `doXxx()` functions used by both paths |
| No SVC in kernel | Kernel binary never executes SVC |
| Clean user path | EL0 user code uses standard SVC → exception → handler |
| Easy to extend | Add new syscall = add case + doXxx function |
| Testable | Can test doXxx functions on regular Go (mock dependencies) |

### Bootstrap Considerations

During early bootstrap (before full kernel services are ready), the `doXxx()` functions
use simplified implementations:

- `doMmap`: Bump allocator from Cardinal's pre-mapped pool
- `doClone`: Return success but don't create thread (single-threaded bootstrap)
- `doFutex`: WAKE is no-op, WAIT would be fatal (no contention possible)
- `doWrite`: Direct UART output (always works)

### What Cardinal Must Prepare

```c
// Cardinal prepares everything kmazarin's ksyscall handlers need

typedef struct {
    // Core info (from DTB)
    uint32_t num_cores;
    uint32_t kernel_core;

    // Memory layout (from DTB)
    uint64_t ram_base;
    uint64_t ram_size;

    // Physical page pool for demand paging (PHYSICAL addresses)
    uint64_t phys_pool_base;    // e.g., 0x4800_0000 (physical)
    uint64_t phys_pool_size;    // e.g., 16MB
    uint64_t phys_pool_next;    // Bump allocator offset (starts at 0)

    // Kernel virtual address range (HIGH memory, EL1-only)
    uint64_t kernel_va_base;    // e.g., 0xFFFF_0000_0000_0000
    uint64_t kernel_va_next;    // Next VA to hand out from doMmap

    // Initial stacks - in HIGH memory
    uint64_t m0_stack_base;     // e.g., 0xFFFF_0000_0100_0000
    uint64_t m0_stack_size;

} RuntimeConfig;
```

### High Memory for Kernel Protection

All kernel memory lives at high virtual addresses (`0xFFFF_xxxx_xxxx_xxxx`). This provides
hardware-enforced isolation from user code:

```
┌─────────────────────────────────────────────────────────────────────┐
│              VIRTUAL ADDRESS SPACE LAYOUT                           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   0xFFFF_FFFF_FFFF_FFFF  ┌──────────────────────────────────────┐  │
│                          │                                      │  │
│                          │  Kernel Memory (EL1 only)            │  │
│                          │  - Bootstrap pool allocations        │  │
│                          │  - Kernel heap (mheap arenas)        │  │
│                          │  - Kernel stacks                     │  │
│                          │  - Page tables                       │  │
│                          │  - Service channels & state          │  │
│                          │                                      │  │
│   0xFFFF_0000_0000_0000  ├──────────────────────────────────────┤  │
│                          │                                      │  │
│                          │  (unmapped gap)                      │  │
│                          │                                      │  │
│   0x0000_FFFF_FFFF_FFFF  ├──────────────────────────────────────┤  │
│                          │                                      │  │
│                          │  User Memory (EL0 + EL1)             │  │
│                          │  - User code                         │  │
│                          │  - User heap                         │  │
│                          │  - User stacks                       │  │
│                          │                                      │  │
│   0x0000_0000_0000_0000  └──────────────────────────────────────┘  │
│                                                                     │
│   Page table AP bits:                                               │
│     High memory: AP=00 (EL1 RW, EL0 no access)                     │
│     User memory: AP=01 (EL1 RW, EL0 RW)                            │
│                                                                     │
│   Wild pointer in user code → accesses 0xFFFF_... → PERMISSION FAULT│
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Bootstrap Demand Paging

During bootstrap, `doMmap()` returns high virtual addresses but doesn't allocate physical
pages immediately. Physical pages are allocated on first access via the page fault handler:

```
┌─────────────────────────────────────────────────────────────────────┐
│              BOOTSTRAP DEMAND PAGING FLOW                           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   1. Go runtime calls mmap() → ksyscall6() → doMmap()              │
│         │                                                           │
│         ▼                                                           │
│   2. doMmap() returns 0xFFFF_0000_1000_0000 (high VA)              │
│      - Just updates kernel_va_next                                  │
│      - Records region as valid for page fault handler               │
│      - NO physical pages allocated yet                              │
│         │                                                           │
│         ▼                                                           │
│   3. Go writes to 0xFFFF_0000_1000_0000                             │
│         │                                                           │
│         ▼                                                           │
│   4. PAGE FAULT (no L3 PTE for that address)                        │
│         │                                                           │
│         ▼                                                           │
│   5. data_abort handler (bootstrap version):                        │
│      a. Check: is faultAddr in valid mmap region? YES               │
│      b. Allocate physical page from phys_pool (bump allocator)      │
│      c. Create L3 PTE: VA → PA, AP=00 (EL1 only), AF=1             │
│      d. ERET (retry the write)                                      │
│         │                                                           │
│         ▼                                                           │
│   6. Write succeeds - page now mapped                               │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Bootstrap Page Fault Handler

This must work immediately when kmazarin starts - before channels, before full kernel:

```go
// Called from data_abort - must be nosplit, no allocations!
// Uses mmapRegions array populated by doMmap()
//
//go:nosplit
func bootstrapPageFaultHandler(faultAddr uint64) bool {
    // Only handle high-memory faults (kernel region)
    if faultAddr < 0xFFFF_0000_0000_0000 {
        return false  // User region - not our problem here
    }

    // Is this a valid region we handed out via doMmap()?
    if !isValidMmapRegion(faultAddr) {
        return false  // Invalid access - real fault
    }

    // Allocate physical page from Cardinal's pool
    physPage := allocBootstrapPhysPage()
    if physPage == 0 {
        throw("bootstrap physical pool exhausted")
    }

    // Map it at the faulting address
    // AP=00: EL1 RW, EL0 no access
    mapPageDirect(faultAddr, physPage, AP_EL1_RW_EL0_NONE)

    return true  // Handled, retry the instruction
}

//go:nosplit
func isValidMmapRegion(addr uint64) bool {
    for i := 0; i < mmapRegionCount; i++ {
        r := &mmapRegions[i]
        if r.valid && addr >= r.base && addr < r.base+r.size {
            return true
        }
    }
    return false
}

//go:nosplit
func allocBootstrapPhysPage() uint64 {
    cfg := getCardinalConfig()

    // Atomic bump allocator
    offset := atomic.AddUint64(&cfg.phys_pool_next, 4096) - 4096
    if offset >= cfg.phys_pool_size {
        return 0  // Exhausted
    }

    // Zero the page (security: don't leak old data)
    physAddr := cfg.phys_pool_base + offset
    zeroPage(physAddr)

    return physAddr
}

//go:nosplit
func mapPageDirect(va, pa uint64, ap uint64) {
    // Walk page tables, create L3 entry
    // Cardinal has pre-populated L0/L1/L2 for the kernel VA range
    // We just need to fill in L3 entries

    l3table := getL3TableForVA(va)  // L2 entry points to this
    l3index := (va >> 12) & 0x1FF

    // Create L3 page descriptor
    // Bits: valid=1, page=1, AP=ap, AF=1, SH=inner-shareable, attridx=normal
    pte := pa | PTE_VALID | PTE_PAGE | (ap << 6) | PTE_AF | PTE_SH_INNER | PTE_NORMAL
    l3table[l3index] = pte

    // Invalidate TLB for this address
    tlbiVA(va)
    dsb()
    isb()
}
```

### Cardinal Page Table Setup for High Memory

Cardinal must pre-create the upper levels of page tables for the kernel VA range:

```c
void prepare_for_kmazarin(RuntimeConfig* cfg) {
    // Set up page tables for kernel high memory range
    // Pre-populate L0, L1, L2 - kmazarin fills L3 on demand

    // L0 entry for 0xFFFF_xxxx range (index 511)
    uint64_t* l0 = (uint64_t*)PAGE_TABLE_BASE;
    uint64_t* l1_kernel = alloc_page_table();
    l0[511] = make_table_entry(l1_kernel);

    // L1 entries covering kernel VA range
    // Each L1 entry covers 1GB
    for (int i = 0; i < KERNEL_L1_ENTRIES; i++) {
        uint64_t* l2 = alloc_page_table();
        l1_kernel[i] = make_table_entry(l2);

        // L2 entries - each covers 2MB
        // Pre-allocate L3 tables so fault handler doesn't need to
        for (int j = 0; j < 512; j++) {
            uint64_t* l3 = alloc_page_table();
            l2[j] = make_table_entry(l3);
            // L3 entries left empty - filled on demand by fault handler
        }
    }

    // Pre-map m0 stack (needs to be ready immediately)
    uint64_t stack_pa = cfg->phys_pool_base;
    cfg->phys_pool_next = cfg->m0_stack_size;  // Reserve from pool

    for (uint64_t off = 0; off < cfg->m0_stack_size; off += 4096) {
        uint64_t va = cfg->m0_stack_base + off;
        uint64_t pa = stack_pa + off;
        map_page(va, pa, AP_EL1_RW);  // Pre-map stack pages
    }

    // Store config at known address
    memcpy((void*)RUNTIME_CONFIG_ADDR, cfg, sizeof(*cfg));
}
```

### doMmap Implementation with Demand Paging

The `doMmap()` function (called from both `ksyscall6` and `handleSVC`) uses demand paging
during bootstrap. It returns a virtual address immediately; physical pages are allocated
on first access via the page fault handler:

```go
// Region tracking for demand paging - simple array, no allocation needed
var mmapRegions [64]struct {
    base  uint64
    size  uint64
    valid bool
}
var mmapRegionCount int

// doMmap - the real mmap implementation
// Called from: ksyscall6 (kernel self-call) or handleSVC (user SVC)
//
//go:nosplit
func doMmap(addr, length, prot, flags, fd, offset uintptr) uintptr {
    if bootstrapComplete {
        // Full implementation: allocate and map pages
        return allocateAndMapPages(addr, length, prot, flags, fd, offset)
    }

    // Bootstrap mode: allocate from kernel VA range (demand paged)
    cfg := getCardinalConfig()

    length = (length + 4095) &^ 4095  // Page align

    // Atomically allocate VA range (not physical pages!)
    va := atomic.AddUint64(&cfg.kernel_va_next, uint64(length)) - uint64(length)

    // Check we haven't exhausted kernel VA space
    if va+uint64(length) > cfg.kernel_va_base+KERNEL_VA_SIZE {
        throw("kernel VA space exhausted")
    }

    // Record this region as valid (for page fault handler)
    recordMmapRegion(va, uint64(length))

    // Physical pages allocated on demand when accessed
    return uintptr(va)
}

//go:nosplit
func recordMmapRegion(base, size uint64) {
    if mmapRegionCount >= len(mmapRegions) {
        throw("too many mmap regions")
    }
    r := &mmapRegions[mmapRegionCount]
    r.base = base
    r.size = size
    r.valid = true
    mmapRegionCount++
}
```

### Transition from Bootstrap to Full Kernel

Once the Go runtime is fully initialized and service goroutines are running, we flip
`bootstrapComplete = true`. After this point, the `doXxx()` functions use full
implementations instead of bootstrap stubs:

```go
func transitionToFullKernel() {
    // Service goroutines are running, channels are created
    // Now doXxx() functions can use full implementations

    bootstrapComplete = true

    // From this point:
    // - doMmap() does real page allocation
    // - doClone() creates real threads
    // - doFutex() does real wait/wake
}
```

### Kmazarin Main with Bootstrap

```go
func kernelMain() {
    // Cardinal (Phase 0) has already:
    // - Set up page tables and MMU
    // - Configured GIC
    // - Set up our stack
    // - Passed RuntimeConfig with core count, etc.

    config := getCardinalConfig()

    // Phase 1: Bootstrap mode - no channels yet
    memoryBootstrapComplete = false

    // Start service goroutines - they use bootstrap allocator
    go memoryServiceLoop()
    go schedulerServiceLoop()

    // Create channels (uses bootstrap allocator)
    memoryServiceChan = make(chan MemoryRequest, 50)
    schedulerServiceChan = make(chan SchedulerRequest, 50)
    timerTickChan = make(chan uint64, 10)
    sleepRequestChan = make(chan SleepRequest, 50)

    // Phase 2: Switch to channel mode
    memoryBootstrapComplete = true
    signalServicesToUseChannels()

    // Start timer
    go timerBridge()
    go timerService()

    // Multi-core: wake secondary cores
    if config.NumCores > 1 {
        wakeSecondaryCores(config.NumCores)
    }

    // Start user code
    startUserCode()
}
```

---

## Data Structures

### User Context

```go
// Saved on SVC entry, restored on ERET
type UserContext struct {
    Regs     [31]uint64  // X0-X30
    SP       uint64      // User stack pointer (SP_EL0)
    ELR      uint64      // Return address
    SPSR     uint64      // Saved processor state

    HandlerID int        // Which syscall handler serves this
    ThreadID  ThreadID   // User thread identifier
}
```

### Syscall Handler

```go
type SyscallHandler struct {
    ID           int
    Stack        []byte          // Kernel stack
    CurrentUser  *UserContext    // Current user being served
    State        HandlerState    // Idle, Busy
}

var syscallHandlers [NumSyscallHandlers]SyscallHandler
```

### Request Types

```go
type SyscallRequest struct {
    Type     SyscallType
    Args     [6]uint64
    From     ThreadID
    Response chan<- SyscallResponse
}

type SleepRequest struct {
    WakeTime uint64
    WakeChan chan<- uint64
    ThreadID ThreadID
}

type SchedulerRequest struct {
    Op       SchedOp  // YIELD, BLOCK, WAKE, CREATE
    ThreadID ThreadID
    Reason   BlockReason
    Response chan<- SchedulerResponse
}
```

---

## Comparison with Alternatives

### vs SMP (Symmetric Multi-Processing)

| Aspect | AMP | SMP |
|--------|-----|-----|
| Lock complexity | No locks (single owner) | Spinlocks required |
| Code simplicity | Simpler kernel | More complex |
| Syscall latency | IPI overhead | Local handling |
| Throughput | Limited by kernel core | Scales with cores |
| Go-ishness | Natural "server" pattern | Needs careful design |

### vs Traditional Monolithic

| Aspect | AMP Go-ish | Traditional |
|--------|------------|-------------|
| IPC mechanism | Go channels | Function calls |
| Synchronization | Channel semantics | Locks/semaphores |
| Subsystem isolation | Strong (goroutine ownership) | Weak |
| Debugging | Go tools work | OS-specific tools |

---

## Implementation Roadmap

### Phase 1: Single Core
1. Implement syscall handler goroutine pool
2. SVC entry/exit that switches to handlers
3. Memory service with channel API
4. Timer bridge goroutine

### Phase 2: Multi-Core
1. Add IPI-based syscall forwarding
2. User core minimal EL1 stubs
3. Cross-core channel implementation
4. Timer forwarding from user cores

### Phase 3: Optimization
1. Syscall batching
2. Per-core caching where beneficial
3. Fast-path for simple syscalls

---

## Runtime Adaptation (Avoiding Build Tags)

A key goal is having a single kernel binary that works on both single-core and multi-core
systems, adapting at runtime rather than compile time.

### Core-Awareness Levels

Not all code needs to know about core count. We categorize code by how "aware" it must be:

```
┌─────────────────────────────────────────────────────────────────────┐
│              CORE-AWARENESS SPECTRUM                                │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│   Level 0: Core-Oblivious (most code)                              │
│   ──────────────────────────────────                               │
│     - Service goroutines (memory, scheduler, timer)                 │
│     - Channel operations                                            │
│     - Request/response handlers                                     │
│     - User context save/restore                                     │
│     Code is IDENTICAL regardless of core count.                     │
│                                                                     │
│   Level 1: Build-Tag Aware (AVOID THIS)                            │
│   ──────────────────────────────────────                           │
│     Code that uses #ifdef MULTICORE or Go build tags.               │
│     This is what we want to ELIMINATE.                              │
│                                                                     │
│   Level 2: Runtime-Aware (entry points)                            │
│   ──────────────────────────────────────                           │
│     - SVC entry: direct switch vs IPI to kernel core                │
│     - Timer IRQ: local handling vs forward to kernel core           │
│     - ERET return: direct vs IPI back to user core                  │
│     - Core initialization: primary vs secondary startup             │
│     Code checks runtime config and branches accordingly.            │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Runtime Configuration

Cardinal (Phase 0) detects hardware and passes config to kmazarin:

```go
// Passed from Cardinal to kmazarin at boot
type RuntimeConfig struct {
    NumCores       uint32    // 1 for single-core, 2-N for multi-core
    KernelCore     uint32    // Core ID that runs kernel (0 for single, N-1 for multi)
    IsKernelCore   bool      // True if THIS core is the kernel core
    TimerCore      uint32    // Core that handles timer (same as KernelCore)

    // Memory layout info
    KernelStart    uintptr
    KernelEnd      uintptr
    UserStart      uintptr
    UserEnd        uintptr
}

// Global, set once at boot, never changes
var runtimeConfig RuntimeConfig
```

### Assembly Entry Point Adaptation

The key insight: assembly entry points can check a global variable and branch.
This is a few extra instructions but keeps code unified:

```asm
// SVC handler - adapts at runtime
TEXT ·svc_entry(SB), NOSPLIT, $0
    // Save minimal state
    STP     (X29, X30), [SP, #-16]!

    // Check if we're on kernel core
    MRS     X0, MPIDR_EL1               // Get current core ID
    AND     X0, X0, #0xFF
    MOVD    ·runtimeConfig+kernelCoreOffset(SB), X1
    CMP     X0, X1
    B.EQ    svc_local                   // We ARE the kernel core

    // Not kernel core - forward via IPI
    B       svc_forward_ipi

svc_local:
    // Handle locally (single-core path, or we ARE the kernel core)
    B       svc_handle_local

svc_forward_ipi:
    // Write to mailbox, send IPI, wait for response
    // ... multi-core forwarding code ...
```

### Go Function Dispatch

For Go code that needs different paths, use function variables:

```go
// Dispatch functions set at boot based on core count
var (
    returnToUserFn func(*UserContext)
    handleTimerFn  func(tick uint64)
)

func initDispatch() {
    if runtimeConfig.NumCores == 1 {
        // Single-core: direct return
        returnToUserFn = returnToUserDirect
        handleTimerFn = handleTimerLocal
    } else {
        // Multi-core: IPI-based
        returnToUserFn = returnToUserViaIPI
        handleTimerFn = handleTimerViaIPI
    }
}

// Called from syscall handlers - doesn't know or care about core count
func returnToUser(ctx *UserContext) {
    returnToUserFn(ctx)  // Dispatches to appropriate implementation
}
```

### File Organization

Structure files to make core-awareness explicit:

```
kmazarin/
├── services/          # Level 0: Core-oblivious
│   ├── memory.go      # Memory service goroutine
│   ├── scheduler.go   # Scheduler service goroutine
│   ├── timer.go       # Timer service goroutine
│   └── channels.go    # Channel definitions
│
├── syscall/           # Level 0: Core-oblivious handlers
│   ├── dispatch.go    # Syscall routing
│   ├── mmap.go        # mmap handler
│   ├── clone.go       # clone handler
│   └── futex.go       # futex handler
│
├── context/           # Level 0: Context management
│   ├── save.go        # Save user context
│   ├── restore.go     # Restore user context
│   └── context.go     # Context struct definitions
│
├── entry/             # Level 2: Runtime-aware entry points
│   ├── svc_arm64.s    # SVC entry (checks core, branches)
│   ├── irq_arm64.s    # IRQ entry (checks core, branches)
│   ├── return.go      # Return dispatch (function pointer)
│   └── dispatch.go    # Runtime dispatch setup
│
├── multicore/         # Multi-core specific (only used when NumCores > 1)
│   ├── ipi.go         # IPI send/receive
│   ├── mailbox.go     # Cross-core mailboxes
│   ├── forward.go     # Request forwarding
│   └── secondary.go   # Secondary core startup
│
└── boot/              # Bootstrap
    ├── config.go      # RuntimeConfig handling
    ├── bootstrap.go   # Phase 1 bootstrap
    └── main.go        # Kernel entry point
```

### Runtime Detection in Cardinal via DTB

Cardinal parses the Device Tree Blob (DTB) to discover hardware configuration.
QEMU passes the DTB address in X0 at boot. The DTB provides:

- **Core count**: `/cpus` node contains one child per CPU
- **Memory regions**: `/memory` node specifies available RAM
- **GIC addresses**: `/intc` node has distributor and redistributor addresses
- **Timer info**: `/timer` node has interrupt numbers

```c
// In Cardinal (Phase 0)
RuntimeConfig* detect_and_configure(void* dtb_addr) {
    RuntimeConfig* cfg = &runtime_config;

    // Parse DTB for hardware info
    int num_cpus = dtb_count_cpus(dtb_addr);
    MemoryRegion* mem = dtb_get_memory(dtb_addr);

    cfg->num_cores = num_cpus;

    // Memory layout from DTB
    cfg->ram_base = mem->base;
    cfg->ram_size = mem->size;

    // Compute memory regions
    cfg->kernel_start = KERNEL_BASE;
    cfg->kernel_end = KERNEL_BASE + KERNEL_SIZE;
    cfg->user_start = cfg->kernel_end;
    cfg->user_end = cfg->ram_base + cfg->ram_size;

    if (cfg->num_cores == 1) {
        cfg->kernel_core = 0;
        cfg->timer_core = 0;
        cfg->is_kernel_core = true;  // Only core IS kernel core
    } else {
        // Last core is kernel core (convention)
        cfg->kernel_core = cfg->num_cores - 1;
        cfg->timer_core = cfg->kernel_core;

        // Only set is_kernel_core for the actual kernel core
        // Secondary cores will check MPIDR at startup
    }

    // Configure GIC based on DTB addresses
    GICInfo* gic = dtb_get_gic(dtb_addr);
    configure_gic(cfg, gic);

    // Route timer only to kernel core
    route_timer_interrupt(cfg->timer_core);

    return cfg;
}

// DTB parsing helpers
int dtb_count_cpus(void* dtb) {
    // Navigate to /cpus node
    // Count child nodes (cpu@0, cpu@1, etc.)
    FdtNode* cpus = fdt_find_node(dtb, "/cpus");
    int count = 0;
    for (FdtNode* child = cpus->first_child; child; child = child->sibling) {
        if (strncmp(child->name, "cpu@", 4) == 0) {
            count++;
        }
    }
    return count;
}

MemoryRegion* dtb_get_memory(void* dtb) {
    // Parse /memory node's "reg" property
    // Returns base address and size
    FdtNode* mem = fdt_find_node(dtb, "/memory");
    FdtProp* reg = fdt_get_prop(mem, "reg");
    // reg contains: <base_addr_hi base_addr_lo size_hi size_lo>
    // (cell sizes depend on #address-cells and #size-cells)
    return parse_reg_property(reg);
}
```

### DTB Structure (QEMU virt machine)

```
/dts-v1/;

/ {
    #address-cells = <2>;
    #size-cells = <2>;

    cpus {
        #address-cells = <1>;
        #size-cells = <0>;

        cpu@0 {
            device_type = "cpu";
            compatible = "arm,cortex-a72";
            reg = <0x0>;
            enable-method = "psci";
        };
        cpu@1 { ... };
        cpu@2 { ... };
        cpu@3 { ... };
    };

    memory@40000000 {
        device_type = "memory";
        reg = <0x0 0x40000000 0x0 0x40000000>;  // 1GB at 0x40000000
    };

    intc@8000000 {
        compatible = "arm,gic-v3";
        reg = <0x0 0x8000000 0x0 0x10000>,      // Distributor
              <0x0 0x80a0000 0x0 0xf60000>;     // Redistributors
        ...
    };

    timer {
        compatible = "arm,armv8-timer";
        interrupts = <1 13 4>,  // Secure phys
                     <1 14 4>,  // Non-secure phys
                     <1 11 4>,  // Virtual
                     <1 10 4>;  // Hypervisor
    };
};
```

### Cardinal Changes Needed for AMP

Current Cardinal capabilities (already implemented):
- ✓ MMU and page table setup
- ✓ GIC initialization
- ✓ ELF loading for kmazarin
- ✓ Stack setup
- ✓ Jump to kmazarin entry

Changes needed for AMP support:

1. **DTB Parsing** (new)
   - Parse `/cpus` to get core count
   - Parse `/memory` to get RAM base and size
   - Parse `/intc` for GIC addresses (currently hardcoded)

2. **RuntimeConfig Handoff** (new)
   - Populate RuntimeConfig struct
   - Pass to kmazarin (via register or known memory location)

3. **Timer Routing** (modify)
   - Route timer IRQ only to kernel core (core N-1)
   - Disable timer on user cores in GIC

4. **Secondary Core Handling** (new, multi-core only)
   - Hold secondary cores in WFI loop
   - Provide wake address for kmazarin to release them later

### Testing Both Paths

With runtime adaptation, we can test both paths on any hardware:

```go
// For testing: override core detection
func TestMultiCorePath(t *testing.T) {
    // Save real config
    saved := runtimeConfig
    defer func() { runtimeConfig = saved }()

    // Pretend we're multi-core
    runtimeConfig.NumCores = 4
    runtimeConfig.KernelCore = 3
    runtimeConfig.IsKernelCore = false
    initDispatch()

    // Test multi-core code paths on single-core hardware
    // ...
}
```

### Benefits Over Build Tags

| Build Tags | Runtime Adaptation |
|------------|-------------------|
| Two binaries | One binary |
| Can't test other path | Can test both paths |
| Compile-time decision | Runtime decision |
| `//go:build multicore` everywhere | Config check in entry points |
| Easy to diverge | Forces unified design |

---

## Open Questions

1. ~~**Timer interrupt handling**: How to minimize latency for time-sensitive operations?~~ *(Addressed above)*
2. ~~**Kernel self-calls**: How does the Go runtime bootstrap before channels exist?~~ *(Addressed above)*
3. ~~**Core awareness**: How much code needs to know about single vs multi-core?~~ *(Addressed above)*

Remaining questions:

1. **IPI latency**: What is the actual overhead of cross-core IPI for syscalls? Need benchmarks.
2. **Channel buffer sizes**: How large should service channels be for optimal throughput?
3. **Syscall handler pool size**: How many dedicated syscall handlers do we need?
