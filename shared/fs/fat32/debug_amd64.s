// shared/fs/fat32/debug_amd64.s
// Debug output stub for x86_64 — no-op in userspace (OUTB requires Ring 0)

#include "textflag.h"

// func debugOut(c byte)
//
// No-op on x86_64 — OUTB is a privileged instruction that causes #GP in Ring 3.
// Use sys.RawWrite for output from .maz modules instead.
TEXT ·debugOut(SB), NOSPLIT, $0-1
	RET
