// shared/fs/fat32/debug_arm64.s
// Debug output stub for ARM64 - no-op for now
// Cardinal can replace this with UART output if needed

#include "textflag.h"

// func debugOut(c byte)
//
// No-op on ARM64 - cardinal/kmazarin can implement their own debug output
TEXT ·debugOut(SB), NOSPLIT, $0-1
	RET
