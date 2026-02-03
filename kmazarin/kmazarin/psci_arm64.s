//go:build arm64

// PSCI (Power State Coordination Interface) functions for SMP support
//
// PSCI is the standard ARM interface for power management, including
// waking secondary CPUs. On QEMU virt machine, PSCI is invoked via HVC.

#include "go_asm.h"
#include "textflag.h"

// PSCI function IDs (SMC64 variants)
#define PSCI_VERSION        0x84000000
#define PSCI_CPU_ON_64      0xC4000003
#define PSCI_CPU_OFF        0x84000002
#define PSCI_AFFINITY_INFO  0xC4000004

// PSCI return codes
#define PSCI_SUCCESS        0
#define PSCI_NOT_SUPPORTED  -1
#define PSCI_INVALID_PARAMS -2
#define PSCI_DENIED         -3
#define PSCI_ALREADY_ON     -4
#define PSCI_ON_PENDING     -5
#define PSCI_INTERNAL_ERROR -6

// psciCpuOnAsm wakes a secondary CPU using PSCI CPU_ON
//
// Parameters:
//   R0 = target MPIDR (affinity value of target CPU)
//   R1 = entry point address (where target CPU starts executing)
//   R2 = context ID (passed to entry point in X0)
//
// Returns:
//   R0 = PSCI return code (0 = success, negative = error)
//
// func psciCpuOnAsm(targetMpidr, entryPoint, contextId uint64) int64
TEXT ·psciCpuOnAsm(SB), NOSPLIT|NOFRAME, $0-32
    // Load parameters from stack (ABI0)
    MOVD    targetMpidr+0(FP), R1    // X1 = target MPIDR
    MOVD    entryPoint+8(FP), R2     // X2 = entry point
    MOVD    contextId+16(FP), R3     // X3 = context ID

    // Set function ID
    MOVD    $PSCI_CPU_ON_64, R0      // X0 = PSCI CPU_ON function ID

    // Invoke PSCI via HVC (Hypervisor Call)
    // QEMU virt machine handles this at EL2
    WORD    $0xD4000002              // HVC #0

    // Return value is in R0
    MOVD    R0, ret+24(FP)
    RET

// psciVersionAsm returns the PSCI version
//
// Returns:
//   R0 = PSCI version (major in bits 31:16, minor in bits 15:0)
//   or negative error code if PSCI not supported
//
// func psciVersionAsm() int64
TEXT ·psciVersionAsm(SB), NOSPLIT|NOFRAME, $0-8
    MOVD    $PSCI_VERSION, R0        // X0 = PSCI VERSION function ID

    // Invoke PSCI via HVC
    WORD    $0xD4000002              // HVC #0

    MOVD    R0, ret+0(FP)
    RET

// psciAffinityInfoAsm checks the power state of a CPU
//
// Parameters:
//   R0 = target affinity (MPIDR value)
//   R1 = lowest affinity level (0 = Aff0, 1 = Aff1, etc.)
//
// Returns:
//   0 = ON
//   1 = OFF
//   2 = ON_PENDING
//   negative = error
//
// func psciAffinityInfoAsm(targetAffinity uint64, lowestLevel uint64) int64
TEXT ·psciAffinityInfoAsm(SB), NOSPLIT|NOFRAME, $0-24
    MOVD    targetAffinity+0(FP), R1
    MOVD    lowestLevel+8(FP), R2
    MOVD    $PSCI_AFFINITY_INFO, R0

    WORD    $0xD4000002              // HVC #0

    MOVD    R0, ret+16(FP)
    RET
