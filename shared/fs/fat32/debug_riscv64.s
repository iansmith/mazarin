// shared/fs/fat32/debug_riscv64.s
// Debug output stub for RISC-V 64-bit - no-op for now

#include "textflag.h"

// func debugOut(c byte)
//
// No-op on RISC-V - diplomat/kmazarin can implement their own debug output
TEXT ·debugOut(SB), NOSPLIT, $0-1
	RET
