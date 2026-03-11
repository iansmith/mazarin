#include "textflag.h"

// Exit hangs the system in an infinite loop.
// Called after kernel panic or fatal error.
TEXT ·Exit(SB), NOSPLIT|NOFRAME, $0
halt:
	WORD	$0x10500073		// WFI
	JMP	halt
