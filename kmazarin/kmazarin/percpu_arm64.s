//go:build !test_stubs

#include "textflag.h"

// ============================================================================
// Per-CPU Assembly Functions
// ============================================================================
//
// These functions provide low-level CPU identification for per-CPU data access.
//
// MPIDR_EL1 (Multiprocessor Affinity Register) format:
//   Bits 7:0   (Aff0) - Core ID within cluster (0-255)
//   Bits 15:8  (Aff1) - Cluster ID (0-255)
//   Bits 23:16 (Aff2) - Higher level grouping
//   Bits 39:32 (Aff3) - Highest level grouping
//   Bit 30     (U)    - Uniprocessor (0 = multiprocessor)
//   Bit 24     (MT)   - Multithreading (1 = SMT cores share resources)
//
// For QEMU virt with -smp 4, Aff0 will be 0, 1, 2, 3 for each core.

// readMPIDRAsm reads the MPIDR_EL1 register.
// Returns: uint64 - the full MPIDR_EL1 value
//
// func readMPIDRAsm() uint64
TEXT ·readMPIDRAsm(SB), NOSPLIT, $0-8
	MRS	MPIDR_EL1, R0		// Read MPIDR_EL1 into R0
	MOVD	R0, ret+0(FP)		// Store return value
	RET

// getCPUIDAsm returns the CPU ID (Aff0 field of MPIDR_EL1).
// This is a fast path for assembly code that needs the CPU ID.
// Returns: uint64 - CPU ID (0 to MaxCPUs-1)
//
// func getCPUIDAsm() uint64
TEXT ·getCPUIDAsm(SB), NOSPLIT, $0-8
	MRS	MPIDR_EL1, R0		// Read MPIDR_EL1
	AND	$0xFF, R0		// Extract Aff0 (bits 7:0)
	MOVD	R0, ret+0(FP)		// Store return value
	RET

// getPerCPUPtrAsm returns a pointer to the current CPU's PerCPU struct.
// This is the fast path for assembly code that needs per-CPU data.
//
// Computes: &perCPUData[0] + (cpuID * PerCPUSize)
//
// Uses: R0 (trashed), R1 (trashed)
// Returns: uintptr - pointer to PerCPU struct for current CPU
//
// func getPerCPUPtrAsm() uintptr
TEXT ·getPerCPUPtrAsm(SB), NOSPLIT|NOFRAME, $0-8
	MRS	MPIDR_EL1, R0			// Read MPIDR_EL1
	AND	$0xFF, R0			// Extract Aff0 (CPU ID)

	// Load PerCPUSize
	MOVD	·PerCPUSize(SB), R1

	// Compute offset: cpuID * PerCPUSize
	MUL	R1, R0, R0

	// Add base address (use $ to get address of array, not value at array)
	MOVD	$·perCPUData(SB), R1		// Load address of perCPUData
	ADD	R1, R0, R0			// R0 = base + offset

	MOVD	R0, ret+0(FP)			// Store return value
	RET
