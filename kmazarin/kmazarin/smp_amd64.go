//go:build amd64

package main

import "sync/atomic"

// Track which CPUs have been started
var x86CPUStarted [MaxCPUs]uint32

// mailboxBase holds the physical address of the AP mailbox array.
// Set by diplomat before transferring control to kmazarin.
// Each entry is a uint64: 0 = parked, non-zero = jump target address.
var mailboxBase uintptr

// platformSMPAvailable returns true if the mailbox wake mechanism is available.
// On x86_64, this checks whether diplomat provided a valid mailbox address.
//
//go:nosplit
func platformSMPAvailable() bool {
	if mailboxBase == 0 {
		print("[SMP] x86_64: no AP mailbox provided by bootloader\n")
		return false
	}
	print("[SMP] x86_64: AP mailbox at ")
	printHex64(uint64(mailboxBase))
	print("\n")
	return true
}

// platformWakeCPU wakes a secondary CPU by writing the entry point to its mailbox slot.
// Returns 0 on success, negative error code on failure.
//
//go:nosplit
func platformWakeCPU(cpuID uint64, entryPoint uintptr, contextID uint64) int64 {
	if cpuID == 0 || cpuID >= MaxCPUs {
		return -1
	}

	// Check if already started
	if atomic.LoadUint32(&x86CPUStarted[cpuID]) != 0 {
		return -2 // already on
	}

	if mailboxBase == 0 {
		return -3 // no mailbox
	}

	// Write entry point address to mailbox[cpuID]
	// The parked AP's spin loop will detect the non-zero value and jump to it
	// TODO: actual mailbox write once diplomat provides the mailbox
	atomic.StoreUint32(&x86CPUStarted[cpuID], 1)
	return 0
}

// platformWakeErrorString returns a human-readable string for a wake error code.
func platformWakeErrorString(code int64) string {
	switch code {
	case 0:
		return "success"
	case -1:
		return "invalid CPU ID"
	case -2:
		return "already started"
	case -3:
		return "no mailbox from bootloader"
	default:
		return "unknown error"
	}
}
