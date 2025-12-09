# Go Runtime Initialization Sequence

This document describes the initialization functions in the Go runtime package, their order of execution, and their purposes. These functions bootstrap the Go runtime from bare assembly to a running Go program.

## Overview

The Go runtime initialization follows a specific sequence:
1. **Assembly Entry** (`rt0_go`) - Initial setup in assembly
2. **CPU & OS Detection** - Detect hardware and OS capabilities  
3. **Argument Processing** - Parse command-line arguments
4. **Scheduler Initialization** - Set up the goroutine scheduler
5. **Subsystem Initialization** - Initialize memory, GC, signals, etc.
6. **Package Initialization** - Run package init functions
7. **Main Goroutine** - Launch user's main function

---

## Phase 1: Assembly Entry Point

### `runtime.rt0_go`
**Location**: `src/runtime/asm_*.s`  
**Purpose**: The very first function called when a Go program starts. Written in assembly.

**Responsibilities**:
- Set up the initial stack
- Save command-line arguments (argc, argv)
- Detect CPU features
- Initialize thread-local storage (TLS)
- Jump to `runtime.check` for validation
- Call `runtime.args` to process arguments
- Call `runtime.osinit` for OS-specific setup
- Call `runtime.schedinit` to initialize the scheduler
- Create the main goroutine to run `runtime.main`
- Call `runtime.mstart` to start the scheduler

**Call Chain**: `_rt0_amd64_darwin` → `_rt0_amd64` → `runtime.rt0_go`

---

## Phase 2: System Checks and Validation

### `runtime.check`
**Purpose**: Verify that the runtime's assumptions about data types and sizes are correct.

**Checks performed**:
- Size of basic types (`int8`, `int16`, `int32`, `int64`, `float32`, `float64`)
- Pointer size matches architecture (32-bit or 64-bit)
- Struct field offsets and alignment
- Constants like `_PageSize` match actual values
- Arithmetic operations work correctly

**Why it matters**: Catches ABI mismatches, wrong compiler flags, or broken builds early.

### `runtime.checkASM`
**Purpose**: Validate that assembly functions are accessible and working.

**Checks**:
- Assembly stubs are callable from Go
- Function linkage is correct
- Assembly calling conventions work

---

## Phase 3: CPU and Hardware Initialization

### `runtime.cpuinit`
**Called from**: `runtime.schedinit`  
**Purpose**: Detect CPU features and capabilities.

**Detects**:
- CPU instruction set extensions (SSE, AVX, AVX2, etc.)
- Cache line size
- CPU vendor and model
- Number of physical CPUs
- Endianness

**Result**: Sets `internal/cpu` package variables for optimized code paths.

### `runtime.asminit`
**Purpose**: Initialize assembly-specific state.

**Sets up**:
- Assembly function pointers
- CPU-specific optimizations
- Platform-specific calling conventions

---

## Phase 4: Argument Processing

### `runtime.args`
**Called from**: `runtime.rt0_go`  
**Purpose**: Process command-line arguments passed to the program.

**Processing**:
- Parses `argc` and `argv` from the OS
- Separates Go runtime flags (GODEBUG, GOTRACEBACK, etc.)
- Stores arguments for `os.Args`
- Processes environment variables

**Runtime Flags Recognized**:
- `GODEBUG` - Enable runtime debug features
- `GOMAXPROCS` - Set max number of OS threads
- `GOTRACEBACK` - Control stack trace verbosity
- `GOMEMLIMIT` - Set soft memory limit

### `runtime.goargs`
**Purpose**: Convert C-style arguments to Go slices.

**Converts**: `char** argv` → `[]string` for `os.Args`

### `runtime.sysargs`
**Purpose**: Platform-specific argument processing (macOS, Linux, Windows).

---

## Phase 5: OS Initialization

### `runtime.osinit`
**Called from**: `runtime.rt0_go`  
**Purpose**: Initialize OS-specific functionality.

**Platform-Specific Setup** (macOS/Darwin):
- Query number of CPUs via `sysctl`
- Set up mach port communication
- Initialize BSD subsystem
- Get physical page size
- Query system memory

**Platform-Specific Setup** (Linux):
- Read `/proc/cpuinfo` for CPU count
- Set up futex support
- Initialize epoll for network polling
- Query huge page support

**Sets**: `runtime.ncpu` (number of CPUs)

---

## Phase 6: Scheduler Initialization

### `runtime.schedinit`
**Called from**: `runtime.rt0_go`  
**Purpose**: Initialize the goroutine scheduler (G-P-M model).

**Initializes**:
1. Stack size limits (`runtime.stackcheck`)
2. Memory allocator (`runtime.mallocinit`)
3. CPU initialization (`runtime.cpuinit`)
4. Module data structures (`runtime.modulesinit`)
5. Type information (`runtime.typelinksinit`, `runtime.itabsinit`)
6. Processor context P (`runtime.(*p).init`)
7. Garbage collector (`runtime.gcinit`)
8. Signal handling (`runtime.initsig`)
9. Tracing support (`runtime.inittrace`)

**Sets up**:
- Global scheduler lock
- Initial P (processor) count based on GOMAXPROCS
- M0 (initial machine/thread)
- G0 (initial goroutine for scheduling)

**Returns**: Ready to run goroutines

---

## Phase 7: Memory Subsystem Initialization

### `runtime.mallocinit`
**Called from**: `runtime.schedinit`  
**Purpose**: Initialize the memory allocator and heap.

**Sets up**:
- Heap arena (address space for Go heap)
- Memory spans (`mspan` structures)
- Size classes for small object allocation
- Per-P allocation caches (`mcache`)
- Central free lists (`mcentral`)
- Page allocator (`pageAlloc`)

**Allocates**:
- Initial heap memory from OS
- Metadata structures for memory management

**Result**: `runtime.mallocgc` (Go's malloc) is ready to use.

### `runtime.(*mheap).init`
**Purpose**: Initialize the global heap structure.

**Sets up**:
- Heap lock
- Span allocator
- Large object allocator
- Sweep state for garbage collection

---

## Phase 8: Module and Type System Initialization

### `runtime.modulesinit`
**Called from**: `runtime.schedinit`  
**Purpose**: Initialize module metadata structures.

**Processes**:
- Linked list of modules (packages)
- PC-to-function tables (for stack traces)
- Source file tables (for debug info)

### `runtime.typelinksinit`
**Purpose**: Build type information tables.

**Creates**:
- Type registry for all types in program
- Type comparison functions
- Type hash functions
- GC metadata for each type (pointer maps)

### `runtime.itabsinit`
**Purpose**: Pre-allocate interface dispatch tables (itabs).

**Sets up**:
- Hash table for interface method dispatch
- Cached itabs for common interfaces

**Why**: Interface calls need to look up the concrete type's method. This pre-caches common cases.

---

## Phase 9: Garbage Collector Initialization

### `runtime.gcinit`
**Called from**: `runtime.schedinit`  
**Purpose**: Initialize the garbage collector.

**Sets up**:
- GC controller state
- Mark worker goroutines
- Sweep state
- Write barrier state
- GC pacing parameters
- Memory limits

**GC Parameters**:
- Initial GC trigger (when to start GC)
- Target heap growth
- CPU limits for GC

**Result**: GC is ready but not yet started.

---

## Phase 10: Signal Handling Initialization

### `runtime.initsig`
**Called from**: `runtime.schedinit`  
**Purpose**: Set up signal handlers for the runtime.

**Installs handlers for**:
- `SIGILL`, `SIGTRAP` - Illegal instruction (Go panics)
- `SIGFPE` - Floating point exceptions
- `SIGSEGV`, `SIGBUS` - Memory access violations (nil pointer, out of bounds)
- `SIGPROF` - CPU profiling timer
- `SIGURG` - Preemption signal (Go 1.14+)
- `SIGINT`, `SIGTERM` - Graceful shutdown

### `runtime.initSigmask`
**Purpose**: Set up initial signal mask (which signals are blocked).

**Blocks**:
- Signals that should not interrupt critical runtime code
- Thread-specific signals

---

## Phase 11: Tracing and Profiling Initialization

### `runtime.inittrace`
**Called from**: `runtime.schedinit`  
**Purpose**: Initialize execution tracing support.

**Sets up**:
- Trace buffers
- Trace event types
- Trace reader goroutine

**Enabled by**: `runtime/trace.Start()`

---

## Phase 12: Thread-Local Storage Initialization

### `runtime.tlsinit`
**Called from**: `runtime.rt0_go` (assembly)  
**Purpose**: Initialize thread-local storage for goroutine context.

**Sets up**:
- TLS slot for current goroutine pointer (`g`)
- TLS slot for current machine pointer (`m`)

**Platform-specific**:
- Uses `pthread_key_create` on POSIX systems
- Uses `TlsAlloc` on Windows
- Direct register access on some platforms (e.g., `%fs` on x86-64)

---

## Phase 13: Starting the First Thread

### `runtime.mstart`
**Called from**: `runtime.rt0_go` (for M0), or when creating new OS threads  
**Purpose**: Start the machine (OS thread) and begin scheduling.

**Sequence**:
1. `runtime.mstart` → `runtime.mstart0` → `runtime.mstart1`
2. Save stack bounds for the thread
3. Initialize signal mask for the thread (`runtime.minit`)
4. Enter the schedule loop (`runtime.schedule`)

### `runtime.mstart0`
**Purpose**: First part of thread startup (can be split-stack).

**Sets up**:
- Stack bounds
- Thread-specific state

### `runtime.mstart1`
**Purpose**: Second part of thread startup (runs on system stack).

**Calls**:
- `runtime.minit` - Initialize M (machine/thread)
- `runtime.schedule` - Enter scheduler loop

### `runtime.minit`
**Purpose**: Initialize per-thread state.

**Sets up**:
- Signal stack (`runtime.minitSignalStack`)
- Signal mask (`runtime.minitSignalMask`)
- Thread ID
- Thread name (for debuggers)

---

## Phase 14: Main Goroutine Initialization

### `runtime.main`
**Runs on**: Main goroutine (G1)  
**Purpose**: Run package init functions and user's main function.

**Sequence**:
1. Set `mainStarted` flag
2. Start system monitor goroutine (`runtime.sysmon`)
3. Lock main goroutine to M0 (`runtime.lockOSThread`)
4. Run runtime package init functions (`runtime.init`)
5. Enable GC (`runtime.gcenable`)
6. Run all package init functions (`runtime_init` from `main` package)
7. Call user's `main.main()` function
8. Call `runtime.exit(0)` when main returns

### `runtime.main.func1`
**Purpose**: Deferred function to unlock main goroutine from M0.

**Called**: When `main.main()` returns

### `runtime.main.func2`
**Purpose**: Panic handler for main goroutine.

**Called**: If `main.main()` panics without recovery

---

## Phase 15: System Monitor

### `runtime.sysmon`
**Runs on**: Special goroutine (not bound to a P)  
**Purpose**: Background monitoring thread for the runtime.

**Monitors**:
1. **Long-running goroutines** - Preempt if running >10ms
2. **Network poller** - Check for network I/O readiness
3. **Timers** - Trigger expired timers
4. **GC pacing** - Force GC if memory pressure
5. **Deadlock detection** - Panic if all goroutines blocked
6. **Scavenging** - Return unused memory to OS

**Runs**: In a loop with exponential backoff (20µs → 10ms)

---

## Phase 16: Package Init Functions

### Package `runtime.init`
**Purpose**: Initialize runtime package variables and state.

**Multiple init phases** (numbered):
- `runtime.init.0` - Early initialization
- `runtime.init.1` - CPU features
- `runtime.init.4` - Profiling support
- `runtime.init.5` - Tracing support
- `runtime.init.6` - Final setup

### User Package Inits
**Executed**: After runtime init, in dependency order

**Order**: Depth-first, dependencies first, imports before importers

---

## Supporting Initialization Functions

### `runtime.(*p).init`
**Purpose**: Initialize a processor (P) context.

**Sets up**:
- Per-P allocation cache (`mcache`)
- Per-P timer heap
- Per-P run queue for goroutines
- Per-P random number generator

### `runtime.mpreinit`
**Purpose**: Pre-initialize machine (M/thread) structure before use.

**Sets up**:
- M ID
- Signal handling state

### `runtime.mcommoninit`
**Purpose**: Common initialization for all M structures.

**Sets up**:
- Allocate M ID
- Add M to global list (`runtime.allm`)
- Initialize locks

---

## Initialization Checks and Validation

### `runtime.checkdead`
**Purpose**: Check if program is deadlocked (all goroutines blocked).

**Called**: Periodically by scheduler when no runnable goroutines

**Panics if**:
- All goroutines are blocked
- No timers scheduled
- No network I/O pending

**Exception**: Deadlock is OK if only background goroutines remain

### `runtime.checkmcount`
**Purpose**: Verify M count hasn't exceeded limits.

### `runtime.checkTimers`
**Purpose**: Verify timer heap is consistent.

---

## Complete Initialization Call Sequence

```
Assembly Entry:
  _rt0_amd64_darwin              # OS-specific entry
  └─→ _rt0_amd64                 # Architecture-specific entry
      └─→ runtime.rt0_go         # Main runtime entry (asm)
          ├─→ runtime.check      # Validate runtime assumptions
          ├─→ runtime.args       # Process command-line args
          ├─→ runtime.osinit     # OS-specific initialization
          ├─→ runtime.schedinit  # Initialize scheduler
          │   ├─→ runtime.stackcheck
          │   ├─→ runtime.mallocinit        # Memory allocator
          │   │   └─→ runtime.(*mheap).init
          │   ├─→ runtime.cpuinit           # CPU detection
          │   ├─→ runtime.modulesinit       # Module metadata
          │   ├─→ runtime.typelinksinit     # Type system
          │   ├─→ runtime.itabsinit         # Interface tables
          │   ├─→ runtime.(*p).init         # Processor P
          │   ├─→ runtime.gcinit            # Garbage collector
          │   ├─→ runtime.initsig           # Signal handlers
          │   └─→ runtime.inittrace         # Tracing support
          ├─→ [Create main goroutine]
          └─→ runtime.mstart     # Start scheduling
              └─→ runtime.mstart0
                  └─→ runtime.mstart1
                      ├─→ runtime.minit           # Thread init
                      │   ├─→ runtime.minitSignalStack
                      │   └─→ runtime.minitSignalMask
                      └─→ runtime.schedule        # Scheduler loop
                          └─→ [Run main goroutine]
                              └─→ runtime.main    # Main goroutine
                                  ├─→ runtime.sysmon (background)
                                  ├─→ runtime.init.*      # Runtime inits
                                  ├─→ [Package inits]     # User package inits
                                  └─→ main.main()         # User's main function
```

---

## Debugging Initialization

### Environment Variables

Control runtime behavior during initialization:

```bash
# Show scheduler traces
GODEBUG=schedtrace=1000 ./program

# Show GC traces  
GODEBUG=gctrace=1 ./program

# Show memory allocator stats
GODEBUG=allocfreetrace=1 ./program

# Set max OS threads
GOMAXPROCS=4 ./program

# Set memory limit (soft)
GOMEMLIMIT=1GiB ./program
```

### Compile-Time Checks

The Go toolchain ensures initialization is correct:

```bash
# Show what init functions exist
go tool compile -S program.go | grep '\.init'

# Show what the linker is doing
go build -ldflags="-v" program.go

# See all runtime symbols
go tool nm program | grep runtime
```

---

## Key Takeaways

1. **Initialization is hierarchical**: Assembly → C → Go
2. **Order matters**: Memory before GC, types before interfaces
3. **Checks everywhere**: `runtime.check` catches ABI mismatches early
4. **Platform-specific**: `osinit`, `asminit`, `tlsinit` adapt to OS/arch
5. **Lazy where possible**: Network polling, profiling initialized on first use
6. **Fail fast**: Deadlock detection, panic on invalid state
7. **Observable**: GODEBUG flags expose internal state

---

## References

- [Go Runtime Source](https://github.com/golang/go/tree/master/src/runtime)
- `src/runtime/proc.go` - Scheduler and initialization
- `src/runtime/asm_amd64.s` - Assembly entry point  
- `src/runtime/os_darwin.go` - macOS-specific code
- `src/runtime/malloc.go` - Memory allocator
- `src/runtime/mgc.go` - Garbage collector

---

*This document is based on Go 1.19.6 runtime found in the websrv binary.*
