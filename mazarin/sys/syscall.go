
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
