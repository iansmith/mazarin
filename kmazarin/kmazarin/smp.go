//go:build arm64

// Package main provides SMP (Symmetric Multi-Processing) boot support.
//
// This file coordinates the boot sequence for secondary CPUs:
// 1. Allocate per-CPU stacks
// 2. Wake each secondary CPU via PSCI CPU_ON
// 3. Wait for each CPU to signal it's online

package main

import (
	"sync/atomic"
)

// Assembly function declarations
func secondaryCPUEntry()
func getSecondaryCPUEntryAddr() uintptr

// StartSecondaryCPUs wakes all secondary CPUs (1 to cpuCount-1).
// Must be called after:
// - Per-CPU data structures are initialized (InitPerCPU)
// - Memory allocator is ready (for stack allocation)
// - Exception vectors are set up
//
// Returns the number of CPUs successfully started.
func StartSecondaryCPUs() int {
	cpuCount := GetCPUCount()
	if cpuCount <= 1 {
		return 0 // Only one CPU, nothing to do
	}

	// Cap at MaxCPUs
	if cpuCount > MaxCPUs {
		cpuCount = MaxCPUs
	}

	// Allocate stacks for secondary CPUs
	if !AllocSecondaryCPUStacks(cpuCount) {
		print("[SMP] Failed to allocate secondary CPU stacks\n")
		return 0
	}

	// Get the entry point address for secondary CPUs
	entryPoint := getSecondaryCPUEntryAddr()
	print("[SMP] Secondary CPU entry point: ")
	printHex64(uint64(entryPoint))
	print("\n")

	// Check PSCI version
	major, minor := GetPsciVersion()
	print("[SMP] PSCI version: ")
	print(uint64String(uint64(major)))
	print(".")
	print(uint64String(uint64(minor)))
	print("\n")

	if major == 0 && minor == 0 {
		print("[SMP] PSCI not available, cannot start secondary CPUs\n")
		return 0
	}

	successCount := 0

	// Wake each secondary CPU
	for cpuID := uint64(1); cpuID < cpuCount; cpuID++ {
		print("[SMP] Waking CPU ")
		print(uint64String(cpuID))
		print("... ")

		// Call PSCI CPU_ON
		// context_id = cpuID (passed to entry point in X0)
		result := WakeCPU(cpuID, entryPoint, cpuID)

		if result == PsciSuccess {
			print("OK\n")
			successCount++
		} else {
			print("FAILED (")
			print(PsciErrorString(result))
			print(")\n")
		}
	}

	// Wait for CPUs to come online (with timeout)
	print("[SMP] Waiting for CPUs to initialize...\n")
	for cpuID := uint64(1); cpuID < cpuCount; cpuID++ {
		// Simple polling with timeout
		timeout := 1000000 // Arbitrary count
		for timeout > 0 {
			if atomic.LoadUint32(&perCPUData[cpuID].Online) != 0 {
				break
			}
			timeout--
		}

		if timeout == 0 {
			print("[SMP] CPU ")
			print(uint64String(cpuID))
			print(" timeout waiting for online\n")
		}
	}

	// Print summary
	onlineCount := 0
	for cpuID := uint64(0); cpuID < cpuCount; cpuID++ {
		if atomic.LoadUint32(&perCPUData[cpuID].Online) != 0 {
			onlineCount++
		}
	}
	print("[SMP] ")
	print(uint64String(uint64(onlineCount)))
	print(" of ")
	print(uint64String(cpuCount))
	print(" CPUs online\n")

	return successCount
}

// GetOnlineCPUCount returns the number of CPUs currently online.
//
//go:nosplit
func GetOnlineCPUCount() int {
	count := 0
	for i := 0; i < MaxCPUs; i++ {
		if atomic.LoadUint32(&perCPUData[i].Online) != 0 {
			count++
		}
	}
	return count
}

// IsCPUOnline returns true if the specified CPU is online.
//
//go:nosplit
func IsCPUOnline(cpuID uint64) bool {
	if cpuID >= MaxCPUs {
		return false
	}
	return atomic.LoadUint32(&perCPUData[cpuID].Online) != 0
}

// printHex64 outputs a uint64 as a 16-digit hex string (local helper)
//
//go:nosplit
func printHex64(val uint64) {
	const hexChars = "0123456789ABCDEF"
	for i := 15; i >= 0; i-- {
		nibble := (val >> uint(i*4)) & 0xF
		print(string(hexChars[nibble]))
	}
}

// uint64String converts a uint64 to its decimal string representation
func uint64String(n uint64) string {
	if n == 0 {
		return "0"
	}

	var buf [20]byte
	i := 19
	for n > 0 {
		buf[i] = byte('0' + (n % 10))
		n /= 10
		i--
	}

	return string(buf[i+1:])
}
