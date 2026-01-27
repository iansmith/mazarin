
// Package kirq provides kernel IRQ handling including timer-based preemption.
//
// This file implements cooperative preemption using Go's native mechanism:
// setting g.preempt=true and g.stackguard0=stackPreempt causes the next
// function call to trigger a yield via the stack growth check.

package kirq

import (
	"sync/atomic"
)

// PreemptOffsets mirrors runtime.PreemptOffsets for go:linkname access.
// These offsets are computed by the runtime using unsafe.Offsetof to ensure
// correctness across Go version changes.
type preemptOffsetsType struct {
	StackGuard0Offset uintptr
	PreemptOffset     uintptr
	GStatusOffset     uintptr
	StackLoOffset     uintptr
	StackHiOffset     uintptr
	StackPreemptValue uintptr
	GRunning          uint32
	GScan             uint32
	GMOffset          uintptr // Offset of g.m from g pointer
	MG0Offset         uintptr // Offset of m.g0 from m pointer (always 0)
	MLocksOffset      uintptr // Offset of m.locks from m pointer
}

// Global preemption offsets - set by InitPreemption(), read by assembly.
// These are exported (capitalized) so assembly can reference them.
var (
	PreemptStackGuard0Offset uintptr // Offset of g.stackguard0 from g pointer
	PreemptPreemptOffset     uintptr // Offset of g.preempt from g pointer
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

	// Async preemption addresses - set by SetAsyncPreemptAddr, read by assembly
	AsyncPreemptAddr        uint64 // Address of runtime.asyncPreempt
	AsyncPreemptWrapperAddr uint64 // Address of main.asyncPreemptWrapper
	ReadyForAsyncPreempt    uint32 // 1 when ready for async preemption
)

// Debug: Timer IRQ counter for tracking IRQ delivery issues
var TimerIRQCount uint64

// Per-goroutine tick tracking for async preemption fallback.
// Indexed by hash of g pointer. When a goroutine doesn't yield
// cooperatively after 10 ticks (100ms), we force async preemption.
var preemptTickCounts [1024]uint32

// Thread struct offsets for assembly access.
// These match the Thread struct layout in threads.go.
// Computed from struct layout:
//   State(4) + TID(4) + FutexAddr(8) + MPtr(8) + GPtr(8) + EntryFunc(8) = 40
//   Context: X[31]*8 + SP(8) + ELR(8) + SPSR(8) = 272
//   LastSeenG at offset 40+272=312, StartTick at offset 320
//   readyNode (pointer) at offset 328, total size 336
const (
	ThreadLastSeenGOffset = 312 // Offset of Thread.LastSeenG
	ThreadStartTickOffset = 320 // Offset of Thread.StartTick
	ThreadSize            = 336 // Size of Thread struct (includes readyNode)
)

// GoroutinePreemptIntervals is the number of 10ms intervals before forcing
// async preemption on a goroutine that hasn't yielded.
// 5 intervals = 50ms max runtime without yield.
// Exported for assembly access.
// DEPRECATED: Use GoroutinePreemptTicks instead for deadline-based preemption.
var GoroutinePreemptIntervals uint64 = 5

// ThreadPreemptIntervals is the number of 10ms intervals before forcing
// a thread preemption (context switch to another thread).
// 20 intervals = 200ms - gives goroutines time to work.
// Exported for assembly access.
// DEPRECATED: Use ThreadPreemptTicks instead for deadline-based preemption.
var ThreadPreemptIntervals uint64 = 20

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

// Debug counters for userspace preemption path
var UserspacePathCount uint64       // How often we enter userspace path
var UserspaceCoopPreemptCount uint64 // How often we set cooperative preemption
var UserspaceGChangedCount uint64   // How often g changed (goroutine switch detected)

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
	PreemptGStatusOffset = offsets.GStatusOffset
	PreemptStackLoOffset = offsets.StackLoOffset
	PreemptStackHiOffset = offsets.StackHiOffset
	PreemptStackPreemptValue = offsets.StackPreemptValue
	PreemptGRunning = offsets.GRunning
	PreemptGScan = offsets.GScan
	PreemptGMOffset = offsets.GMOffset
	PreemptMG0Offset = offsets.MG0Offset
	PreemptMLocksOffset = offsets.MLocksOffset

	// Read system timer frequency
	SystemTimerFrequency = uint64(asm_readCntfrqEl0())
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

// gHash returns a hash index for a g pointer (0-1023).
// Used to index into preemptTickCounts array.
//
//go:nosplit
func gHash(gptr uintptr) uint32 {
	// Simple hash: shift right by 8 (since g structs are large, ~500 bytes)
	// and mask to get index in range [0, 1023]
	return uint32((gptr >> 8) & 0x3FF)
}

// ResetPreemptTicks resets the tick counter for a goroutine.
// Called when a goroutine yields cooperatively (e.g., from scheduler).
//
//go:nosplit
func ResetPreemptTicks(gptr uintptr) {
	idx := gHash(gptr)
	atomic.StoreUint32(&preemptTickCounts[idx], 0)
}

// IncrementPreemptTicks increments the tick counter for a goroutine.
// Returns the new tick count. Called from assembly timer handler.
//
//go:nosplit
func IncrementPreemptTicks(gptr uintptr) uint32 {
	idx := gHash(gptr)
	return atomic.AddUint32(&preemptTickCounts[idx], 1)
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
