# Multi-Architecture HAL Design for Kmazarin

## Overview

This document analyzes the feasibility and design of supporting multiple CPU
architectures (ARM64, x86_64, RISC-V) in the kmazarin kernel. It draws on
the experience of porting the diplomat UEFI bootloader from x86_64 to ARM64,
which used function pointer tables (`PlatformOps`, `BootSequence`) to abstract
architecture-specific implementations.

**Key conclusion:** Diplomat's function-pointer-table approach worked because
UEFI is itself a table of function pointers. For a kernel, where hardware
calls *into* your code (exceptions, interrupts), the right abstraction is
Go's build tag system with clean interface boundaries — not runtime function
pointer indirection.

---

## Current Architecture Footprint

### Code Volume

| Category | Lines | Percentage |
|----------|------:|------------|
| Go code (non-test) | 25,697 | ~82% |
| Go code (tests) | 5,581 | ~18% of Go |
| Assembly (all ARM64) | 4,460 | ~15% of non-test |
| Go files with ARM64 references | 81 files | — |
| Go files with `//go:build arm64` | 27 files | — |

### Assembly Breakdown

| File | Lines | Purpose |
|------|------:|---------|
| `kmazarin/exceptions_arm64.s` | 2,237 | Exception vectors, SVC dispatch, abort handling |
| `kirq/preempt_arm64.s` | 420 | Async preemption, register save/restore |
| `ksyscall/abi_stubs_arm64.s` | 360 | ABI0 tail-call stubs |
| `kmem/asm_barriers_arm64.s` | 228 | Memory barriers (DMB, DSB, ISB), TTBR access |
| `kmazarin/smp_entry_arm64.s` | 174 | Secondary CPU boot entry |
| `arch/arm64/gic/gicv2_arm64.s` | 18 | GIC barrier stubs |
| `ds/spinlock_arm64.s` | 145 | Atomic CAS, store-release |
| `kmazarin/gic_arm64.s` | 146 | GIC register access |
| `ksyscall/launch_arm64.s` | 101 | Thread launch, initial context |
| `kmazarin/psci_arm64.s` | 91 | PSCI firmware calls (CPU_ON) |
| `kmazarin/panic_arm64.s` | 81 | Panic handler, stack dump |
| `kmazarin/runtime_arm64.s` | 75 | Runtime support stubs |
| `kmazarin/percpu_arm64.s` | 65 | Per-CPU data access (MPIDR) |
| `kirq/timer_arm64.s` | 46 | Timer register access |
| `asm/mmio_arm64.s` | 126 | MMIO read/write utilities |
| Other small stubs | ~60 | Various |
| **Total** | **4,460** | |

---

## Architecture-Specific Subsystems

There are 7 subsystems that need per-architecture implementations. Everything
else (scheduler, ready queues, work stealing, syscall logic, futex, VirtIO
drivers, FAT32, device tree parsing) is architecture-neutral Go code that
works through these interfaces.

### 1. Exception Entry and Dispatch (~2,200 lines asm)

The largest single piece. Hardware dictates the entry mechanism — you cannot
abstract "how does the CPU enter an exception handler" behind a function
pointer. The CPU jumps to a fixed address.

**ARM64:** VBAR_EL1 points to a vector table with 16 entries (4 exception
types x 4 source ELs). Each vector saves all registers to an exception frame
on SP_EL1, then calls into Go.

**x86_64 equivalent:** IDT (Interrupt Descriptor Table) with 256 entries.
Each ISR stub pushes an interrupt frame, swaps to kernel stack, calls into Go.

**RISC-V equivalent:** `mtvec`/`stvec` register points to trap handler.
Single entry point, `mcause`/`scause` determines exception type.

**Boundary with Go code (the key interface):**

```go
// These functions are called FROM assembly after register save.
// They are the arch-neutral entry points into the kernel.

// Syscall dispatch — called from SVC/SYSCALL/ECALL handler
func SyscallDispatch(syscallNum, a0, a1, a2, a3, a4, a5 uint64) int64

// IRQ dispatch — called from IRQ handler
func irqDispatchInternal(irqNum, framePtr, elr, spEl0 uint64) (
    newELR, newSP, newLR uint64, doPreempt bool)

// Page fault — called from data/instruction abort handler
func HandlePageFaultAsm(faultAddr uint64) uint64

// Thread preemption check — called from timer IRQ path
func CheckThreadPreemption(framePtr uint64) uint64

// Syscall state capture — called before syscall dispatch
func SetSyscallELR(elr uint64)
func SetSyscallSPSR(spsr uint64)
```

The assembly is architecture-specific but these Go functions are not. This is
the critical abstraction boundary.

### 2. Context Switch (~420 lines asm)

Saves/restores the full register set for thread switching.

**ARM64:** 31 general registers (X0-X30) + SP + ELR_EL1 + SPSR_EL1.

**x86_64:** 16 general registers (RAX-R15) + RIP + RFLAGS + RSP + segment
registers. Also needs to save/restore SSE/AVX state (FXSAVE/XSAVE).

**RISC-V:** 32 general registers (x0-x31) + PC + sstatus.

**Current ThreadContext struct (ARM64-specific):**

```go
type ThreadContext struct {
    X    [31]uint64 // General purpose registers x0-x30
    SP   uint64     // SP_EL0
    ELR  uint64     // Return address (ELR_EL1)
    SPSR uint64     // Processor state (SPSR_EL1)
}
```

**Abstraction approach:** Each architecture defines its own `ThreadContext`
in a `_arm64.go` / `_amd64.go` file. The scheduler doesn't inspect context
internals — it just passes opaque `*ThreadContext` pointers to the
architecture-specific save/restore assembly.

**Go interface boundary:**

```go
// Called from assembly, returns pointer to next thread's context
func DoContextSwitch(framePtr, targetPtr uintptr) *ThreadContext
func GetSyscallSwitchTarget() uintptr
```

### 3. Interrupt Controller (~500 lines Go + asm)

**ARM64:** GICv2 (Generic Interrupt Controller) — MMIO registers at
GICD/GICC base addresses discovered from device tree.

**x86_64:** Local APIC + I/O APIC — MMIO at 0xFEE00000 (LAPIC) or MSR-based
(x2APIC). I/O APIC at address from ACPI MADT table.

**RISC-V:** PLIC (Platform-Level Interrupt Controller) — MMIO registers,
base from device tree.

**Existing Go interface (already architecture-neutral):**

```go
type InterruptController interface {
    RegisterHandler(irq uint32, handler func())
    EnableIRQ(irq uint32)
    DisableIRQ(irq uint32)
    SetIRQTarget(irq uint32, cpuMask uint8)
    SetIRQEdgeTriggered(irq uint32)
    SetIRQPriority(irq uint32, priority uint8)
    DispatchIRQ()
    DumpIRQState(irq uint32)
}
```

This interface already abstracts the hardware. GICv2, APIC, and PLIC would
each implement it. The `arch/` directory is the right place:

```
arch/arm64/gic/gicv2.go      # existing
arch/amd64/apic/apic.go      # new
arch/riscv64/plic/plic.go    # new
```

### 4. Page Tables (~2,500 lines Go + ~230 lines asm)

All three architectures use multi-level page tables with similar structure
but different formats.

**ARM64:** 4-level (L0-L3), TTBR0_EL1 (user) + TTBR1_EL1 (kernel),
descriptor format with block/table/page types, ASID in TTBR0 bits [63:48].

**x86_64:** 4-level (PML4-PT) or 5-level, CR3 register, PTE format with
Present/Writable/User/NX bits.

**RISC-V:** Sv39 (3-level) or Sv48 (4-level), `satp` register, PTE format
with Valid/Read/Write/Execute bits.

**Current ARM64 API:**

```go
func GetTTBR0L0PA() uintptr
func CreateProcessPageTable() uintptr
func SwitchToProcessPageTableWithL0(l0PA uintptr)
func SwitchTTBR0WithASID(l0PA uintptr, asid uint16)

// Assembly helpers
func readTTBR0EL1() uintptr
func readTTBR1EL1() uintptr
func writeTTBR0Asm(val uint64)
func tlbiVale1is()    // TLB invalidation
```

**Abstraction approach:** The page table *format* differs per architecture,
but the *operations* are the same. Each architecture defines:

```go
// Per-architecture, selected by build tag
func ReadPageTableBase() uintptr
func WritePageTableBase(val uint64)
func CreateProcessPageTable() uintptr
func SwitchToProcessPageTable(ptBase uintptr)
func InvalidateTLBEntry(va uintptr)
func InvalidateTLBAll()
func MapPage(va, pa uintptr, flags uint64) error
func UnmapPage(va uintptr) error
```

The page table entry format, constants, and internal structure stay
per-architecture in `kmem/paging_arm64.go`, `kmem/paging_amd64.go`, etc.

### 5. SMP Boot (~350 lines Go + asm)

Completely different mechanism per architecture.

**ARM64:** PSCI firmware interface (HVC/SMC instruction). Call
`PSCI_CPU_ON(cpuID, entryPoint, contextID)` to wake a core.

**x86_64:** SIPI (Startup Inter-Processor Interrupt) sequence. Write to
LAPIC ICR register: INIT IPI → 10ms delay → SIPI with entry vector.

**RISC-V:** SBI HSM (Hart State Management) extension. Call
`sbi_hart_start(hartid, start_addr, opaque)`.

**Current API:**

```go
func StartSecondaryCPUs() int
func WakeCPU(cpuID, entryPoint, contextId uint64) int64
func GetPsciVersion() (major, minor uint16)
func GetCPUPowerState(cpuID uint64) int64
func IsCPUOnline(cpuID uint64) bool
func GetOnlineCPUCount() int
```

**Abstraction approach:** The boot mechanism is completely different but the
Go-level API is the same. Each architecture implements `StartSecondaryCPUs()`
and `WakeCPU()` differently. The secondary CPU entry assembly
(`smp_entry_arm64.s`) sets up per-CPU stacks and calls
`secondaryCPUEntry()` — a shared Go function.

### 6. Timer (~100 lines asm, ~200 lines Go)

**ARM64:** Generic timer — `CNTPCT_EL0` (counter), `CNTP_TVAL_EL0` (compare),
`CNTFRQ_EL0` (frequency). Timer IRQ is PPI 30.

**x86_64:** LAPIC timer (per-CPU, one-shot or periodic), calibrated against
PIT or TSC. Or HPET for system-wide timing.

**RISC-V:** SBI timer extension. `rdtime` instruction for counter,
`sbi_set_timer()` for next interrupt.

**Current API:**

```go
func InitTimer()
func GetTimerFrequency() uint32
func ReadCounterValue() uint64
func rearmTimer(ticks int32)
```

This API is already clean enough to implement per-architecture with build
tags.

### 7. Spinlocks and Memory Barriers (~370 lines asm)

**ARM64:** `LDAXRW`/`STLXRW` (load-acquire exclusive / store-release
exclusive), `DMB`/`DSB`/`ISB` barriers, `CNTVCT_EL0` for timing.

**x86_64:** `LOCK CMPXCHG` (atomic CAS), `MFENCE`/`LFENCE`/`SFENCE`
barriers, `RDTSC` for timing. x86 has a stronger memory model (TSO) so
fewer barriers are needed.

**RISC-V:** `LR`/`SC` (load-reserved / store-conditional) or atomic AMO
instructions, `fence` instruction for barriers.

**Current API:**

```go
// In ds/spinlock_arm64.s
func CompareAndSwapUint32(addr *uint32, old, new uint32) uint64
func StoreUint32(addr *uint32, val uint32)
func CurrentTime(fakeTime uint64) uint64
func nanoWait(ticks uint64)

// In kmem/asm_barriers_arm64.s
func Dsb()  // Data synchronization barrier
func Dmb()  // Data memory barrier
func Isb()  // Instruction synchronization barrier
```

These have direct equivalents on every architecture.

---

## Per-CPU Identification

Each architecture identifies the current CPU differently:

| Architecture | Register | Extraction |
|-------------|----------|------------|
| ARM64 | MPIDR_EL1 | `Aff0` bits [7:0] |
| x86_64 | LAPIC ID | Read from `APIC_BASE + 0x20`, or CPUID leaf 0x0B |
| RISC-V | `mhartid` CSR | Direct read, or `tp` register by convention |

**Current API:**

```go
func GetCPUID() uint64   // Returns 0-based CPU index
func readMPIDRAsm() uint64
```

The `GetCPUID()` function is the right abstraction. Each architecture
implements `readMPIDRAsm()` / `readLAPICId()` / `readHartId()` and maps
it to a 0-based index.

---

## Existing Interfaces (Already Architecture-Neutral)

Kmazarin already has some architecture-neutral interfaces that would carry
over unchanged:

```go
// Console output
type Console interface {
    KWrite(p []byte) int
    KWriteString(s string)
    KPrintf(format string, args ...interface{})
    KErrPrintf(format string, args ...interface{})
    KPrintHex(value interface{})
    Breadcrumb(b byte)
}

// Interrupt controller
type InterruptController interface {
    RegisterHandler(irq uint32, handler func())
    EnableIRQ(irq uint32)
    DisableIRQ(irq uint32)
    SetIRQTarget(irq uint32, cpuMask uint8)
    SetIRQEdgeTriggered(irq uint32)
    SetIRQPriority(irq uint32, priority uint8)
    DispatchIRQ()
    DumpIRQState(irq uint32)
}

// IRQ handler types
type IRQHandlerSimple func(irqNum uint64)
type IRQHandlerCanPreempt func(
    irqNum uint64, framePtr uintptr, elr, spEl0 uint64,
) PreemptInfo
```

---

## Assembly-to-Go Boundary (The Critical Interface)

The exception assembly calls into Go using ABI0 stack-based calling through
macros (`GO_CALL_N_M`). These macros handle frame setup:

```
GO_CALL_7_1  → 7 args, 1 return (syscall dispatch)
GO_CALL_4_4  → 4 args, 4 returns (IRQ dispatch)
GO_CALL_2_1  → 2 args, 1 return (context switch)
GO_CALL_1_1  → 1 arg, 1 return (page fault)
```

The Go functions called from assembly are the same regardless of
architecture. Only the assembly that *calls* them differs. This means:

- `SyscallDispatch()` is shared Go code
- `irqDispatchInternal()` is shared Go code
- `HandlePageFaultAsm()` is shared Go code
- `CheckThreadPreemption()` is shared Go code

The assembly on each architecture saves registers to a frame, extracts the
relevant parameters (syscall number, fault address, etc.), and calls the
same Go functions with the same signatures.

---

## Proposed Directory Structure

```
kmazarin/
  kmazarin/
    threads.go              # Scheduler, queues — arch-neutral
    threads_arm64.go        # ThreadContext struct, SPSR/ELR helpers
    threads_amd64.go        # ThreadContext struct, RFLAGS/RIP helpers
    exceptions_arm64.s      # ARM64 vector table, register save
    exceptions_amd64.s      # x86_64 IDT stubs, register save
    smp.go                  # StartSecondaryCPUs — arch-neutral API
    smp_arm64.go            # PSCI-based CPU wake
    smp_amd64.go            # SIPI-based CPU wake
    percpu.go               # PerCPU struct — arch-neutral
    percpu_arm64.go         # MPIDR-based GetCPUID
    percpu_amd64.go         # LAPIC-based GetCPUID
    percpu_arm64.s          # readMPIDRAsm
    percpu_amd64.s          # readLAPICId

  kmem/
    paging.go               # MapPage/UnmapPage API — arch-neutral
    paging_arm64.go         # ARM64 L0-L3 format, TTBR, ASID
    paging_amd64.go         # x86_64 PML4 format, CR3
    asm_barriers_arm64.s    # DMB/DSB/ISB
    asm_barriers_amd64.s    # MFENCE/LFENCE/SFENCE

  ksyscall/
    dispatch.go             # SyscallDispatch — arch-neutral
    dispatch_arm64.s        # SVC entry point
    dispatch_amd64.s        # SYSCALL entry point
    launch_arm64.s          # Thread launch
    launch_amd64.s          # Thread launch

  kirq/
    timer.go                # Timer API — arch-neutral
    timer_arm64.go          # Generic timer implementation
    timer_amd64.go          # LAPIC timer implementation
    preempt_arm64.s         # ARM64 preemption assembly
    preempt_amd64.s         # x86_64 preemption assembly

  ds/
    spinlock.go             # Spinlock Go API — arch-neutral
    spinlock_arm64.s        # LDXR/STXR CAS
    spinlock_amd64.s        # LOCK CMPXCHG

  arch/
    arm64/gic/gicv2.go      # GICv2 driver
    amd64/apic/apic.go      # APIC driver
    riscv64/plic/plic.go    # PLIC driver (future)
```

---

## x86_64 Port: Work Estimate

| Subsystem | New Assembly | New/Modified Go | Notes |
|-----------|------------:|----------------:|-------|
| Exception entry (IDT) | ~1,500 | ~200 | Largest piece |
| Context switch | ~300 | ~100 | Simpler register set but FPU state |
| APIC driver | ~100 asm | ~400 | Replace GICv2 |
| Page tables (PML4/CR3) | ~150 asm | ~800 | Different format, same ops |
| SMP boot (SIPI) | ~200 | ~200 | AP trampoline in real mode |
| Timer (LAPIC/TSC) | ~50 | ~150 | Calibration needed |
| Spinlocks/barriers | ~80 | ~20 | Simpler on x86 (TSO) |
| Per-CPU (LAPIC ID) | ~30 | ~50 | |
| ABI stubs | ~200 | 0 | x86_64 Go ABI is different |
| Thread launch | ~80 | ~50 | |
| **Total** | **~2,700** | **~1,970** | |

The x86_64 port is roughly 4,700 lines of new architecture-specific code.
The remaining ~25,000 lines of Go (scheduler, syscall logic, VirtIO drivers,
filesystem, device tree) would be shared unchanged.

---

## Why Not Function Pointer Tables

Diplomat's HAL uses `PlatformOps` — a struct of function pointers assigned
at boot. This works for a bootloader but is wrong for a kernel:

1. **Hardware calls you.** Exception vectors, IDT entries, and trap handlers
   are addresses baked into CPU registers (VBAR_EL1, IDTR, stvec). The CPU
   jumps there directly. No function pointer indirection is possible.

2. **Hot path overhead.** Every syscall, every context switch, every
   interrupt would go through an indirect call. The Go compiler cannot
   inline or optimize indirect calls. On a kernel hot path this matters.

3. **Build tags are free.** Go's `_arm64.go` / `_amd64.go` filename
   convention resolves everything at compile time. The compiled kernel
   contains only one architecture's code with direct calls throughout.

4. **Diplomat's naming problem.** The `PlatformOps` struct has fields named
   `ReadCR3`, `WriteCR3`, `DisableWriteProtect` — x86 concepts that don't
   exist on ARM64. We papered over this with wrapper functions. In a kernel
   with many more operations, this naming mismatch would be pervasive.

The right abstraction for a kernel is:
- **Go interfaces** for hardware that has interchangeable drivers
  (InterruptController, Console)
- **Build-tag-separated implementations** for everything else
- **Consistent function signatures** across architectures so the shared Go
  code calls the same function names regardless of target

---

## RISC-V Considerations

RISC-V would follow the same pattern as x86_64. Key differences:

| Subsystem | RISC-V Equivalent |
|-----------|-------------------|
| Exception entry | `stvec` → single trap handler, `scause` for type |
| Context switch | 32 GPRs (x0-x31) + `sepc` + `sstatus` |
| Interrupt controller | PLIC (Platform-Level Interrupt Controller) |
| Page tables | Sv39 (3-level) or Sv48 (4-level), `satp` register |
| SMP boot | SBI HSM extension (`sbi_hart_start`) |
| Timer | SBI timer (`sbi_set_timer`), `rdtime` instruction |
| Atomics | LR/SC (load-reserved / store-conditional) |
| Per-CPU ID | `mhartid` CSR or `tp` register |
| Memory model | RVWMO (weak, similar to ARM64 — needs fences) |

RISC-V assembly tends to be simpler than ARM64 (single trap entry point
vs 16 vector entries, simpler privilege model) but the SBI firmware layer
adds indirection similar to ARM64's PSCI.

Go supports `GOARCH=riscv64` and the RISC-V UEFI ecosystem (EDK2 on QEMU)
is functional, so both the diplomat UEFI path and a bare-metal cardinal
path are viable.

---

## Summary

Kmazarin can support multiple architectures. The work is:

1. **~4,700 lines per new architecture** (assembly + arch-specific Go)
2. **7 subsystems** to implement per architecture
3. **~25,000 lines of shared Go** that works unchanged
4. **Build tags, not function pointers** — compile-time selection, not runtime

The architecture-neutral core (scheduler, syscalls, drivers, filesystem) is
already well-separated from the hardware-specific layers. The main work is
writing and debugging the exception handling assembly, which is inherently
per-architecture and represents roughly half the total effort.
