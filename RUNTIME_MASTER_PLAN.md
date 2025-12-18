# Go Runtime Master Plan for Mazboot

## End Goal

Run a "hello world" program compiled by the **standard Go compiler** (`go build`)
on bare-metal aarch64. No modifications to Go source code. The runtime initializes
itself via `rt0_go`, and our role is limited to:

1. Providing initial entry point that jumps to `rt0_go`
2. Implementing Linux syscalls that the runtime calls
3. Setting up hardware (MMU, interrupts, UART)

When complete, this code gets **removed**:
- `initRuntimeStubs()` - runtime does its own g0/m0/P setup
- `initGoHeap()` - runtime calls mallocinit itself
- `createKernelGoroutine()` - runtime's newproc works
- Manual write barrier setup - runtime handles this

What **remains**:
- `exceptions.s` - syscall emulation layer
- Page table setup and demand paging
- UART/framebuffer drivers
- Timer/interrupt handling

## Approach

1. **Leave Go runtime unchanged** - All adaptation happens in our syscall layer
2. **Systematic progression** - Work through init sequence item by item
3. **Test-driven** - Each item gets a verification test (removed once working)
4. **Build scaffolding as needed** - VM tables, interrupt handlers, etc.
5. **Remove our scaffolding** - As each runtime piece starts working, delete our version

## Current Infrastructure

### Permanent (keeps)
| Component | Status | Notes |
|-----------|--------|-------|
| mmap syscall | Working | Bump allocator + demand paging |
| munmap syscall | Working | No-op (don't reclaim) |
| madvise syscall | Working | No-op |
| mprotect syscall | Working | No-op |
| Timer interrupts | Working | Generic timer |
| UART output | Working | PL011 |
| Page tables | Working | 4KB pages, demand paging |
| Exception vectors | Working | Syscall dispatch |

### Temporary Scaffolding (to be removed)
| Component | Status | Replaced By |
|-----------|--------|-------------|
| g0/m0 linkage in lib.s | Working | rt0_go does this |
| initRuntimeStubs() | Working | schedinit() |
| initGoHeap() | Working | mallocinit() called by schedinit |
| Write barrier manual setup | Working | procresize() creates real P |
| createKernelGoroutine() | Working | newproc() |
| Manual goroutine switching | Working | schedule()

---

## Initialization Sequence Items

### Item 1: g0 stack bounds setup

**Location**: `rt0_go` in `asm_arm64.s` lines 89-102

**What it does**:
- Sets `g0.stack.lo` and `g0.stack.hi` from OS-provided stack
- Sets `g0.stackguard0` and `g0.stackguard1`

**Syscalls needed**: None

**Scaffolding needed**:
- Linker symbols for g0 address
- Initial stack allocation in our bootloader

**Current status**: IMPLEMENTED in `lib.s`

**Test**: Verify stack bounds are set correctly, call a deeply recursive function

---

### Item 2: g0 ↔ m0 linkage

**Location**: `rt0_go` in `asm_arm64.s` lines 104-115

**What it does**:
- Sets `g0.m = &m0`
- Sets `m0.g0 = &g0`
- Sets x28 register to point to g0

**Syscalls needed**: None

**Scaffolding needed**:
- Linker symbols for g0, m0 addresses
- Assembly to set x28

**Current status**: IMPLEMENTED in `lib.s` and `runtime_stub.go`

**Test**: Verify `getg()` returns correct g0, verify `g0.m.g0 == g0`

---

### Item 3: runtime.args()

**Location**: `runtime1.go:67`

**What it does**:
```go
func args(c int32, v **byte) {
    argc = c
    argv = v
    sysargs(c, v)  // Also parses auxv for page size, random seed
}
```

**Syscalls needed**: None (with proper auxv structure)

**Scaffolding needed**:
- Assembly helper to set up minimal argv/envp/auxv structure
- auxv must include AT_PAGESZ, AT_NULL minimum

**Current status**: ✅ COMPLETE (2024-12-18)

**Test**: `call_runtime_args` in lib.s - sets up argv/auxv and calls runtime.args

**Implementation notes**:
- Created `call_runtime_args` assembly function in lib.s
- Sets up minimal structure: argc=0, empty argv/envp, auxv with AT_PAGESZ=4096
- Test prints "PASS" on successful completion

**TODO**: Add AT_RANDOM to auxv once RNG is initialized (see Device Initialization).
This provides 16 bytes of entropy that sysauxv() stores in startupRand.

---

### Item 4: runtime.osinit()

**Location**: `os_linux.go:356`

**What it does**:
```go
func osinit() {
    ncpu = getCPUCount()
    physHugePageSize = getHugePageSize()
    osArchInit()
}
```

**Syscalls needed**:
- `sched_getaffinity` (204) - for getCPUCount()
- Reading `/sys/kernel/mm/hugepages/...` - for getHugePageSize()

**Scaffolding needed**:
- Implement sched_getaffinity to return ncpu=1
- Implement getHugePageSize to return 0 (no huge pages)

**Current status**: NOT YET CALLED

**Test**: Call osinit(), verify ncpu=1, physHugePageSize=0

**Implementation notes**:
- getCPUCount uses sched_getaffinity syscall
- Can stub to always return 1 CPU
- getHugePageSize reads sysfs, can stub to return 0

---

### Item 5: runtime.schedinit()

**Location**: `proc.go:832`

This is complex - break into sub-items:

#### Item 5a: lockInit() calls

**What it does**: Initializes lock ranks for deadlock detection

**Syscalls needed**: None

**Current status**: Should work (just sets struct fields)

---

#### Item 5b: ticks.init()

**Location**: `runtime2.go`

**What it does**: Initializes tick counter for timing

**Syscalls needed**: None (uses `cputicks()` which reads cycle counter)

**Scaffolding needed**: Ensure `cputicks()` works (reads CNTVCT_EL0)

**Test**: Verify ticks.init() completes, ticks increase over time

---

#### Item 5c: stackinit()

**Location**: `stack.go:168`

**What it does**:
```go
func stackinit() {
    for i := range stackpool {
        stackpool[i].item.span.init()  // Sets first=nil, last=nil
        lockInit(&stackpool[i].item.mu, ...)
    }
    for i := range stackLarge.free {
        stackLarge.free[i].init()
        lockInit(&stackLarge.lock, ...)
    }
}
```

**Syscalls needed**: None

**Scaffolding needed**: None - just initializes empty lists

**Current status**: NOT YET CALLED (but trivial)

**Test**: Call stackinit(), verify pools are empty but initialized

---

#### Item 5d: mallocinit()

**Location**: `malloc.go:556`

**What it does**:
- Initializes heap arena
- Sets up mheap structures
- Reserves virtual address space via mmap

**Syscalls needed**:
- `mmap` (222) - reserve heap arena (PROT_NONE initially)
- Potentially `madvise` (233)

**Current status**: IMPLEMENTED - we call this via `asm.CallMallocinit()`

**Test**: Call mallocinit(), allocate memory with `new()`, verify it works

---

#### Item 5e: cpuinit()

**Location**: `os_linux_arm64.go`

**What it does**: Detects CPU features (HWCAP)

**Syscalls needed**:
- Reads from auxiliary vector (AT_HWCAP, AT_HWCAP2)
- Or reads `/proc/self/auxv`

**Scaffolding needed**:
- Provide fake auxv with ARM64 features we support
- Or stub HWCAP to reasonable defaults

**Test**: Call cpuinit(), verify cpu.ARM64 feature flags are set

---

#### Item 5f: randinit()

**Location**: `os_linux.go`

**What it does**: Seeds random number generator

**Syscalls needed**:
- `getrandom` (278) - get random bytes from kernel

**Scaffolding needed**:
- Implement getrandom syscall
- Initialize RNG as part of device init (see "Device Initialization" section)
- Use ARM timer (CNTVCT_EL0) XOR'd with addresses for entropy

**Note**: RNG initialization should happen alongside other device init (UART,
framebuffer) in our bootloader, BEFORE jumping to rt0_go. This provides:
1. AT_RANDOM bytes in auxv for args() → sysargs()
2. Entropy source for getrandom syscall used by randinit()

**Test**: Call randinit(), verify fastrand() returns varying values

---

#### Item 5g: alginit()

**Location**: `alg.go`

**What it does**: Initializes hash algorithm (uses random seed from randinit)

**Syscalls needed**: None (uses randinit's seed)

**Test**: Verify map operations work after alginit

---

#### Item 5h: mcommoninit(m0)

**Location**: `proc.go:958`

**What it does**:
- Assigns m ID
- Initializes m's signal stack
- Sets up profiling stacks

**Syscalls needed**:
- `mmap` - for signal stack allocation (gsignal)

**Scaffolding needed**: mmap already implemented

**Test**: Verify m0 fields are initialized, m0.gsignal exists

---

#### Item 5i: modulesinit()

**Location**: `symtab.go`

**What it does**: Initializes module data (function tables, type info)

**Syscalls needed**: None

**Test**: Verify activeModules is populated

---

#### Item 5j: typelinksinit()

**Location**: `type.go`

**What it does**: Initializes type links for reflection

**Syscalls needed**: None

**Test**: Verify type assertions work

---

#### Item 5k: itabsinit()

**Location**: `iface.go`

**What it does**: Initializes interface tables

**Syscalls needed**: None

**Test**: Verify interface conversions work

---

#### Item 5l: stkobjinit()

**Location**: `stkframe.go`

**What it does**: Initializes stack object tracking for GC

**Syscalls needed**: None

**Test**: Part of GC verification later

---

#### Item 5m: gcinit()

**Location**: `mgc.go`

**What it does**: Initializes garbage collector state

**Syscalls needed**: None (just sets up data structures)

**Test**: Verify gcController is initialized

---

#### Item 5n: procresize(procs)

**Location**: `proc.go:5669`

**What it does**:
- Creates P (processor) structures
- Allocates mcache for each P
- Links P to allp array

**Syscalls needed**:
- `mmap` - for P allocation
- `mmap` - for mcache allocation

**Current status**: Should work with existing mmap

**Test**: Verify allp[0] exists, has valid mcache

---

### Item 6: runtime.newproc(runtime.mainPC)

**Location**: `proc.go:5158`

**What it does**:
- Creates the main goroutine that will run `runtime.main`
- Allocates g struct and stack via `malg()`
- Puts goroutine on run queue

**Syscalls needed**:
- `mmap` - for goroutine stack (via stackalloc)

**Scaffolding needed**:
- `systemstack()` must work (switches to g0 stack)

**Current status**: Partially working (we create goroutines manually)

**Test**: Call newproc with a test function, verify g is created with valid stack

---

### Item 7: runtime.mstart()

**Location**: `proc.go:1869` (mstart0) and `asm_arm64.s:178`

**What it does**:
- Sets up g0 stack bounds (if not already set)
- Calls `mstart1()` which calls `schedule()`
- Enters scheduler loop

**Sub-items**:

#### Item 7a: mstart0/mstart1

**What it does**:
- Initializes g0.sched for returning from goroutines
- Calls `asminit()` (no-op on arm64)
- Calls `minit()` for thread-local setup
- Calls `mstartm0()` for m0-specific setup (signal handlers)

**Syscalls needed**:
- `sigaction` (134) - install signal handlers
- `sigaltstack` (132) - set up signal stack
- `rt_sigprocmask` (135) - manage signal mask

**Scaffolding needed**:
- Signal handling infrastructure (or stub these syscalls)
- For bare-metal: signals become interrupts

---

#### Item 7b: schedule()

**Location**: `proc.go:3786`

**What it does**:
- Finds a runnable goroutine
- Calls `execute()` to run it
- Loops forever

**Syscalls needed**: None directly (but goroutines may syscall)

**Test**: Verify scheduler finds and runs our main goroutine

---

### Item 8: runtime.main()

**Location**: `proc.go:148`

**What it does**:
```go
func main() {
    mainStarted = true
    if GOARCH != "wasm" {
        systemstack(func() {
            newm(sysmon, nil, -1)  // Start sysmon thread
        })
    }
    lockOSThread()
    // ... init tasks ...
    gcenable()
    // ... more init ...
    main_main()  // Finally call user's main!
}
```

#### Item 8a: newm(sysmon, nil, -1)

**What it does**: Creates new OS thread for system monitor

**Syscalls needed**:
- `clone` (220) - create new thread
- `mmap` - for thread stack

**Scaffolding needed**:
- For single-core: Either stub clone or run sysmon cooperatively
- sysmon does: retake P's, preemption, netpoll, GC pacing

**Implementation options**:
1. Implement clone to actually work (requires SMP support)
2. Stub clone to return error (sysmon won't run, some features degraded)
3. Run sysmon inline periodically (timer interrupt triggers it)

---

#### Item 8b: doInit(runtime_inittasks)

**What it does**: Runs runtime package init functions

**Syscalls needed**: Various (depends on init functions)

**Test**: Verify runtime init completes

---

#### Item 8c: gcenable()

**What it does**: Enables garbage collector

**Syscalls needed**: None directly

**Scaffolding needed**: GC requires:
- Write barriers (DONE)
- Stack scanning (runtime handles)
- STW capability (trivial on single core)

---

#### Item 8d: doInit(main_inittasks)

**What it does**: Runs all package init() functions

**Syscalls needed**: Various (package-dependent)

---

#### Item 8e: main_main()

**What it does**: Calls user's `main.main()`

**Syscalls needed**: Whatever user code needs

---

## Syscall Implementation Tracker

| Syscall | Number | Required By | Status | Notes |
|---------|--------|-------------|--------|-------|
| read | 63 | various | TODO | |
| write | 64 | print/panic | TODO | Route to UART |
| openat | 56 | file ops | TODO | Virtual filesystem |
| close | 57 | file ops | TODO | |
| mmap | 222 | malloc, stack | DONE | Bump + demand paging |
| munmap | 215 | malloc | DONE | No-op |
| mprotect | 226 | stack guard | DONE | No-op |
| madvise | 233 | malloc hints | DONE | No-op |
| brk | 214 | not used | N/A | Go uses mmap |
| clone | 220 | newm (threads) | TODO | See Item 8a |
| futex | 98 | locks | TODO | Spin on single-core |
| sigaction | 134 | signals | TODO | Map to interrupts |
| rt_sigprocmask | 135 | signals | TODO | |
| sigaltstack | 132 | signals | TODO | |
| sched_getaffinity | 204 | osinit | TODO | Return 1 CPU |
| getrandom | 278 | randinit | TODO | Timer-based entropy |
| clock_gettime | 113 | nanotime | TODO | Use ARM timer |
| nanosleep | 101 | time.Sleep | TODO | Timer + WFI |
| exit_group | 94 | os.Exit | TODO | Halt |
| getpid | 172 | various | TODO | Return 1 |
| gettid | 178 | various | TODO | Return 1 |
| tgkill | 131 | signals | TODO | |
| pipe2 | 59 | runtime | TODO | |
| epoll_create1 | 20 | netpoll | TODO | |
| epoll_ctl | 21 | netpoll | TODO | |
| epoll_pwait | 22 | netpoll | TODO | |

## Scaffolding Components

### Virtual Memory
- [x] Page tables (4KB pages)
- [x] Demand paging fault handler
- [ ] Guard pages for stack overflow detection
- [ ] Memory region tracking (for mprotect)

### Interrupts
- [x] Exception vectors
- [x] Timer interrupt
- [ ] Interrupt → signal delivery
- [ ] Preemption via timer

### Threading (if multi-core)
- [ ] Per-CPU data structures
- [ ] Inter-processor interrupts
- [ ] Spinlock implementation

### I/O
- [ ] Virtual filesystem for /proc, /sys
- [ ] UART as stdin/stdout
- [ ] Block device interface

### Device Initialization (Pre-Runtime)

These must be initialized in our bootloader BEFORE jumping to rt0_go,
as the runtime expects them to be available:

- [x] UART - for print/panic output (write syscall)
- [x] Framebuffer - for graphical output
- [ ] Timer - for nanotime, scheduling (already have interrupts, need syscall)
- [ ] RNG - for AT_RANDOM in auxv and getrandom syscall
  - Use ARM generic timer (CNTVCT_EL0) XOR'd with memory addresses
  - Initialize 16 bytes of entropy for AT_RANDOM before args()
  - Provide getrandom syscall for randinit()

## Test Strategy

Each item gets a test function that:
1. Calls the initialization step
2. Verifies expected state changes
3. Exercises functionality that depends on it

Tests are in `test_runtime_init.go` (or similar) and removed once the full
sequence works end-to-end.

Example:
```go
func TestItem5d_mallocinit() {
    // mallocinit already called

    // Test: allocate various sizes
    p1 := new(int)
    *p1 = 42
    if *p1 != 42 {
        panic("heap allocation failed")
    }

    // Test: allocate slice
    s := make([]byte, 4096)
    s[0] = 1
    s[4095] = 2

    print("TestItem5d_mallocinit: PASS\n")
}
```

## Execution Order

Work through items in order. Each item may require:
1. Implementing syscalls
2. Building scaffolding
3. Writing test
4. Verifying test passes
5. Moving to next item

When all items pass individually, run full sequence:
`rt0_go → args → osinit → schedinit → newproc → mstart → runtime.main → main.main`

## Current Position

**Completed**:
- Item 1: g0 stack bounds ✅
- Item 2: g0 ↔ m0 linkage ✅
- Item 3: runtime.args() ✅ (2024-12-18)
- Item 5d: mallocinit ✅ (working via initGoHeap)

**Next**: Item 4 (osinit) - needs sched_getaffinity syscall

---

## Final Milestone: Hello World

When all items are complete, the test is a standard Go program:

```go
// hello.go - compiled with: GOOS=linux GOARCH=arm64 go build hello.go
package main

func main() {
    println("Hello, World!")
}
```

### Embedding the Go Binary (No Filesystem)

Since we have no filesystem or ELF loader, the Go binary must be embedded in our
kernel image. Approach:

```
1. Build hello world externally:
   $ GOOS=linux GOARCH=arm64 go build -o hello hello.go

2. Extract loadable content from ELF:
   $ objcopy -O binary hello hello.bin
   # Or use a script to extract text/data/bss segments with addresses

3. Embed in kernel image:
   # Option A: Include as binary blob at fixed address
   # Option B: Link Go's .o files with our bootloader (complex)
   # Option C: Concatenate bootloader + hello.bin with known layout

4. Boot sequence:
   - Bootloader runs first (our assembly)
   - Sets up MMU, maps hello.bin to correct virtual addresses
   - Sets up exception vectors
   - Jumps to _rt0_arm64_linux entry point in hello.bin
```

**Build system additions needed**:
- Script to extract Go ELF layout (entry point, segment addresses)
- Linker script to place Go binary at correct address
- Or: load Go binary to its preferred address and jump to entry

**Memory layout** (example):
```
0x40000000  Our bootloader code (small, <64KB)
0x40100000  Go binary (hello.bin) loaded here
            - .text, .rodata, .data, .bss
            - Runtime expects specific layout
0x50000000+ Heap arena (mmap returns addresses here)
```

**Key insight**: The Go binary is self-contained. It includes:
- `_rt0_arm64_linux` entry point
- Full runtime (g0, m0, scheduler, GC, etc.)
- `main.main` user code

Our bootloader just needs to get it into memory and jump to it.

### What this proves

- `rt0_go` runs successfully (Items 1-2)
- `args()` and `osinit()` work (Items 3-4)
- `schedinit()` completes (Item 5 - heap, stacks, GC init, P creation)
- `newproc()` creates main goroutine (Item 6)
- `mstart()` enters scheduler (Item 7)
- `runtime.main()` runs init and calls main.main (Item 8)
- `println()` works (write syscall to UART)
- Program exits cleanly (exit_group syscall)

**Boot sequence when complete**:
```
Hardware reset
    ↓
Our bootloader (assembly, ~1-2KB)
    - Set up MMU with identity mapping
    - Map Go binary to its expected addresses
    - Set up exception vectors (for syscalls)
    - Set up initial stack for rt0_go
    - Jump to _rt0_arm64_linux
    ↓
Go runtime (unmodified, embedded in image)
    - rt0_go sets up g0/m0
    - schedinit initializes everything (syscalls to our handlers)
    - newproc creates main goroutine
    - mstart runs scheduler
    - runtime.main → main.main
    ↓
"Hello, World!" on UART (via write syscall)
    ↓
exit_group syscall → halt/reboot
```

**Scaffolding removed at this point**:
- [x] initRuntimeStubs()
- [x] initGoHeap()
- [x] createKernelGoroutine()
- [x] Manual g0/m0 setup in lib.s
- [x] KernelMain() - replaced by rt0_go calling main.main
- [x] All Go code in src/mazboot/golang/main/ (except syscall handlers)

**What remains**:
- Minimal bootloader assembly (~1-2KB)
- exceptions.s with syscall handlers
- Page table setup (pre-rt0_go)
- Demand paging fault handler
- UART driver (called by write syscall handler)
