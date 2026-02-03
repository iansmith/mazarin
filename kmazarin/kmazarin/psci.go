//go:build arm64

// Package main provides PSCI (Power State Coordination Interface) support
// for SMP CPU management.
//
// PSCI is the ARM standard for power management across cores. This file
// provides the Go interface to PSCI for waking secondary CPUs.

package main

import "sync/atomic"

// PSCI error codes
const (
	PsciSuccess       = 0
	PsciNotSupported  = -1
	PsciInvalidParams = -2
	PsciDenied        = -3
	PsciAlreadyOn     = -4
	PsciOnPending     = -5
	PsciInternalError = -6
)

// PSCI affinity info return values
const (
	PsciAffinityOn        = 0
	PsciAffinityOff       = 1
	PsciAffinityOnPending = 2
)

// Track which CPUs have been started
var cpuStarted [MaxCPUs]uint32

// Assembly functions implemented in psci_arm64.s
func psciCpuOnAsm(targetMpidr, entryPoint, contextId uint64) int64
func psciVersionAsm() int64
func psciAffinityInfoAsm(targetAffinity uint64, lowestLevel uint64) int64

// GetPsciVersion returns the PSCI version as (major, minor).
// Returns (0, 0) if PSCI is not supported.
//
//go:nosplit
func GetPsciVersion() (major, minor uint16) {
	result := psciVersionAsm()
	if result < 0 {
		return 0, 0
	}
	major = uint16((result >> 16) & 0xFFFF)
	minor = uint16(result & 0xFFFF)
	return
}

// WakeCPU wakes a secondary CPU using PSCI CPU_ON.
//
// Parameters:
//   - cpuID: The CPU ID to wake (1-7, CPU 0 is already running)
//   - entryPoint: The address where the CPU should start executing
//   - contextId: Value passed to the entry point in X0 (typically CPU ID)
//
// Returns:
//   - 0 on success
//   - negative PSCI error code on failure
//
//go:nosplit
func WakeCPU(cpuID uint64, entryPoint uintptr, contextId uint64) int64 {
	if cpuID == 0 {
		return PsciInvalidParams // Can't wake CPU 0, it's already running
	}
	if cpuID >= MaxCPUs {
		return PsciInvalidParams
	}

	// Check if already started
	if atomic.LoadUint32(&cpuStarted[cpuID]) != 0 {
		return PsciAlreadyOn
	}

	// Construct MPIDR for the target CPU
	// On QEMU virt with single cluster: MPIDR = CPU ID in Aff0 (bits 7:0)
	targetMpidr := cpuID

	result := psciCpuOnAsm(targetMpidr, uint64(entryPoint), contextId)
	if result == PsciSuccess {
		atomic.StoreUint32(&cpuStarted[cpuID], 1)
	}
	return result
}

// GetCPUPowerState returns the power state of a CPU.
//
// Returns:
//   - PsciAffinityOn (0) if CPU is on
//   - PsciAffinityOff (1) if CPU is off
//   - PsciAffinityOnPending (2) if CPU is powering on
//   - negative error code on failure
//
//go:nosplit
func GetCPUPowerState(cpuID uint64) int64 {
	if cpuID >= MaxCPUs {
		return PsciInvalidParams
	}
	// MPIDR for target CPU (Aff0 = cpuID)
	targetMpidr := cpuID
	return psciAffinityInfoAsm(targetMpidr, 0) // Level 0 = Aff0
}

// PsciErrorString returns a human-readable error string for PSCI error codes.
func PsciErrorString(code int64) string {
	switch code {
	case PsciSuccess:
		return "success"
	case PsciNotSupported:
		return "not supported"
	case PsciInvalidParams:
		return "invalid parameters"
	case PsciDenied:
		return "denied"
	case PsciAlreadyOn:
		return "already on"
	case PsciOnPending:
		return "on pending"
	case PsciInternalError:
		return "internal error"
	default:
		return "unknown error"
	}
}
