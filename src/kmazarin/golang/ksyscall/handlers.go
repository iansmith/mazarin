
package ksyscall

import (
	"kmazarin/kthread"
)

// Syscall return codes for assembly
const (
	SyscallReturnNormal = 0 // Return normally with value in x0
	SyscallReturnSwitch = 1 // Context switch to thread in x1
	SyscallReturnBlock  = 2 // Block current thread, switch to thread in x1
)

// Futex operations are defined in futex.go

// SyscallCloneHandler handles the clone syscall
// Called from assembly with clone parameters already extracted from stack
// Returns: (tid int64, switchTo int32)
//   - tid: TID of new thread (returned to parent)
//   - switchTo: -1 to return normally, >=0 to switch to that thread
//
//go:nosplit
//go:noinline
func SyscallCloneHandler(stack, entryFunc, mPtr, gPtr uint64) (tid int64, switchTo int32) {
	// Create new thread in READY state
	newTID := kthread.ThreadCreate(stack, entryFunc, mPtr, gPtr)
	if newTID < 0 {
		return -1, -1 // Error, return -1 to caller
	}

	// Return TID to parent, don't switch yet
	// The new thread will run when parent blocks or timer preempts
	return int64(newTID), -1
}

// SyscallFutexHandler handles the futex syscall
// Returns: (result int64, switchTo int32)
//   - result: 0 for WAIT, number woken for WAKE
//   - switchTo: -1 to return normally, >=0 to switch to that thread
//
//go:nosplit
//go:noinline
func SyscallFutexHandler(uaddr uint64, op int32, val uint32) (result int64, switchTo int32) {
	// Mask off private flag
	opMasked := op & 0x7F

	switch opMasked {
	case FutexWait:
		// FUTEX_WAIT: Block until someone wakes us
		nextThread := kthread.ThreadBlockFutex(uaddr)
		if nextThread >= 0 {
			// Context switch to next thread
			return 0, nextThread
		}
		// No other thread ready - spurious wakeup
		return 0, -1

	case FutexWake:
		// FUTEX_WAKE: Wake up to 'val' threads blocked on this address
		woken := kthread.ThreadWakeFutex(uaddr, int32(val))
		return int64(woken), -1

	default:
		return 0, -1
	}
}

// SyscallNanosleepHandler handles the nanosleep syscall
// duration is in nanoseconds
// Returns: (result int64, switchTo int32)
//
//go:nosplit
//go:noinline
func SyscallNanosleepHandler(seconds uint64, nanoseconds uint64) (result int64, switchTo int32) {
	// Calculate total nanoseconds
	totalNs := seconds*1000000000 + nanoseconds

	// Convert to timer ticks
	// ticks = ns * freq / 1e9
	// To avoid overflow: ticks = (ns / 1000) * (freq / 1000000)
	//                         = (ns / 1000) * (freq_mhz)
	const timerFrequencyHz = 62500000 // 62.5 MHz for QEMU
	freqMHz := uint64(timerFrequencyHz / 1000000)
	ticks := (totalNs / 1000) * freqMHz

	if ticks == 0 {
		ticks = 1 // Minimum 1 tick
	}

	// Block current thread
	nextThread := kthread.ThreadBlockSleep(ticks)
	if nextThread >= 0 {
		return 0, nextThread
	}

	// No other thread ready - don't actually sleep
	return 0, -1
}

// TimerTickHandler is called from timer interrupt
// Checks for threads to wake and optionally preempts
// Returns: switchTo int32 (-1 for no switch, >=0 to switch)
//
//go:nosplit
//go:noinline
func TimerTickHandler() int32 {
	// Increment global tick counter
	kthread.ThreadIncrementTick()

	// Check sleeping threads
	kthread.ThreadCheckSleepers()

	// For now, no preemption - just wake sleepers
	// Could add round-robin preemption here later

	return -1
}

// SyscallMmapHandler handles the mmap syscall
// For now, this is a stub that will be implemented with proper page allocation
// Returns: (addr int64, error int32)
//
//go:nosplit
//go:noinline
func SyscallMmapHandler(addr uint64, length uint64, prot int32, flags int32, fd int32, offset uint64) (result int64, err int32) {
	// TODO: Implement proper page allocation and mapping
	// For now, return error (not implemented)
	return -1, 0
}

// SyscallMunmapHandler handles the munmap syscall
// For now, this is a stub that will be implemented with proper page deallocation
// Returns: (result int64, error int32)
//
//go:nosplit
//go:noinline
func SyscallMunmapHandler(addr uint64, length uint64) (result int64, err int32) {
	// TODO: Implement proper page deallocation
	// For now, return success (no-op)
	return 0, 0
}
