// spinlock_riscv64.s - RISC-V atomic primitives for spinlocks
//
// This file implements atomic operations using RISC-V A extension (RV64A).
// These instructions are part of the standard RISC-V atomic extension.
//
// COMPATIBILITY: Uses LR.W/SC.W (Load-Reserved/Store-Conditional) which is
// part of the RV64A extension. All operations use the TIME CSR for timing.

#include "textflag.h"

// CompareAndSwapUint32 - RISC-V atomic CAS with timing
//
// Atomically compares *addr to old value, and if equal, stores new value.
// Returns elapsed time from start to completion (TIME CSR counter):
//   - 0 = CAS failed
//   - non-zero = CAS succeeded (elapsed ticks)
//
// Uses LR.W.AQ (Load-Reserved Word with Acquire) and SC.W.RL (Store-Conditional
// Word with Release) which provide the necessary memory ordering guarantees.
//
// Timing: Reads TIME CSR (0xC01) at start and end to measure actual elapsed time.
// This matches the counter used by the ktimer package for consistency.
//
// func CompareAndSwapUint32(addr *uint32, old, new uint32) uint64
TEXT ·CompareAndSwapUint32(SB), NOSPLIT|NOFRAME, $0-25
	MOV	addr+0(FP), A0      // A0 = address
	MOVW	old+8(FP), A1       // A1 = old value (expected)
	MOVW	new+12(FP), A2      // A2 = new value (desired)

	// Get start time from TIME CSR (0xC01)
	// CSRRS A3, time, zero = read TIME into A3
	WORD $0xC01026F3        // A3 = TIME (rd=A3=x13, rs1=x0)

cas_loop:
	// LR.W.AQ A4, (A0) - Load-Reserved Word with Acquire
	// Encoding: funct5=00010, aq=1, rl=0, rs2=00000, rs1=A0, funct3=010, rd=A4, opcode=0x2F
	// = 0x14002000 | (A4<<7) | (A0<<15)
	// = 0x14002000 | (14<<7) | (10<<15) = 0x14002000 | 0x700 | 0x50000 = 0x14052700
	WORD $0x1405272F        // A4 = *A0 (reserved), aq semantics

	// Compare A4 (actual) to A1 (expected)
	BNE	A1, A4, cas_fail    // If not equal, CAS fails

	// SC.W.RL A5, A2, (A0) - Store-Conditional Word with Release
	// Encoding: funct5=00011, aq=0, rl=1, rs2=A2, rs1=A0, funct3=010, rd=A5, opcode=0x2F
	// = 0x1A002000 | (A5<<7) | (A0<<15) | (A2<<20)
	// = 0x1A002000 | (15<<7) | (10<<15) | (12<<20) = 0x1A002000 | 0x780 | 0x50000 | 0xC00000
	WORD $0x1AC527AF        // *A0 = A2 (if reserved), A5 = status, rl semantics

	// A5 = 0 on success, non-zero on failure
	BNE	A5, ZERO, cas_loop  // If store failed, retry

	// Success: swap performed - get end time
	// CSRRS A6, time, zero = read TIME into A6
	WORD $0xC0102873        // A6 = TIME (rd=A6=x16, rs1=x0)

	SUB	A3, A6, A7          // A7 = elapsed = end - start
	BNE	A7, ZERO, have_elapsed // If elapsed != 0, use it
	MOV	$1, A7              // Else return 1 (minimum non-zero success)

have_elapsed:
	MOV	A7, ret+16(FP)      // Return elapsed time (non-zero)
	RET

cas_fail:
	MOV	$0, A0              // Return 0 (failure)
	MOV	A0, ret+16(FP)
	RET

// StoreUint32 - RISC-V atomic store with release semantics
//
// Atomically stores val to *addr with release semantics.
// This ensures all prior memory operations are visible before this store.
//
// Uses AMOSWAP.W.RL (Atomic Memory Operation Swap with Release).
// Alternatively, could use fence + SW, but AMOSWAP is cleaner.
//
// func StoreUint32(addr *uint32, val uint32)
TEXT ·StoreUint32(SB), NOSPLIT|NOFRAME, $0-12
	MOV	addr+0(FP), A0      // A0 = address
	MOVW	val+8(FP), A1       // A1 = value

	// AMOSWAP.W.RL zero, A1, (A0) - Swap with release, discard old value
	// Encoding: funct5=00001, aq=0, rl=1, rs2=A1, rs1=A0, funct3=010, rd=zero, opcode=0x2F
	// = 0x0A002000 | (0<<7) | (A0<<15) | (A1<<20)
	// = 0x0A002000 | 0x0 | 0x50000 | 0xB0000 = 0x0A152000
	WORD $0x0AB5202F        // *A0 = A1, release semantics, discard old value

	RET

// CurrentTime - Get current counter value (real or fake for testing)
//
// If fakeTime parameter is non-zero, returns fakeTime (for testing).
// If fakeTime is zero, reads TIME CSR (0xC01).
//
// This matches the counter used by ktimer package (RISC-V TIME CSR).
//
// func CurrentTime(fakeTime uint64) uint64
TEXT ·CurrentTime(SB), NOSPLIT|NOFRAME, $0-16
	MOV	fakeTime+0(FP), A0  // A0 = fakeTime parameter
	BNE	A0, ZERO, use_fake  // If non-zero, use fake time

	// fakeTime is zero - read TIME CSR
	// CSRRS A0, time, zero = read TIME into A0
	WORD $0xC0102573        // A0 = TIME (rd=A0=x10, rs1=x0)

use_fake:
	MOV	A0, ret+8(FP)       // Return A0 (either fake or real)
	RET

// nanoWait - Busy-wait for exact tick count using TIME CSR
//
// Spins in a tight loop reading TIME CSR (0xC01) until
// the specified number of ticks have elapsed. Does NOT yield CPU.
//
// This is counter-based (not instruction-based), so timing is accurate
// regardless of CPU frequency, cache state, or branch prediction.
//
// Uses the same TIME CSR as the ktimer package for consistency.
//
// func nanoWait(ticks uint64)
TEXT ·nanoWait(SB), NOSPLIT|NOFRAME, $0-8
	MOV	ticks+0(FP), A0     // A0 = tick count to wait

	// CSRRS A1, time, zero = read TIME into A1
	WORD $0xC01025F3        // A1 = start TIME (rd=A1=x11, rs1=x0)

	ADD	A0, A1, A2          // A2 = target = start + ticks

wait_loop:
	// CSRRS A3, time, zero = read TIME into A3
	WORD $0xC01026F3        // A3 = current TIME (rd=A3=x13, rs1=x0)

	BLTU	A3, A2, wait_loop   // If current < target, keep waiting

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
	MOV	$1, A0              // Return 1 (success with minimal elapsed time)
	MOV	A0, ret+16(FP)
	RET

// SpinlockLose - Test stub that always fails
//
// Simulates a failed CAS operation without actually performing atomics.
// Returns 0 to indicate failure.
// Used for testing timeout/retry code paths where the lock is contended.
//
// func SpinlockLose(addr *uint32, old, new uint32) uint64
TEXT ·SpinlockLose(SB), NOSPLIT|NOFRAME, $0-25
	MOV	$0, A0              // Return 0 (failure)
	MOV	A0, ret+16(FP)
	RET

// NanoWaitStub - Test stub that does nothing
//
// No-op wait function for testing without actual delays.
// Used to skip timing waits in unit tests.
//
// func NanoWaitStub(ticks uint64)
TEXT ·NanoWaitStub(SB), NOSPLIT|NOFRAME, $0-8
	RET                         // Immediate return (no wait)

// spinlockDeadHandler writes diagnostic to NS16550 UART and halts.
// func spinlockDeadHandler()
TEXT ·spinlockDeadHandler(SB), NOSPLIT|NOFRAME, $0
	// Write '!' 'L' to NS16550 UART at 0x10000000
	MOV	$0x10000000, X5
	MOV	$'!', X6
	MOVB	X6, (X5)
	MOV	$'L', X6
	MOVB	X6, (X5)
spinlock_dead_halt_riscv:
	WORD	$0x10500073		// WFI
	JMP	spinlock_dead_halt_riscv
