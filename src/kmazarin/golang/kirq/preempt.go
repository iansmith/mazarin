
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
var GoroutinePreemptIntervals uint64 = 5

// ThreadPreemptIntervals is the number of 10ms intervals before forcing
// a thread preemption (context switch to another thread).
// 20 intervals = 200ms - gives goroutines time to work.
// Exported for assembly access.
var ThreadPreemptIntervals uint64 = 20

// NeedsAsyncPreempt is set by assembly when a goroutine has exceeded
// the preemption threshold and needs async preemption injection.
// Checked by Go timer handler after TimerIRQHandlerAsm returns.
var NeedsAsyncPreempt uint32

// NeedsThreadPreempt is set by assembly when the current thread has exceeded
// the thread preemption threshold and should be switched out.
// Checked by exception handler after TimerIRQHandlerAsm returns.
var NeedsThreadPreempt uint32

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

	// Read system timer frequency
	SystemTimerFrequency = uint64(asm_readCntfrqEl0())
	if SystemTimerFrequency == 0 {
		SystemTimerFrequency = 62500000 // Default for QEMU virt
	}

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
