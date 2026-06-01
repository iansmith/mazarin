// spinlock_arm64.s - ARMv8.0-compatible atomic primitives for spinlocks
//
// This file implements atomic operations using ARMv8.0 baseline instructions.
// These instructions work on ALL ARMv8 processors (v8.0, v8.1, v8.2, etc.).
//
// COMPATIBILITY: Uses LDAXR/STLXR (Load/Store Exclusive) which is part of
// ARMv8.0 baseline. Does NOT use ARMv8.1+ LSE instructions (LDADD, CAS, etc.).

#include "textflag.h"

// CompareAndSwapUint32 - ARMv8.0 compatible atomic CAS with timing
//
// Atomically compares *addr to old value, and if equal, stores new value.
// Returns elapsed time from start to completion (real hardware counter):
//   - 0 = CAS failed
//   - non-zero = CAS succeeded (elapsed ticks)
//
// Uses LDAXRW (Load-Acquire Exclusive) and STLXRW (Store-Release Exclusive)
// which provide the necessary memory ordering guarantees.
//
// Timing: Reads CNTVCT_EL0 at start and end to measure actual elapsed time.
//
// func CompareAndSwapUint32(addr *uint32, old, new uint32) uint64
TEXT ·CompareAndSwapUint32(SB), NOSPLIT|NOFRAME, $0-25
	MOVD	addr+0(FP), R0      // R0 = address
	MOVW	old+8(FP), R1       // R1 = old value (expected)
	MOVW	new+12(FP), R2      // R2 = new value (desired)

	// Get start time
	MRS	CNTVCT_EL0, R10     // R10 = start counter

cas_loop:
	LDAXRW	(R0), R3            // Load-Acquire Exclusive: R3 = *addr
	CMPW	R1, R3              // Compare R3 (actual) to R1 (expected)
	BNE	cas_fail            // If not equal, CAS fails
	STLXRW	R2, (R0), R4        // Store-Release Exclusive: *addr = new, R4 = status
	CBNZ	R4, cas_loop        // If store failed (R4 != 0), retry

	// Success: swap performed - get end time
	MRS	CNTVCT_EL0, R5      // R5 = end counter
	SUB	R10, R5, R6         // R6 = elapsed = end - start
	CBNZ	R6, have_elapsed    // If elapsed != 0, use it
	MOVD	$1, R6              // Else return 1 (minimum non-zero success)
have_elapsed:
	MOVD	R6, ret+16(FP)      // Return elapsed time (non-zero)
	RET

cas_fail:
	CLREX                       // Clear exclusive monitor
	MOVD	$0, R0              // Return 0 (failure)
	MOVD	R0, ret+16(FP)
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

// CurrentTime - Get current counter value (real or fake for testing)
//
// If fakeTime parameter is non-zero, returns fakeTime (for testing).
// If fakeTime is zero, reads CNTVCT_EL0 (hardware counter).
//
// func CurrentTime(fakeTime uint64) uint64
TEXT ·CurrentTime(SB), NOSPLIT|NOFRAME, $0-16
	MOVD	fakeTime+0(FP), R0  // R0 = fakeTime parameter
	CBNZ	R0, use_fake        // If non-zero, use fake time

	// fakeTime is zero - read hardware counter
	MRS	CNTVCT_EL0, R0      // R0 = real counter value

use_fake:
	MOVD	R0, ret+8(FP)       // Return R0 (either fake or real)
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

// ==============================================================================
// Test stubs for spinlock testing
// ==============================================================================

// SpinlockWin - Test stub that always succeeds
//
// Simulates a successful CAS operation without actually performing atomics.
// Returns non-zero elapsed time to indicate success.
// Used for testing code paths where the lock should always be acquired.
//
// func SpinlockWin(addr *uint32, old, new uint32) uint64
TEXT ·SpinlockWin(SB), NOSPLIT|NOFRAME, $0-25
	MOVD	$1, R0              // Return 1 (success with minimal elapsed time)
	MOVD	R0, ret+16(FP)
	RET

// SpinlockLose - Test stub that always fails
//
// Simulates a failed CAS operation without actually performing atomics.
// Returns 0 to indicate failure.
// Used for testing timeout/retry code paths where the lock is contended.
//
// func SpinlockLose(addr *uint32, old, new uint32) uint64
TEXT ·SpinlockLose(SB), NOSPLIT|NOFRAME, $0-25
	MOVD	$0, R0              // Return 0 (failure)
	MOVD	R0, ret+16(FP)
	RET

// NanoWaitStub - Test stub that does nothing
//
// No-op wait function for testing without actual delays.
// Used to skip timing waits in unit tests.
//
// func NanoWaitStub(ticks uint64)
TEXT ·NanoWaitStub(SB), NOSPLIT|NOFRAME, $0-8
	RET                         // Immediate return (no wait)

// spinlockDeadHandler writes diagnostic to PL011 UART and halts.
// Prints "!L <16-hex-digit lock address>\n" so the deadlocked lock can be
// identified (cross-reference the address with `bin/target-nm build/kmazarin.elf`).
// Fully self-contained: no external calls, no locks — safe to run while a lock
// is wedged. Uses only R0-R5; NOFRAME so no stack touched.
// func spinlockDeadHandler(lockAddr uintptr)
TEXT ·spinlockDeadHandler(SB), NOSPLIT|NOFRAME, $0-8
	// Write '!' 'L' ' ' to PL011 UART via TTBR1 (kernel) mapping.
	// Must use the high-address alias (0xFFFFFFFF09000000) — the physical
	// address 0x09000000 is a low address (TTBR0) and may not be mapped when
	// TTBR0 is set to a user shepherd's page table.
	MOVD	$0xFFFFFFFF09000000, R0
	MOVD	$'!', R1
	MOVB	R1, (R0)
	MOVD	$'L', R1
	MOVB	R1, (R0)
	MOVD	$' ', R1
	MOVB	R1, (R0)

	// Print the 64-bit lock address as 16 hex digits, MSB first.
	MOVD	lockAddr+0(FP), R2  // R2 = value to print
	MOVD	$60, R3             // R3 = shift amount, starts at bit 60
hexloop_arm64:
	LSR	R3, R2, R4          // R4 = value >> shift
	AND	$0xF, R4, R4        // R4 = nibble (0..15)
	CMP	$10, R4             // nibble < 10 ?
	BLT	hex_digit_arm64
	ADD	$('a'-10), R4, R4   // 'a'..'f'
	B	hex_emit_arm64
hex_digit_arm64:
	ADD	$'0', R4, R4        // '0'..'9'
hex_emit_arm64:
	MOVB	R4, (R0)            // emit nibble char
	SUBS	$4, R3, R3          // shift -= 4, set flags
	BGE	hexloop_arm64       // continue while shift >= 0

	MOVD	$'\n', R1
	MOVB	R1, (R0)
spinlock_dead_halt_arm64:
	WFI
	JMP	spinlock_dead_halt_arm64
