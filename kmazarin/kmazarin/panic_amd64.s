//go:build !test_stubs

#include "textflag.h"

// Exit terminates the system.
// On x86_64 QEMU, uses isa-debug-exit device at port 0x501 if available,
// otherwise halts in a loop.
TEXT ·Exit(SB), NOSPLIT, $0-0
	// Disable interrupts
	CLI

	// Try isa-debug-exit: write to port 0x501
	// QEMU exits with status (value << 1) | 1
	MOVL	$0x501, DX
	MOVL	$0, AX		// exit code 0 -> QEMU exit code 1
	OUTL

	// If isa-debug-exit not available, halt loop
halt:
	HLT
	JMP	halt
