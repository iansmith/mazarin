#include "textflag.h"

// getCPUIDAsm reads the initial APIC ID from CPUID leaf 1.
// The APIC ID is in EBX[31:24].
//
// func getCPUIDAsm() uint64
TEXT ·getCPUIDAsm(SB), NOSPLIT|NOFRAME, $0-8
    MOVL    $1, AX              // CPUID leaf 1
    CPUID                       // EBX[31:24] = initial APIC ID
    SHRL    $24, BX             // Shift APIC ID to low bits
    MOVBQZX BL, AX              // Zero-extend to 64 bits
    MOVQ    AX, ret+0(FP)
    RET

// getPerCPUPtrAsm returns a pointer to the current CPU's PerCPU data.
// It computes: &perCPUData[0] + (cpuID * PerCPUSize)
//
// func getPerCPUPtrAsm() uintptr
TEXT ·getPerCPUPtrAsm(SB), NOSPLIT|NOFRAME, $0-8
    // Get CPU ID (APIC ID from CPUID leaf 1)
    MOVL    $1, AX
    CPUID
    SHRL    $24, BX
    MOVBQZX BL, AX              // AX = CPU ID

    // Compute offset: cpuID * PerCPUSize
    MOVQ    ·PerCPUSize(SB), CX
    IMULQ   CX, AX              // AX = cpuID * PerCPUSize

    // Add base address
    LEAQ    ·perCPUData(SB), CX  // CX = &perCPUData[0]
    ADDQ    CX, AX               // AX = base + offset

    MOVQ    AX, ret+0(FP)
    RET

// readMPIDRAsm is a compatibility function for cross-architecture code.
// On x86_64, it returns the APIC ID (same as getCPUIDAsm).
//
// func readMPIDRAsm() uint64
TEXT ·readMPIDRAsm(SB), NOSPLIT|NOFRAME, $0-8
    MOVL    $1, AX
    CPUID
    SHRL    $24, BX
    MOVBQZX BL, AX
    MOVQ    AX, ret+0(FP)
    RET
