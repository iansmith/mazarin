# Plan: Priest ↔ Kernel Context Switch

## Overview

This document details how priest (the userspace syscall router) context switches to kmazarin (the kernel) when handling syscalls, and the overall process/program model for Mazzy userspace.

## Terminology

| Term | Definition |
|------|------------|
| **Priest** | A "process" in the Unix sense. Has its own Go runtime, memory space, scheduler. |
| **Program** | A "thin client" within a priest. Shares the priest's runtime via trampolines. |
| **PCB** | Priest Control Block - kernel's bookkeeping for a priest |
| **ProgramCB** | Program Control Block - kernel's bookkeeping for a program |

## Process/Program Hierarchy

```
┌─────────────────────────────────────────────┐
│                  Kernel                      │
│  ┌─────────────┐  ┌─────────────┐           │
│  │ PCB (Priest │  │ PCB (Priest │  ...      │
│  │    ID=1)    │  │    ID=2)    │           │
│  └─────────────┘  └─────────────┘           │
│        │                │                    │
└────────┼────────────────┼────────────────────┘
         │                │
    ┌────┴────┐      ┌────┴────┐
    ▼         ▼      ▼         ▼
┌───────┐ ┌───────┐ ┌───────┐ ┌───────┐
│Program│ │Program│ │Program│ │Program│
│  0x01 │ │  0x02 │ │  0x01 │ │  0x02 │
└───────┘ └───────┘ └───────┘ └───────┘
   helloworld  foo     bar      baz

PID = (priest_id << 8) | program_id

Priest 1 alone:     0x0100
Priest 1, prog 1:   0x0101
Priest 1, prog 2:   0x0102
Priest 2 alone:     0x0200
```

## Kernel Data Structures

### Priest Control Block (PCB)

```go
type PriestControlBlock struct {
    // Identity
    PriestID    uint8           // 1-255 (0 reserved for kernel)
    State       PriestState     // Running, Blocked, Zombie

    // Memory layout
    EntryPoint  uint64          // ELF entry point
    LoadBase    uint64          // Base address where loaded
    HeapStart   uint64          // Start of heap region
    HeapEnd     uint64          // Current heap break

    // Virtual memory
    PageTableRoot uint64        // Root of page table tree
    MappedPages   []PageMapping // All mapped pages for cleanup

    // Programs within this priest
    ProgramCount uint8
    Programs     [256]*ProgramControlBlock

    // Thread context (for when priest itself is scheduled)
    Context     ThreadContext

    // Runtime state
    AsyncEventPending bool      // Event waiting to be delivered
    AsyncEventType    uint32
    AsyncEventData    uint64
}

type PriestState int32
const (
    PriestFree    PriestState = 0
    PriestRunning PriestState = 1
    PriestReady   PriestState = 2
    PriestBlocked PriestState = 3
    PriestZombie  PriestState = 4
)
```

### Program Control Block

```go
type ProgramControlBlock struct {
    // Identity
    ProgramID   uint8           // 1-255 within priest
    Priest      *PriestControlBlock
    State       ProgramState

    // Memory layout
    EntryPoint  uint64          // Program's _start or main
    StackBase   uint64          // Bottom of stack (low address)
    StackTop    uint64          // Top of stack (high address, initial SP)

    // Mapped pages (for cleanup on exit)
    MappedPages []PageMapping

    // Exit status
    ExitCode    int32
}

type ProgramState int32
const (
    ProgramFree    ProgramState = 0
    ProgramRunning ProgramState = 1
    ProgramReady   ProgramState = 2
    ProgramZombie  ProgramState = 3
)
```

## Syscalls

### Launch - Create a New Priest

```go
// Syscall number: 0x1001
// Creates a new priest from an ELF file
//
// Args:
//   filename: path to ELF file (e.g., "/priest2.elf")
//
// Returns:
//   priest_id on success (1-255)
//   negative errno on failure
func SyscallLaunch(filenamePtr uint64) int64
```

**Kernel actions:**
1. Allocate new PCB, assign priest_id
2. Load ELF from filesystem
3. Allocate page table for new address space
4. Map ELF segments into address space
5. Set entry point from ELF header
6. Initialize heap region
7. Mark priest as Ready
8. Return priest_id

### Run - Create a Program within Current Priest

```go
// Syscall number: 0x1002
// Creates a new program (thin client) in the calling priest
//
// Args:
//   filename: path to ELF file (e.g., "/helloworld.elf")
//
// Returns:
//   program_id on success (1-255)
//   negative errno on failure
func SyscallRun(filenamePtr uint64) int64
```

**Kernel actions:**
1. Identify calling priest from current context
2. Allocate ProgramControlBlock
3. Load ELF, patch runtime trampolines to priest's runtime
4. Allocate stack page via AllocPages (1 page = 4KB initially)
5. Set up poison pill at stack bottom
6. Map program into priest's address space
7. Return program_id

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
4. Record mapping in PCB/ProgramCB for cleanup
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

### Exit - Terminate Program or Priest

```go
// Syscall number: 0x1004
// Terminates current program (or priest if no program context)
//
// Args:
//   exitCode: exit status
//
// Returns: does not return
func SyscallExit(exitCode int64)
```

### Reap - Clean Up Zombie Program

```go
// Syscall number: 0x1005
// Cleans up a terminated program, frees resources
//
// Args:
//   programID: ID of zombie program to reap
//
// Returns:
//   exit code of program
//   negative errno on failure
func SyscallReap(programID uint64) int64
```

## Stack Allocation and Growth

### Initial Stack Setup

```
Program stack (4KB initial):

    ┌─────────────────┐ ← StackTop (initial SP)
    │                 │
    │  (grows down)   │
    │                 │
    │                 │
    ├─────────────────┤
    │  Poison Pill    │ ← Return addr = program_exit_handler
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

This works transparently because programs use priest's runtime (via trampolines), and priest's runtime manages all goroutine stacks.

### Poison Pill for Termination

When program's `main()` returns:
```
main() returns
    ↓
RET instruction pops return address
    ↓
Return address = program_exit_handler (in priest)
    ↓
program_exit_handler(exit_code):
    sys.Exit(exit_code)  // Syscall to kernel
    ↓
Kernel:
    - Mark program as Zombie
    - Record exit_code
    - Inject async event to priest
    ↓
Priest receives event:
    - Logs program termination
    - Calls sys.Reap(program_id)
    ↓
Kernel frees program resources
```

## Async Event Delivery

### Safety Model

The kernel **never directly calls userspace code** while in supervisor mode. Instead, it modifies the saved userspace context before ERET:

```
Kernel wants to deliver event to priest:

1. Kernel is handling syscall/interrupt
2. Check if priest has pending async event
3. If yes, before ERET:

   // Save priest's original return point
   priest.SavedELR = exception_frame.ELR_EL1
   priest.SavedX0 = exception_frame.X0

   // Redirect to event handler
   exception_frame.ELR_EL1 = priest.AsyncEventHandler
   exception_frame.X0 = event_type
   exception_frame.X1 = event_data

4. ERET to userspace
5. Priest resumes at AsyncEventHandler (in USER mode!)
6. Handler processes event, writes to channel
7. Handler returns to SavedELR (original code)
```

### Priest Event Handler

```go
// In priest - address known to kernel
//go:nosplit
func AsyncEventHandler(eventType uint32, eventData uint64) {
    // This runs in userspace, safe to use channels
    select {
    case asyncEventChan <- AsyncEvent{eventType, eventData}:
        // Delivered
    default:
        // Channel full, event dropped (or panic)
    }

    // Return to original execution point
    // (kernel set up return address)
}

// Priest's event processing goroutine
func eventProcessor() {
    for event := range asyncEventChan {
        switch event.Type {
        case EventProgramExited:
            programID := uint8(event.Data >> 8)
            exitCode := int32(event.Data & 0xFF)
            handleProgramExit(programID, exitCode)
        case EventSignal:
            // Handle signal
        }
    }
}
```

### Event Types

```go
const (
    EventProgramExited = 1  // Program terminated
    EventPriestExited  = 2  // Child priest terminated
    EventSignal        = 3  // Unix-style signal
    EventTimer         = 4  // Timer expired
)
```

## Program Termination Flow

### Normal Program Exit

```
1. Program's main() returns
2. Returns to poison pill → program_exit_handler
3. program_exit_handler calls Exit(0)
4. Kernel:
   - Sets program state = Zombie
   - Records exit code
   - Queues async event for priest
5. Priest receives EventProgramExited
6. Priest calls Reap(program_id)
7. Kernel:
   - Unmaps program's pages
   - Frees ProgramControlBlock
   - Returns exit code
```

### Program Crash

```
1. Program triggers fault (null pointer, etc.)
2. Kernel catches exception
3. Kernel:
   - Sets program state = Zombie
   - Records exit code = -signal_number
   - Queues async event
4. Same as normal exit from here
```

### Priest Termination

```
1. Priest calls Exit() or crashes
2. Kernel:
   - For each program in priest:
     - Kill program (set Zombie)
     - Free program resources
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
       SysGetTime    = 0x1000
       SysLaunch     = 0x1001
       SysRun        = 0x1002
       SysAllocPages = 0x1003
       SysExit       = 0x1004
       SysReap       = 0x1005
   )
   ```

2. **Implement AllocPages** (`ksyscall/memory.go`)
   - Allocate physical pages
   - Map into caller's address space
   - Track for cleanup

3. **Implement Exit** (`ksyscall/process.go`)
   - Mark program/priest as Zombie
   - Queue async event

### Phase 2: Process Management

1. **Create PCB/ProgramCB structures** (`kthread/process.go`)

2. **Implement Launch syscall**
   - Load ELF
   - Create PCB
   - Set up address space

3. **Implement Run syscall**
   - Load ELF with runtime patching
   - Allocate stack
   - Set up poison pill
   - Create ProgramCB

### Phase 3: Async Events

1. **Add event injection to exception return path**
   - Check for pending events before ERET
   - Modify saved context to redirect to handler

2. **Implement Reap syscall**
   - Free program resources
   - Return exit code

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
| `ksyscall/process.go` | Create | Launch, Run, Exit, Reap |
| `kthread/process.go` | Create | PCB, ProgramCB structures |
| `kmazarin/exceptions_arm64.s` | Modify | Async event injection |
| `src/mazarin/sys/process.go` | Create | Client-side syscall wrappers |
| `src/flock/cmd/priest/events.go` | Create | Event handler, channel |
