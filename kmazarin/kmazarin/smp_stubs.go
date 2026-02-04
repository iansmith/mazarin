//go:build test_stubs

package main

// Test stubs for SMP platform functions.
// Used when building tests without actual architecture-specific assembly.

func secondaryCPUEntry() {}

func getSecondaryCPUEntryAddr() uintptr { return 0 }

func platformWakeCPU(cpuID uint64, entryPoint uintptr, contextID uint64) int64 {
	return 0
}

func platformSMPAvailable() bool {
	return true
}

func platformWakeErrorString(code int64) string {
	return "test stub"
}
