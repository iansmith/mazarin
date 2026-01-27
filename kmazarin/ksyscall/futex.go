package ksyscall

import (
	"mazzy/kmazarin/console"
	"sync/atomic"
	"unsafe"
)

// Futex call statistics for debugging fairness
var (
	FutexWaitCalls    uint64 // Total FUTEX_WAIT calls
	FutexWaitBlocked  uint64 // Calls that blocked thread (switched to another)
	FutexWaitEagain   uint64 // Calls that returned EAGAIN (same thread continues)
	FutexWakeCalls    uint64 // Total FUTEX_WAKE calls
)

// PrintFutexStats prints futex statistics for debugging
func PrintFutexStats() (waitCalls, blocked, eagain, wakeCalls uint64) {
	return atomic.LoadUint64(&FutexWaitCalls),
		atomic.LoadUint64(&FutexWaitBlocked),
		atomic.LoadUint64(&FutexWaitEagain),
		atomic.LoadUint64(&FutexWakeCalls)
}

// TODO(ABI0): This file uses the tail-call stub workaround for SyscallFutex.
// We should investigate why the compiler-generated ABI0 wrapper doesn't work
// correctly when called via function pointer from the dispatch table, and fix
// our code to be compatible with it. See abi_stubs_arm64.s for details.

// Futex operations
const (
	FutexWait        = 0
	FutexWake        = 1
	FutexWaitPrivate = 128
	FutexWakePrivate = 129
)

// syscallFutexInternal implements the futex(2) syscall
// Properly blocks threads on FUTEX_WAIT and wakes them on FUTEX_WAKE
//
//go:nosplit
//go:noinline
func syscallFutexInternal(uaddr, op, val, timeout, uaddr2, val3 uint64) int64 {
	// Mask off the private flag
	opMasked := int32(op) & 0x7F

	switch opMasked {
	case FutexWait:
		atomic.AddUint64(&FutexWaitCalls, 1)

		// FUTEX_WAIT: Block until value changes or we're woken
		// Check if value matches expected (spurious wakeup check)
		uaddrPtr := (*uint32)(unsafe.Pointer(uintptr(uaddr)))
		currentVal := atomic.LoadUint32(uaddrPtr)

		if currentVal != uint32(val) {
			atomic.AddUint64(&FutexWaitEagain, 1)
			return -11 // -EAGAIN: value already changed
		}

		// Try to find another thread to run and block current thread
		// ThreadBlockFutex only marks us blocked if there's a thread to switch to
		nextThread := ThreadBlockFutex(uaddr)

		if nextThread != 0 {
			// Successfully blocked - context switch to next thread
			atomic.AddUint64(&FutexWaitBlocked, 1)
			console.Breadcrumb('[')
			console.Breadcrumb('F')
			console.Breadcrumb('U')
			console.Breadcrumb('T')
			console.Breadcrumb(']')
			SetSyscallSwitchTarget(nextThread)
			// Return 0 - when we're woken up later, we'll return success
			return 0
		}

		// No ready thread to switch to - we couldn't actually block
		// Return EAGAIN to let Go's runtime handle internal goroutine scheduling
		// This is important for fairness: the thread continues and Go can
		// switch to another runnable goroutine within the same thread.
		atomic.AddUint64(&FutexWaitEagain, 1)
		return -11 // -EAGAIN

	case FutexWake:
		atomic.AddUint64(&FutexWakeCalls, 1)
		// FUTEX_WAKE: Wake up to 'val' threads waiting on this address
		woken := ThreadWakeFutex(uaddr, int32(val))
		return int64(woken)

	default:
		// Unknown futex operation
		return -38 // ENOSYS
	}
}
