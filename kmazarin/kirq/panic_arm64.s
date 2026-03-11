
#include "textflag.h"

// Exit hangs the system in an infinite loop
// Called after kernel panic or fatal error
//
TEXT ·Exit(SB), NOSPLIT|NOFRAME, $0
halt:
	WFI  // Wait for interrupt (low power)
	B    halt
