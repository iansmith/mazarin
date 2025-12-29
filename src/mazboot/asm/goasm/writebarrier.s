#include "textflag.h"

// writebarrier.s - Custom write barrier for bare-metal (no GC)
// Converted to Go Plan 9 assembly syntax

// Dummy buffer in BSS for write barrier (1KB, zero-filled)
// The write barrier functions return this address so callers can write harmlessly
GLOBL ·write_barrier_dummy_buffer(SB), NOPTR, $1024

// Custom write barrier functions that replace Go runtime's GC write barriers
// These return a dummy buffer address instead of tracking writes for GC
//
// Calling convention:
//   - R27 (x27) = destination address
//   - R2 (x2) = new value (pointer to assign)
//   - R1 (x1) = old value (for GC tracking - we ignore it)
//   - Return: R25 (x25) = buffer address (caller writes here harmlessly)

// runtime·gcWriteBarrier2 - called for 2-pointer writes (16 bytes)
TEXT runtime·gcWriteBarrier2(SB), NOSPLIT|NOFRAME, $0
    MOVD $·write_barrier_dummy_buffer(SB), R25
    RET

// runtime·gcWriteBarrier3 - called for 3-pointer writes (24 bytes)
TEXT runtime·gcWriteBarrier3(SB), NOSPLIT|NOFRAME, $0
    MOVD $·write_barrier_dummy_buffer(SB), R25
    RET

// runtime·gcWriteBarrier4 - called for 4-pointer writes (32 bytes)
TEXT runtime·gcWriteBarrier4(SB), NOSPLIT|NOFRAME, $0
    MOVD $·write_barrier_dummy_buffer(SB), R25
    RET

// gcWriteBarrier - main function (fallback)
TEXT gcWriteBarrier(SB), NOSPLIT|NOFRAME, $0
    RET

// our_gcWriteBarrier - alias for objcopy redirection
TEXT our_gcWriteBarrier(SB), NOSPLIT|NOFRAME, $0
    RET
