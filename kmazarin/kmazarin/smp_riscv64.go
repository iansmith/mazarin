//go:build riscv64 && !test_stubs

package main

import "sync/atomic"

// SBI HSM error codes
const (
	SbiSuccess         int64 = 0
	SbiErrFailed       int64 = -1
	SbiErrNotSupported int64 = -2
	SbiErrInvalidParam int64 = -3
	SbiErrDenied       int64 = -4
	SbiErrInvalidAddr  int64 = -5
	SbiErrAlreadyAvail int64 = -6
	SbiErrAlreadyStarted int64 = -7
)

// Track which harts have been started
var riscvHartStarted [MaxCPUs]uint32

// sbiHartStartAsm calls SBI HSM hart_start
// Extension ID (a7) = 0x48534D ("HSM"), Function ID (a6) = 0 (hart_start)
// a0 = hart ID, a1 = start address, a2 = opaque value
// Returns SBI error code in a0
func sbiHartStartAsm(hartID uint64, startAddr uintptr, opaque uint64) int64

// platformSMPAvailable returns true if SBI HSM is available.
// SBI is always available in QEMU's OpenSBI firmware.
//
//go:nosplit
func platformSMPAvailable() bool {
	print("[SMP] RISC-V: SBI HSM hart_start available\n")
	return true
}

// platformWakeCPU wakes a secondary hart using SBI HSM hart_start.
// Returns 0 on success, negative SBI error code on failure.
//
//go:nosplit
func platformWakeCPU(cpuID uint64, entryPoint uintptr, contextID uint64) int64 {
	if cpuID == 0 || cpuID >= MaxCPUs {
		return SbiErrInvalidParam
	}

	// Check if already started
	if atomic.LoadUint32(&riscvHartStarted[cpuID]) != 0 {
		return SbiErrAlreadyStarted
	}

	result := sbiHartStartAsm(cpuID, entryPoint, contextID)
	if result == SbiSuccess {
		atomic.StoreUint32(&riscvHartStarted[cpuID], 1)
	}
	return result
}

// platformWakeErrorString returns a human-readable string for an SBI error code.
func platformWakeErrorString(code int64) string {
	switch code {
	case SbiSuccess:
		return "success"
	case SbiErrFailed:
		return "failed"
	case SbiErrNotSupported:
		return "not supported"
	case SbiErrInvalidParam:
		return "invalid parameter"
	case SbiErrDenied:
		return "denied"
	case SbiErrInvalidAddr:
		return "invalid address"
	case SbiErrAlreadyAvail:
		return "already available"
	case SbiErrAlreadyStarted:
		return "already started"
	default:
		return "unknown error"
	}
}
