// spinlock_arm64.s - ARMv8.0-compatible atomic primitives for spinlocks
//
// This file implements atomic operations using ARMv8.0 baseline instructions.
// These instructions work on ALL ARMv8 processors (v8.0, v8.1, v8.2, etc.).
//
// COMPATIBILITY: Uses LDAXR/STLXR (Load/Store Exclusive) which is part of
// ARMv8.0 baseline. Does NOT use ARMv8.1+ LSE instructions (LDADD, CAS, etc.).

#include "textflag.h"

// CompareAndSwapUint32 - ARMv8.0 compatible atomic CAS
//
// Atomically compares *addr to old value, and if equal, stores new value.
// Returns true if the swap was performed.
//
// Uses LDAXRW (Load-Acquire Exclusive) and STLXRW (Store-Release Exclusive)
// which provide the necessary memory ordering guarantees.
//
// func CompareAndSwapUint32(addr *uint32, old, new uint32) bool
TEXT ·CompareAndSwapUint32(SB), NOSPLIT|NOFRAME, $0-25
	MOVD	addr+0(FP), R0      // R0 = address
	MOVW	old+8(FP), R1       // R1 = old value (expected)
	MOVW	new+12(FP), R2      // R2 = new value (desired)

cas_loop:
	LDAXRW	(R0), R3            // Load-Acquire Exclusive: R3 = *addr
	CMPW	R1, R3              // Compare R3 (actual) to R1 (expected)
	BNE	cas_fail            // If not equal, CAS fails
	STLXRW	R2, (R0), R4        // Store-Release Exclusive: *addr = new, R4 = status
	CBNZ	R4, cas_loop        // If store failed (R4 != 0), retry

	// Success: swap performed
	MOVD	$1, R0
	MOVB	R0, ret+16(FP)
	RET

cas_fail:
	CLREX                       // Clear exclusive monitor
	MOVD	$0, R0
	MOVB	R0, ret+16(FP)
	RET

// StoreUint32 - ARMv8.0 atomic store with release semantics
//
// Atomically stores val to *addr with release semantics.
// This ensures all prior memory operations are visible before this store.
//
// Uses STLRW (Store-Release Register) which is part of ARMv8.0 baseline.
//
// func StoreUint32(addr *uint32, val uint32)
TEXT ·StoreUint32(SB), NOSPLIT|NOFRAME, $0-12
	MOVD	addr+0(FP), R0      // R0 = address
	MOVW	val+8(FP), R1       // R1 = value
	STLRW	R1, (R0)            // Store-Release: *addr = val
	RET

// nanoWait - Busy-wait for exact tick count using hardware counter
//
// Spins in a tight loop reading CNTVCT_EL0 (virtual counter) until
// the specified number of ticks have elapsed. Does NOT yield CPU.
//
// This is counter-based (not instruction-based), so timing is accurate
// regardless of CPU frequency, cache state, or branch prediction.
//
// At 62.5MHz (QEMU default):
//   31 ticks = 500 nanoseconds
//   62 ticks = 1 microsecond
//
// func nanoWait(ticks uint64)
TEXT ·nanoWait(SB), NOSPLIT|NOFRAME, $0-8
	MOVD	ticks+0(FP), R0     // R0 = tick count to wait
	MRS	CNTVCT_EL0, R1      // R1 = start counter value
	ADD	R0, R1, R2          // R2 = target = start + ticks

wait_loop:
	MRS	CNTVCT_EL0, R3      // R3 = current counter value
	CMP	R2, R3              // Compare target to current
	BHI	wait_loop           // If target > current, keep waiting

	RET
