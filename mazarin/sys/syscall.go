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

// ConsoleFloodTest asks the kernel to emit n (clamped to 64) filler lines
// through klog.Errf from inside THIS syscall's handler context. With the
// soft-IRQ console active, sustained calls saturate the kernel console
// ring and force pushStringFull's ring-full park from handler context —
// the yield path under test in xfertest's console-backpressure stage
// (MAZ-193). Returns the number of lines the kernel emitted. Test-only.
// Marker 0xDBB is reserved by ksyscall.DebugMarkerConsoleFlood.
//
//go:nosplit
func ConsoleFloodTest(n uint32) int64 {
	r1, _, _ := RawSyscall(mazzy.SysDebugPrint, 0xDBB, uintptr(n), 0, 0, 0, 0)
	return int64(r1)
}

// RingPushParkCount returns the kernel console's cumulative ring-full
// park count (pushStringFull entries into YieldToReadyThread). Test-only
// companion to ConsoleFloodTest: xfertest samples it before and after
// the flood to assert the park path was actually exercised (MAZ-193).
// Marker 0xDBC is reserved by ksyscall.DebugMarkerRingPushParks.
//
//go:nosplit
func RingPushParkCount() uint64 {
	r1, _, _ := RawSyscall(mazzy.SysDebugPrint, 0xDBC, 0, 0, 0, 0, 0)
	return uint64(r1)
}

// HandlerCtxDropCount returns the kernel console's cumulative count of
// ring-full drops taken in handler context (pushStringFull's MAZ-193
// refuse-to-park branch). Test-only companion to ConsoleFloodTest: on a
// fixed kernel the flood must drive this counter, proving the
// backpressure path under test actually ran. Marker 0xDBD is reserved
// by ksyscall.DebugMarkerHandlerCtxDrops.
//
//go:nosplit
func HandlerCtxDropCount() uint64 {
	r1, _, _ := RawSyscall(mazzy.SysDebugPrint, 0xDBD, 0, 0, 0, 0, 0)
	return uint64(r1)
}

// GetPTEFlags returns the caller's L3 PTE permission flags for va as ELF
// permission bits: bit 0 = ELF_PF_X (execute), bit 1 = ELF_PF_W (write),
// bit 2 = ELF_PF_R (read — always set for a valid mapping). Returns 0 if
// the page is unmapped at L3 in the caller's address space.
//
// Test-only — used by xfertest to verify the TransferPages /
// TransferDMAClump rollback restores caller permissions exactly (MAZ-39).
// Marker 0xDBA is reserved by ksyscall.DebugMarkerGetPTEFlags.
//
//go:nosplit
func GetPTEFlags(va uintptr) uint32 {
	r1, _, _ := RawSyscall(mazzy.SysDebugPrint, 0xDBA, va, 0, 0, 0, 0)
	return uint32(r1)
}
