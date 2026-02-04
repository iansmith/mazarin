// spinlock_amd64.s - x86_64 atomic primitives for spinlocks
//
// This file implements atomic operations using x86_64 baseline instructions.
// These work on all x86_64 processors.
//
// CRITICAL: Uses TSC (Time Stamp Counter) via RDTSC for timing, matching
// the ktimer package's counter source. This ensures consistent timing
// measurements across the kernel.

#include "textflag.h"

// CompareAndSwapUint32 - x86_64 atomic CAS with timing
//
// Atomically compares *addr to old value, and if equal, stores new value.
// Returns elapsed TSC ticks from start to completion:
//   - 0 = CAS failed
//   - non-zero = CAS succeeded (elapsed ticks)
//
// Uses LOCK CMPXCHGL which provides full sequential consistency.
//
// Timing: Reads TSC at start and end to measure actual elapsed time.
//
// func CompareAndSwapUint32(addr *uint32, old, new uint32) uint64
TEXT ·CompareAndSwapUint32(SB), NOSPLIT|NOFRAME, $0-25
	MOVQ	addr+0(FP), DI      // DI = address
	MOVL	old+8(FP), AX       // AX = old value (expected)
	MOVL	new+12(FP), CX      // CX = new value (desired)

	// Get start time (TSC)
	BYTE $0x0F; BYTE $0x31  // RDTSC: EDX:EAX = TSC
	MOVL	AX, R8              // R8 = low 32 bits of start TSC
	MOVL	DX, R9              // R9 = high 32 bits of start TSC
	SHLQ	$32, R9             // R9 = high << 32
	ORQ	R8, R9              // R9 = full 64-bit start TSC

	// Reload expected value (RDTSC clobbered AX)
	MOVL	old+8(FP), AX       // AX = old value (expected)

	// Atomic compare-and-swap
	LOCK
	CMPXCHGL	CX, (DI)    // Compare *DI to AX; if equal, *DI = CX
	JNE	cas_fail            // If not equal (ZF=0), CAS failed

	// Success: swap performed - get end time
	BYTE $0x0F; BYTE $0x31  // RDTSC: EDX:EAX = TSC
	MOVL	AX, R10             // R10 = low 32 bits of end TSC
	MOVL	DX, R11             // R11 = high 32 bits of end TSC
	SHLQ	$32, R11            // R11 = high << 32
	ORQ	R10, R11            // R11 = full 64-bit end TSC

	// Calculate elapsed = end - start
	SUBQ	R9, R11             // R11 = elapsed
	TESTQ	R11, R11            // Check if elapsed == 0
	JNE	have_elapsed        // If non-zero, use it
	MOVQ	$1, R11             // Else return 1 (minimum non-zero success)

have_elapsed:
	MOVQ	R11, ret+16(FP)     // Return elapsed time (non-zero)
	RET

cas_fail:
	MOVQ	$0, AX              // Return 0 (failure)
	MOVQ	AX, ret+16(FP)
	RET

// StoreUint32 - x86_64 atomic store with release semantics
//
// Atomically stores val to *addr with release semantics.
// This ensures all prior memory operations are visible before this store.
//
// x86_64 has Total Store Order (TSO) - all stores have implicit release
// semantics. However, we use XCHGL for full sequential consistency.
//
// func StoreUint32(addr *uint32, val uint32)
TEXT ·StoreUint32(SB), NOSPLIT|NOFRAME, $0-12
	MOVQ	addr+0(FP), DI      // DI = address
	MOVL	val+8(FP), AX       // AX = value
	XCHGL	AX, (DI)            // Atomic exchange (provides full barrier)
	RET

// CurrentTime - Get current TSC value (real or fake for testing)
//
// If fakeTime parameter is non-zero, returns fakeTime (for testing).
// If fakeTime is zero, reads TSC via RDTSC.
//
// func CurrentTime(fakeTime uint64) uint64
TEXT ·CurrentTime(SB), NOSPLIT|NOFRAME, $0-16
	MOVQ	fakeTime+0(FP), AX  // AX = fakeTime parameter
	TESTQ	AX, AX              // Check if fakeTime == 0
	JNE	use_fake            // If non-zero, use fake time

	// fakeTime is zero - read TSC
	BYTE $0x0F; BYTE $0x31  // RDTSC: EDX:EAX = TSC
	SHLQ	$32, DX             // DX = high << 32
	ORQ	DX, AX              // AX = full 64-bit TSC

use_fake:
	MOVQ	AX, ret+8(FP)       // Return AX (either fake or real)
	RET

// nanoWait - Busy-wait for exact tick count using TSC
//
// Spins in a tight loop reading TSC (via RDTSC) until the specified
// number of ticks have elapsed. Does NOT yield CPU.
//
// This is counter-based (not instruction-based), so timing is accurate
// (though TSC frequency varies by CPU and power state - kernel should
// calibrate and adjust if needed).
//
// func nanoWait(ticks uint64)
TEXT ·nanoWait(SB), NOSPLIT|NOFRAME, $0-8
	MOVQ	ticks+0(FP), CX     // CX = tick count to wait

	// Get start TSC
	BYTE $0x0F; BYTE $0x31  // RDTSC: EDX:EAX = TSC
	MOVL	AX, R8              // R8 = low 32 bits
	MOVL	DX, R9              // R9 = high 32 bits
	SHLQ	$32, R9             // R9 = high << 32
	ORQ	R8, R9              // R9 = start TSC

	ADDQ	CX, R9              // R9 = target = start + ticks

wait_loop:
	// Read current TSC
	BYTE $0x0F; BYTE $0x31  // RDTSC: EDX:EAX = TSC
	MOVL	AX, R10             // R10 = low 32 bits
	MOVL	DX, R11             // R11 = high 32 bits
	SHLQ	$32, R11            // R11 = high << 32
	ORQ	R10, R11            // R11 = current TSC

	CMPQ	R11, R9             // Compare current to target
	JB	wait_loop           // If current < target, keep waiting

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
	MOVQ	$1, AX              // Return 1 (success with minimal elapsed time)
	MOVQ	AX, ret+16(FP)
	RET

// SpinlockLose - Test stub that always fails
//
// Simulates a failed CAS operation without actually performing atomics.
// Returns 0 to indicate failure.
// Used for testing timeout/retry code paths where the lock is contended.
//
// func SpinlockLose(addr *uint32, old, new uint32) uint64
TEXT ·SpinlockLose(SB), NOSPLIT|NOFRAME, $0-25
	MOVQ	$0, AX              // Return 0 (failure)
	MOVQ	AX, ret+16(FP)
	RET

// NanoWaitStub - Test stub that does nothing
//
// No-op wait function for testing without actual delays.
// Used to skip timing waits in unit tests.
//
// func NanoWaitStub(ticks uint64)
TEXT ·NanoWaitStub(SB), NOSPLIT|NOFRAME, $0-8
	RET                         // Immediate return (no wait)
