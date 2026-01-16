# Multi-Core Preemption Design

This document outlines the changes needed to support multi-core (SMP) operation for the kmazarin kernel's preemption system.

## Current Single-Core Architecture

The current implementation uses global variables that assume single-core execution:

```go
// kirq/preempt.go
var NeedsAsyncPreempt uint32        // Flag for async preemption
var PreemptOffsetsValid uint32      // Whether offsets are initialized

// kmazarin/main.go
var currentThread *Thread           // Current executing thread
```

Timer tracking is stored in the `Thread` struct:
```go
type Thread struct {
    // ...
    LastSeenG  uintptr  // Last goroutine seen by timer (offset 312)
    StartTick  uint64   // When this goroutine started running (offset 320)
}
```

This works because:
- All memory accesses are sequentially consistent on one core
- Timer interrupts only affect the single core's state
- No cross-core visibility concerns

## Multi-Core Requirements

### 1. Per-CPU Data Structure

Each CPU needs its own independent state:

```go
// Maximum supported CPUs (ARM64 typically supports up to 256)
const MaxCPUs = 8

// PerCPU holds all per-processor state
type PerCPU struct {
    // Preemption state
    NeedsAsyncPreempt uint32    // Set by timer when threshold exceeded
    PreemptOffsetsValid uint32  // Whether g struct offsets are known

    // Goroutine tracking for preemption timing
    LastSeenG  uintptr          // Last goroutine seen on this CPU
    StartTick  uint64           // When current goroutine started

    // Current execution context
    CurrentThread *Thread       // Thread running on this CPU

    // Stack pointers
    ExceptionStackTop uintptr   // SP_EL1 for this CPU

    // Padding to avoid false sharing (align to cache line)
    _pad [64 - 40]byte          // Pad to 64 bytes (typical cache line)
}

// CPU-local data indexed by CPU ID
var perCPU [MaxCPUs]PerCPU

// Ensure cache-line alignment to prevent false sharing
// Each PerCPU should be 64-byte aligned
```

### 2. CPU ID Access

ARM64 provides the CPU ID via `MPIDR_EL1`:

```asm
// GetCPUID returns the current CPU's ID (0-based)
// func GetCPUID() uint64
TEXT ·GetCPUID(SB), NOSPLIT, $0-8
    // MRS X0, MPIDR_EL1
    WORD    $0xD53800A0
    // Extract Aff0 (bits 0-7) for CPU ID within cluster
    AND     $0xFF, R0
    MOVD    R0, ret+0(FP)
    RET
```

For accessing per-CPU data in assembly:

```asm
// Macro to get per-CPU data pointer into Rd
// Clobbers R0, R1
// Result: Rd = &perCPU[cpuid]

// Step 1: Get CPU ID
WORD    $0xD53800A0          // mrs x0, MPIDR_EL1
AND     $0xFF, R0            // R0 = CPU ID

// Step 2: Calculate offset (each PerCPU is 64 bytes)
LSL     $6, R0, R0           // R0 = cpuid * 64

// Step 3: Add base address
MOVD    $·perCPU(SB), R1
ADD     R0, R1, Rd           // Rd = &perCPU[cpuid]
```

### 3. Modified Timer Handler

The timer handler needs to access per-CPU data instead of globals:

```asm
TEXT ·TimerIRQHandlerAsm(SB), NOSPLIT|NOFRAME, $0
    // Get per-CPU data pointer
    WORD    $0xD53800A0          // mrs x0, MPIDR_EL1
    AND     $0xFF, R0
    LSL     $6, R0, R0           // R0 = cpuid * 64
    MOVD    $·perCPU(SB), R1
    ADD     R0, R1, R9           // R9 = &perCPU[cpuid] (keep in R9)

    // Re-arm timer (same as before)
    // ...

    // Check PreemptOffsetsValid from per-CPU struct
    MOVW    4(R9), R0            // perCPU.PreemptOffsetsValid
    CBZ     R0, timer_return

    // ... rest of handler uses R9 as base for per-CPU access ...

    // Set NeedsAsyncPreempt in per-CPU struct
    MOVW    $1, R8
    MOVW    R8, 0(R9)            // perCPU.NeedsAsyncPreempt = 1

timer_return:
    RET
```

### 4. Memory Ordering for Shared Data

For any data shared between CPUs (not per-CPU), use proper atomic operations:

#### Store-Release (STLR)
Makes all prior writes visible before this store completes:
```asm
// STLR Wt, [Xn]
// Encoding: 0x889FFC00 | (Rn << 5) | Rt
// Example: stlr w0, [x1]
WORD    $0x889FFC20          // stlr w0, [x1]
```

#### Load-Acquire (LDAR)
Ensures this load completes before any subsequent memory access:
```asm
// LDAR Wt, [Xn]
// Encoding: 0x88DFFC00 | (Rn << 5) | Rt
// Example: ldar w0, [x1]
WORD    $0x88DFFC20          // ldar w0, [x1]
```

#### Compare-And-Swap (for lock-free structures)
```asm
// LDXR/STXR pair for atomic CAS
// Example: atomically set *x1 = x0 if *x1 == x2
retry:
    WORD    $0x885F7C40      // ldxr w0, [x1] (load exclusive)
    CMP     R0, R2
    BNE     fail
    WORD    $0x88007C20      // stxr w0, w0, [x1] (store exclusive)
    CBNZ    R0, retry        // Retry if store failed
    // Success
fail:
```

### 5. Per-CPU Exception Stacks

Each CPU needs its own exception stack:

```go
// In memory layout, allocate per-CPU stacks
const ExceptionStackSize = 128 * 1024  // 128KB per CPU

// Stack layout (growing down):
// CPU 0: 0x5F000000 - 0x5F020000
// CPU 1: 0x5F020000 - 0x5F040000
// CPU 2: 0x5F040000 - 0x5F060000
// etc.

func GetExceptionStackTop(cpuid uint64) uintptr {
    return 0x5F000000 + uintptr((cpuid+1)*ExceptionStackSize)
}
```

During CPU startup, each CPU sets its own SP_EL1:
```asm
// Called during secondary CPU init
// R0 = CPU ID
TEXT ·InitCPUStack(SB), NOSPLIT, $0
    // Calculate stack top: 0x5F000000 + (cpuid+1) * 0x20000
    ADD     $1, R0
    LSL     $17, R0, R0          // * 0x20000 (128KB)
    MOVD    $0x5F000000, R1
    ADD     R0, R1, R0

    // Set SP_EL1 (we're in EL1h mode during init)
    MOV     R0, SP
    RET
```

### 6. GIC Configuration for Multi-Core

The GIC needs to route interrupts appropriately:

```go
// Route timer interrupt to all CPUs (each CPU gets its own timer)
// PPI (Private Peripheral Interrupt) 27 is automatically per-CPU

// For shared interrupts (SPIs like UART), route to specific CPU:
func SetInterruptAffinity(irq uint32, targetCPU uint32) {
    // GICD_ITARGETSR registers (offset 0x800)
    // Each byte targets one IRQ to CPUs (bit N = CPU N)
    regOffset := 0x800 + (irq & ^uint32(3))
    byteOffset := irq & 3

    current := mmioRead32(GICD_BASE + uintptr(regOffset))
    mask := uint32(0xFF) << (byteOffset * 8)
    target := uint32(1<<targetCPU) << (byteOffset * 8)

    mmioWrite32(GICD_BASE+uintptr(regOffset), (current & ^mask) | target)
}
```

### 7. UART Ring Buffer Synchronization

For multi-core UART access, either:

**Option A: Per-CPU TX Buffers (Recommended)**
```go
type PerCPU struct {
    // ...
    TxRingBuffer [4096]byte
    TxRingHead   uint32
    TxRingTail   uint32
}
// Single RX buffer with spinlock (input is serialized anyway)
```

**Option B: Spinlock on Shared Buffer**
```go
var uartLock uint32  // 0 = unlocked, 1 = locked

func acquireUARTLock() {
    for {
        // LDAXR/STXR spinlock
        if atomicCAS(&uartLock, 0, 1) {
            return
        }
        // Spin with WFE for power efficiency
        hint_wfe()
    }
}

func releaseUARTLock() {
    atomicStore(&uartLock, 0)
    hint_sev()  // Wake waiting CPUs
}
```

### 8. Secondary CPU Startup

To bring up additional CPUs:

```go
func StartSecondaryCPUs() {
    for cpuid := uint64(1); cpuid < NumCPUs; cpuid++ {
        // Write entry point to spin-table or use PSCI
        startSecondary(cpuid, secondaryCPUEntry)
    }
}

// Entry point for secondary CPUs
func secondaryCPUEntry() {
    cpuid := GetCPUID()

    // Initialize per-CPU state
    InitCPUStack(cpuid)
    InitPerCPUData(cpuid)

    // Enable interrupts
    EnableLocalInterrupts()

    // Enter scheduler
    schedule()
}
```

## Migration Path

### Phase 1: Per-CPU Data Structure (No Functional Change)
1. Create `PerCPU` struct with all current global state
2. Add `GetCPUID()` function
3. Modify timer handler to use `perCPU[0]` explicitly
4. Test single-core still works

### Phase 2: Dynamic CPU ID Lookup
1. Modify timer handler to look up CPU ID and index into `perCPU`
2. Modify exception handler similarly
3. Still single-core, but code is multi-core ready

### Phase 3: Secondary CPU Startup
1. Add per-CPU exception stacks
2. Implement secondary CPU init sequence
3. Test with 2 CPUs

### Phase 4: Shared Resource Locking
1. Add spinlocks for UART and other shared resources
2. Ensure scheduler run queues have proper synchronization
3. Test with full multi-core workload

## Appendix: ARM64 Atomic Instruction Encodings

For reference, correct encodings for atomic operations:

| Instruction | Encoding | Description |
|-------------|----------|-------------|
| `LDAR Wt, [Xn]` | `0x88DFFC00 \| (Rn<<5) \| Rt` | Load-acquire (32-bit) |
| `LDAR Xt, [Xn]` | `0xC8DFFC00 \| (Rn<<5) \| Rt` | Load-acquire (64-bit) |
| `STLR Wt, [Xn]` | `0x889FFC00 \| (Rn<<5) \| Rt` | Store-release (32-bit) |
| `STLR Xt, [Xn]` | `0xC89FFC00 \| (Rn<<5) \| Rt` | Store-release (64-bit) |
| `LDXR Wt, [Xn]` | `0x885F7C00 \| (Rn<<5) \| Rt` | Load-exclusive (32-bit) |
| `STXR Ws, Wt, [Xn]` | `0x88007C00 \| (Rs<<16) \| (Rn<<5) \| Rt` | Store-exclusive (32-bit) |

Example for `STLR W8, [X9]`:
- Rt = 8, Rn = 9
- Encoding = `0x889FFC00 | (9 << 5) | 8` = `0x889FFD28`
