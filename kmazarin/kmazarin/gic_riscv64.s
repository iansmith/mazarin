//go:build !test_stubs

#include "textflag.h"

// ReadTime reads the time CSR (rdtime pseudo-instruction).
// func ReadTime() uint64
TEXT ·ReadTime(SB), NOSPLIT, $0-8
	// RDTIME A0 = CSRRS A0, time, zero
	// time CSR = 0xC01
	WORD	$0xC0102573		// csrr a0, time
	MOV	A0, ret+0(FP)
	RET

// ReadSSTATUS reads the sstatus CSR.
// func ReadSSTATUS() uint64
TEXT ·ReadSSTATUS(SB), NOSPLIT, $0-8
	// CSRR A0, sstatus
	WORD	$0x10002573		// csrr a0, sstatus
	MOV	A0, ret+0(FP)
	RET

// RearmTimerNow re-arms the S-mode timer via SBI set_timer.
// Sets the timer to fire ~10ms from now.
// func RearmTimerNow()
TEXT ·RearmTimerNow(SB), NOSPLIT, $0-0
	// Read current time
	// CSRR A0, time
	WORD	$0xC0102573		// a0 = rdtime

	// Add ~10ms worth of ticks (assuming 10MHz timebase from DTB)
	// QEMU riscv virt: timebase-frequency = 10000000 (10MHz)
	// 10ms * 10MHz = 100000 ticks
	MOV	$100000, A1
	ADD	A0, A1, A0		// A0 = now + 100000

	// SBI set_timer (legacy extension)
	// A0 = stime_value, A7 = 0 (legacy set_timer)
	MOV	$0, A7
	WORD	$0x00000073		// ECALL

	// Re-enable timer interrupt in sie CSR
	// CSRS sie, 0x20 (STIE = bit 5)
	MOV	$0x20, A0
	// CSRS sie, a0
	WORD	$0x10452073		// csrs sie, a0

	RET

// DisableTimerHardware disables the S-mode timer interrupt in sie CSR.
// func DisableTimerHardware()
TEXT ·DisableTimerHardware(SB), NOSPLIT, $0-0
	// Clear STIE (bit 5) in sie CSR
	MOV	$0x20, A0
	// CSRC sie, a0
	WORD	$0x10453073		// csrc sie, a0
	RET
