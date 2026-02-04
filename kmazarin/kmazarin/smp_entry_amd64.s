#include "textflag.h"

// secondaryCPUEntry is the entry point for secondary CPUs on x86_64.
//
// CPUs arrive here in 64-bit long mode with paging enabled (parked by diplomat).
// The CPU's APIC ID identifies which CPU this is.
//
// Steps:
// 1. Read APIC ID (CPUID leaf 1, EBX[31:24]) -> CPU ID
// 2. Compute perCPU pointer: &perCPUData + cpuID * PerCPUSize
// 3. Load G0StackTop (offset 32) -> set RSP
// 4. Load ExceptionStackTop (offset 48) -> save for TSS IST later
// 5. Store 1 to Online (offset 64) with release semantics
// 6. Enable interrupts (STI)
// 7. HLT loop
//
// func secondaryCPUEntry()
TEXT ·secondaryCPUEntry(SB), NOSPLIT|NOFRAME, $0
    // Step 1: Get CPU ID from APIC ID
    MOVL    $1, AX
    CPUID
    SHRL    $24, BX
    MOVBQZX BL, AX              // AX = CPU ID

    // Save CPU ID in a callee-saved register
    MOVQ    AX, R12             // R12 = CPU ID (preserved)

    // Step 2: Compute per-CPU data address
    LEAQ    ·perCPUData(SB), R13 // R13 = base of perCPUData array
    MOVQ    ·PerCPUSize(SB), R14 // R14 = size of one PerCPU struct
    IMULQ   R12, R14             // R14 = cpuID * PerCPUSize
    ADDQ    R14, R13             // R13 = pointer to our PerCPU struct

    // Step 3: Set up stack from G0StackTop (offset 32)
    MOVQ    32(R13), SP          // RSP = G0StackTop

    // Check if stack is allocated
    TESTQ   SP, SP
    JZ      stack_not_ready

    // Step 4: Save ExceptionStackTop (offset 48) for later TSS/IST setup
    MOVQ    48(R13), R15         // R15 = ExceptionStackTop (preserved)

    // Step 5: Mark CPU as online (offset 64)
    MOVL    $1, AX
    XCHGL   AX, 64(R13)         // Atomic store with full barrier

    // Step 6: Enable interrupts
    STI

    // Step 7: HLT loop - wait for work
secondary_idle_loop:
    HLT
    JMP     secondary_idle_loop

stack_not_ready:
    // Stacks not yet allocated - busy wait and retry
    MOVQ    $0x100000, CX
delay_loop:
    DECQ    CX
    JNZ     delay_loop
    JMP     ·secondaryCPUEntry(SB)

// getSecondaryCPUEntryAddr returns the address of secondaryCPUEntry
// for use with the AP mailbox wake mechanism.
//
// func getSecondaryCPUEntryAddr() uintptr
TEXT ·getSecondaryCPUEntryAddr(SB), NOSPLIT|NOFRAME, $0-8
    LEAQ    ·secondaryCPUEntry(SB), AX
    MOVQ    AX, ret+0(FP)
    RET
