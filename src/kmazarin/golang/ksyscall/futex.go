//go:build qemuvirt && aarch64

package ksyscall

import (
	"sync/atomic"
	"unsafe"
	_ "unsafe" // for go:linkname
)

// Futex operations
const (
	FutexWait        = 0
	FutexWake        = 1
	FutexWaitPrivate = 128
	FutexWakePrivate = 129
)

// Thread functions imported from main package via go:linkname
// These handle the actual thread state management

//go:linkname ThreadBlockFutex main.ThreadBlockFutex
func ThreadBlockFutex(futexAddr uint64) int32

//go:linkname ThreadWakeFutex main.ThreadWakeFutex
func ThreadWakeFutex(futexAddr uint64, maxWake int32) int32

//go:linkname SetSyscallSwitchTarget main.SetSyscallSwitchTarget
func SetSyscallSwitchTarget(target int32)

//go:linkname ThreadFindReady main.ThreadFindReady
func ThreadFindReady() int32

// SyscallFutex implements the futex(2) syscall
// Properly blocks threads on FUTEX_WAIT and wakes them on FUTEX_WAKE
//
//go:nosplit
func SyscallFutex(uaddr, op, val, timeout, uaddr2, val3 uint64) int64 {
	uart := uintptr(0xFFFFFFFF09000000)

	// Mask off the private flag
	opMasked := int32(op) & 0x7F

	switch opMasked {
	case FutexWait:
		*(*byte)(unsafe.Pointer(uart)) = 'W'

		// FUTEX_WAIT: Block until value changes or we're woken
		// Check if value matches expected (spurious wakeup check)
		uaddrPtr := (*uint32)(unsafe.Pointer(uintptr(uaddr)))
		currentVal := atomic.LoadUint32(uaddrPtr)

		if currentVal != uint32(val) {
			*(*byte)(unsafe.Pointer(uart)) = 'a'
			return -11 // -EAGAIN: value already changed
		}

		// Find another thread to run
		nextThread := ThreadBlockFutex(uaddr)

		if nextThread < 0 {
			// No other threads to run - we're the only one
			// This happens when M0 waits for M1, but M1 isn't ready yet
			// In this case, we fake the wakeup (same as before)
			*(*byte)(unsafe.Pointer(uart)) = 'f'
			atomic.StoreUint32(uaddrPtr, 1)
			return 0
		}

		// Signal that we need to context switch to nextThread
		*(*byte)(unsafe.Pointer(uart)) = 's'
		hexChars := "0123456789ABCDEF"
		*(*byte)(unsafe.Pointer(uart)) = hexChars[(nextThread>>4)&0xF]
		*(*byte)(unsafe.Pointer(uart)) = hexChars[nextThread&0xF]

		SetSyscallSwitchTarget(nextThread)

		// Return 0 - when we're woken up later, we'll return success
		return 0

	case FutexWake:
		*(*byte)(unsafe.Pointer(uart)) = 'K'

		// FUTEX_WAKE: Wake up to 'val' threads waiting on this address
		woken := ThreadWakeFutex(uaddr, int32(val))

		// Print number of threads woken
		hexChars := "0123456789ABCDEF"
		*(*byte)(unsafe.Pointer(uart)) = hexChars[(woken>>4)&0xF]
		*(*byte)(unsafe.Pointer(uart)) = hexChars[woken&0xF]

		return int64(woken)

	default:
		// Unknown futex operation
		*(*byte)(unsafe.Pointer(uart)) = '?'
		return -38 // ENOSYS
	}
}
