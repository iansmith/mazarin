# Timer-Based Preemption and Monitoring Plan

## Overview

This document describes the implementation of goroutine preemption and runtime monitoring using ARM timer interrupts instead of Unix signals and the sysmon thread.

**Key Terminology:**
- **Timer Interrupt** = Fires every 20ms
- **Tick** = One timer interrupt firing (increments counter by 1)
- **250 ticks** = 250 timer interrupt firings = 5 seconds

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        MAZZY SYSTEM                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌────────────────────┐         ┌─────────────────────┐        │
│  │  ARM Timer (20ms)  │         │  Go Runtime         │        │
│  │  EL1 Interrupt     │         │  Scheduler          │        │
│  └─────────┬──────────┘         └──────────┬──────────┘        │
│            │                               │                     │
│            │ Timer IRQ fires               │                     │
│            │ (every 20ms = 1 tick)         │                     │
│            ▼                               │                     │
│  ┌─────────────────────┐                  │                     │
│  │ Timer IRQ Handler   │                  │                     │
│  │ (exceptions.s)      │                  │                     │
│  ├─────────────────────┤                  │                     │
│  │ 1. Re-arm timer     │                  │                     │
│  │ 2. Increment counter│                  │                     │
│  │ 3. Output dot       │ (every 250 interrupt firings = 5s)     │
│  │ 4. Check preemption │◄─────────────────┘                     │
│  │ 5. Inject asyncPreempt (if needed)                           │
│  │ 6. Signal channels  │──┐                                     │
│  └─────────────────────┘  │                                     │
│                            │                                     │
│                            ▼                                     │
│                  ┌──────────────────┐                           │
│                  │  Timer Channels  │                           │
│                  │  (3 channels)    │                           │
│                  └────────┬─────────┘                           │
│                           │                                     │
│         ┌─────────────────┼─────────────────┐                  │
│         │                 │                 │                  │
│         ▼                 ▼                 ▼                  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐           │
│  │ GC Monitor  │  │ Scavenger   │  │ Schedtrace  │           │
│  │ Goroutine   │  │ Monitor     │  │ Monitor     │           │
│  ├─────────────┤  │ Goroutine   │  │ Goroutine   │           │
│  │Count ticks  │  ├─────────────┤  ├─────────────┤           │
│  │Every 6000:  │  │Check wake   │  │Every 50:    │           │
│  │runtime.GC() │  │flag         │  │schedtrace() │           │
│  └─────────────┘  └─────────────┘  └─────────────┘           │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Configuration

### Timer Settings

- **Timer Interval**: 20 milliseconds
- **Timer Frequency**: 62.5 MHz (QEMU ARM Generic Timer)
- **Hardware ticks per interrupt**: 1,250,000 (at 62.5 MHz)
- **Software tick**: Each timer interrupt firing (increments counter by 1)

### Counter Thresholds (in Timer Interrupt Firings)

| Output/Action | Interrupt Firings (Ticks) | Time Interval |
|--------------|---------------------------|---------------|
| **Dot Output** | 250 | 5 seconds |
| **GC Trigger** | 6000 | 120 seconds (2 minutes) |
| **Scavenger** | On demand | When wake flag set |
| **Schedtrace** | 50 | 1 second |

**Example Timeline:**

```
Interrupt #1   (0.02s):  counter = 1
Interrupt #2   (0.04s):  counter = 2
Interrupt #50  (1.00s):  counter = 50  → Schedtrace prints
Interrupt #100 (2.00s):  counter = 100 → Schedtrace prints
Interrupt #250 (5.00s):  counter = 250 → DOT OUTPUT, Schedtrace prints
Interrupt #251 (5.02s):  counter = 1   (dot counter resets)
...
Interrupt #6000 (120s): counter = 6000 → GC triggers
```

## Syscall Stubs Required

The following syscalls must be stubbed to prevent sysmon from starting:

| Syscall | Number | Stub Behavior | Reason |
|---------|--------|---------------|--------|
| `rt_sigaction` | 134 | Return 0 (success) | No real signals needed |
| `rt_sigprocmask` | 135 | Return 0 (success) | No signal masking needed |
| `sigaltstack` | 132 | Return 0 (success) | Timer uses exception stack |
| `tgkill` | 131 | Return 0 (success) | No signal sending needed |
| `clone` | 220 | Return ENOSYS | Prevents sysmon thread creation |
| `nanosleep` | 101 | Return 0 (success) | Not needed without sysmon |

## sysmon Functionality Mapping

### What sysmon Does vs What We Do

| sysmon Function | Frequency | Our Solution | Status |
|----------------|-----------|--------------|--------|
| Preempt long-running goroutines | Every loop | Timer interrupt injects asyncPreempt | ✅ Replacement |
| Retake P's from syscalls | Every loop | Not needed (bare-metal) | ⚠️ N/A |
| Poll network | Every 10ms | Would need netpoll goroutine | ❌ Future |
| Force GC | Every 2 min | GC monitor goroutine | ✅ Replacement |
| Wake scavenger | On demand | Scavenger monitor goroutine | ✅ Replacement |
| Schedule trace | Configurable | Schedtrace monitor goroutine | ✅ Replacement |
| Update GOMAXPROCS | Every 1 sec | Not needed (bare-metal) | ⚠️ N/A |

## Data Sources

### Tick Counter (Timer Interrupt Firings)

```go
// Global atomic counter - incremented each time timer interrupt fires
var timerTickCount atomic.Uint64

// Incremented on every timer interrupt
func timerSignal() {
    count := timerTickCount.Add(1)  // One more interrupt fired
    // ...
}
```

**Source**: We maintain this counter
- Starts at 0
- Increments by 1 each time the timer interrupt fires
- Represents: "How many times has the timer interrupt fired?"

### Dot Output Counter (Separate Counter)

```assembly
# In exceptions.s
.data
dot_counter:
    .quad 0

# In handle_timer_irq:
# Load dot counter
# Increment it
# If >= 250, output dot and reset to 0
```

**Source**: Separate counter in assembly
- Starts at 0
- Increments by 1 each interrupt
- Resets to 0 after outputting dot
- Only purpose: Track when to output dot

### Time (nanotime)

**Hardware Source**: ARM Generic Timer counter register `CNTVCT_EL0`

This is different from our software tick counter!

```assembly
# Read current hardware time
MRS CNTVCT_EL0, R0      # Returns: uint64 hardware tick count
                         # At 62.5 MHz, this increments 62,500,000 times/second
```

**Conversion to Nanoseconds**:

```go
func nanotime() int64 {
    hardwareTicks := readTimerCounter()  // Read CNTVCT_EL0
    return int64(hardwareTicks) * 16     // At 62.5 MHz: 16ns per hardware tick
}
```

**Timer Frequency Reading**:

```assembly
# Read timer frequency (once at boot)
MRS CNTFRQ_EL0, R0      # Returns: uint64 frequency in Hz (e.g., 62500000)
```

## File Structure

```
src/mazboot/
├── asm/aarch64/
│   ├── exceptions.s          # MODIFY: Timer IRQ handler with dot output
│   └── timer.s              # NEW: Timer counter reading functions
├── golang/main/
│   ├── timer_channels.go     # NEW: Channel definitions and timerSignal()
│   ├── nanotime.go          # NEW: Time implementation
│   ├── gc_monitor.go        # NEW: GC monitor goroutine
│   ├── scavenger_monitor.go # NEW: Scavenger monitor goroutine
│   ├── schedtrace_monitor.go# NEW: Schedtrace monitor goroutine
│   ├── monitor_config.go    # NEW: Configuration constants
│   ├── monitor_debug.go     # NEW: Debug utilities
│   ├── syscall.go           # MODIFY: Add syscall stubs
│   └── kernel.go            # MODIFY: Start monitors
```

## Implementation Steps

### Step 1: Timer Counter Reading (Assembly)

**File**: `src/mazboot/asm/aarch64/timer.s`

```assembly
#include "textflag.h"

// func readTimerCounter() uint64
// Returns hardware tick count (NOT our software tick counter)
TEXT ·readTimerCounter(SB),NOSPLIT|NOFRAME,$0-8
    MRS CNTVCT_EL0, R0
    MOVD R0, ret+0(FP)
    RET

// func getTimerFrequency() uint64
TEXT ·getTimerFrequency(SB),NOSPLIT|NOFRAME,$0-8
    MRS CNTFRQ_EL0, R0
    MOVD R0, ret+0(FP)
    RET
```

### Step 2: Time Implementation (Go)

**File**: `src/mazboot/golang/main/nanotime.go`

```go
package main

import (
    "sync/atomic"
    _ "unsafe"
)

var (
    timerFrequency uint64  // Hardware frequency (e.g., 62,500,000 Hz)
    nanosPerTick int64     // Nanoseconds per hardware tick
)

//go:nosplit
func readTimerCounter() uint64  // Read CNTVCT_EL0

//go:nosplit
func getTimerFrequency() uint64  // Read CNTFRQ_EL0

func initTime() {
    timerFrequency = getTimerFrequency()
    if timerFrequency == 0 {
        timerFrequency = 62500000  // Default: 62.5 MHz
        print("WARNING: CNTFRQ_EL0 returned 0, using default 62.5 MHz\r\n")
    }
    nanosPerTick = int64(1000000000 / timerFrequency)
    print("Time: frequency=", timerFrequency, " Hz, ns_per_tick=", nanosPerTick, "\r\n")
}

//go:nosplit
func nanotime() int64 {
    hardwareTicks := readTimerCounter()
    return int64(hardwareTicks) * nanosPerTick
}
```

### Step 3: Timer Channels (Go)

**File**: `src/mazboot/golang/main/timer_channels.go`

```go
package main

import "sync/atomic"

type TimerTick struct {
    Count     uint64  // How many times timer interrupt has fired
    Timestamp int64   // Current time in nanoseconds
}

// Counter: How many times has timer interrupt fired?
var timerTickCount atomic.Uint64

const timerChannelBuffer = 10

var (
    gcTimerChan         = make(chan TimerTick, timerChannelBuffer)
    scavengerTimerChan  = make(chan TimerTick, timerChannelBuffer)
    schedtraceTimerChan = make(chan TimerTick, timerChannelBuffer)
)

// Called each time timer interrupt fires
//go:nosplit
func timerSignal() {
    now := nanotime()
    count := timerTickCount.Add(1)  // One more interrupt firing

    tick := TimerTick{
        Count:     count,  // Total interrupt firings
        Timestamp: now,    // Current time
    }

    // Non-blocking sends to all channels
    select {
    case gcTimerChan <- tick:
    default:
    }

    select {
    case scavengerTimerChan <- tick:
    default:
    }

    select {
    case schedtraceTimerChan <- tick:
    default:
    }
}

func getCurrentTick() uint64 {
    return timerTickCount.Load()
}
```

### Step 4: Timer Interrupt Handler (Assembly)

**File**: `src/mazboot/asm/aarch64/exceptions.s`

Add dot output counter and logic:

```assembly
# ========== DATA SECTION ==========
.data
.align 3

# Counter for dot output (separate from timerTickCount)
# Counts how many timer interrupts since last dot
# Resets to 0 after outputting dot
dot_counter:
    .quad 0

# ========== CODE SECTION ==========
.text
.globl handle_timer_irq
handle_timer_irq:
    # ========== 1. RE-ARM TIMER ==========
    # Set timer to fire again in 20ms
    mrs x0, CNTVCT_EL0              # Read current hardware counter
    ldr x1, =1250000                # 20ms worth of hardware ticks @ 62.5 MHz
    add x0, x0, x1                  # Calculate next interrupt time
    msr CNTV_CVAL_EL0, x0           # Set compare value

    # ========== 2. DOT OUTPUT CHECK ==========
    # Check if we should output a dot
    # Every 250 timer interrupt firings = 5 seconds

    # Load dot counter
    adrp x0, dot_counter
    add x0, x0, :lo12:dot_counter
    ldr x1, [x0]                    # x1 = current dot counter value

    # Increment dot counter (one more interrupt fired)
    add x1, x1, #1
    str x1, [x0]                    # Save incremented value

    # Check if we've reached 250 interrupt firings
    cmp x1, #250
    blt no_dot                      # If < 250, skip dot output

    # We've hit 250 interrupt firings - output dot and reset
    str xzr, [x0]                   # Reset dot counter to 0

    # Output dot to ring buffer
    mov x0, #'.'
    bl fb_putc_irq                  # Call existing ring buffer function

no_dot:
    # ========== 3. CHECK FOR PREEMPTION ==========
    # Call Go function: checkAndInjectPreempt() -> (bool, uintptr)
    bl checkAndInjectPreempt

    # x0 = shouldPreempt (bool)
    # x1 = asyncPreempt address (if preempting)

    cbz x0, no_preempt              # If false, skip injection

    # ========== 4. INJECT ASYNCPREEMPT ==========
    # Modify saved ELR_EL1 to point to asyncPreempt
    # (Stack frame offset depends on your exception handler)
    # TODO: Verify this offset matches your exception frame layout
    str x1, [sp, #248]              # Modify return PC

no_preempt:
    # ========== 5. SIGNAL TIMER CHANNELS ==========
    # Increment timerTickCount and send to Go channels
    bl timerSignal

    # ========== 6. RETURN FROM INTERRUPT ==========
    ret
```

### Step 5: GC Monitor (Go)

**File**: `src/mazboot/golang/main/gc_monitor.go`

```go
package main

import (
    "runtime"
    "sync/atomic"
)

const (
    TimerIntervalMS = 20
    GCPeriodSeconds = 120
    GCTickInterval = (GCPeriodSeconds * 1000) / TimerIntervalMS  // 6000 interrupt firings
)

var (
    gcTriggerCount atomic.Uint64
    lastGCTick uint64
    gcMonitorEnabled = true
)

func startGCMonitor() {
    if !gcMonitorEnabled {
        print("GC Monitor: disabled\r\n")
        return
    }
    go gcMonitorLoop()
}

func gcMonitorLoop() {
    print("GC Monitor: started\r\n")
    print("  Will trigger GC every ", GCTickInterval, " timer interrupt firings (")
    print(GCPeriodSeconds, " seconds)\r\n")

    for tick := range gcTimerChan {
        // tick.Count = total number of timer interrupt firings
        if tick.Count - lastGCTick >= GCTickInterval {
            lastGCTick = tick.Count
            count := gcTriggerCount.Add(1)

            print("\r\n═══════════════════════════════════════════════\r\n")
            print("GC Monitor: Triggering GC #", count, "\r\n")
            print("  Timer interrupt firings: ", tick.Count, "\r\n")
            print("  Time: ", tick.Timestamp/1000000000, " seconds\r\n")

            var mBefore runtime.MemStats
            runtime.ReadMemStats(&mBefore)
            print("  Heap before: ", mBefore.HeapAlloc/1024, " KB\r\n")

            runtime.GC()

            var mAfter runtime.MemStats
            runtime.ReadMemStats(&mAfter)
            print("  Heap after: ", mAfter.HeapAlloc/1024, " KB\r\n")
            print("  Freed: ", (mBefore.HeapAlloc-mAfter.HeapAlloc)/1024, " KB\r\n")
            print("═══════════════════════════════════════════════\r\n\r\n")
        }
    }
}
```

### Step 6: Scavenger Monitor (Go)

**File**: `src/mazboot/golang/main/scavenger_monitor.go`

```go
package main

import (
    "unsafe"
    _ "unsafe"
)

//go:linkname scavenger runtime.scavenger
var scavenger scavengerState

type scavengerState struct {
    lock       mutex
    g          guintptr
    parked     bool
    timer      *timer
    sysmonWake atomic_Uint32
}

//go:linkname wakeScavenger runtime.(*scavengerState).wake
func wakeScavenger(s *scavengerState)

var scavengerMonitorEnabled = true

func startScavengerMonitor() {
    if !scavengerMonitorEnabled {
        print("Scavenger Monitor: disabled\r\n")
        return
    }
    go scavengerMonitorLoop()
}

func scavengerMonitorLoop() {
    print("Scavenger Monitor: started\r\n")

    wakeCount := 0
    for tick := range scavengerTimerChan {
        if scavenger.sysmonWake.Load() != 0 {
            wakeCount++
            print("Scavenger Monitor: waking at interrupt #", tick.Count)
            print(" (wake #", wakeCount, ")\r\n")
            wakeScavenger(&scavenger)
        }
    }
}
```

### Step 7: Schedtrace Monitor (Go)

**File**: `src/mazboot/golang/main/schedtrace_monitor.go`

```go
package main

import _ "unsafe"

//go:linkname schedtrace runtime.schedtrace
func schedtrace(detailed bool)

const (
    SchedtracePeriodSeconds = 1
    SchedtraceTickInterval = (SchedtracePeriodSeconds * 1000) / TimerIntervalMS  // 50 interrupt firings
)

var (
    schedtraceEnabled = true
    schedtraceDetailed = true
    lastSchedtraceTick uint64
)

func startSchedtraceMonitor() {
    if !schedtraceEnabled {
        print("Schedtrace Monitor: disabled\r\n")
        return
    }
    go schedtraceMonitorLoop()
}

func schedtraceMonitorLoop() {
    print("Schedtrace Monitor: started\r\n")
    print("  Will print every ", SchedtraceTickInterval, " timer interrupt firings (")
    print(SchedtracePeriodSeconds, " second)\r\n")

    traceCount := 0
    for tick := range schedtraceTimerChan {
        if tick.Count - lastSchedtraceTick >= SchedtraceTickInterval {
            lastSchedtraceTick = tick.Count
            traceCount++

            print("\r\n───────────────────────────────────────────────\r\n")
            print("Schedtrace #", traceCount, " at interrupt #", tick.Count)
            print(" (", tick.Timestamp/1000000000, "s)\r\n")

            schedtrace(schedtraceDetailed)

            print("───────────────────────────────────────────────\r\n\r\n")
        }
    }
}
```

### Step 8: Syscall Stubs (Go)

**File**: `src/mazboot/golang/main/syscall.go`

Add to the existing switch statement:

```go
func HandleSyscall(num int64, arg0, arg1, arg2, arg3, arg4, arg5 uint64) (r1, r2 uint64, err error) {
    switch num {

    // ========== SIGNAL STUBS (no real signals) ==========
    case 134: // rt_sigaction
        return 0, 0, nil

    case 135: // rt_sigprocmask
        return 0, 0, nil

    case 132: // sigaltstack
        return 0, 0, nil

    case 131: // tgkill
        return 0, 0, nil

    // ========== THREAD CREATION (disable sysmon) ==========
    case 220: // clone
        // Return ENOSYS to prevent sysmon from starting
        return 0, 0, syscall.ENOSYS

    case 101: // nanosleep
        return 0, 0, nil

    // ... existing syscalls ...
    }
}
```

### Step 9: Start Monitors (Go)

**File**: `src/mazboot/golang/main/kernel.go`

Modify main() to start monitors:

```go
func main() {
    // ... existing hardware initialization ...

    print("\r\n═══════════════════════════════════════════════\r\n")
    print("mazboot: Runtime Initialization\r\n")
    print("═══════════════════════════════════════════════\r\n")

    // Initialize time system
    print("mazboot: Initializing time system...\r\n")
    initTime()

    // Start monitoring goroutines
    print("mazboot: Starting monitors...\r\n")
    startGCMonitor()
    startScavengerMonitor()
    startSchedtraceMonitor()

    print("mazboot: All monitors started\r\n")
    print("═══════════════════════════════════════════════\r\n\r\n")

    // ... start kernel goroutines ...

    // Hand off to scheduler (never returns)
    print("mazboot: Transferring control to Go scheduler\r\n")
    print("═══════════════════════════════════════════════\r\n\r\n")
    asm.CallRuntimeMstart()

    panic("BUG: scheduler returned!")
}
```

## Testing Strategy

### Test 1: Dot Output
- **Expected**: Dot appears every 5 seconds (250 timer interrupt firings)
- **Verify**: Timer interrupt is firing regularly
- **Output**: `....................` (one dot per 5 seconds)

### Test 2: Timer Channels
- **Expected**: Monitor goroutines receive tick notifications
- **Verify**: Channels are working, goroutines wake up

### Test 3: GC Trigger
- **Expected**: GC runs after 6000 timer interrupt firings (2 minutes)
- **Verify**: GC monitor is working
- **Output**: GC statistics printed

### Test 4: Schedtrace
- **Expected**: Scheduler stats printed after 50 interrupt firings (1 second)
- **Verify**: Schedtrace monitor is working
- **Output**: SCHED statistics every second

### Test 5: Preemption
- **Expected**: Busy-loop goroutine gets preempted, other goroutines run
- **Verify**: Preemption is working

## Performance Impact

### Timer Interrupt Overhead (every 20ms)

```
Operation                     Instructions    Time (est @ 2 GHz)
────────────────────────────────────────────────────────────────
Re-arm timer                  ~10             ~5 ns
Dot counter inc/check         ~8              ~4 ns
Conditional dot output        ~5              ~2 ns
Check preemption              ~50             ~25 ns
Signal channels (3×)          ~30 each        ~45 ns
────────────────────────────────────────────────────────────────
Total per interrupt           ~140            ~81 ns
Overhead percentage           81ns / 20ms     0.0004%
Dot output (when triggered)   +~100           +~50 ns
```

### Memory Usage

```
Component                     Memory
──────────────────────────────────────
3 timer channels (buffered)   ~3 KB
3 monitor goroutine stacks    ~6 KB
Timer tick structures         Minimal
Dot counter                   8 bytes
──────────────────────────────────────
Total additional memory       ~10 KB
```

## Summary

### What We Get

✅ Goroutine preemption (20ms quantum)
✅ Periodic GC (every 6000 interrupt firings = 2 minutes)
✅ Scavenger monitoring
✅ Schedule tracing (every 50 interrupt firings = 1 second)
✅ Visual heartbeat (dot every 250 interrupt firings = 5 seconds)
✅ No thread creation (no clone)
✅ No signal infrastructure
✅ Simple, predictable behavior

### What We Don't Get

❌ Network poller (would need separate goroutine)
❌ Syscall retaking (not needed for bare-metal)
❌ Dynamic GOMAXPROCS (fixed at init)
❌ Sub-20ms preemption granularity

### System Architecture After Implementation

```
Goroutines Running:
  • Go scheduler loop (in runtime.mstart) - Forever
  • GC monitor - Forever (wakes every 6000 interrupt firings)
  • Scavenger monitor - Forever (wakes on demand)
  • Schedtrace monitor - Forever (wakes every 50 interrupt firings)
  • Your kernel goroutines - Forever

Timer Interrupt (fires every 20ms):
  • Re-arm timer for next 20ms
  • Increment dot counter
  • Output dot if counter >= 250 (reset counter)
  • Check preemption, inject asyncPreempt if needed
  • Increment timerTickCount
  • Signal 3 channels (non-blocking)

Data Flow:
  Timer IRQ → timerSignal() → 3 Channels → 3 Monitor Goroutines
  Timer IRQ → checkPreempt() → Inject asyncPreempt (if needed)
  Timer IRQ → dot counter → fb_putc_irq('.') every 250 firings
```

## Glossary

| Term | Meaning |
|------|---------|
| **Timer interrupt** | EL1 IRQ that fires every 20ms |
| **Tick** | One firing of the timer interrupt |
| **250 ticks** | 250 timer interrupt firings = 5 seconds |
| **timerTickCount** | Software counter: how many times has interrupt fired? |
| **dot_counter** | Separate counter in assembly for dot output |
| **Hardware tick** | One increment of CNTVCT_EL0 (62.5M per second) |
| **Hardware counter** | CNTVCT_EL0 register value |
