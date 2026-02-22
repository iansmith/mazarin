// diplomat/main/diplomat_entry_wrapper_riscv64.s
// Assembly wrapper for DiplomatEntry that writes debug marker then calls Go code.

#include "textflag.h"

// diplomatEntryWrapper calls DiplomatEntry.
// The M-mode trap handler (set up by trampoline) will catch any exceptions.
TEXT ·diplomatEntryWrapper(SB), NOSPLIT|NOFRAME, $0
	// Call DiplomatEntry - if this traps, the trap handler will print diagnostics
	CALL	·DiplomatEntry(SB)

	// Infinite loop (DiplomatEntry never returns)
	WORD	$0xffdff06f	// JAL back to self
