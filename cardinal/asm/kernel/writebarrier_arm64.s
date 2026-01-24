// writebarrier_arm64.s - GC Write Barrier Stubs for Bare-Metal Environment
//
// ============================================================================
// OVERVIEW
// ============================================================================
// Cardinal runs without a full Go runtime - no GC, no P structures, no wbBuf.
// The Go compiler generates calls to gcWriteBarrier2/3/4 for pointer writes.
// These functions normally record writes to p.wbBuf for GC tracking.
//
// Since we don't have GC in Cardinal, we provide stubs that return a dummy
// buffer address. The compiler-generated code writes to this buffer (harmless)
// AND to the actual destination (the real assignment).
//
// Functions:
//   - gcWriteBarrier2() - Stub for 2-pointer writes (16 bytes)
//   - gcWriteBarrier3() - Stub for 3-pointer writes (24 bytes)
//   - gcWriteBarrier4() - Stub for 4-pointer writes (32 bytes)
//   - gcWriteBarrier()  - Fallback stub (no-op)
//
// WHY NOT DECOMPOSE:
// These functions are already atomic primitives (1-2 instructions each).
// They perform the minimal operation required by the Go compiler's calling
// convention: return a buffer address or simply return.
//
// LIFECYCLE:
// Once kmazarin starts, schedinit() creates P structures with real wbBuf,
// and Go's standard gcWriteBarrier works correctly.
//
// REGISTER USAGE (from Go runtime asm_arm64.s):
//   R25 = return value (pointer to buffer where caller writes)
//   R27 = destination address (set by caller before call)
//   R0-R3 = values to write (caller handles these)
//
// DUMMY BUFFER:
// The dummy buffer is defined in Go code (writebarrier_buffer.go):
//   var writeBarrierDummyBuffer [128]uintptr
// We reference it via: main·writeBarrierDummyBuffer(SB)

#include "textflag.h"

// ============================================================================
// gcWriteBarrier2() - Stub for 2-Pointer Writes (16 Bytes)
// ============================================================================
// Returns dummy buffer address in R25 for 2-pointer writes
// Caller will use STP (Store Pair) to write to both [R25] (dummy) and [R27] (real destination)
//
// Returns: R25 = address of writeBarrierDummyBuffer
//
// Segments:
//   1. Load dummy buffer address to R25
//   2. Return to caller
//
TEXT runtime·gcWriteBarrier2(SB), NOSPLIT|NOFRAME, $0
	// Segment 1: Load dummy buffer address
	MOVD	$main·writeBarrierDummyBuffer(SB), R25

	// Segment 2: Return
	RET

// ============================================================================
// gcWriteBarrier3() - Stub for 3-Pointer Writes (24 Bytes)
// ============================================================================
// Returns dummy buffer address in R25 for 3-pointer writes
//
// Returns: R25 = address of writeBarrierDummyBuffer
//
// Segments:
//   1. Load dummy buffer address to R25
//   2. Return to caller
//
TEXT runtime·gcWriteBarrier3(SB), NOSPLIT|NOFRAME, $0
	// Segment 1: Load dummy buffer address
	MOVD	$main·writeBarrierDummyBuffer(SB), R25

	// Segment 2: Return
	RET

// ============================================================================
// gcWriteBarrier4() - Stub for 4-Pointer Writes (32 Bytes)
// ============================================================================
// Returns dummy buffer address in R25 for 4-pointer writes
//
// Returns: R25 = address of writeBarrierDummyBuffer
//
// Segments:
//   1. Load dummy buffer address to R25
//   2. Return to caller
//
TEXT runtime·gcWriteBarrier4(SB), NOSPLIT|NOFRAME, $0
	// Segment 1: Load dummy buffer address
	MOVD	$main·writeBarrierDummyBuffer(SB), R25

	// Segment 2: Return
	RET

// ============================================================================
// gcWriteBarrier() - Fallback Write Barrier Stub
// ============================================================================
// Main entry point called if specific variants (2/3/4) aren't found
// Simply returns - caller handles the assignment directly
//
// Segments:
//   1. Return to caller (no-op)
//
TEXT runtime·gcWriteBarrier(SB), NOSPLIT|NOFRAME, $0
	// Segment 1: Return (no-op)
	RET
