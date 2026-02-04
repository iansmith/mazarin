//go:build !test_stubs

#include "textflag.h"

// Exit hangs the system in an infinite loop.
TEXT ·Exit(SB), NOSPLIT|NOFRAME, $0
halt:
	HLT
	JMP	halt
