//go:build test_stubs

package main

// Test stubs for per-CPU assembly functions.
// These are used when building tests without actual ARM64 assembly.

// testCPUID is the simulated CPU ID for testing.
// Tests can set this to simulate different CPUs.
var testCPUID uint64 = 0

// readMPIDRAsm returns a simulated MPIDR_EL1 value for testing.
func readMPIDRAsm() uint64 {
	// Return testCPUID in Aff0 position (bits 7:0)
	return testCPUID & 0xFF
}

// getCPUIDAsm returns the simulated CPU ID for testing.
func getCPUIDAsm() uint64 {
	return testCPUID & 0xFF
}

// getPerCPUPtrAsm returns pointer to current CPU's PerCPU struct for testing.
func getPerCPUPtrAsm() uintptr {
	cpuID := testCPUID & 0xFF
	if cpuID >= MaxCPUs {
		cpuID = MaxCPUs - 1
	}
	return PerCPUDataBaseAddr() + uintptr(cpuID)*PerCPUSize
}

// SetTestCPUID sets the simulated CPU ID for testing.
// Call this before tests that need to simulate different CPUs.
func SetTestCPUID(cpuID uint64) {
	testCPUID = cpuID
}
