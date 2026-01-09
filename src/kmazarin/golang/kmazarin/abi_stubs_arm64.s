// abi_stubs_arm64.s - ABI0 entry points for functions called from assembly
//
// When assembly in this package calls main·functionName, Go expects ABI0 entry
// points. These stubs provide ABI0 wrappers that read arguments from the stack
// and call the ABIInternal implementations with arguments in registers.

#include "textflag.h"

// SyscallDispatch is called from exceptions_arm64.s
// Go signature: func SyscallDispatch(syscallNum, arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64
// ABI0: 7 args (56 bytes) + 1 return (8 bytes) = 64 bytes
//
// Tail-call to the internal function. The .abi0 wrapper will read args from
// our caller's stack (which is exactly where they were placed by exceptions_arm64.s).
// This avoids adding to the nosplit stack chain.
TEXT ·SyscallDispatch(SB), NOSPLIT, $0-64
	JMP	·syscallDispatchInternal(SB)

// IRQDispatch is called from exceptions_arm64.s
// Go signature: func IRQDispatch(irqNum uint64, framePtr uintptr, elr, spEl0 uint64) (newELR, newSP, newLR uint64, doPreempt bool)
// ABI0: 4 args (32 bytes) + 4 returns (32 bytes) = 64 bytes
//
// Tail-call to internal function. The internal's .abi0 wrapper reads from our stack.
TEXT ·IRQDispatch(SB), NOSPLIT, $0-64
	JMP	·irqDispatchInternal(SB)

// HandlePageFaultAsm is called from data_abort in exceptions_arm64.s
// Go signature: func HandlePageFaultAsm(faultAddr uint64) uint64
// ABI0: 1 arg (8 bytes) + 1 return (8 bytes) = 16 bytes
// Returns 1 if handled, 0 if not.
//
// SAFETY: This stub validates all addresses before use to prevent cascading faults.
// All addresses must be in high memory (0xFFFFFFFF00000000+) since kmazarin runs in TTBR1.
TEXT ·HandlePageFaultAsm(SB), NOSPLIT, $0-16
	// Use hardcoded high-memory UART address (defined in mmio_constants.go)
	// UART_BASE = 0xFFFFFFFF09000000 (high memory, always mapped)
	MOVD	$0xFFFFFFFF09000000, R10

	// SAFETY CHECK: Verify fault address is accessible before printing
	// Fault address is at 8(RSP) in ABI0 convention
	MOVD	8(RSP), R12

	// Check if fault address top nibble is valid (not in unmapped region)
	LSR	$60, R12, R11
	CMP	$0, R11           // 0x0xxxxxxx = unmapped
	BEQ	fault_in_unmapped
	CMP	$0x4, R11         // 0x4xxxxxxx = Cardinal low memory (unmapped after init)
	BEQ	fault_in_cardinal

	// Fault address looks reasonable - print diagnostic
	MOVD	$'P', R11         // 'P' = Page fault
	MOVB	R11, (R10)

	// Print top nibble of fault address
	LSR	$60, R12, R11
	AND	$0xF, R11
	CMP	$10, R11
	BLT	fault_digit
	ADD	$('A'-10), R11
	B	fault_print
fault_digit:
	ADD	$'0', R11
fault_print:
	MOVB	R11, (R10)

	// DEBUG: Print '@' before loading symbol
	MOVD	$'@', R11
	MOVB	R11, (R10)

	// CRITICAL: Load target handler address and validate
	MOVD	$·handlePageFaultInternal(SB), R20

	// DEBUG: Print '#' after loading symbol
	MOVD	$'#', R11
	MOVB	R11, (R10)

	// SAFETY CHECK: Verify target is in high memory (0xFxxxxxxx)
	LSR	$60, R20, R11
	CMP	$0xF, R11
	BNE	target_low_memory

	// DEBUG: Print 'OK' to show target is valid
	MOVD	$'O', R11
	MOVB	R11, (R10)
	MOVD	$'K', R11
	MOVB	R11, (R10)

	// Target is valid - tail call to handler
	JMP	(R20)

fault_in_unmapped:
	// Fault at 0x0xxxxxxx (NULL or unmapped) - print error and hang
	MOVD	$'N', R11
	MOVB	R11, (R10)
	MOVD	$'U', R11
	MOVB	R11, (R10)
	MOVD	$'L', R11
	MOVB	R11, (R10)
	MOVD	$'L', R11
	MOVB	R11, (R10)
	MOVD	$'!', R11
	MOVB	R11, (R10)
null_hang:
	B	null_hang

fault_in_cardinal:
	// Fault at 0x4xxxxxxx (Cardinal memory - should be unmapped) - print error and hang
	MOVD	$'C', R11
	MOVB	R11, (R10)
	MOVD	$'A', R11
	MOVB	R11, (R10)
	MOVD	$'R', R11
	MOVB	R11, (R10)
	MOVD	$'D', R11
	MOVB	R11, (R10)
	MOVD	$'!', R11
	MOVB	R11, (R10)
cardinal_hang:
	B	cardinal_hang

target_low_memory:
	// Handler address is in low memory (relocation bug!) - print error and hang
	MOVD	$'B', R11
	MOVB	R11, (R10)
	MOVD	$'U', R11
	MOVB	R11, (R10)
	MOVD	$'G', R11
	MOVB	R11, (R10)
	MOVD	$'!', R11
	MOVB	R11, (R10)

	// Print handler address nibbles for debugging
	MOVD	$' ', R11
	MOVB	R11, (R10)
	LSR	$60, R20, R11
	AND	$0xF, R11
	ADD	$'0', R11
	MOVB	R11, (R10)
bug_hang:
	B	bug_hang
