// diplomat/main/diplomat_entry_wrapper_riscv64.s
// Assembly wrapper for DiplomatEntry that writes debug marker then calls Go code.

#include "textflag.h"

// diplomatEntryWrapper writes debug markers and calls DiplomatEntry.
// The M-mode trap handler (set up by trampoline) will catch any exceptions.
TEXT ·diplomatEntryWrapper(SB), NOSPLIT|NOFRAME, $0
	// Load UART base into X5
	// LUI X5, 0x10000
	WORD	$0x100002b7

	// Write 'E' to prove we entered wrapper
	WORD	$0x04500313	// ADDI X6, X0, 'E'
	WORD	$0x00628023	// SB X6, 0(X5)

	// Write 'W' before calling DiplomatEntry
	WORD	$0x05700313	// ADDI X6, X0, 'W'
	WORD	$0x00628023	// SB X6, 0(X5)

	// Call DiplomatEntry - if this traps, the trap handler will print diagnostics
	CALL	·DiplomatEntry(SB)

	// Write 'X' if we return (should never happen - DiplomatEntry never returns)
	WORD	$0x100002b7	// LUI X5, 0x10000 (reload UART base, may have been clobbered)
	WORD	$0x05800313	// ADDI X6, X0, 'X'
	WORD	$0x00628023	// SB X6, 0(X5)

	// Infinite loop
	WORD	$0xffdff06f	// JAL back to self
