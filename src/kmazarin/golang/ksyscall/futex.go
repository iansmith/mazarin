//go:build qemuvirt && aarch64

package ksyscall

import (
	"kmazarin/console"
	"sync/atomic"
	"unsafe"
	_ "unsafe" // for go:linkname
)

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

// Thread functions imported from main package via go:linkname
// These handle the actual thread state management
// Note: Functions return uintptr (thread node pointer, 0 = none)

//go:linkname ThreadBlockFutex main.ThreadBlockFutex
func ThreadBlockFutex(futexAddr uint64) uintptr

//go:linkname ThreadWakeFutex main.ThreadWakeFutex
func ThreadWakeFutex(futexAddr uint64, maxWake int32) int32

// SetSyscallSwitchTarget links to kmazarin's main package (kmazarin/golang/kmazarin/threads.go)
// Note: "main" refers to kmazarin's main package, not cardinal's, since this is part of the kmazarin binary
// target is a uintptr (thread node pointer, 0 = no switch)
//go:linkname SetSyscallSwitchTarget main.SetSyscallSwitchTarget
func SetSyscallSwitchTarget(target uintptr)

//go:linkname ThreadFindReady main.ThreadFindReady
func ThreadFindReady() uintptr

// WaitForInterrupt puts CPU into low-power idle state until interrupt
// Used to prevent busy-wait when futex can't block
//go:linkname waitForInterrupt main.WaitForInterrupt
func waitForInterrupt()

// SyscallFutex is the exported entry point called via function pointer from dispatch.go
// Implemented as an assembly stub in abi_stubs_arm64.s that tail-calls syscallFutexInternal
func SyscallFutex(uaddr, op, val, timeout, uaddr2, val3 uint64) int64

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
		// FUTEX_WAIT: Block until value changes or we're woken
		// Check if value matches expected (spurious wakeup check)
		uaddrPtr := (*uint32)(unsafe.Pointer(uintptr(uaddr)))
		currentVal := atomic.LoadUint32(uaddrPtr)

		if currentVal != uint32(val) {
			return -11 // -EAGAIN: value already changed
		}

		// Try to find another thread to run and block current thread
		// ThreadBlockFutex only marks us blocked if there's a thread to switch to
		nextThread := ThreadBlockFutex(uaddr)

		if nextThread != 0 {
			// Successfully blocked - context switch to next thread
			SetSyscallSwitchTarget(nextThread)
			// Return 0 - when we're woken up later, we'll return success
			return 0
		}

		// No ready thread to switch to - we couldn't actually block
		// DEBUG: Print 'W' to show we're entering idle wait
		console.WriteByte('W')

		// Enter idle loop waiting for interrupt (timer will fire and may wake threads)
		// WFI - Wait For Interrupt: puts CPU in low-power state until interrupt
		waitForInterrupt()

		// After WFI, check if someone woke us (value changed or wake signal)
		currentVal = atomic.LoadUint32(uaddrPtr)
		if currentVal != uint32(val) {
			return 0 // Value changed - treat as woken
		}

		// Value still matches - retry
		return -11 // -EAGAIN

	case FutexWake:
		// FUTEX_WAKE: Wake up to 'val' threads waiting on this address
		woken := ThreadWakeFutex(uaddr, int32(val))
		return int64(woken)

	default:
		// Unknown futex operation
		return -38 // ENOSYS
	}
}
