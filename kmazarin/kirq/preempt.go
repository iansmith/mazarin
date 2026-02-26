
// Package kirq provides kernel IRQ handling including timer-based preemption.
//
// This file implements cooperative preemption using Go's native mechanism:
// setting g.preempt=true and g.stackguard0=stackPreempt causes the next
// function call to trigger a yield via the stack growth check.

package kirq

import (
	"mazzy/kmazarin/ktimer"
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

	// Async preemption addresses - set by SetAsyncPreemptAddr, read by assembly
	AsyncPreemptAddr        uint64 // Address of runtime.asyncPreempt
	AsyncPreemptWrapperAddr uint64 // Address of main.asyncPreemptWrapper
	ReadyForAsyncPreempt    uint32 // 1 when ready for async preemption
)

// TimerIRQCount is incremented by assembly on each timer IRQ.
// Used by the scheduler for time accounting.
var TimerIRQCount uint64

// Thread struct offsets for assembly access.
// IMPORTANT: These are computed dynamically via unsafe.Offsetof() in
// main.initThreadOffsets() and stored in main.ThreadLastSeenGOffset,
// main.ThreadStartTickOffset, etc. Assembly loads these at runtime.
//
// DO NOT use hardcoded offsets - struct layout may change!

// TimerRearmTicks is the number of raw timer ticks to add when re-arming
// the timer. At 10MHz: 10ms = 100,000 ticks.
// Set by InitPreemptThresholds() based on actual timer frequency.
// Exported for assembly access (used by preempt_riscv64.s).
var TimerRearmTicks uint64 = 100000

// GoroutinePreemptTicks is the number of raw timer ticks before forcing
// async preemption on a goroutine. At 62.5MHz: 50ms = 3,125,000 ticks.
// Set by InitPreemptThresholds() based on actual timer frequency.
// Exported for assembly access.
var GoroutinePreemptTicks uint64 = 3125000

// ThreadPreemptTicks is the number of raw timer ticks before forcing
// a thread preemption. At 62.5MHz: 200ms = 12,500,000 ticks.
// Set by InitPreemptThresholds() based on actual timer frequency.
// Exported for assembly access.
var ThreadPreemptTicks uint64 = 12500000

// InitPreemptThresholds calculates preemption thresholds based on timer frequency.
// Call this after SystemTimerFrequency is set.
func InitPreemptThresholds() {
	freq := SystemTimerFrequency
	if freq == 0 {
		freq = 62500000 // Default QEMU frequency
	}
	// TimerRearmTicks = 10ms worth of ticks
	TimerRearmTicks = freq / 100 // freq * 0.01 = freq / 100
	// GoroutinePreemptTicks = 50ms worth of ticks
	GoroutinePreemptTicks = freq / 20 // freq * 0.05 = freq / 20
	// ThreadPreemptTicks = 200ms worth of ticks
	ThreadPreemptTicks = freq / 5 // freq * 0.2 = freq / 5
}

// NeedsAsyncPreempt is set by assembly when a goroutine has exceeded
// the preemption threshold and needs async preemption injection.
// Checked by Go timer handler after TimerIRQHandlerAsm returns.
var NeedsAsyncPreempt uint32

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

	// Read system timer frequency via ktimer
	SystemTimerFrequency = uint64(ktimer.Frequency())
	if SystemTimerFrequency == 0 {
		SystemTimerFrequency = 62500000 // Default for QEMU virt
	}

	// Initialize deadline-based preemption thresholds based on timer frequency
	InitPreemptThresholds()

	// Memory barrier to ensure all stores are visible before setting valid flag
	atomic.StoreUint32(&PreemptOffsetsValid, 1)
}

// SetAsyncPreemptAddr sets the asyncPreempt address and marks ready for async preemption.
// Called from main package after getting the address from RuntimeConfig.
//
//go:nosplit
func SetAsyncPreemptAddr(addr uintptr) {
	AsyncPreemptAddr = uint64(addr)
}

// SetAsyncPreemptWrapperAddr sets the asyncPreemptWrapper address.
// Called from main package init() after getting the address via assembly helper.
// The IRQ handler reads this address to inject async preemption.
//
//go:nosplit
func SetAsyncPreemptWrapperAddr(addr uintptr) {
	AsyncPreemptWrapperAddr = uint64(addr)
}

// SetReadyForAsyncPreempt marks the system as ready for async preemption.
// Called when the Go runtime is fully initialized.
//
//go:nosplit
func SetReadyForAsyncPreempt() {
	atomic.StoreUint32(&ReadyForAsyncPreempt, 1)
}

// GetPreemptOffsetDebug returns the preemption offsets for debug printing.
// Only valid after InitPreemption() has been called.
func GetPreemptOffsetDebug() (stackguard0, preempt, status, stackPreempt uintptr, gRunning, gScan uint32) {
	return PreemptStackGuard0Offset, PreemptPreemptOffset, PreemptGStatusOffset,
		PreemptStackPreemptValue, PreemptGRunning, PreemptGScan
}

// GetTimerTicksFor10ms returns the number of timer ticks for 10ms interval.
// Uses SystemTimerFrequency to compute accurate tick count.
//
//go:nosplit
func GetTimerTicksFor10ms() uint64 {
	return (SystemTimerFrequency * 10) / 1000
}

// KernelYieldCount tracks how many times the kernel voluntarily yielded
// due to preemption checks in direct function calls.
var KernelYieldCount uint64

// KernelWriteCount counts writes for yielding decisions
var kernelWriteCount uint64

// KernelYieldInterval controls how often the kernel yields (every N writes)
// Setting this to match priest behavior: priests do many syscalls per timeslice,
// and each syscall gives a chance to yield. We simulate similar behavior.
const KernelYieldInterval = 100

// CheckAndYieldPreemption simulates what would happen if kernel direct function
// calls also checked for preemption (like userspace syscalls do).
//
// This yields every KernelYieldInterval writes to give other threads a chance
// to run. This simulates the behavior of priests, which can be preempted on
// every syscall return if another thread is waiting.
//
// Returns true if we yielded, false otherwise.
//
//go:nosplit
func CheckAndYieldPreemption() bool {
	// TEMPORARILY DISABLED: SVC yield causes crashes
	// The crash is E:20 (instruction abort) with ELR=0, suggesting
	// corrupted return address during context switch.
	return false

	// Increment write counter
	count := atomic.AddUint64(&kernelWriteCount, 1)

	// Only yield every N writes
	if count%KernelYieldInterval != 0 {
		return false
	}

	// Track yield count for debugging
	atomic.AddUint64(&KernelYieldCount, 1)

	// Issue SVC #124 (sched_yield) to trigger a voluntary yield
	// This will go through the exception handler which will:
	// 1. Call SyscallSchedYield
	// 2. If another thread is ready, switch to it
	// 3. If no other thread ready, return immediately
	asmYieldSVC()

	return true
}

// asmYieldSVC is implemented in preempt_yield_arm64.s
// Issues SVC #124 (sched_yield syscall) to trigger a voluntary yield.
func asmYieldSVC()

// ============================================================================
// Per-CPU Accessors (implemented in main package via linkname)
// ============================================================================

// getPerCPUNeedsAsyncPreempt returns the per-CPU NeedsAsyncPreempt value.
// Implemented in main/linkname_impl.go.
//
//go:linkname getPerCPUNeedsAsyncPreempt
func getPerCPUNeedsAsyncPreempt() uint32

// setPerCPUNeedsAsyncPreempt sets the per-CPU NeedsAsyncPreempt value.
// Implemented in main/linkname_impl.go.
//
//go:linkname setPerCPUNeedsAsyncPreempt
func setPerCPUNeedsAsyncPreempt(val uint32)

// syncGlobalNeedsAsyncPreemptToPerCPU copies the global to per-CPU.
// Implemented in main/linkname_impl.go.
//
//go:linkname syncGlobalNeedsAsyncPreemptToPerCPU
func syncGlobalNeedsAsyncPreemptToPerCPU(globalVal uint32)
