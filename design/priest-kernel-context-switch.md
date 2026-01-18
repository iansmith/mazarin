# Plan: Priest ↔ Kernel Context Switch

## Overview

This document details how priest (the userspace syscall router) context switches to kmazarin (the kernel) when handling syscalls, and the overall process/program model for Mazzy userspace.

## Terminology

| Term | Definition |
|------|------------|
| **Priest** | A "process" in the Unix sense. Has its own Go runtime, memory space, scheduler. |
| **Thread** | A kernel-scheduled execution context within a priest. Like an OS thread (M in Go runtime). |
| **Thin** | A "thin client" program within a priest. Becomes goroutines scheduled by priest's runtime. |
| **PCB** | Priest Control Block - kernel's bookkeeping for a priest |
| **KThread** | Kernel Thread - kernel's bookkeeping for a schedulable thread |
| **ThinCB** | Thin Control Block - kernel's bookkeeping for a thin client program |

### Scheduling Hierarchy

```
┌─────────────────────────────────────────────────────────────────┐
│                         KERNEL                                   │
│  Schedules: KThreads (OS-level threads)                         │
│  On preemption: may switch to thread in DIFFERENT priest        │
│  If different priest: must switch page tables                   │
└─────────────────────────────────────────────────────────────────┘
                              │
          ┌───────────────────┼───────────────────┐
          ▼                   ▼                   ▼
    ┌───────────┐       ┌───────────┐       ┌───────────┐
    │  Priest 1 │       │  Priest 2 │       │  Priest 3 │
    │  (PCB)    │       │  (PCB)    │       │  (PCB)    │
    └───────────┘       └───────────┘       └───────────┘
          │                   │
    ┌─────┼─────┐       ┌─────┼─────┐
    ▼     ▼     ▼       ▼     ▼     ▼
  ┌───┐ ┌───┐ ┌───┐   ┌───┐ ┌───┐ ┌───┐
  │KT1│ │KT2│ │KT3│   │KT1│ │KT2│ │KT3│    ← KThreads (kernel schedules these)
  └───┘ └───┘ └───┘   └───┘ └───┘ └───┘
    │     │     │       │     │     │
    └─────┴─────┘       └─────┴─────┘
          │                   │
          ▼                   ▼
    ┌───────────┐       ┌───────────┐
    │ Go Runtime│       │ Go Runtime│         ← Each priest has own runtime
    │ Scheduler │       │ Scheduler │
    └───────────┘       └───────────┘
          │                   │
    ┌─────┴─────┐       ┌─────┴─────┐
    ▼     ▼     ▼       ▼     ▼     ▼
  ┌───┐ ┌───┐ ┌───┐   ┌───┐ ┌───┐ ┌───┐
  │ G │ │ G │ │ G │   │ G │ │ G │ │ G │    ← Goroutines (runtime schedules these)
  └───┘ └───┘ └───┘   └───┘ └───┘ └───┘
    │           │             │
    ▼           ▼             ▼
  ┌─────┐    ┌─────┐       ┌─────┐
  │Thin1│    │Thin2│       │Thin1│           ← Thin programs (run as goroutines)
  └─────┘    └─────┘       └─────┘
```

**Key insight:** The kernel schedules KThreads. When preempting KThread A (priest 1) to run KThread B (priest 2), the kernel must switch page tables because they're in different address spaces.

## Process/Thin Hierarchy

```
┌──────────────────────────────────────────────────────────────────┐
│                            Kernel                                 │
│  ┌─────────────────┐  ┌─────────────────┐                        │
│  │ PCB             │  │ PCB             │  ...                   │
│  │ (ptr: 0x42...)  │  │ (ptr: 0x42...)  │                        │
│  │ LinuxPID=1      │  │ LinuxPID=2      │                        │
│  └─────────────────┘  └─────────────────┘                        │
│          │                    │                                   │
└──────────┼────────────────────┼───────────────────────────────────┘
           │                    │
      ┌────┴────┐          ┌────┴────┐
      ▼         ▼          ▼         ▼
  ┌───────┐ ┌───────┐  ┌───────┐ ┌───────┐
  │ThinCB │ │ThinCB │  │ThinCB │ │ThinCB │
  │ptr:.. │ │ptr:.. │  │ptr:.. │ │ptr:.. │
  │LThinID│ │LThinID│  │LThinID│ │LThinID│
  │  =1   │ │  =2   │  │  =1   │ │  =2   │
  └───────┘ └───────┘  └───────┘ └───────┘
  helloworld   foo       bar       baz

Native Mazzy API returns 64-bit token (pointer to control block):
  Launch() → 0x4200_1000 (pointer to PCB)
  Run()    → 0x4200_5000 (pointer to ThinCB)

Linux Emulation PID (16-bit, from counters in control blocks):
  Priest 1 alone:     LinuxPID << 8 = 0x0100
  Priest 1, thin 1:   (LinuxPID << 8) | LinuxThinID = 0x0101
  Priest 1, thin 2:   0x0102
  Priest 2 alone:     0x0200
  Priest 2, thin 1:   0x0201
```

## Kernel Data Structures

The kernel uses `util.DLinkedList[T]` for managing PCBs and TCBs. This provides:
- O(1) insertion and removal
- Each node has a back-pointer to its list for validation
- Once removed from the list, Go's GC handles struct cleanup

### Global Lists

```go
// In kthread/process.go

import "kmazarin/util"

// Global priest list - all active priests
var allPriests = util.NewDLinkedList[*PCB]()

// Current execution context (set during syscall entry)
var currentPCB *PCB
var currentThinCB *ThinCB  // nil if priest is running without a thin (set by Run syscall)
```

### Tokens vs PIDs: Two Return Value Models

Kmazarin supports two return value models for process/thread creation:

**Native Mazzy API (64-bit tokens):**

Native syscalls return 64-bit tokens that are simply pointers to kernel data structures:

```go
// Launch returns *PCB as uint64
pcb := (*PCB)(unsafe.Pointer(uintptr(SyscallLaunch(...))))

// Run returns *ThinCB as uint64
thin := (*ThinCB)(unsafe.Pointer(uintptr(SyscallRun(...))))

// CreateThread returns *KThread as uint64
kt := (*KThread)(unsafe.Pointer(uintptr(SyscallCreateThread(...))))
```

These tokens are opaque to userspace but can be passed back to syscalls for O(1) lookup.

**Linux Emulation (16-bit PIDs):**

When emulating Linux syscalls (fork, clone, getpid, etc.), we return small integers:

```go
// Linux PID format (16-bit):
//   Priest alone:  priest_number << 8        (e.g., 0x0100 for priest 1)
//   Thin program:  (priest_number << 8) | thin_number  (e.g., 0x0103)
//
// These are COUNTERS stored in control blocks, NOT pointers.
```

**Counter Management:**

Each control block stores a small counter used only for Linux emulation:

- **Priest counter**: 1-127, stored in `PCB.LinuxPID`
  - Counter resets to 1 when reaching 128, provided fewer than 127 priests are active
  - If 127 priests active, Launch fails with EAGAIN

- **Thin counter**: 1-255 per priest, stored in `ThinCB.LinuxThinID`
  - Counter resets to 1 when reaching 256, provided fewer than 255 thins active in that priest
  - If 255 thins active, Run fails with EAGAIN

**Determining Current PID (Linux Emulation):**

When a Linux syscall like `getpid()` is invoked:

1. Determine calling context (may require examining the `g` of the calling goroutine)
2. If running in priest code (not inside a thin): return `priest.LinuxPID << 8`
3. If running inside a thin: return `(priest.LinuxPID << 8) | thin.LinuxThinID`

```go
func SyscallGetPid() int64 {
    pcb := GetCurrentPCB()
    thin := GetCurrentThinCB()  // May need g inspection

    if thin == nil {
        // Running in priest, not a thin
        return int64(pcb.LinuxPID) << 8
    }
    // Running inside a thin
    return int64(pcb.LinuxPID)<<8 | int64(thin.LinuxThinID)
}
```

**ASID (Address Space ID) Notes:**

ARM64 ASID width is implementation-defined:
- **8-bit ASID**: TCR_EL1.AS = 0, gives 256 ASIDs
- **16-bit ASID**: TCR_EL1.AS = 1, gives 65536 ASIDs

**ASID = LinuxPID:** For simplicity, each priest's ASID is set to its `LinuxPID` value. This provides a direct mapping and avoids maintaining a separate ASID allocation table.

**Cardinal configuration (enable 16-bit ASIDs):**

In `src/cardinal/boot/mmu_init_arm64.s`, when setting up TCR_EL1, set the AS bit:

```asm
// In cardinal's MMU initialization (src/cardinal/boot/mmu_init_arm64.s)
// Look for TCR_EL1 configuration, around the section that sets up translation control

// Current code likely has something like:
//   LDR X0, =TCR_VALUE
//   MSR TCR_EL1, X0

// Add AS bit (bit 36) to enable 16-bit ASIDs:
//   TCR_EL1.AS = 1 (bit 36)
//
// Example: if current TCR value is 0x00000000_1B5503510:
//   New value with AS=1:  0x00000010_1B5503510  (set bit 36)

#define TCR_AS_16BIT    (1 << 36)   // Enable 16-bit ASIDs

// Or in Go constants (src/cardinal/mmu/tcr.go):
const TCR_AS_16BIT = 1 << 36
```

First verify CPU supports 16-bit ASIDs by checking `ID_AA64MMFR0_EL1.ASIDBits`:
- 0b0000: Only 8-bit ASID supported
- 0b0010: 16-bit ASID supported

```asm
// Check ASID support (in cardinal boot)
MRS X0, ID_AA64MMFR0_EL1
UBFX X1, X0, #4, #4        // Extract ASIDBits field (bits 7:4)
CMP X1, #2
B.NE asid_8bit_only        // Fall back to 8-bit if not supported
// Enable 16-bit ASIDs in TCR_EL1
```

Since we limit priests to 127 (`LinuxPID` 1-127), even 8-bit ASIDs (256 values) would suffice, but 16-bit gives headroom for future expansion.

### Priest Control Block (PCB)

```go
type PCB struct {
    // Synchronization
    lock        sync.Mutex      // Protects mutable PCB fields

    // Identity
    LinuxPID    uint8           // 1-127, counter for Linux emulation only (NOT a pointer)
    State       PriestState     // Running, Blocked, Zombie

    // List membership - for O(1) removal
    listNode    *util.DNode[*PCB]  // Our node in allPriests list

    // Memory layout
    EntryPoint  uint64          // ELF entry point
    LoadBase    uint64          // Base address where loaded
    HeapStart   uint64          // Start of heap region
    HeapEnd     uint64          // Current heap break

    // Symbol table (for patching thins and event delivery)
    Symbols     map[string]uint64  // symbol name → virtual address

    // Virtual memory
    PageTableRoot uint64        // Root of page table tree
    MappedPages   []PageMapping // All mapped pages for cleanup

    // Thin clients within this priest
    Thins           *util.DLinkedList[*ThinCB]
    NextLinuxThinID uint8  // Counter for Linux emulation (1-255, wraps with reuse check)

    // Kernel threads belonging to this priest (at least 3 per priest)
    Threads     *util.DLinkedList[*KThread]

    // Async event delivery (addresses from ELF symbol table at load time)
    AsyncEventHandler   uint64  // Address of "AsyncEventHandler" symbol
    AsyncEventTrampoline uint64 // Address of "asyncEventReturn" symbol

    // Event queue (kernel-side ring buffer)
    eventLock         sync.Mutex      // Protects EventQueue access
    EventQueue        [16]AsyncEvent  // Fixed-size event buffer
    EventQueueHead    uint8           // Next slot to write
    EventQueueTail    uint8           // Next slot to read
    EventsInFlight    uint32          // Atomic: 0 = false, 1 = true (for multi-core safety)

    // Saved context for event delivery
    SavedELR          uint64  // Original return address before event injection
    SavedX0           uint64  // Original X0 value
    SavedX1           uint64  // Original X1 value

    // Scheduler affinity counter
    // Decremented each tick. When > 0, scheduler prefers threads from this priest.
    // Reset to PriestAffinityTicks when scheduling a thread from a different priest.
    AffinityCounter   int32
}

type PriestState int32
const (
    PriestRunning PriestState = 1
    PriestReady   PriestState = 2
    PriestBlocked PriestState = 3
    PriestZombie  PriestState = 4
)
```

### Thin Control Block (ThinCB)

```go
type ThinCB struct {
    // Identity
    LinuxThinID uint8           // 1-255, counter for Linux emulation only (NOT a pointer)
    Priest      *PCB            // Back-pointer to owning priest
    State       ThinState

    // List membership - for O(1) removal from priest's thin list
    listNode    *util.DNode[*ThinCB]

    // Memory layout (thin's code/data mapped into priest's address space)
    EntryPoint  uint64          // Thin's _start or main
    LoadBase    uint64          // Base address where thin is loaded
    StackBase   uint64          // Bottom of stack (low address)
    StackTop    uint64          // Top of stack (high address, initial SP)

    // Mapped pages (for cleanup on exit)
    // These are pages specific to this thin, not shared with priest
    MappedPages []PageMapping

    // Exit status
    ExitCode    int32
}

type ThinState int32
const (
    ThinRunning ThinState = 1
    ThinReady   ThinState = 2
    ThinZombie  ThinState = 3
)
```

### Kernel Thread (KThread)

The kernel schedules KThreads. Each KThread belongs to exactly one priest. Multiple KThreads from the same priest can run simultaneously on different CPUs.

```go
type KThread struct {
    // Identity
    TID         TID             // Thread ID (monotonic counter, returned to userspace)
    Priest      *PCB            // Owning priest (backpointer for page table on context switch)
    State       KThreadState

    // List memberships
    priestNode  *util.DNode[*KThread]  // Node in priest's Threads list
    schedNode   *util.DNode[*KThread]  // Node in scheduler's run queue

    // Saved CPU context (when not running)
    Context     ThreadContext   // SP, PC, X0-X30, etc.

    // Stack for this thread (in priest's address space)
    StackBase   uint64          // Bottom of stack (low address)
    StackTop    uint64          // Top of stack (high address)

    // CPU affinity (optional, for future use)
    CurrentCPU  int32           // CPU this thread is running on, -1 if not running
    AffinityMask uint64         // Which CPUs this thread can run on

    // Scheduling
    Priority    int32           // Scheduling priority
    TimeSlice   int32           // Remaining time slice in ticks (negative = blocked signal)
    IsKernelThread bool         // True if this is a kernel-internal thread (never preempted for user threads)
}

type KThreadState int32
const (
    KThreadRunning  KThreadState = 1  // Currently executing on a CPU
    KThreadReady    KThreadState = 2  // Ready to run, in run queue
    KThreadBlocked  KThreadState = 3  // Waiting for something (I/O, lock, etc.)
    KThreadZombie   KThreadState = 4  // Exited, waiting to be reaped
)

// Scheduling constants
const (
    // SignalTicksLeft indicates a thread is blocked on a syscall
    // Using -130 as a distinctive negative value that won't be confused with small negatives
    SignalTicksLeft int32 = -130

    // Timer tick period (from kirq/preempt.go)
    TickPeriodMs = 20  // Timer IRQ fires every 20ms

    // ThreadTimeSliceTicks: number of ticks before a thread is preempted
    // 100ms / 20ms = 5 ticks
    ThreadTimeSliceTicks int32 = 5

    // PriestAffinityTicks: number of ticks to prefer scheduling threads from same priest
    // 300ms / 20ms = 15 ticks
    PriestAffinityTicks int32 = 15
)
```

### Scheduler Data Structures

```go
// Per-CPU scheduler state
type CPUSchedState struct {
    CurrentThread *KThread      // Thread currently running on this CPU
    IdleThread    *KThread      // Idle thread for this CPU (runs when nothing else to do)
}

// Global scheduler state
var (
    cpuState      [MAX_CPUS]CPUSchedState
    runQueue      *util.DLinkedList[*KThread]  // Threads ready to run
    runQueueLock  Spinlock                      // Protects runQueue
)

// GetCurrentKThread returns the thread running on the current CPU
func GetCurrentKThread() *KThread {
    cpuID := getCurrentCPU()
    return cpuState[cpuID].CurrentThread
}

// GetCurrentPCB returns the priest of the current thread
func GetCurrentPCB() *PCB {
    kt := GetCurrentKThread()
    if kt == nil {
        return nil  // Kernel context, no priest
    }
    return kt.Priest
}
```

### Management APIs

```go
// CreatePCB allocates a new PCB and adds it to the global list
func CreatePCB() *PCB {
    pcb := &PCB{
        State:           PriestReady,
        Thins:           util.NewDLinkedList[*ThinCB](),
        Threads:         util.NewDLinkedList[*KThread](),
        NextLinuxThinID: 1,  // Start thin counter at 1
    }

    // Allocate Linux PID for emulation (1-127, with wraparound)
    allocateLinuxPID(pcb)  // Sets pcb.LinuxPID

    allPriestsLock.Lock()
    pcb.listNode = allPriests.PushBack(pcb)
    allPriestsLock.Unlock()

    return pcb
}

// CreateKThread creates a new kernel thread belonging to the given priest
func CreateKThread(priest *PCB, entryPoint uint64, stackSize uint64) *KThread {
    kt := &KThread{
        TID:     allocateTID(kt),  // Global atomic counter
        Priest:       priest,
        State:        KThreadReady,
        CurrentCPU:   -1,
        AffinityMask: ^uint64(0),  // Can run on any CPU
        Priority:     0,
        TimeSlice:    ThreadTimeSliceTicks,
    }

    // Allocate stack in priest's address space
    stackPages := allocPhysicalPages(stackSize / PAGE_SIZE)
    kt.StackBase = findFreeVirtualRange(priest, stackSize)
    kt.StackTop = kt.StackBase + stackSize
    mapPages(priest.PageTableRoot, kt.StackBase, stackPages, PF_R|PF_W)

    // Set up initial context
    kt.Context.PC = entryPoint
    kt.Context.SP = kt.StackTop

    // Add to priest's thread list
    priest.lock.Lock()
    kt.priestNode = priest.Threads.PushBack(kt)
    priest.lock.Unlock()

    // Add to scheduler's run queue
    runQueueLock.Lock()
    kt.schedNode = runQueue.PushBack(kt)
    runQueueLock.Unlock()

    return kt
}

// allocateLinuxThinID assigns a Linux-style thin ID within a priest (1-255)
func allocateLinuxThinID(priest *PCB) uint8 {
    // Called with priest.lock held
    startID := priest.NextLinuxThinID
    for {
        // Check if this ID is in use
        inUse := false
        for node := priest.Thins.Front(); node != nil; node = node.Next() {
            if node.Value().LinuxThinID == priest.NextLinuxThinID {
                inUse = true
                break
            }
        }
        if !inUse {
            id := priest.NextLinuxThinID
            priest.NextLinuxThinID++
            if priest.NextLinuxThinID == 0 {
                priest.NextLinuxThinID = 1  // Wrap, skip 0
            }
            return id
        }
        priest.NextLinuxThinID++
        if priest.NextLinuxThinID == 0 {
            priest.NextLinuxThinID = 1
        }
        if priest.NextLinuxThinID == startID {
            panic("no available Linux thin IDs (255 thins active)")
        }
    }
}

// CreateThinCB allocates a new ThinCB and adds it to the priest's thin list
func CreateThinCB(priest *PCB) *ThinCB {
    priest.lock.Lock()
    defer priest.lock.Unlock()

    thin := &ThinCB{
        LinuxThinID: allocateLinuxThinID(priest),
        Priest:      priest,
        State:       ThinReady,
    }

    thin.listNode = priest.Thins.PushBack(thin)
    return thin
}

// ReleaseResourcesThinCB cleans up thin resources and removes from priest's list
func ReleaseResourcesThinCB(thin *ThinCB) {
    priest := thin.Priest

    // 1. Unmap all pages specific to this thin
    for _, mapping := range thin.MappedPages {
        unmapPage(priest.PageTableRoot, mapping.VirtAddr)
        freePhysicalPage(mapping.PhysAddr)
    }
    thin.MappedPages = nil

    // 2. Remove from priest's thin list
    priest.lock.Lock()
    priest.Thins.Remove(thin.listNode)
    priest.lock.Unlock()
    thin.listNode = nil

    // 3. Clear back-pointer
    thin.Priest = nil
}

// ReleaseResourcesKThread cleans up thread resources
func ReleaseResourcesKThread(kt *KThread) {
    priest := kt.Priest

    // 1. Remove from run queue (if present)
    runQueueLock.Lock()
    if kt.schedNode != nil {
        runQueue.Remove(kt.schedNode)
        kt.schedNode = nil
    }
    runQueueLock.Unlock()

    // 2. Free stack pages
    unmapRange(priest.PageTableRoot, kt.StackBase, kt.StackTop-kt.StackBase)

    // 3. Remove from priest's thread list
    priest.lock.Lock()
    priest.Threads.Remove(kt.priestNode)
    priest.lock.Unlock()
    kt.priestNode = nil

    kt.Priest = nil
}

// ReleaseResourcesPCB cleans up priest resources and removes from global list
// Must be called AFTER all thins and threads have been cleaned up
func ReleaseResourcesPCB(pcb *PCB) {
    // 1. Ensure all threads are cleaned up first
    if !pcb.Threads.IsEmpty() {
        panic("ReleaseResourcesPCB: threads still exist")
    }

    // 2. Ensure all thins are cleaned up
    if !pcb.Thins.IsEmpty() {
        panic("ReleaseResourcesPCB: thins still exist")
    }

    // 3. Unmap all priest pages
    for _, mapping := range pcb.MappedPages {
        unmapPage(pcb.PageTableRoot, mapping.VirtAddr)
        freePhysicalPage(mapping.PhysAddr)
    }
    pcb.MappedPages = nil

    // 4. Free page tables
    freePageTables(pcb.PageTableRoot)

    // 5. Remove from global priest list
    allPriestsLock.Lock()
    allPriests.Remove(pcb.listNode)
    allPriestsLock.Unlock()
    pcb.listNode = nil
}
```

### Cleanup Sequences

**Thin Exit (normal or crash):**
```
1. Mark thin as Zombie (thin.State = ThinZombie)
2. Record exit code (thin.ExitCode = code)
3. Queue async event to priest (EventThinExited)
4. Context switch to priest's event handler
5. Priest calls Reap(thin_id) syscall
6. Kernel calls ReleaseResourcesThinCB(thin)
   - Unmaps thin's pages
   - Removes from priest.Thins list
   - GC collects ThinCB
```

**Thread Exit:**
```
1. Thread calls ExitThread() or crashes
2. Remove thread from run queue
3. Mark thread as Zombie (kt.State = KThreadZombie)
4. If this was the last thread in priest, trigger priest exit
5. Otherwise, kernel calls ReleaseResourcesKThread(kt)
   - Frees thread stack
   - Removes from priest.Threads
   - GC collects KThread
```

**Priest Exit (normal or crash):**
```
1. For each thread in priest.Threads:
   a. Mark thread as Zombie
   b. Remove from run queue
   c. ReleaseResourcesKThread(kt)
2. For each thin in priest.Thins:
   a. Mark thin as Zombie
   b. ReleaseResourcesThinCB(thin)
3. Mark priest as Zombie (pcb.State = PriestZombie)
4. If parent priest exists, queue EventPriestExited
5. Parent calls ReapPriest(priest_id)
6. Kernel calls ReleaseResourcesPCB(pcb)
   - Unmaps priest's pages
   - Frees page tables
   - Removes from allPriests
   - GC collects PCB
```

## Concurrency and Locking

### Multi-Core Considerations

On a multi-core system, multiple CPUs can execute kernel code simultaneously. This creates race conditions when accessing shared data structures (PCB, KThread, ThinCB, global lists).

**Key scenarios:**
1. Two CPUs handling syscalls for different priests - access to `allPriests` list
2. Two CPUs handling syscalls for same priest (multiple threads) - access to same PCB
3. One CPU creating/destroying KThread/ThinCB while another iterates lists
4. Event injection racing with syscall handling on same PCB
5. Multiple threads from same priest making concurrent syscalls

### Locking Strategy

```go
// Global lock for allPriests list
var allPriestsLock sync.Mutex

// Per-PCB lock for priest state and its thins list
type PCB struct {
    lock sync.Mutex  // Protects all mutable PCB fields

    // ... other fields
}

// Per-KThread lock for thread-specific state
type KThread struct {
    lock sync.Mutex  // Protects mutable KThread fields

    // ... other fields
}
```

### Lock Ordering (to prevent deadlock)

Always acquire locks in this order:
```
1. allPriestsLock (global)
2. runQueueLock (global scheduler)
3. pcb.lock (specific priest)
4. kt.lock (specific thread, if needed)
```

**Example - Creating a new priest:**
```go
func CreatePCB() *PCB {
    pcb := &PCB{
        PriestID: allocateLinuxPID(pcb),  // Must be atomic or locked
        State:    PriestReady,
        Thins:    util.NewDLinkedList[*ThinCB](),
        Threads:  util.NewDLinkedList[*KThread](),
    }

    allPriestsLock.Lock()
    pcb.listNode = allPriests.PushBack(pcb)
    allPriestsLock.Unlock()

    return pcb
}
```

**Example - Creating a KThread:**
```go
func CreateKThread(pcb *PCB, entry uint64, stackSize uint64) *KThread {
    kt := &KThread{
        TID: allocateTID(kt),  // Global atomic counter
        Priest:   pcb,
        State:    KThreadReady,
    }

    // Allocate stack, set up context...

    pcb.lock.Lock()
    kt.priestNode = pcb.Threads.PushBack(kt)
    pcb.lock.Unlock()

    runQueueLock.Lock()
    kt.schedNode = runQueue.PushBack(kt)
    runQueueLock.Unlock()

    return kt
}
```

### EventsInFlight Must Be Atomic

On multi-core, the `EventsInFlight` flag and event queue operations need atomic access:

```go
type PCB struct {
    // ...

    // Event queue - protected by eventLock
    eventLock     sync.Mutex
    EventQueue    [16]AsyncEvent
    EventQueueHead uint8
    EventQueueTail uint8
    EventsInFlight uint32  // Use atomic operations: 0 = false, 1 = true

    // Saved context - only accessed when EventsInFlight is set
    // (so protected by the "logical lock" of EventsInFlight)
    SavedELR uint64
    SavedX0  uint64
    SavedX1  uint64
}

func QueueAsyncEvent(pcb *PCB, eventType uint32, eventData uint64) {
    pcb.eventLock.Lock()
    defer pcb.eventLock.Unlock()

    nextHead := (pcb.EventQueueHead + 1) % 16
    if nextHead == pcb.EventQueueTail {
        panic("AsyncEvent queue full")
    }
    pcb.EventQueue[pcb.EventQueueHead] = AsyncEvent{Type: eventType, Data: eventData}
    pcb.EventQueueHead = nextHead
}

func MaybeInjectEvent(pcb *PCB, frame *ExceptionFrame) {
    // Try to atomically set EventsInFlight from 0 to 1
    if !atomic.CompareAndSwapUint32(&pcb.EventsInFlight, 0, 1) {
        // Already handling an event, don't inject
        return
    }

    pcb.eventLock.Lock()
    if pcb.EventQueueHead == pcb.EventQueueTail {
        // No events pending - release the flag
        pcb.eventLock.Unlock()
        atomic.StoreUint32(&pcb.EventsInFlight, 0)
        return
    }

    event := pcb.EventQueue[pcb.EventQueueTail]
    pcb.EventQueueTail = (pcb.EventQueueTail + 1) % 16
    pcb.eventLock.Unlock()

    // Save context and inject (EventsInFlight is already set)
    pcb.SavedELR = frame.ELR_EL1
    pcb.SavedX0 = frame.X0
    pcb.SavedX1 = frame.X1

    frame.ELR_EL1 = pcb.AsyncEventHandler
    frame.X0 = uint64(event.Type)
    frame.X1 = event.Data
    frame.X30 = pcb.AsyncEventTrampoline
}

func SyscallEventHandled(frame *ExceptionFrame) {
    pcb := GetCurrentPCB()

    // Restore context
    frame.ELR_EL1 = pcb.SavedELR
    frame.X0 = pcb.SavedX0
    frame.X1 = pcb.SavedX1

    // Clear flag - allows next event
    atomic.StoreUint32(&pcb.EventsInFlight, 0)

    // Try to inject next event (will re-acquire flag if successful)
    MaybeInjectEvent(pcb, frame)
}
```

### Interrupt Masking vs Locks

**Disabling interrupts** (via `MSR DAIFSet, #0xF` on ARM64) only affects the current CPU:
- Sufficient for single-core critical sections
- NOT sufficient for multi-core - other CPUs continue running
- Still useful to prevent preemption during lock-held sections

**Spinlocks** are needed for multi-core:
- Busy-wait until lock is available
- Should disable interrupts while holding to prevent deadlock (interrupt handler tries to acquire same lock)

```go
type Spinlock struct {
    locked uint32
}

func (s *Spinlock) Lock() {
    // Disable interrupts on this CPU
    disableInterrupts()
    // Spin until we acquire
    for !atomic.CompareAndSwapUint32(&s.locked, 0, 1) {
        // Hint to CPU that we're spinning
        arm64Yield()
    }
}

func (s *Spinlock) Unlock() {
    atomic.StoreUint32(&s.locked, 0)
    enableInterrupts()
}
```

### Exception Handler Critical Sections

When handling exceptions (syscalls, interrupts), the kernel must be careful:

```
1. Exception entry - interrupts automatically masked by hardware (PSTATE.DAIF)
2. Save context to stack
3. Identify exception type
4. For syscalls:
   a. Re-enable interrupts (optional, for preemptible kernel)
   b. Acquire necessary locks
   c. Do work
   d. Release locks
   e. Disable interrupts
   f. MaybeInjectEvent (with proper locking as shown above)
   g. Restore context
   h. ERET
```

**Critical rule:** Never hold a lock across ERET. The lock would remain held while userspace runs.

### Single-Core Simplification (Phase 1)

For initial implementation, we can simplify by assuming single-core:

```go
// Single-core: just disable interrupts for critical sections
func withInterruptsDisabled(fn func()) {
    daif := disableInterrupts()
    fn()
    restoreInterrupts(daif)
}

// No locks needed - interrupt disabling is sufficient
func CreateThinCB(pcb *PCB) *ThinCB {
    var thin *ThinCB
    withInterruptsDisabled(func() {
        thin = &ThinCB{...}
        thin.listNode = pcb.Thins.PushBack(thin)
    })
    return thin
}
```

Mark single-core assumptions clearly in code:
```go
// TODO(multicore): Replace interrupt disabling with proper locking
```

### Priest Threading Model

Each priest starts with **one kernel thread**. The Go runtime within the priest will create additional threads as needed via the `CreateThread` syscall:

- **Main thread**: Runs `_start` → Go's `runtime.main` → user's `main()`
- **Sysmon thread**: Created by Go runtime for monitoring, GC triggers, preemption signals
- **Additional M's**: Created on demand when GOMAXPROCS > 1 or during blocking calls

**Implications:**
- Multiple KThreads from the same priest can run simultaneously on different CPUs
- Multiple CPUs can execute same priest's code simultaneously
- PCB.lock is contended when threads make concurrent syscalls
- Must handle: two threads making syscalls simultaneously
- Event injection must be per-thread, not per-priest (see below)

### Thread Scheduling and Context Switch

**Kernel Thread Exception:**

Threads marked with `IsKernelThread = true` are never preempted in favor of user threads. These are internal kernel threads that perform critical operations (GC, memory management, etc.). When all user threads are blocked, only kernel threads continue running.

```go
// Schedule picks the next thread to run on the current CPU
// Implements priest affinity scheduling:
// - If current priest's AffinityCounter > 0, prefer threads from same priest
// - If AffinityCounter == 0, look at OTHER priests first
// - If no ready threads anywhere, enter WFI idle loop
func Schedule() {
    cpuID := getCurrentCPU()
    oldThread := cpuState[cpuID].CurrentThread

    // Put old thread back in run queue (if it was running, not blocked)
    if oldThread != nil && oldThread.State == KThreadRunning {
        oldThread.State = KThreadReady
        runQueueLock.Lock()
        oldThread.schedNode = runQueue.PushBack(oldThread)
        runQueueLock.Unlock()
    }

    // Determine current priest for affinity decisions
    var currentPriest *PCB
    if oldThread != nil {
        currentPriest = oldThread.Priest
    }

    newThread := selectNextThread(currentPriest)

    if newThread == nil {
        // No ready threads - enter WFI idle loop
        cpuState[cpuID].CurrentThread = nil
        idleLoop()
        // idleLoop returns when an interrupt wakes us
        // Re-run scheduler to pick the newly-ready thread
        Schedule()
        return
    }

    // Update affinity counter if switching priests
    if currentPriest != nil && newThread.Priest != currentPriest {
        // Switching to different priest - reset its affinity counter
        newThread.Priest.AffinityCounter = PriestAffinityTicks
    }

    newThread.State = KThreadRunning
    newThread.CurrentCPU = int32(cpuID)
    newThread.TimeSlice = ThreadTimeSliceTicks  // Reset time slice
    cpuState[cpuID].CurrentThread = newThread

    // Switch context
    if oldThread != nil && oldThread.Priest != newThread.Priest {
        // Different priest - must switch page tables
        switchPageTable(newThread.Priest.PageTableRoot)
    }

    // Restore thread's saved context and return to userspace
    restoreContext(&newThread.Context)
}

// selectNextThread implements priest affinity scheduling
func selectNextThread(currentPriest *PCB) *KThread {
    runQueueLock.Lock()
    defer runQueueLock.Unlock()

    if runQueue.IsEmpty() {
        return nil
    }

    // If we have a current priest with remaining affinity, prefer its threads
    if currentPriest != nil && currentPriest.AffinityCounter > 0 {
        // First pass: look for threads from current priest
        thread := findThreadFromPriest(currentPriest)
        if thread != nil {
            return thread
        }
        // No threads from current priest - fall through to check others
    }

    // Either no current priest, affinity exhausted, or no threads from current priest
    // Look at OTHER priests first (if affinity counter is 0)
    if currentPriest != nil && currentPriest.AffinityCounter <= 0 {
        // Prefer threads from OTHER priests
        thread := findThreadNotFromPriest(currentPriest)
        if thread != nil {
            return thread
        }
        // No threads from other priests - check current priest as fallback
        thread = findThreadFromPriest(currentPriest)
        if thread != nil {
            return thread
        }
    }

    // No affinity considerations - just pick first available
    return runQueue.PopFront()
}

// findThreadFromPriest removes and returns a thread belonging to the given priest
// Returns nil if no such thread exists in the run queue
func findThreadFromPriest(priest *PCB) *KThread {
    for node := runQueue.Front(); node != nil; node = node.Next() {
        kt := node.Value()
        if kt.Priest == priest && !kt.IsKernelThread {
            runQueue.Remove(node)
            return kt
        }
    }
    return nil
}

// findThreadNotFromPriest removes and returns a thread NOT belonging to the given priest
// Returns nil if all threads belong to the given priest
func findThreadNotFromPriest(priest *PCB) *KThread {
    for node := runQueue.Front(); node != nil; node = node.Next() {
        kt := node.Value()
        if kt.Priest != priest && !kt.IsKernelThread {
            runQueue.Remove(node)
            return kt
        }
    }
    return nil
}

// idleLoop enters low-power wait state until an interrupt arrives
// This is called when there are no ready threads to run
func idleLoop() {
    // Enable interrupts so we can wake up
    enableInterrupts()

    for {
        // Check if any threads became ready (e.g., from previous interrupt)
        runQueueLock.Lock()
        hasThreads := !runQueue.IsEmpty()
        runQueueLock.Unlock()

        if hasThreads {
            return  // Exit idle loop, scheduler will pick a thread
        }

        // ARM64 Wait For Interrupt - enters low-power state
        // CPU wakes when any interrupt fires
        wfi()
    }
}

// wfi executes the ARM64 WFI instruction
//go:nosplit
func wfi() {
    // Assembly: WFI
    // Puts CPU in low-power state until interrupt
}

// Timer interrupt handler - triggers preemption
func TimerInterruptHandler() {
    cpuID := getCurrentCPU()
    kt := cpuState[cpuID].CurrentThread

    if kt == nil {
        // Idle loop - interrupt woke us up, return to check for threads
        return
    }

    // Never preempt kernel threads for user threads
    if kt.IsKernelThread {
        return
    }

    // Don't decrement if thread is blocked (TimeSlice == SignalTicksLeft)
    if kt.TimeSlice == SignalTicksLeft {
        return  // Thread is blocked, waiting for syscall completion
    }

    // Decrement priest affinity counter
    if kt.Priest != nil && kt.Priest.AffinityCounter > 0 {
        kt.Priest.AffinityCounter--
    }

    // Decrement thread time slice
    kt.TimeSlice--
    if kt.TimeSlice == 0 {
        // Save current context
        saveContext(&kt.Context)
        // Pick new thread (possibly from different priest)
        Schedule()
    }
}

// BlockCurrentThread marks the current thread as blocked
// Called when a syscall needs to wait (e.g., futex, I/O)
func BlockCurrentThread() {
    cpuID := getCurrentCPU()
    kt := cpuState[cpuID].CurrentThread
    if kt == nil {
        return
    }

    kt.State = KThreadBlocked
    kt.TimeSlice = SignalTicksLeft  // Signal that thread is blocked

    // Remove from run queue if present
    runQueueLock.Lock()
    if kt.schedNode != nil {
        runQueue.Remove(kt.schedNode)
        kt.schedNode = nil
    }
    runQueueLock.Unlock()

    // Schedule another thread
    Schedule()
}

// UnblockThread marks a thread as ready and adds it to the run queue
// Called when the condition the thread was waiting for is satisfied
func UnblockThread(kt *KThread) {
    kt.State = KThreadReady
    kt.TimeSlice = ThreadTimeSliceTicks  // Reset time slice

    runQueueLock.Lock()
    kt.schedNode = runQueue.PushBack(kt)
    runQueueLock.Unlock()
}
```

### Cross-Priest Context Switch

When switching from thread A (priest 1) to thread B (priest 2):

```
1. Save thread A's registers to A.Context
2. Set A.State = KThreadReady, add to run queue
3. Switch page table: TTBR0_EL1 = priest2.PageTableRoot
4. Invalidate TLB (or use ASID to avoid full flush)
5. Set B.State = KThreadRunning
6. Restore thread B's registers from B.Context
7. ERET to thread B's saved PC
```

**Page table switch (ARM64):**
```asm
// Switch to new page table
MSR TTBR0_EL1, X0       // X0 = new page table root
ISB                      // Instruction barrier
TLBI VMALLE1            // Invalidate all TLB entries (or use ASID)
DSB SY                   // Data synchronization barrier
ISB                      // Instruction barrier
```

### Event Delivery with Multiple Threads

With multiple threads per priest, event injection becomes more complex. We need to decide which thread receives the event.

**Option: Dedicated event thread**
- One thread in each priest is designated as the "event thread"
- Events are always injected into that thread
- Other threads continue running normally
- PCB tracks: `EventThread *KThread`

**Option: Inject into any available thread**
- On syscall return, if current thread's priest has pending events, inject
- First thread to return from syscall handles the event
- Requires per-priest "event handling in progress" flag (already have EventsInFlight)

**Recommendation:** Dedicated event thread is simpler and more predictable.

### Two-Level Preemption Model

Mazzy has two distinct preemption mechanisms operating at different levels:

**Level 1: Goroutine Preemption (within priest's Go runtime)**

The existing mechanism in `src/kmazarin/golang/kirq/preempt.go` handles preemption of goroutines within a single priest:

```
Timer IRQ (20ms) → TimerIRQHandlerAsm (assembly)
                        ↓
        Set g.preempt = true, poison g.stackguard0
                        ↓
        Next function call triggers morestack
                        ↓
        Go runtime yields goroutine to scheduler
```

If a goroutine runs 50ms without yielding (5 timer ticks), async preemption is forced by injecting `runtime.asyncPreempt()`.

**This mechanism preempts goroutines but NOT kernel threads.** The KThread continues running on its CPU; only the goroutine scheduled on that thread changes.

**Level 2: Kernel Thread Preemption (cross-priest scheduling)**

To preempt a KThread and run a different priest's thread, we need kernel-level preemption:

```
Timer IRQ (20ms) → TimerIRQHandler
                        ↓
        Check if current KThread has exhausted time slice
                        ↓
        If yes: Save full context (all regs + SP + PC)
                        ↓
        Select next KThread from run queue (may be different priest)
                        ↓
        If different priest: Switch page tables (TTBR0_EL1)
                        ↓
        Restore new thread's context, ERET
```

**Key differences:**

| Aspect | Goroutine Preemption | KThread Preemption |
|--------|---------------------|-------------------|
| What's preempted | Goroutine (G) | Kernel thread (M) |
| Scheduler | Go runtime (in priest) | Kernel (in kmazarin) |
| Page tables | Same | May change |
| Context saved | g struct, small | Full CPU registers |
| Triggered by | stackguard0 poison | Timer + time slice check |
| Existing code | `kirq/preempt.go` | **TODO: implement** |

**Implementation approach:**

The timer IRQ handler should:
1. First, handle goroutine preemption (existing code)
2. Then, check KThread time slice and trigger kernel scheduling if needed

```go
// In timer IRQ handler
func TimerIRQHandler(frame *ExceptionFrame) {
    kt := GetCurrentKThread()
    if kt == nil {
        return  // Kernel context or idle
    }

    // Level 1: Goroutine preemption (existing)
    handleGoroutinePreemption()

    // Level 2: KThread preemption (new)
    kt.TimeSlice--
    if kt.TimeSlice == 0 {
        kt.TimeSlice = ThreadTimeSliceTicks
        // Save context and reschedule
        saveContext(kt, frame)
        Schedule()  // May switch to different priest
    }
}
```

**Time slice values:**
- Goroutine async preempt: 50ms (5 × 20ms ticks) - see `kirq/preempt.go`
- KThread time slice: 100ms (ThreadTimeSliceTicks = 5 × 20ms ticks)
- Priest affinity: 300ms (PriestAffinityTicks = 15 × 20ms ticks)
- Blocked thread signal: SignalTicksLeft = -130 (distinctive negative value)

## Syscalls

### Syscall Return Value Model

**Native Mazzy syscalls** return 64-bit tokens (pointers to kernel structures):

```go
// Syscall returns are uint64 tokens (pointers)
func SyscallLaunch(...)  uint64  // Returns uintptr(*PCB)
func SyscallRun(...)     uint64  // Returns uintptr(*ThinCB)
func SyscallCreateThread(...) uint64  // Returns uintptr(*KThread)
```

These can be passed back to other syscalls for O(1) lookup without maps.

**Linux emulation syscalls** return small integers using the counters in control blocks:

```go
// Linux PID type for emulation only
type LinuxPID int32

func MakeLinuxPID(pcb *PCB, thin *ThinCB) LinuxPID {
    if thin == nil {
        // Priest alone
        return LinuxPID(pcb.LinuxPID) << 8
    }
    // Inside a thin
    return LinuxPID(pcb.LinuxPID)<<8 | LinuxPID(thin.LinuxThinID)
}

func (p LinuxPID) PriestNum() uint8 { return uint8(p >> 8) }
func (p LinuxPID) ThinNum() uint8   { return uint8(p & 0xFF) }
func (p LinuxPID) IsThin() bool     { return (p & 0xFF) != 0 }
```

**Looking up control blocks from Linux PIDs:**

For Linux emulation, we need to map from LinuxPID back to control blocks:

```go
// Global tracking for Linux emulation
var (
    linuxPIDToPCB     [128]*PCB           // LinuxPID (1-127) → PCB
    linuxPIDLock      sync.RWMutex
    nextLinuxPID      uint8 = 1           // Counter, wraps at 128
)

func allocateLinuxPID(pcb *PCB) uint8 {
    linuxPIDLock.Lock()
    defer linuxPIDLock.Unlock()

    // Find next available slot
    startID := nextLinuxPID
    for {
        if linuxPIDToPCB[nextLinuxPID] == nil {
            linuxPIDToPCB[nextLinuxPID] = pcb
            pcb.LinuxPID = nextLinuxPID
            nextLinuxPID++
            if nextLinuxPID >= 128 {
                nextLinuxPID = 1
            }
            return pcb.LinuxPID
        }
        nextLinuxPID++
        if nextLinuxPID >= 128 {
            nextLinuxPID = 1
        }
        if nextLinuxPID == startID {
            panic("no available Linux PIDs (127 priests active)")
        }
    }
}

func LookupPCBByLinuxPID(pid uint8) *PCB {
    linuxPIDLock.RLock()
    defer linuxPIDLock.RUnlock()
    if pid == 0 || pid >= 128 {
        return nil
    }
    return linuxPIDToPCB[pid]
}
```

**Thread tokens** are also pointers (for native API) but Linux emulation uses gettid() which would need similar counter logic if needed.

### Launch - Create a New Priest

```go
// Syscall number: 0x1001
// Creates a new priest from an ELF file
//
// Args:
//   filename: path to ELF file (e.g., "/priest2.elf")
//
// Returns (Native Mazzy):
//   uint64 token (pointer to PCB) on success
//   0 on failure (check errno via separate syscall)
//
// Returns (Linux emulation fork/clone):
//   LinuxPID on success (pcb.LinuxPID << 8)
//   negative errno on failure
func SyscallLaunch(filenamePtr uint64) uint64
```

**Kernel actions:**

1. **Allocate PCB**
   ```go
   pcb := CreatePCB()  // Assigns priest_id, adds to allPriests
   ```

2. **Load ELF from filesystem**
   ```go
   elfData, err := fs.ReadFile(filename)
   if err != nil {
       ReleaseResourcesPCB(pcb)
       return -ENOENT
   }
   elfFile, err := elf.Parse(elfData)
   ```

3. **Allocate page table for new address space**
   ```go
   pcb.PageTableRoot = allocatePageTable()
   ```

4. **Map ELF segments into address space**
   ```go
   for _, phdr := range elfFile.ProgramHeaders {
       if phdr.Type != PT_LOAD {
           continue
       }
       // Allocate physical pages
       pages := allocPhysicalPages(phdr.Memsz)
       // Copy segment data
       copy(pages, elfData[phdr.Offset:phdr.Offset+phdr.Filesz])
       // Zero BSS portion
       zero(pages[phdr.Filesz:phdr.Memsz])
       // Map with appropriate permissions (R/W/X from phdr.Flags)
       mapPages(pcb.PageTableRoot, phdr.Vaddr, pages, phdr.Flags)
       // Track for cleanup
       pcb.MappedPages = append(pcb.MappedPages, ...)
   }
   ```

5. **Look up required symbols from ELF symbol table**
   ```go
   // Build symbol table for later use (runtime patching, event delivery)
   pcb.Symbols = elfFile.BuildSymbolTable()

   // These symbols MUST exist in a valid priest binary
   pcb.EntryPoint = pcb.Symbols["_start"]  // or from ELF header e_entry
   pcb.AsyncEventHandler = pcb.Symbols["AsyncEventHandler"]
   pcb.AsyncEventTrampoline = pcb.Symbols["asyncEventReturn"]

   if pcb.AsyncEventHandler == 0 || pcb.AsyncEventTrampoline == 0 {
       ReleaseResourcesPCB(pcb)
       return -EINVAL  // Invalid priest binary - missing required symbols
   }

   // Validate other required symbols for thin support
   if pcb.Symbols["thin_exit_handler"] == 0 {
       ReleaseResourcesPCB(pcb)
       return -EINVAL
   }
   ```

6. **Initialize heap region**
   ```go
   // Heap starts after highest loaded segment
   pcb.HeapStart = alignUp(highestVaddr, PAGE_SIZE)
   pcb.HeapEnd = pcb.HeapStart
   ```

7. **Set up initial stack for priest**
   ```go
   stackPages := allocPhysicalPages(PRIEST_STACK_SIZE)
   stackBase := PRIEST_STACK_VADDR
   mapPages(pcb.PageTableRoot, stackBase, stackPages, PF_R|PF_W)
   pcb.Context.SP = stackBase + PRIEST_STACK_SIZE
   pcb.Context.PC = pcb.EntryPoint
   ```

8. **Create initial thread and return token**
   ```go
   // Create one initial thread - the Go runtime will create additional
   // threads as needed (typically 2 more for sysmon and other maintenance)
   CreateKThread(pcb, pcb.EntryPoint, THREAD_STACK_SIZE)

   pcb.State = PriestReady
   // Native Mazzy: return pointer as token
   return uint64(uintptr(unsafe.Pointer(pcb)))
   // Linux emulation would return: int64(pcb.LinuxPID) << 8
   ```

**Required priest symbols:**

| Symbol | Purpose |
|--------|---------|
| `_start` | Entry point (or use ELF header e_entry) |
| `AsyncEventHandler` | Called when kernel delivers async event |
| `asyncEventReturn` | Trampoline to restore context after event |
| `thin_exit_handler` | Return address pushed on thin stacks |

### Run - Create a Thin within Current Priest

```go
// Syscall number: 0x1002
// Creates a new thin client in the calling priest
//
// Args:
//   filename: path to ELF file (e.g., "/helloworld.elf")
//
// Returns (Native Mazzy):
//   uint64 token (pointer to ThinCB) on success
//   0 on failure (check errno via separate syscall)
//
// Returns (Linux emulation):
//   LinuxPID on success ((pcb.LinuxPID << 8) | thin.LinuxThinID)
//   negative errno on failure
func SyscallRun(filenamePtr uint64) uint64
```

**Kernel actions:**

1. **Identify calling priest and allocate ThinCB**
   ```go
   pcb := GetCurrentPCB()
   thin := CreateThinCB(pcb)  // Assigns thin_id, adds to pcb.Thins
   ```

2. **Load thin ELF from filesystem**
   ```go
   elfData, err := fs.ReadFile(filename)
   if err != nil {
       ReleaseResourcesThinCB(thin)
       return -ENOENT
   }
   elfFile, err := elf.Parse(elfData)
   ```

3. **Patch runtime trampolines**

   Thin binaries are built with the thin-overlay (stubbed runtime). The kernel patches
   call sites to redirect to the priest's actual runtime functions:

   ```go
   // Look up priest's runtime function addresses
   priestRuntime := map[string]uint64{
       "runtime.newobject":   pcb.Symbols["runtime.newobject"],
       "runtime.growslice":   pcb.Symbols["runtime.growslice"],
       "runtime.makechan":    pcb.Symbols["runtime.makechan"],
       // ... other runtime functions
   }

   // For each call instruction in thin's .text that targets a stubbed function,
   // patch the target address to point to priest's runtime
   for _, reloc := range elfFile.Relocations {
       if target, ok := priestRuntime[reloc.Symbol]; ok {
           patchCallTarget(thinCode, reloc.Offset, target)
       }
   }
   ```

   **Alternative: PLT-style trampolines**

   Instead of patching, the thin binary can use a PLT (Procedure Linkage Table)
   that the kernel fills in at load time:

   ```
   Thin binary:                    Priest:
   ┌──────────────┐               ┌──────────────────┐
   │ call plt[0]  │ ─────────────▶│ runtime.newobject│
   │ call plt[1]  │ ─────────────▶│ runtime.growslice│
   │ ...          │               │ ...              │
   └──────────────┘               └──────────────────┘

   PLT (in thin's address space):
   ┌────────────────────┐
   │ [0]: JMP 0x...     │  ← kernel writes priest's newobject addr
   │ [1]: JMP 0x...     │  ← kernel writes priest's growslice addr
   └────────────────────┘
   ```

4. **Map thin into priest's address space**
   ```go
   // Find free virtual address range in priest's space
   loadAddr := findFreeVirtualRange(pcb, elfFile.Size())

   for _, phdr := range elfFile.ProgramHeaders {
       if phdr.Type != PT_LOAD {
           continue
       }
       pages := allocPhysicalPages(phdr.Memsz)
       copy(pages, patchedCode[phdr.Offset:])
       mapPages(pcb.PageTableRoot, loadAddr+phdr.Vaddr, pages, phdr.Flags)
       thin.MappedPages = append(thin.MappedPages, ...)
   }

   thin.EntryPoint = loadAddr + elfFile.Entry
   ```

5. **Allocate stack and set up poison pill**
   ```go
   // Allocate 4KB initial stack
   stackPages := allocPhysicalPages(PAGE_SIZE)
   stackBase := findFreeVirtualRange(pcb, PAGE_SIZE)
   mapPages(pcb.PageTableRoot, stackBase, stackPages, PF_R|PF_W)

   thin.StackBase = stackBase
   thin.StackTop = stackBase + PAGE_SIZE

   // Set up poison pill - when main() returns, it jumps to thin_exit_handler
   // Stack layout at start:
   //   [StackTop - 8]  = thin_exit_handler address (return address)
   //   [StackTop - 16] = 0 (fake saved FP)
   //   SP = StackTop - 16

   poisonAddr := pcb.Symbols["thin_exit_handler"]
   writeToUserspace(pcb, thin.StackTop-8, poisonAddr)
   writeToUserspace(pcb, thin.StackTop-16, 0)  // Fake frame pointer
   ```

6. **Mark thin as Ready and return token**
   ```go
   thin.State = ThinReady
   // Native Mazzy: return pointer as token
   return uint64(uintptr(unsafe.Pointer(thin)))
   // Linux emulation would return: int64(pcb.LinuxPID)<<8 | int64(thin.LinuxThinID)
   ```

**Starting the thin:**

After `Run()` returns, the priest schedules the thin by calling `StartThin(thin_pid)`:

```go
// Syscall number: 0x1006
// Starts execution of a ready thin
//
// Args:
//   thinPID: PID returned by Run() (priest_id << 8 | thin_id)
//
// Returns:
//   0 on success
//   negative errno on failure
func SyscallStartThin(thinPID int64) int64
```

Kernel sets up context switch:
```go
pid := PID(thinPID)
pcb := findPCB(pid.PriestID())
if pcb == nil {
    return -ESRCH
}
thin := findThinCB(pcb, pid.ThinID())
if thin == nil || thin.State != ThinReady {
    return -EINVAL  // Not found or not ready
}

thin.State = ThinRunning
// Set up exception frame to return to thin's entry point
frame.ELR_EL1 = thin.EntryPoint
frame.SP_EL0 = thin.StackTop - 16  // After poison pill setup
// ERET will jump to thin's _start/main
```

### Process Control Syscalls

```go
// Syscall number: 0x100A
// Starts a priest (begins executing its first thread)
//
// Args:
//   priestID: PID returned by Launch() (1-255)
//
// Returns:
//   0 on success
//   negative errno on failure
func SyscallStartPriest(priestID int64) int64

// Syscall number: 0x100B
// Kills a priest or thin (non-graceful termination)
//
// Args:
//   pid: priest PID (1-255) or thin PID (priest_id << 8 | thin_id)
//
// Returns:
//   0 on success
//   negative errno on failure
func SyscallKill(pid int64) int64

// Syscall number: 0x100C
// Waits for a priest or thin to exit and retrieves exit code
//
// Args:
//   pid: priest PID or thin PID
//
// Returns:
//   exit code on success
//   negative errno on failure
func SyscallWait(pid int64) int64
```

### CreateThread - Create a New Kernel Thread

```go
// Syscall number: 0x1008
// Creates a new kernel thread in the calling priest
//
// Args:
//   entryPoint: address where thread should start executing
//   stackSize:  size of stack to allocate (in bytes, rounded up to page size)
//
// Returns:
//   tid on success (monotonic thread ID)
//   negative errno on failure
func SyscallCreateThread(entryPoint uint64, stackSize uint64) int64
```

**Kernel actions:**

```go
func SyscallCreateThread(entryPoint uint64, stackSize uint64) int64 {
    pcb := GetCurrentPCB()
    if pcb == nil {
        return -EINVAL  // Must be called from a priest context
    }

    // Validate entry point is within priest's address space
    if !isValidUserAddress(pcb, entryPoint) {
        return -EFAULT
    }

    // Round stack size up to page boundary
    stackSize = alignUp(stackSize, PAGE_SIZE)
    if stackSize < MIN_STACK_SIZE {
        stackSize = MIN_STACK_SIZE  // e.g., 16KB minimum
    }

    // Create the thread (allocates stack, adds to run queue)
    kt := CreateKThread(pcb, entryPoint, stackSize)
    if kt == nil {
        return -ENOMEM
    }

    // Allocate TID and return it
    tid := allocateTID(kt)
    return int64(tid)
}
```

**Usage by priest's Go runtime:**

The Go runtime creates M's (machine threads) via this syscall:

```go
// In priest's runtime - called when Go needs a new M
func newm(fn func()) {
    // Allocate M struct
    mp := allocm()

    // Create kernel thread starting at mstart
    threadID := sys.CreateThread(
        uintptr(unsafe.Pointer(mstart)),  // Entry point
        DEFAULT_M_STACK_SIZE,              // e.g., 8MB
    )
    if threadID < 0 {
        panic("failed to create thread")
    }

    mp.threadID = uint32(threadID)
}
```

### ExitThread - Terminate Current Thread

```go
// Syscall number: 0x1009
// Terminates the calling thread
//
// Args:
//   exitCode: thread exit status (mostly unused, for debugging)
//
// Returns: does not return
func SyscallExitThread(exitCode int64)
```

**Kernel actions:**

```go
func SyscallExitThread(exitCode int64) {
    kt := GetCurrentKThread()
    pcb := kt.Priest

    // Mark thread as zombie
    kt.State = KThreadZombie

    // Remove from run queue
    runQueueLock.Lock()
    if kt.schedNode != nil {
        runQueue.Remove(kt.schedNode)
        kt.schedNode = nil
    }
    runQueueLock.Unlock()

    // Check if this was the last thread in the priest
    pcb.lock.Lock()
    isLastThread := (pcb.Threads.Size() == 1)
    pcb.lock.Unlock()

    if isLastThread {
        // Last thread exiting - terminate the priest
        SyscallExit(exitCode)
        // Does not return
    }

    // Clean up thread resources
    ReleaseResourcesKThread(kt)

    // Schedule another thread
    Schedule()
    // Does not return
}
```

### AllocPages - Page-Aligned Memory Allocation

```go
// Syscall number: 0x1003
// Allocates page-aligned memory
//
// Args:
//   count: number of 4KB pages to allocate
//
// Returns:
//   virtual address of allocated region (page-aligned)
//   negative errno on failure
func SyscallAllocPages(count uint64) int64
```

**Kernel actions:**
1. Allocate physical pages
2. Find free virtual address range in caller's address space
3. Map pages with RW permissions
4. Record mapping in PCB/ThinCB for cleanup
5. Return virtual address

**Usage for stacks:**
```go
// In priest, allocating stack for new program:
stackAddr, err := sys.AllocPages(1)  // 4KB initial stack
if err != nil {
    return err
}
// Stack grows down, so SP starts at top
initialSP := stackAddr + 4096

// Go's runtime will handle stack growth:
// - If stack overflows, Go allocates new larger stack
// - Copies old stack to new
// - Updates SP
// - Continues execution
```

### Exit - Terminate Thin or Priest

```go
// Syscall number: 0x1004
// Terminates current thin (or priest if no thin context)
//
// Args:
//   exitCode: exit status
//
// Returns: does not return
func SyscallExit(exitCode int64)
```

### Reap - Clean Up Zombie Thin

```go
// Syscall number: 0x1005
// Cleans up a terminated thin, frees resources
//
// Args:
//   thinID: ID of zombie thin to reap
//
// Returns:
//   exit code of thin
//   negative errno on failure
func SyscallReap(thinID uint64) int64
```

## Stack Allocation and Growth

### Initial Stack Setup

```
Thin stack (4KB initial):

    ┌─────────────────┐ ← StackTop (initial SP)
    │                 │
    │  (grows down)   │
    │                 │
    │                 │
    ├─────────────────┤
    │  Poison Pill    │ ← Return addr = thin_exit_handler
    │  Frame          │
    ├─────────────────┤ ← StackBase
    │  Guard Page     │ ← Unmapped, triggers fault on overflow
    └─────────────────┘
```

### Stack Growth (Handled by Go Runtime)

Go's runtime handles stack growth automatically:
1. Function prologue checks if stack space sufficient
2. If not, runtime allocates new larger stack (typically 2x)
3. Copies old stack contents to new stack
4. Updates SP and frame pointers
5. Continues execution

This works transparently because thins use priest's runtime (via trampolines), and priest's runtime manages all goroutine stacks.

### Poison Pill for Termination

When thin's `main()` returns:
```
main() returns
    ↓
RET instruction pops return address
    ↓
Return address = thin_exit_handler (in priest)
    ↓
thin_exit_handler(exit_code):
    sys.Exit(exit_code)  // Syscall to kernel
    ↓
Kernel:
    - Mark thin as Zombie
    - Record exit_code
    - Inject async event to priest
    ↓
Priest receives event:
    - Logs thin termination
    - Calls sys.Reap(thin_id)
    ↓
Kernel frees thin resources
```

## Async Event Delivery

### Safety Model

The kernel **never directly calls userspace code** while in supervisor mode. Instead, it modifies the saved userspace context before ERET to redirect execution to the event handler.

### Event Queue (Kernel-Side)

Events are queued in the PCB's ring buffer. The kernel panics if the queue is full - this indicates a bug (event loop not running) rather than a recoverable condition.

```go
type AsyncEvent struct {
    Type uint32
    Data uint64
}

// Queue an event for delivery to priest
func QueueAsyncEvent(pcb *PCB, eventType uint32, eventData uint64) {
    // Calculate next write position
    nextHead := (pcb.EventQueueHead + 1) % 16

    // If queue is full, panic - this should never happen in correct code
    if nextHead == pcb.EventQueueTail {
        panic("AsyncEvent queue full - priest event loop is stuck or not running")
    }

    // Write event to queue
    pcb.EventQueue[pcb.EventQueueHead] = AsyncEvent{
        Type: eventType,
        Data: eventData,
    }
    pcb.EventQueueHead = nextHead
}

// Check if events are pending
func HasPendingEvents(pcb *PCB) bool {
    return pcb.EventQueueHead != pcb.EventQueueTail && !pcb.EventsInFlight
}

// Dequeue next event (called when injecting)
func DequeueEvent(pcb *PCB) AsyncEvent {
    event := pcb.EventQueue[pcb.EventQueueTail]
    pcb.EventQueueTail = (pcb.EventQueueTail + 1) % 16
    return event
}
```

**Why 16 slots?**
- Typical events: thin exit, signals, timers
- 16 is generous - if you have 16 pending events, something is wrong
- Panic is intentional: forces debugging rather than silent event loss

### Event Injection (Before ERET)

On every return to userspace, the kernel checks for pending events:

```go
// Called from exception return path, before ERET
func MaybeInjectEvent(pcb *PCB, frame *ExceptionFrame) {
    // Don't inject if no events or already handling one
    if !HasPendingEvents(pcb) {
        return
    }

    event := DequeueEvent(pcb)

    // Mark that we're delivering an event (prevents reentrancy)
    pcb.EventsInFlight = true

    // Save original context for later restoration
    pcb.SavedELR = frame.ELR_EL1
    pcb.SavedX0 = frame.X0
    pcb.SavedX1 = frame.X1

    // Redirect to event handler
    frame.ELR_EL1 = pcb.AsyncEventHandler
    frame.X0 = uint64(event.Type)
    frame.X1 = event.Data

    // Set LR to trampoline so handler "returns" to context restoration
    frame.X30 = pcb.AsyncEventTrampoline
}
```

### Reentrancy Prevention

The `EventsInFlight` flag prevents nested event delivery:

```
Scenario without protection:
1. Event A queued
2. Kernel injects Event A, saves context
3. Handler runs, makes syscall
4. Event B queued
5. Kernel returns from syscall, sees Event B pending
6. Kernel injects Event B, OVERWRITES saved context ← BUG!
7. Event B handler returns
8. Restores wrong context ← CRASH

With EventsInFlight:
1. Event A queued
2. Kernel injects Event A, sets EventsInFlight=true
3. Handler runs, makes syscall
4. Event B queued
5. Kernel returns from syscall, sees EventsInFlight=true
6. Kernel does NOT inject Event B (stays in queue)
7. Event A handler calls asyncEventReturn syscall
8. Kernel clears EventsInFlight, restores context
9. On NEXT syscall return, Event B is injected
```

### Context Restoration

The priest must provide an `asyncEventReturn` trampoline that the handler "returns" to:

```asm
// In priest - this is the asyncEventReturn symbol
// Called when AsyncEventHandler does RET (via LR set by kernel)
TEXT ·asyncEventReturn(SB), NOSPLIT, $0
    // Make syscall to tell kernel we're done handling the event
    MOV  $0x1007, X8      // SysEventHandled
    SVC  $0
    // Kernel restores original context and returns there
    // We never reach here
```

**Kernel SysEventHandled implementation:**

```go
// Syscall number: 0x1007
// Called by asyncEventReturn to signal event handling is complete
func SyscallEventHandled(frame *ExceptionFrame) {
    pcb := GetCurrentPCB()

    // Clear in-flight flag - allows next event to be delivered
    pcb.EventsInFlight = false

    // Restore original context
    frame.ELR_EL1 = pcb.SavedELR
    frame.X0 = pcb.SavedX0
    frame.X1 = pcb.SavedX1

    // If more events pending, inject the next one now
    MaybeInjectEvent(pcb, frame)

    // ERET will return to original code (or next event handler)
}
```

### Complete Flow Diagram

```
                    KERNEL                              USERSPACE (Priest)
                    ──────                              ──────────────────
Thin exits:
1. QueueAsyncEvent(EventThinExited)
2. Return from syscall...
3. MaybeInjectEvent():
   - EventsInFlight = true
   - Save ELR, X0, X1
   - ELR = AsyncEventHandler
   - LR = asyncEventReturn
4. ERET ─────────────────────────────────────▶ 5. AsyncEventHandler(type, data):
                                                  - Non-blocking send to channel
                                                  - RET (goes to asyncEventReturn)
                                              6. asyncEventReturn:
                                                  - SVC SysEventHandled
7. SyscallEventHandled(): ◀─────────────────────
   - EventsInFlight = false
   - Restore ELR, X0, X1
   - MaybeInjectEvent() (for next event)
8. ERET ─────────────────────────────────────▶ 9. Original code continues
                                                  (or next event handler)

Meanwhile, in another goroutine:
                                              10. eventProcessor():
                                                  - Reads from channel
                                                  - Calls handleThinExit()
                                                  - Calls sys.Reap(thin_id)
```

### Priest Event Handler Implementation

```go
// In priest/events.go

// Channel for async events - buffered to match kernel queue size
var asyncEventChan = make(chan AsyncEvent, 16)

// AsyncEventHandler is called by kernel via context injection
// Address looked up from ELF symbol table at priest load time
//
//go:nosplit
//go:noescape
func AsyncEventHandler(eventType uint32, eventData uint64)

// Assembly implementation (must be nosplit, minimal stack usage)
// TEXT ·AsyncEventHandler(SB), NOSPLIT, $0-16
//     // eventType in X0, eventData in X1
//     // Try non-blocking send to channel
//     // If channel full, we have a bug - but can't panic here
//     // Just drop and hope eventProcessor catches up
//     RET  // Returns to asyncEventReturn via LR

// eventProcessor runs as a goroutine, handles events from channel
func eventProcessor() {
    for event := range asyncEventChan {
        switch event.Type {
        case EventThinExited:
            thinID := uint8(event.Data >> 8)
            exitCode := int32(event.Data & 0xFF)
            handleThinExit(thinID, exitCode)

        case EventPriestExited:
            priestID := uint8(event.Data >> 8)
            exitCode := int32(event.Data & 0xFF)
            handlePriestExit(priestID, exitCode)

        case EventSignal:
            signum := int(event.Data)
            handleSignal(signum)

        case EventTimer:
            timerID := event.Data
            handleTimer(timerID)
        }
    }
}

func handleThinExit(thinID uint8, exitCode int32) {
    log.Printf("Thin %d exited with code %d", thinID, exitCode)
    // Clean up resources
    code := sys.Reap(uint64(thinID))
    if code < 0 {
        log.Printf("Reap failed: %d", code)
    }
}
```

### Event Types

```go
const (
    EventThinExited    = 1  // Thin terminated (data: thin_id<<8 | exit_code)
    EventPriestExited  = 2  // Child priest terminated
    EventSignal        = 3  // Unix-style signal (data: signal number)
    EventTimer         = 4  // Timer expired (data: timer ID)
)
```

## Thin Termination Flow

### Normal Thin Exit

```
1. Thin's main() returns
2. Returns to poison pill → thin_exit_handler
3. thin_exit_handler calls Exit(0)
4. Kernel:
   - Sets thin state = Zombie
   - Records exit code
   - Queues async event for priest
5. Priest receives EventThinExited
6. Priest calls Reap(thin_id)
7. Kernel:
   - Calls ReleaseResourcesThinCB()
   - Returns exit code
```

### Thin Crash

```
1. Thin triggers fault (null pointer, etc.)
2. Kernel catches exception
3. Kernel:
   - Sets thin state = Zombie
   - Records exit code = -signal_number
   - Queues async event
4. Same as normal exit from here
```

### Priest Termination

```
1. Priest calls Exit() or crashes
2. Kernel:
   - For each thin in priest:
     - Kill thin (set Zombie)
     - ReleaseResourcesThinCB()
   - Free all priest pages
   - Free page tables
   - Set priest state = Zombie
   - If parent priest exists, notify it
3. Parent priest reaps child priest
```

## Current Architecture (Reference)

### What Already Exists

| Component | Location | Purpose |
|-----------|----------|---------|
| Exception vectors | `kmazarin/exceptions_arm64.s:29-116` | 2KB-aligned vector table |
| Exception frame | `kmazarin/exceptions_arm64.s:18-26` | 320-byte register save area |
| SVC handler | `kmazarin/exceptions_arm64.s:289-307` | Dispatches to Go handler |
| Syscall dispatch | `ksyscall/dispatch.go` | Routes by syscall number |
| Thread context | `kthread/thread.go:21-30` | ThreadContext struct |
| Mazzy syscalls | `ksyscall/mazzy.go` | 0x1000+ syscall numbers |

### Exception Frame Layout (320 bytes on SP_EL1)

```
Offset   Content          Description
------   -------          -----------
0-64     X0-X7            Syscall args / general regs
64-224   X8-X27           X8 = syscall number
224-248  X28-X30          g register, FP, LR
256-264  ELR_EL1          Return PC (next instruction after SVC)
264-272  SPSR_EL1         Saved processor state
272-280  FAR_EL1          Fault address (for aborts)
280-288  ESR_EL1          Exception syndrome
288-296  SP_EL0           User stack pointer
```

## Implementation Plan

### Phase 1: Core Syscalls

1. **Add syscall numbers** (`ksyscall/mazzy.go`)
   ```go
   const (
       SysGetTime      = 0x1000
       SysLaunch       = 0x1001  // Returns priest PID (1-255)
       SysRun          = 0x1002  // Returns thin PID (priest<<8 | thin)
       SysAllocPages   = 0x1003
       SysExit         = 0x1004
       SysReap         = 0x1005
       SysStartThin    = 0x1006  // Takes thin PID
       SysEventHandled = 0x1007
       SysCreateThread = 0x1008  // Returns TID (monotonic counter)
       SysExitThread   = 0x1009
       SysStartPriest  = 0x100A  // Takes priest PID
       SysKill         = 0x100B  // Takes priest/thin PID
       SysWait         = 0x100C  // Takes priest/thin PID
   )
   ```

2. **Implement AllocPages** (`ksyscall/memory.go`)
   - Allocate physical pages
   - Map into caller's address space
   - Track for cleanup

3. **Implement Exit** (`ksyscall/process.go`)
   - Mark thin/priest as Zombie
   - Queue async event

### Phase 2: Process Management

1. **Create PCB/KThread/ThinCB structures** (`kthread/process.go`)

2. **Implement Launch syscall**
   - Load ELF
   - Create PCB
   - Set up address space

3. **Implement Run syscall**
   - Load ELF with runtime patching
   - Allocate stack
   - Set up poison pill
   - Create ThinCB

### Phase 3: Async Events

1. **Add event injection to exception return path**
   - Check for pending events before ERET
   - Modify saved context to redirect to handler
   - Set LR to asyncEventReturn trampoline

2. **Implement Reap syscall**
   - Free thin resources via ReleaseResourcesThinCB()
   - Return exit code

3. **Implement EventHandled syscall**
   - Clear EventsInFlight flag
   - Restore saved context (ELR, X0, X1)
   - Check for more pending events

### Phase 4: Fast Path (Optional Optimization)

For simple read-only syscalls, skip full context save:
```asm
svc_fast_path:
    CMP X8, #0x1000          // GetTime?
    BEQ fast_gettime
    B svc_slow_path

fast_gettime:
    // Read cached time, return immediately
    // No full register save/restore needed
```

## Files to Create/Modify

| File | Action | Purpose |
|------|--------|---------|
| `ksyscall/mazzy.go` | Modify | Add new syscall numbers |
| `ksyscall/memory.go` | Create | AllocPages implementation |
| `ksyscall/process.go` | Create | Launch, Run, Exit, Reap, StartThin |
| `ksyscall/events.go` | Create | EventHandled syscall, MaybeInjectEvent |
| `kthread/process.go` | Create | PCB, KThread, ThinCB structures |
| `kthread/sched.go` | Create | Scheduler, run queue, context switch |
| `kmazarin/exceptions_arm64.s` | Modify | Call MaybeInjectEvent before ERET |
| `src/mazarin/sys/process.go` | Create | Client-side syscall wrappers |
| `src/flock/cmd/priest/events.go` | Create | Event handler, asyncEventChan |
| `src/flock/cmd/priest/events_arm64.s` | Create | AsyncEventHandler, asyncEventReturn asm |
