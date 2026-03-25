
// Package kirq provides kernel IRQ handling including timer-based thread preemption.
//
// Goroutine-level preemption is handled by the Go runtime in userspace via
// SIGURG signals. The kernel only performs thread-level preemption when a
// thread (shepherd) has exceeded its time quantum.

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

	// Kernel goroutine async preemption offsets
	MSignalPendingOffset uintptr // Offset of m.signalPending (atomic.Uint32)
	MPreemptGenOffset    uintptr // Offset of m.preemptGen (atomic.Uint32)
	MCurgOffset          uintptr // Offset of m.curg (*g)
	MMallocingOffset     uintptr // Offset of m.mallocing (int32)
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

	// Kernel goroutine async preemption offsets
	PreemptMSignalPendingOffset uintptr // Offset of m.signalPending (atomic.Uint32)
	PreemptMPreemptGenOffset    uintptr // Offset of m.preemptGen (atomic.Uint32)
	PreemptMCurgOffset          uintptr // Offset of m.curg (*g)
	PreemptMMallocingOffset     uintptr // Offset of m.mallocing (int32)
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

// Kernel timing policy values. Set by InitPreemptConfig() from TOML boot
// config. Assembly handlers read the computed tick variables
// (TimerRearmTicks, ThreadPreemptTicks) at runtime.
var (
	// KernelTickRate is the timer tick frequency in Hz. Default 250 (4ms ticks).
	// Set from TOML kernel_tick_rate.
	KernelTickRate uint64 = 250

	// PreemptAfterTicks is the number of kernel ticks per preemption quantum.
	// Default 25 (25 × 4ms = 100ms at 250Hz). Set from TOML preempt_after_ticks.
	PreemptAfterTicks uint64 = 25

	// TickIntervalMs is the kernel timer tick period in milliseconds.
	// Computed: 1000 / KernelTickRate. Default 4ms.
	TickIntervalMs uint64 = 4

	// PreemptIntervalMs is the thread preemption quantum in milliseconds.
	// Computed: TickIntervalMs * PreemptAfterTicks. Default 100ms.
	// Fine-grained goroutine preemption within each shepherd is handled by
	// Go's sysmon sending SIGURG signals at ~10ms intervals.
	PreemptIntervalMs uint64 = 100
)

// TimerRearmTicks is the number of raw timer ticks for one kernel tick
// (TickIntervalMs). Set by InitPreemptConfig() from timer frequency.
// Exported for assembly access (used by preempt_arm64.s, preempt_riscv64.s).
// Note: These defaults are never used at runtime — InitPreemptConfig()
// is called before the timer is enabled (EnableTimerIRQ).
var TimerRearmTicks uint64 = 40000 // Default: 4ms at 10MHz (RISC-V)

// ThreadPreemptTicks is the number of raw timer ticks before forcing a
// thread preemption (PreemptIntervalMs). Set by InitPreemptConfig().
// Exported for assembly access.
// Note: Default never used at runtime — see TimerRearmTicks comment above.
var ThreadPreemptTicks uint64 = 6250000 // Default: 100ms at 62.5MHz (ARM64)

// InitPreemptConfig sets the timing policy from TOML config values and
// computes all derived tick counts. Caller must set SystemTimerFrequency
// before calling. Pass 0 for either parameter to use defaults.
func InitPreemptConfig(tickRate, preemptTicks int) {
	if tickRate > 0 {
		KernelTickRate = uint64(tickRate)
	}
	if preemptTicks > 0 {
		PreemptAfterTicks = uint64(preemptTicks)
	}

	// Derived millisecond values.
	TickIntervalMs = 1000 / KernelTickRate
	PreemptIntervalMs = TickIntervalMs * PreemptAfterTicks

	// Hardware counter ticks per kernel tick and per preemption quantum.
	TimerRearmTicks = SystemTimerFrequency / KernelTickRate
	ThreadPreemptTicks = TimerRearmTicks * PreemptAfterTicks
}

// NeedsThreadPreempt is set by assembly when the current thread has exceeded
// the thread preemption threshold and should be switched out.
// Checked by exception handler after TimerIRQHandlerAsm returns.
var NeedsThreadPreempt uint32

// Diagnostic counters for timer preemption debugging (written by assembly)
var DbgTimerReachedCheck uint64  // timer ticks that reached deadline comparison
var DbgTimerDeadlineHit uint64   // times current >= deadline (preempt signaled)
var DbgTimerDeadlineNotHit uint64 // times current < deadline (no preempt)

// Timer interval instrumentation (written by assembly timer handler)
var DbgTimerPrevCounter uint64    // counter at previous timer fire
var DbgTimerFirstCounter uint64   // counter at first timer fire
var DbgTimerLatestCounter uint64  // counter at most recent timer fire
var DbgTimerMaxDelta uint64       // largest gap between consecutive fires

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
	PreemptMSignalPendingOffset = offsets.MSignalPendingOffset
	PreemptMPreemptGenOffset = offsets.MPreemptGenOffset
	PreemptMCurgOffset = offsets.MCurgOffset
	PreemptMMallocingOffset = offsets.MMallocingOffset

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


