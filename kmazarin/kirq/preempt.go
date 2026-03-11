
// Package kirq provides kernel IRQ handling including timer-based thread preemption.
//
// Goroutine-level preemption is handled by the Go runtime in userspace via
// SIGURG signals. The kernel only performs thread-level preemption when a
// thread (priest) has exceeded its time quantum.

package kirq

import (
	"sync/atomic"
	_ "unsafe" // For go:linkname
)

// PreemptOffsets mirrors runtime.PreemptOffsets for go:linkname access.
// These offsets are computed by the runtime using unsafe.Offsetof to ensure
// correctness across Go version changes.
type preemptOffsetsType struct {
	StackGuard0Offset uintptr
	PreemptOffset     uintptr
	PreemptStopOffset uintptr // Offset of g.preemptStop from g pointer
	GStatusOffset     uintptr
	StackLoOffset     uintptr
	StackHiOffset     uintptr
	StackPreemptValue uintptr
	GRunning          uint32
	GScan             uint32
	GMOffset          uintptr // Offset of g.m from g pointer
	MG0Offset         uintptr // Offset of m.g0 from m pointer (always 0)
	MLocksOffset      uintptr // Offset of m.locks from m pointer
	MGsignalOffset    uintptr // Offset of m.gsignal from m pointer
}

// Global preemption offsets - set by InitPreemption(), read by assembly.
// These are exported (capitalized) so assembly can reference them.
var (
	PreemptStackGuard0Offset uintptr // Offset of g.stackguard0 from g pointer
	PreemptPreemptOffset     uintptr // Offset of g.preempt from g pointer
	PreemptPreemptStopOffset uintptr // Offset of g.preemptStop from g pointer
	PreemptGStatusOffset     uintptr // Offset of g.atomicstatus from g pointer
	PreemptStackLoOffset     uintptr // Offset of stack.lo (always 0)
	PreemptStackHiOffset     uintptr // Offset of stack.hi (always 8)
	PreemptStackPreemptValue uintptr // stackPreempt poison value
	PreemptGRunning          uint32  // _Grunning constant (2)
	PreemptGScan             uint32  // _Gscan bit mask (0x1000)
	PreemptOffsetsValid      uint32  // 1 when offsets have been initialized
	PreemptGMOffset          uintptr // Offset of g.m from g pointer
	PreemptMG0Offset         uintptr // Offset of m.g0 from m pointer (always 0)
	PreemptMLocksOffset      uintptr // Offset of m.locks from m pointer
	PreemptMGsignalOffset    uintptr // Offset of m.gsignal from m pointer
)

// TimerIRQCount is incremented by assembly on each timer IRQ.
// Used by the scheduler for time accounting.
var TimerIRQCount uint64

// Thread struct offsets for assembly access.
// IMPORTANT: These are computed dynamically via unsafe.Offsetof() in
// main.initThreadOffsets() and stored in main.ThreadStartTickOffset,
// main.ThreadPreemptDeadlineOffset, etc. Assembly loads these at runtime.
//
// DO NOT use hardcoded offsets - struct layout may change!

// Kernel timing policy constants. All timer and preemption intervals are
// derived from these two values. Assembly handlers read the computed tick
// variables (TimerRearmTicks, ThreadPreemptTicks) at runtime.
const (
	// TickIntervalMs is the kernel timer tick rate in milliseconds.
	// Matches Linux CONFIG_HZ=250 (4ms). Controls how often deadlines
	// are processed and pending signals are delivered.
	TickIntervalMs = 4

	// ThreadPreemptMs is the thread-level preemption quantum in milliseconds.
	// This is the coarse scheduling interval for switching between priests.
	// Fine-grained goroutine preemption within each priest is handled by
	// Go's sysmon sending SIGURG signals at ~10ms intervals.
	ThreadPreemptMs = 100
)

// TimerRearmTicks is the number of raw timer ticks for one kernel tick
// (TickIntervalMs). Set by InitPreemptThresholds() from timer frequency.
// Exported for assembly access (used by preempt_arm64.s, preempt_riscv64.s).
// Note: These defaults are never used at runtime — InitPreemptThresholds()
// is called (via InitPreemption) before the timer is enabled (EnableTimerIRQ).
var TimerRearmTicks uint64 = 40000 // Default: 4ms at 10MHz (RISC-V)

// ThreadPreemptTicks is the number of raw timer ticks before forcing a
// thread preemption (ThreadPreemptMs). Set by InitPreemptThresholds().
// Exported for assembly access.
// Note: Default never used at runtime — see TimerRearmTicks comment above.
var ThreadPreemptTicks uint64 = 6250000 // Default: 100ms at 62.5MHz (ARM64)

// InitPreemptThresholds calculates TimerRearmTicks and ThreadPreemptTicks
// from SystemTimerFrequency and the policy constants above.
// Caller must set SystemTimerFrequency before calling.
func InitPreemptThresholds() {
	TimerRearmTicks = (SystemTimerFrequency * TickIntervalMs) / 1000
	ThreadPreemptTicks = (SystemTimerFrequency * ThreadPreemptMs) / 1000
}

// NeedsThreadPreempt is set by assembly when the current thread has exceeded
// the thread preemption threshold and should be switched out.
// Checked by exception handler after TimerIRQHandlerAsm returns.
var NeedsThreadPreempt uint32

// Kernel time accounting - measures time spent in kernel mode
// All values are in timer ticks (use SystemTimerFrequency to convert to seconds)
var KernelTimerIRQTicks uint64      // Total ticks spent in timer IRQ handler
var KernelSyscallTicks uint64       // Total ticks spent in syscall handlers
var KernelContextSwitchTicks uint64 // Total ticks spent in context switches
var SyscallCount uint64             // Total number of syscalls
var ContextSwitchCount uint64       // Total number of context switches
var KernelStartTick uint64          // Counter value at start of measurement

// AddKernelTimerTicks adds ticks spent in timer IRQ handler (called from Go)
//
//go:nosplit
func AddKernelTimerTicks(ticks uint64) {
	atomic.AddUint64(&KernelTimerIRQTicks, ticks)
}

// AddKernelSyscallTicks adds ticks spent in syscall handler (called from Go)
//
//go:nosplit
func AddKernelSyscallTicks(ticks uint64) {
	atomic.AddUint64(&KernelSyscallTicks, ticks)
	atomic.AddUint64(&SyscallCount, 1)
}

// AddKernelContextSwitchTicks adds ticks spent in context switch (called from Go)
//
//go:nosplit
func AddKernelContextSwitchTicks(ticks uint64) {
	atomic.AddUint64(&KernelContextSwitchTicks, ticks)
	atomic.AddUint64(&ContextSwitchCount, 1)
}

// GetKernelTimeStats returns kernel time statistics
// Returns: timerTicks, syscallTicks, contextSwitchTicks, totalKernelTicks
//
//go:nosplit
func GetKernelTimeStats() (timerTicks, syscallTicks, contextSwitchTicks, totalKernelTicks uint64) {
	timerTicks = atomic.LoadUint64(&KernelTimerIRQTicks)
	syscallTicks = atomic.LoadUint64(&KernelSyscallTicks)
	contextSwitchTicks = atomic.LoadUint64(&KernelContextSwitchTicks)
	totalKernelTicks = timerTicks + syscallTicks + contextSwitchTicks
	return
}

// StartKernelTimeAccounting initializes the start tick for elapsed time calculation
//
//go:nosplit
func StartKernelTimeAccounting() {
	atomic.StoreUint64(&KernelStartTick, ReadCounterValue())
}

// GetElapsedTicks returns the elapsed ticks since StartKernelTimeAccounting was called
//
//go:nosplit
func GetElapsedTicks() uint64 {
	start := atomic.LoadUint64(&KernelStartTick)
	if start == 0 {
		return 0
	}
	return ReadCounterValue() - start
}

// System timer frequency in Hz - read from CNTFRQ_EL0 at init.
// Exported for use by other packages and assembly.
var SystemTimerFrequency uint64

// InitPreemption initializes the preemption subsystem.
// Must be called after Go runtime is fully initialized.
// Reads preemption offsets from runtime and stores them in global variables
// accessible by assembly.
//
// NOTE: Timer frequency and tick thresholds are set separately by
// initTimerFrequency() during DTB processing in simpleMain().
//
//go:nosplit
func InitPreemption() {
	// Read offsets from runtime
	offsets := getPreemptOffsets()

	// Store in globals for assembly access
	PreemptStackGuard0Offset = offsets.StackGuard0Offset
	PreemptPreemptOffset = offsets.PreemptOffset
	PreemptPreemptStopOffset = offsets.PreemptStopOffset
	PreemptGStatusOffset = offsets.GStatusOffset
	PreemptStackLoOffset = offsets.StackLoOffset
	PreemptStackHiOffset = offsets.StackHiOffset
	PreemptStackPreemptValue = offsets.StackPreemptValue
	PreemptGRunning = offsets.GRunning
	PreemptGScan = offsets.GScan
	PreemptGMOffset = offsets.GMOffset
	PreemptMG0Offset = offsets.MG0Offset
	PreemptMLocksOffset = offsets.MLocksOffset
	PreemptMGsignalOffset = offsets.MGsignalOffset

	// Memory barrier to ensure all stores are visible before setting valid flag
	atomic.StoreUint32(&PreemptOffsetsValid, 1)
}

// GetTimerTicksFor10ms returns the number of timer ticks for 10ms interval.
// Uses SystemTimerFrequency to compute accurate tick count.
//
//go:nosplit
func GetTimerTicksFor10ms() uint64 {
	return (SystemTimerFrequency * 10) / 1000
}


