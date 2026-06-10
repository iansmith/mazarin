#include "textflag.h"

// probeAPICIDAsm reads the initial APIC ID from CPUID leaf 1 (EBX[31:24]).
// This is the ONLY function here that executes the CPUID instruction — which is
// an unconditional VM-exit. It runs ONCE at boot (initCPUIDCacheArch) to fill
// cpuIDCache; the hot-path accessors below read that cache instead, so the
// scheduler/IRQ paths no longer storm CPUID VM-exits on the nested-KVM x86 host
// (MAZ-136). x86 is single-CPU today (no AP mailbox), so one probe suffices.
//
// func probeAPICIDAsm() uint64
TEXT ·probeAPICIDAsm(SB), NOSPLIT|NOFRAME, $0-8
    MOVL    $1, AX              // CPUID leaf 1
    CPUID                       // EBX[31:24] = initial APIC ID
    SHRL    $24, BX
    MOVBQZX BL, AX
    MOVQ    AX, ret+0(FP)
    RET

// getCPUIDAsm returns the current CPU's APIC ID from cpuIDCache — no CPUID, no
// VM-exit. (The cross-arch counterpart percpu_arm64.s reads MPIDR_EL1 directly,
// which never traps; this is the x86 implementation of the same interface.)
//
// func getCPUIDAsm() uint64
TEXT ·getCPUIDAsm(SB), NOSPLIT|NOFRAME, $0-8
    MOVQ    ·cpuIDCache(SB), AX
    MOVQ    AX, ret+0(FP)
    RET

// getPerCPUPtrAsm returns &perCPUData[0] + cpuIDCache*PerCPUSize — no CPUID.
//
// func getPerCPUPtrAsm() uintptr
TEXT ·getPerCPUPtrAsm(SB), NOSPLIT|NOFRAME, $0-8
    MOVQ    ·cpuIDCache(SB), AX  // AX = cached CPU ID
    MOVQ    ·PerCPUSize(SB), CX
    IMULQ   CX, AX               // AX = cpuID * PerCPUSize
    LEAQ    ·perCPUData(SB), CX  // CX = &perCPUData[0]
    ADDQ    CX, AX               // AX = base + offset
    MOVQ    AX, ret+0(FP)
    RET

// readMPIDRAsm is the cross-architecture compatibility name; on x86 it returns
// the cached APIC ID (no CPUID).
//
// func readMPIDRAsm() uint64
TEXT ·readMPIDRAsm(SB), NOSPLIT|NOFRAME, $0-8
    MOVQ    ·cpuIDCache(SB), AX
    MOVQ    AX, ret+0(FP)
    RET
