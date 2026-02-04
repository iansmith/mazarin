//go:build riscv64 && !test_stubs

#include "textflag.h"

// PlatformRearmTimer sets the timer using SBI set_timer call.
// func PlatformRearmTimer(ticks uint64)
TEXT ·PlatformRearmTimer(SB), NOSPLIT, $0-8
	MOV	ticks+0(FP), A0  // A0 = ticks to add

	// Read current counter value from TIME CSR (0xC01)
	// RDTIME instruction: CSRRS x10, time, x0
	// Encoding: 0xC0102573 (reads CSR 0xC01 into X10/A0)
	// But we need result in A1, so use X11/A1 as destination
	// CSRRS x11, time, x0 = 0xC01025F3
	WORD	$0xC01025F3  // Read TIME CSR into A1 (X11)

	// Calculate absolute deadline: current + ticks
	ADD	A0, A1, A0  // A0 = current + ticks (deadline)

	// Call SBI set_timer (legacy interface)
	// a7 (X17) = 0 (legacy timer extension)
	// a0 (X10) = stime_value (already in A0)
	MOV	$0, A7  // A7 = legacy SBI timer extension ID
	// ECALL instruction encoding: 0x00000073
	WORD	$0x00000073

	RET

// PlatformReadCounter reads the TIME CSR and returns the counter value.
// func PlatformReadCounter() uint64
TEXT ·PlatformReadCounter(SB), NOSPLIT, $0-8
	// Read TIME CSR (0xC01) into A0
	// RDTIME instruction: CSRRS x10, time, x0
	// Encoding: 0xC0102573 (reads CSR 0xC01 into X10/A0)
	WORD	$0xC0102573
	MOV	A0, ret+0(FP)
	RET
