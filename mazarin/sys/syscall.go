
// Package sys provides the client-side API for Mazzy-specific syscalls.
package sys

import (
	"mazzy/shared/mazzy"
	_ "unsafe" // For go:linkname
)

// DebugPutChar writes a single character to the kernel debug output.
// Uses a direct syscall with no Go runtime locks/synchronization.
// Safe to call from busy loops without blocking.
//
//go:nosplit
func DebugPutChar(c byte) {
	RawSyscall(mazzy.SysDebugPrint, uintptr(c), 0, 0, 0, 0, 0)
}

// DumpKernelStatus asks the kernel to emit one extra [status]
// snapshot at its next bottom-half wakeup. Diagnostic-only — call
// this from a .maz program when it observes an event of interest
// (slow syscall, async error) so the resulting log line shows what
// the kernel was doing concurrently. Marker 0xDB7 is reserved by
// ksyscall.DebugMarkerStatusDump.
//
//go:nosplit
func DumpKernelStatus() {
	RawSyscall(mazzy.SysDebugPrint, 0xDB7, 0, 0, 0, 0, 0)
}

// SetMapFailInjection arms (arm=true) or disarms (arm=false) the kernel's
// one-shot MapPageInProcess failure flag. When armed, the next single call
// to kmem.MapPageInProcess returns false without performing the mapping
// and then disarms itself. Test-only — used by xfertest to provoke the
// TransferDMAClump rollback path. Marker 0xDB8 is reserved by
// ksyscall.DebugMarkerSetMapFailNext.
//
//go:nosplit
func SetMapFailInjection(arm bool) {
	var v uintptr
	if arm {
		v = 1
	}
	RawSyscall(mazzy.SysDebugPrint, 0xDB8, v, 0, 0, 0, 0)
}

// SetMapFailAfter arms the kernel's countdown MapPageInProcess failure
// counter (kmem.MapPageInProcessFailAfter), sibling to SetMapFailInjection
// above. n >= 1 fires on the Nth forward MapPageInProcess call after
// arming (n=1 ⇒ next call, n=4 ⇒ four calls later); n <= 0 disarms the
// counter. The kernel decrement-and-fire is race-free, and rollback
// paths that re-enter MapPageInProcess will NOT re-fire — see
// kmem.MapPageInProcessFailAfter for the full convention.
//
// The cast through uint32 then uintptr preserves the int32 bit pattern
// across the syscall boundary; the kernel re-casts back via
// int32(uint32(v1)). Test-only — used by xfertest to exercise
// SyscallTransferPages mid-clump rollback (MAZ-37). Marker 0xDB9 is
// reserved by ksyscall.DebugMarkerSetMapFailAfter.
//
//go:nosplit
func SetMapFailAfter(n int32) {
	RawSyscall(mazzy.SysDebugPrint, 0xDB9, uintptr(uint32(n)), 0, 0, 0, 0)
}
