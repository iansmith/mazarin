# AArch64 Interrupt Implementation Plan for Mazarin Kernel

**Status**: Design Document  
**Target**: QEMU aarch64-virt machine  
**Priority**: IRQs (System Timer) → UART → Framebuffer

---

## Table of Contents

1. [Overview](#overview)
2. [AArch64 Exception Handling Architecture](#aarch64-exception-handling-architecture)
3. [Memory Layout and Exception Vectors](#memory-layout-and-exception-vectors)
4. [GIC (Generic Interrupt Controller) Integration](#gic-generic-interrupt-controller-integration)
5. [System Timer (ARM Generic Timer)](#system-timer-arm-generic-timer)
6. [Implementation Phases](#implementation-phases)
7. [Code Structure](#code-structure)
8. [Key Differences from ARM32](#key-differences-from-arm32)

---

## Overview

The Mazarin kernel will implement AArch64 exception handling to support:
- **IRQs** from hardware (timer, UART, etc.)
- **Exceptions** (data abort, instruction abort, undefined instruction)
- **System calls** (supervisor calls - SVC)

**Current scope**: Single CPU (EL1 - kernel execution level), reliable interrupt handling for system timer and UART.

**Hardware**:
- QEMU aarch64-virt machine
- ARM Generic Timer (integrated into CPU cores)
- Generic Interrupt Controller (GIC) at `0x08000000`
- PL011 UART at `0x09000000`

---

## AArch64 Exception Handling Architecture

### Exception Levels
AArch64 uses **Exception Levels** instead of processor modes:
- **EL0**: User mode (applications)
- **EL1**: Kernel mode (OS kernel)
- **EL2**: Hypervisor mode (not used here)
- **EL3**: Secure monitor mode (not used here)

We run the Mazarin kernel at **EL1**.

### Key Registers (at EL1)

| Register | Purpose |
|----------|---------|
| `SPSR_EL1` | Saved Program Status Register (stores PSTATE when exception occurs) |
| `ELR_EL1` | Exception Link Register (return address) |
| `ESR_EL1` | Exception Syndrome Register (exception details) |
| `FAR_EL1` | Fault Address Register (faulting address for page faults) |
| `SP_EL1` | Stack pointer for EL1 |
| `VBAR_EL1` | Vector Base Address Register (address of exception vectors) |

### PSTATE (Status bits in SPSR)
- **bits [31:10]**: Reserved
- **bit [9]**: PAN (Privileged Access Never)
- **bit [8]**: E (Endianness)
- **bit [7:6]**: DAIFset (interrupts: D=debug, A=SError, I=IRQ, F=FIQ)
- **bits [5:0]**: M[5:0] (execution mode - 0 = EL0t, 4 = EL1t, etc.)

**Important**: To disable/enable interrupts, we set/clear the **I bit** (bit 7) in PSTATE.

---

## Memory Layout and Exception Vectors

### Address Space (QEMU aarch64-virt)
```
0x00000000 - 0x08000000     ROM/Flash region (kernel loaded at 0x200000)
0x08000000 - 0x08010000     GIC (Generic Interrupt Controller)
0x09000000 - 0x09010000     UART0 (PL011)
0x40000000 - 0x40100000     DTB (Device Tree Blob) - 1MB
0x40100000 - 0x41000000     BSS section (after DTB)
0x41000000 - 0x60000000     Heap and available space
0x60000000                  Stack top (grows downward from here)
0x41000000                  Stack bottom (16MB into RAM)
```

**Note**: Stack grows downward from 0x60000000 to 0x41000000 (496MB available). BSS comes after DTB at 0x40100000.

### Exception Vector Table Layout

The exception vector table **must be 2KB aligned** and contains 4 groups of 4 exception vectors (128 bytes per group):

```
VBAR_EL1 + 0x000    Group 1: Current EL, using SP_EL0
  0x000 - 0x080       Synchronous exception
  0x080 - 0x100       IRQ
  0x100 - 0x180       FIQ
  0x180 - 0x200       SError

VBAR_EL1 + 0x200    Group 2: Current EL, using SP_EL1
  0x200 - 0x280       Synchronous exception
  0x280 - 0x300       IRQ
  0x300 - 0x380       FIQ
  0x380 - 0x400       SError

VBAR_EL1 + 0x400    Group 3: Lower EL, AArch64
  0x400 - 0x480       Synchronous exception
  0x480 - 0x500       IRQ
  0x500 - 0x580       FIQ
  0x580 - 0x600       SError

VBAR_EL1 + 0x600    Group 4: Lower EL, AArch32
  0x600 - 0x680       Synchronous exception
  0x680 - 0x700       IRQ
  0x700 - 0x780       FIQ
  0x780 - 0x800       SError
```

**For our kernel at EL1**:
- We will use **Group 2** (Current EL with SP_EL1)
- Exception handlers will be 128 bytes each (7 instructions + padding)
- Handlers must fit within their 128-byte slot

### Exception Entry Point Order

Each 128-byte entry point holds one exception type:
1. **Synchronous** (instruction abort, data abort, system call, etc.)
2. **IRQ** (interrupt request)
3. **FIQ** (fast interrupt - not commonly used)
4. **SError** (system error)

---

## GIC (Generic Interrupt Controller) Integration

### GIC Overview
The **Generic Interrupt Controller** distributes interrupts from peripherals to CPU cores. In QEMU aarch64-virt:
- **Base address**: `0x08000000`
- **GIC version**: GICv2 (emulated)
- **Distributor**: `0x08000000` (controls interrupt routing)
- **CPU Interface**: `0x08010000` (per-CPU interrupt handling)

### Key GIC Registers

#### Distributor Registers (at 0x08000000)
| Offset | Register | Purpose |
|--------|----------|---------|
| `0x000` | `GICD_CTLR` | Distributor control (enable/disable) |
| `0x100` | `GICD_ISENABLER0` | Enable IRQs 0-31 |
| `0x104` | `GICD_ISENABLER1` | Enable IRQs 32-63 |
| `0x180` | `GICD_ICENABLER0` | Disable IRQs 0-31 |
| `0x184` | `GICD_ICENABLER1` | Disable IRQs 32-63 |
| `0x400` | `GICD_IPRIORITYR0` | Priority for IRQs 0-3 (1 byte per IRQ) |
| `0x800` | `GICD_ITARGETSR0` | Target CPU for IRQs 0-3 |

#### CPU Interface Registers (at 0x08010000)
| Offset | Register | Purpose |
|--------|----------|---------|
| `0x000` | `GICC_CTLR` | CPU interface control |
| `0x004` | `GICC_PMR` | Priority mask (which IRQs to accept) |
| `0x00C` | `GICC_IAR` | Interrupt acknowledge (get IRQ number) |
| `0x010` | `GICC_EOIR` | End of interrupt (signal completion) |

### GIC Initialization Steps

1. **Disable all interrupts**:
   ```
   Set GICD_ICENABLER0 and GICD_ICENABLER1 to 0xFFFFFFFF (disable all)
   ```

2. **Set up CPU interface**:
   ```
   Set GICC_PMR to 0xFF (accept all priority levels)
   Set GICC_CTLR bit 0 = 1 (enable CPU interface)
   ```

3. **Enable distributor**:
   ```
   Set GICD_CTLR bit 0 = 1 (enable distributor)
   ```

4. **Enable specific interrupts** (as needed):
   ```
   Set GICD_ISENABLER[n] bits for specific IRQs
   Set GICD_IPRIORITYR[n] for priority
   Set GICD_ITARGETSR[n] for CPU target (bit 0 for CPU0)
   ```

### IRQ Numbers in QEMU aarch64-virt

**PPI (Private Peripheral Interrupts)**: 16-31
- IRQ 30: System timer
- IRQ 27: UART0
- IRQ 26: RTC

These are mapped to GIC IRQs by adding `32` (the PPI base):
- System timer: GIC IRQ 30
- UART0: GIC IRQ 33 (actually this needs verification - see notes below)

---

## System Timer (ARM Generic Timer)

### Overview
The ARM Generic Timer is a **per-core timer** built into each AArch64 CPU. It includes:
- A 64-bit **system counter** (read-only, increments at a fixed frequency)
- Per-core **timer comparators** that fire interrupts

### Key Timer Registers

All accessed via `MRS`/`MSR` instructions (system registers):

| Register | Purpose |
|----------|---------|
| `CNTFRQ_EL0` | Counter frequency (read-only, set by firmware) |
| `CNTP_TVAL_EL0` | Physical timer value (countdown from here) |
| `CNTP_CTL_EL0` | Physical timer control (enable, interrupt status) |
| `CNTP_CVAL_EL0` | Physical timer comparator (absolute time to fire) |
| `CNTPCT_EL0` | Physical counter (current system counter value) |

### Timer Operation Modes

**Mode 1: Relative (TVAL)**
```
CNTP_TVAL_EL0 = N    // Interrupt in N ticks
// When CNTPCT_EL0 + N is reached, interrupt fires
```

**Mode 2: Absolute (CVAL)**
```
CNTP_CVAL_EL0 = N    // Interrupt when CNTPCT_EL0 reaches N
```

### Timer Control Register (CNTP_CTL_EL0)
- **bit [0]**: ENABLE (1 = timer enabled, 0 = disabled)
- **bit [1]**: IMASK (1 = interrupt masked, 0 = interrupt enabled)
- **bit [2]**: ISTATUS (read-only: 1 = interrupt pending)

### Timer Interrupt Setup

**To set up a timer interrupt**:
```
1. Set CNTP_TVAL_EL0 = ticks_from_now
2. Set CNTP_CTL_EL0 bit 0 = 1 (enable timer)
3. Set CNTP_CTL_EL0 bit 1 = 0 (unmask interrupt)
4. GIC routes this to IRQ 30 (physical timer)
```

**To reload the timer** (in the interrupt handler):
```
// Option A: Relative countdown
MSR CNTP_TVAL_EL0, x0    // x0 = ticks for next interval

// Option B: Absolute time
MRS x0, CNTPCT_EL0       // Read current counter
ADD x0, x0, x1           // Add ticks to current
MSR CNTP_CVAL_EL0, x0    // Set new comparator
```

### QEMU aarch64-virt Timer Frequency

In QEMU, `CNTFRQ_EL0` is typically set to **62500000** Hz (62.5 MHz) or **19200000** Hz (19.2 MHz). You should read this register at boot time.

For 1 second interrupts:
```
1 second = CNTFRQ_EL0 ticks

Example at 62.5 MHz:
CNTP_TVAL_EL0 = 62500000   // 1 second timer
```

---

## EL0 Device Drivers and Signal Delivery (Future Feature)

### Vision: EL0 Device Drivers via Signal Trampolines

Mazarin will eventually support device drivers running at EL0 (user space) with the following architecture:

- **Device driver code** runs at EL0 (unprivileged)
- **Hardware interrupts** are handled by kernel (EL1)
- **Kernel delivers signals** to EL0 drivers via signal trampolines
- **Drivers respond** by setting flags or queuing work
- **Drivers return** via syscall-based trampoline

This design:
- Isolates driver faults from kernel stability
- Allows multiple independent device drivers
- Uses Linux's proven signal-based model
- Defers complex processing to bottom-half handlers

### Nested Interrupt Strategy: Simple Masking

**Selected Approach**: **Strategy 1 (Disable Interrupts During Signal Delivery)**

When delivering a signal to an EL0 device driver:

1. **Save complete context** (all registers, PC, SP, PSTATE) to kernel structures
2. **Disable interrupts** (set I-bit in PSTATE)
3. **Set up signal trampoline** on EL0 stack with return address and context
4. **Modify ELR_EL1** to point to signal handler function
5. **Execute ERET** → jumps to signal handler at EL0 (interrupts disabled)

**During signal handler execution**:
- Interrupts remain **disabled**
- New interrupts are **queued** by hardware/GIC
- Handler must be **fast** (typical device driver: read register, set flag)
- No nested interrupt contexts

**When signal handler returns** (via syscall):
1. EL0 executes `SVC #SYS_rt_sigreturn` (syscall to kernel)
2. Kernel validates return address and context
3. Kernel **re-enables interrupts** (clear I-bit)
4. Kernel restores original context via `ERET`
5. Original EL0 code resumes
6. Queued interrupts are now processed

### Benefits of This Approach

- **No context stack needed** - only one signal context at a time
- **Simple implementation** - easier to test and debug
- **Deterministic behavior** - no surprise nested preemptions
- **Aligns with best practices** - handlers should be short anyway
- **Upgradeable** - can move to Strategy 2 (full nesting) later if needed

### Implementation Timeline

- **Phase 1-4**: Kernel interrupt infrastructure (timer, UART, etc.)
- **Phase 5+**: EL0 process support
- **Phase 6+**: Signal delivery and device drivers

---

## Implementation Phases

### Phase 1: Exception Vector Table & Basic Handlers
**Goal**: Set up the exception vector table and catch exceptions without crashing.

**Deliverables**:
1. Create `src/asm/exceptions.s` with exception vector table
2. Implement minimal exception handlers (log and hang)
3. Set `VBAR_EL1` and enable the vector table
4. Test: Trigger a division by zero or invalid instruction to verify handler is called

**Files to create/modify**:
- `src/asm/exceptions.s` (new)
- `src/go/mazarin/kernel.go` (initialize VBAR_EL1)
- `src/go/mazarin/exceptions.go` (new - exception dispatching)

---

### Phase 2: GIC Initialization
**Goal**: Initialize the Generic Interrupt Controller to route interrupts to CPU.

**Deliverables**:
1. GIC distributor initialization
2. CPU interface setup
3. Go code to query and enable specific IRQs
4. Test: Verify GIC is responding to MMIO reads

**Files to create/modify**:
- `src/go/mazarin/gic.go` (new - GIC control)
- `src/go/mazarin/kernel.go` (call GIC init)

---

### Phase 3: System Timer & IRQ Handler
**Goal**: Implement the system timer and handle timer interrupts.

**Deliverables**:
1. Implement `IRQ` exception handler in assembly (minimal save/restore)
2. Go code to dispatch to specific IRQ handlers
3. System timer initialization and interrupt handler
4. Test: Timer fires every 1 second, prints message

**Files to create/modify**:
- `src/asm/exceptions.s` (expand IRQ handler)
- `src/go/mazarin/gic.go` (add IRQ dispatching)
- `src/go/mazarin/timer.go` (new - timer control)
- `src/go/mazarin/kernel.go` (integrate timer)

---

### Phase 4: UART Interrupts
**Goal**: Handle UART receive interrupts for input.

**Deliverables**:
1. Enable UART RX interrupt in GIC
2. Implement UART interrupt handler
3. Read character from UART when interrupt fires
4. Test: Type characters, they appear via interrupt handler

**Files to create/modify**:
- `src/go/mazarin/uart.go` (add interrupt setup and handler)
- `src/go/mazarin/gic.go` (register UART IRQ)

---

### Phase 5: Framebuffer Interrupts (Future)
**Goal**: Handle frame buffer DMA completion interrupts.

**Deliverables**:
1. Configure framebuffer for interrupt-driven updates
2. Handle framebuffer interrupt
3. Test: Smooth graphics updates without polling

---

## Signal Delivery Architecture (For EL0 Drivers)

### Signal Trampoline Flow

When a device driver at EL0 needs to be notified of an interrupt:

```
Timeline:
[1] Device driver code running at EL0
    └─ x0=value, x1=value, ... SP_EL0=kernel_stack
    
[2] Hardware interrupt fires → Kernel transition to EL1
    └─ ELR_EL1=return_address
    └─ SPSR_EL1=EL0_pstate
    
[3] Kernel exception handler (EL1)
    └─ Reads exception info
    └─ Decides: "This driver needs signal X"
    
[4] Kernel prepares signal delivery
    └─ Allocates signal frame on EL0 stack
    └─ Saves driver context (all x0-x30, LR, SPSR)
    └─ Pushes signal return trampoline on stack
    
[5] Kernel modifies exception return state
    └─ ELR_EL1 = address of signal handler function
    └─ SPSR_EL1 = EL0 with interrupts disabled (I-bit set)
    └─ Sets up x0 = signal number (parameter to handler)
    
[6] Kernel executes ERET
    └─ Returns to signal handler at EL0
    └─ Interrupts are DISABLED
    
[7] Signal handler runs at EL0
    └─ Does device driver work (read register, set flag, etc.)
    └─ Must be short/fast
    
[8] Signal handler returns
    └─ LR points to signal return trampoline (set in step [4])
    └─ Executes RET → jumps to trampoline
    
[9] Signal return trampoline (on user stack)
    └─ MOV x0, #SYS_rt_sigreturn
    └─ SVC #0   ← Syscall back to kernel
    
[10] Kernel syscall handler (rt_sigreturn)
    └─ Validates return address
    └─ Restores all original context
    └─ Sets I-bit = 0 (re-enable interrupts)
    └─ Executes ERET
    
[11] Original driver code resumes
    └─ All registers restored
    └─ Interrupts re-enabled
    └─ Queued interrupts processed
```

### Signal Frame Layout (on EL0 stack)

```
SP before signal delivery
    ↓
[saved registers x0-x30]      ← 31 × 8 bytes = 248 bytes
[saved LR]                    ← 8 bytes
[saved SPSR_EL1]              ← 8 bytes
[saved original SP]           ← 8 bytes
[signal number (for handler)] ← 8 bytes
[signal return trampoline]    ← 4 instructions × 4 bytes = 16 bytes
    ↓
SP during signal handler
```

**Total frame size**: ~296 bytes + alignment

### Syscall-Based Signal Return (rt_sigreturn)

The signal return mechanism uses a syscall because:
1. **Isolation**: EL0 code cannot modify SPSR_EL1 or ELR_EL1 directly
2. **Validation**: Kernel validates return address before restoring
3. **Safety**: Prevents EL0 code from returning to arbitrary kernel locations

#### Syscall Definition

```go
// In Go (kernel side)
const SYS_rt_sigreturn = 139  // AArch64 syscall number

func handleSyscall_rt_sigreturn() {
    // Get signal frame pointer from stack
    // Validate frame signature
    // Restore all registers from frame
    // Set SPSR_EL1 with interrupts enabled (I-bit clear)
    // Return via ERET
}
```

#### Trampoline Assembly (placed on user stack)

```asm
# Signal return trampoline (16 bytes)
.align 8
rt_sigreturn_trampoline:
    MOV   x0, #139              # SYS_rt_sigreturn
    SVC   #0                    # Syscall
    # Control never returns here (kernel restores context)
    NOP                         # Padding to 16 bytes
    NOP
```

### Interrupt Masking During Signal Delivery

When a signal handler is executing (after step [6] above):

| Scenario | I-bit | FIQ-bit | Behavior |
|----------|-------|---------|----------|
| Signal handler running | SET (1) | SET (1) | Interrupts disabled; queued by hardware |
| After rt_sigreturn syscall | CLEAR (0) | CLEAR (0) | Interrupts enabled; queued handled |
| New interrupt arrives | - | - | Queued; will be processed when I-bit is cleared |

**Important**: The kernel must properly restore the original I-bit state when returning from `rt_sigreturn`. If the original code was running with interrupts disabled, that state must be preserved.

### Signal Handler Constraints

Since signal handlers run with interrupts disabled:

1. **Must be fast** (microseconds, not milliseconds)
   - Device register reads: OK
   - Setting flags/counters: OK
   - Calling functions: OK (but keep them short)
   - Sleeping/blocking: **NOT OK** - would hang entire system

2. **Cannot block on resources**
   - No locks/mutexes that might be held by interrupted code
   - No malloc/free (use pre-allocated buffers)
   - No I/O that waits

3. **Must be reentrant**
   - Same signal might arrive during handler (after I-bit restored)
   - Use atomic operations for shared state

4. **Should follow POSIX signal handler rules**
   - Use only async-signal-safe functions
   - Avoid library calls that use locks

### Example: Simple Device Driver Signal Handler

```c
// Device driver at EL0 that handles interrupts via signals

volatile uint32_t device_irq_count = 0;
volatile uint8_t device_status = 0;

// This runs at EL0 with interrupts disabled
void device_interrupt_handler(int signal_num) {
    // Read device register to acknowledge interrupt
    uint32_t status = *(volatile uint32_t*)DEVICE_STATUS_REG;
    
    // Update atomic counters
    device_irq_count++;
    device_status = status & 0xFF;
    
    // Signal main loop that work is available
    // (main loop will do actual processing)
}

// Main device driver loop (at EL0)
int main() {
    // Register handler for signal
    signal(DEVICE_IRQ_SIGNAL, device_interrupt_handler);
    
    // Main loop
    while (1) {
        if (device_irq_count > 0) {
            // Process device event
            process_device_event();
            device_irq_count = 0;
        }
    }
}
```

### Kernel Side: Delivering Signal to EL0 Process

```go
// In kernel exception handler (when UART interrupt fires, for example)
func handleUARTInterrupt() {
    // Read UART status
    char := readUART()
    
    // Find the EL0 process that owns this device
    driver := findDriverForUART()
    
    if driver != nil {
        // Deliver signal to driver process
        deliverSignal(driver, UART_IRQ_SIGNAL)
    }
}

// Deliver signal to a process
func deliverSignal(process *Process, signalNum int) {
    // Save current exception context temporarily
    savedELR := readELR_EL1()
    savedSPSR := readSPSR_EL1()
    
    // Save process's full context
    saveProcessContext(process, savedELR, savedSPSR)
    
    // Set up signal frame on process's stack
    signalFrame := setupSignalFrame(process, signalNum)
    
    // Modify exception return to go to signal handler
    writeELR_EL1(signalNum.HandlerAddress)
    
    // Keep interrupts disabled (I-bit set in PSTATE)
    pstate := readSPSR_EL1()
    pstate |= PSTATE_I_BIT  // Set I bit to disable IRQs
    writeSPSR_EL1(pstate)
    
    // Return will now go to signal handler
}
```

---

## Code Structure

### Exception Vector Table (assembly)

```
src/asm/exceptions.s:
  - exception_vector_start (2KB aligned)
    - Group 2 exception handlers (current EL, SP_EL1)
      - sync_handler_el1
      - irq_handler_el1
      - fiq_handler_el1
      - serror_handler_el1
  - sync_handler_el1    (128 bytes max)
  - irq_handler_el1     (128 bytes max)
  - fiq_handler_el1     (128 bytes max)
  - serror_handler_el1  (128 bytes max)
  - Full vector entries for all 4 groups (mostly placeholders)
```

### Exception Handler Prologue/Epilogue

Each handler must:
1. **Save context** (registers used by handler)
2. **Call Go dispatch function** with exception info
3. **Restore context**
4. **Return** using `ERET`

Structure:
```asm
irq_handler_el1:
    // Save registers used by this handler
    stp x0, x1, [sp, #-16]!
    stp x2, x3, [sp, #-16]!
    // Call Go handler (handles IRQ dispatch and ACK)
    bl exception_handler  // or irq_handler
    // Restore
    ldp x2, x3, [sp], #16
    ldp x0, x1, [sp], #16
    eret
```

### Go Exception Handling (kernel.go / exceptions.go)

```go
// Initialize exception handling
func ExceptionsInit() {
    // Set exception vector base address
    // In assembly: MSR VBAR_EL1, x0
    setVectorBaseAddress(vectorTableAddress)
    
    // Enable IRQ bit in PSTATE
    EnableIRQs()
}

// Dispatch exceptions to specific handlers
func ExceptionHandler(excInfo ExceptionInfo) {
    switch excInfo.Type {
    case SYNC_EXCEPTION:
        handleSyncException(excInfo)
    case IRQ:
        handleIRQ()
    case FIQ:
        handleFIQ()
    case SERROR:
        handleSError(excInfo)
    }
}

// IRQ dispatcher
func handleIRQ() {
    irqNum := gic.AcknowledgeInterrupt()
    
    switch irqNum {
    case IRQ_TIMER:
        timerHandler()
    case IRQ_UART:
        uartHandler()
    // ... more IRQs
    }
    
    gic.EndOfInterrupt(irqNum)
}
```

### GIC Control (gic.go)

```go
func GICInit() {
    // Disable all interrupts
    // Set up CPU interface
    // Enable distributor
}

func GICEnableIRQ(irqNum uint32) {
    // Enable specific IRQ in GICD_ISENABLER
    // Set priority
    // Set target CPU
}

func AcknowledgeInterrupt() uint32 {
    // Read GICC_IAR to get IRQ number
}

func EndOfInterrupt(irqNum uint32) {
    // Write GICC_EOIR to signal completion
}
```

### Timer Control (timer.go)

```go
func TimerInit() {
    // Read CNTFRQ_EL0 to get timer frequency
    // Enable GIC IRQ 30
}

func SetTimerInterval(ticks uint64) {
    // Write CNTP_TVAL_EL0 with countdown
    // Enable timer via CNTP_CTL_EL0
}

func TimerInterruptHandler() {
    // Reload timer for next interval
    // Do work (increment time counter, etc.)
}
```

---

## Key Differences from ARM32

| Aspect | ARM32 | AArch64 |
|--------|-------|--------|
| **Exception vectors** | Single table at 0x00000000 | 4 groups at VBAR_EL1 (2KB aligned) |
| **Entry size** | 4 bytes per vector | 128 bytes per vector (must fit handler) |
| **Mode switching** | Via CPSR mode bits | Via exception levels (EL0-EL3) |
| **Interrupt control** | CPSR I/F bits directly | SPSR/PSTATE I/F bits via MRS/MSR |
| **Return** | `MOVS PC, LR` | `ERET` |
| **Context save** | Manual save to stack | Must save in handler |
| **Timer** | System timer peripheral | Built-in ARM Generic Timer |
| **Interrupt controller** | Per-board implementation | Generic Interrupt Controller (GIC) |
| **Register width** | 32-bit (r0-r15) | 64-bit (x0-x30) |

---

## Important Notes & Considerations

### Stack Alignment
- AArch64 **ABI requires 16-byte stack alignment** at function calls
- Before calling Go functions, ensure `SP % 16 == 0`

### Exception Handler Size
- Each handler has **128 bytes** maximum
- A typical prologue/epilogue uses ~40 bytes, leaving ~88 bytes for work
- For complex logic, create a separate assembly function and branch to it

### Interrupt Masking
To disable/enable interrupts:
```asm
// Disable IRQs (set I bit in PSTATE)
MSR DAIFSET, #2      // Set I bit (bit 1 in DAIF set)

// Enable IRQs (clear I bit)
MSR DAIFCLR, #2      // Clear I bit
```

Or use simpler instructions:
```asm
// Disable
DI                   // Not standard; use DAIFSET instead

// Enable  
EI                   // Not standard; use DAIFCLR instead
```

### GIC IRQ Numbers
- **PPIs (Private Peripheral Interrupts)** are 16-31
- **SPIs (Shared Peripheral Interrupts)** are 32+
- Timer interrupt is typically **PPI 30** (physical timer)
- UART interrupt varies by device tree (device tree driven)

For QEMU aarch64-virt, check the device tree or QEMU source to confirm exact IRQ mappings.

### Testing Strategy

1. **Phase 1**: Trigger undefined instruction, catch in exception handler
2. **Phase 2**: Read a GIC register, verify MMIO works
3. **Phase 3**: Set timer, watch for interrupt fire and handler execution
4. **Phase 4**: Type on UART, receive interrupt
5. Full system: Generate load (timer interrupts), handle input (UART), maintain system stability

### Memory for Exception Vector

The vector table must be 2KB aligned. Placement options:
- After kernel BSS section (recommended)
- In a dedicated memory region

In `linker.ld`:
```ld
.vectors :
{
    . = ALIGN(0x800);  /* 2KB alignment */
    PROVIDE(__exception_vectors = .);
    *(.exception_vectors)
    . = ALIGN(0x800);
}
```

---

## Implementation Checklist

- [ ] Phase 1: Exception vector table and basic handlers
- [ ] Phase 2: GIC initialization and control
- [ ] Phase 3: System timer interrupts
- [ ] Phase 4: UART interrupts
- [ ] Phase 5: Framebuffer interrupts (optional for MVP)

---

## References

- ARM Architecture Reference Manual - AArch64 exception handling
- ARM Generic Interrupt Controller v2 (GICv2) specification
- ARM Generic Timer specification
- QEMU aarch64-virt machine documentation
- Device tree for QEMU aarch64-virt (check QEMU source for IRQ mappings)

