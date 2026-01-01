// writebarrier.s - Write Barrier Stubs for Mazboot (Go/Plan9 Assembly)
//
// Mazboot runs without a full Go runtime - no GC, no P structures, no wbBuf.
// The Go compiler generates calls to gcWriteBarrier2/3/4 for pointer writes.
// These functions normally record writes to p.wbBuf for GC tracking.
//
// Since we don't have GC, we provide stubs that return a dummy buffer address.
// The compiler-generated code writes to this buffer (harmless) AND to the
// actual destination (the real assignment).
//
// Once kmazarin starts, schedinit() creates P structures with real wbBuf,
// and Go's standard gcWriteBarrier works correctly.
//
// Register usage (from Go runtime asm_arm64.s):
//   R25 = return value (pointer to buffer where caller writes)
//   R27 = destination address (set by caller before call)
//   R0-R3 = values to write (caller handles these)
//
// The dummy buffer is defined in Go code (writebarrier_buffer.go) as:
//   var writeBarrierDummyBuffer [128]uintptr
//
// We reference it via the symbol: main·writeBarrierDummyBuffer(SB)

#include "textflag.h"

// ============================================================================
// gcWriteBarrier2 - Called for 2-pointer writes (16 bytes)
// ============================================================================
// Returns dummy buffer address in R25.
// Caller will STP to [R25] and to actual destination [R27].
//
TEXT runtime·gcWriteBarrier2(SB), NOSPLIT|NOFRAME, $0
	MOVD	$main·writeBarrierDummyBuffer(SB), R25
	RET

// ============================================================================
// gcWriteBarrier3 - Called for 3-pointer writes (24 bytes)
// ============================================================================
TEXT runtime·gcWriteBarrier3(SB), NOSPLIT|NOFRAME, $0
	MOVD	$main·writeBarrierDummyBuffer(SB), R25
	RET

// ============================================================================
// gcWriteBarrier4 - Called for 4-pointer writes (32 bytes)
// ============================================================================
TEXT runtime·gcWriteBarrier4(SB), NOSPLIT|NOFRAME, $0
	MOVD	$main·writeBarrierDummyBuffer(SB), R25
	RET

// ============================================================================
// gcWriteBarrier - Main entry point (fallback)
// ============================================================================
// Called if specific variants aren't found. Just return - caller does assignment.
//
TEXT runtime·gcWriteBarrier(SB), NOSPLIT|NOFRAME, $0
	RET
