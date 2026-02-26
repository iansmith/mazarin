//go:build test_stubs

package ksyscall

import "unsafe"

// Stub implementations for forward declarations when running tests.
// These replace the assembly/linkname implementations during test builds.

// ============================================================================
// From panic.go
// ============================================================================

func Exit() {}

func KernelPanic(msg string) {
	panic("KernelPanic: " + msg)
}

// ============================================================================
// From nanosleep_asm.go
// ============================================================================

func GetCurrentThread() uintptr { return 0 }

func GetCurrentThreadTID() int16 { return 0 }

func AddDeadline(deadline uint64, tid int32) bool { return true }

func AddDeadlineStatic(deadline uint64, tid int32) {}

func ThreadBlockSleep() uintptr { return 0 }

// ============================================================================
// From clone_asm.go
// ============================================================================

func CloneThread(stack, returnAddr, spsr, mp, gp, fn uint64) int16 { return 1 }

func GetSyscallELR() uint64 { return 0 }

func GetSyscallSPSR() uint64 { return 0 }

func GetSyscallCloneRegs() (r12, r13, r9 uint64) { return 0, 0, 0 }

func SetSyscallCloneTLS(tls uint64) {}

// ============================================================================
// From exit_asm.go
// ============================================================================

func haltForever() {
	for {
	}
}

func ThreadExit() uintptr { return 0 }

// ============================================================================
// From futex_asm.go
// ============================================================================

func ThreadBlockFutex(futexAddr uint64, expectedVal uint32) uintptr { return 0 }

func ThreadWakeFutex(futexAddr uint64, maxWake int32) int32 { return 0 }

func SetSyscallSwitchTarget(target uintptr) {}

func ThreadFindReady() uintptr { return 0 }

func waitForInterrupt() {}

func SyscallFutex(uaddr, op, val, timeout, uaddr2, val3 uint64) int64 { return 0 }

// ============================================================================
// From mmap_asm.go
// ============================================================================

func getRuntimeConfig() interface{} { return nil }

// ============================================================================
// From launch_asm.go
// ============================================================================

func jumpToUserspace(entryPoint, stackPtr uint64) {}

func EnableTimerIRQ() {}

func CreateUserspaceThread(entryPoint, stackPtr uint64, pageTableL0PA uintptr, asyncPreemptAddr uint64) int16 {
	return 1
}

func unsafePointer(p uintptr) *byte {
	return (*byte)(unsafe.Pointer(p))
}

// ============================================================================
// From stubs_asm.go
// ============================================================================

func threadFindReadyForYield() uintptr { return 0 }

func setSyscallSwitchTargetForYield(target uintptr) {}

// ============================================================================
// From wait_softirq_asm.go
// ============================================================================

func ThreadBlockSoftIRQ(bundlePtr uint64) uintptr { return 0 }

func RegisterSoftIRQDispatcher() int64 { return 0 }

func GetPendingSoftIRQ(bundlePtr uint64) bool { return false }

// ============================================================================
// From register_async_preempt_asm.go
// ============================================================================

func RegisterAsyncPreemptAddr(asyncPreemptAddr uint64) int64 { return 0 }

// ============================================================================
// From signal_asm.go
// ============================================================================

func GetSignalAction(sig int) signalAction { return signalAction{} }

func SetSignalAction(sig int, handler, flags, restorer, mask uint64) {}

func ThreadLookupByTID(tid int32) uintptr { return 0 }

func SetThreadPendingSignal(threadPtr uintptr, signum int) {}

func GetThreadSignalStack(threadPtr uintptr) (base, sp, size uint64) { return 0, 0, 0 }

func SetThreadSignalStack(threadPtr uintptr, base, sp, size uint64) {}

func GetThreadPID(threadPtr uintptr) int16 { return -1 }

func RestoreFromSignalFrame(threadPtr uintptr) {}
