#include "textflag.h"

// Kmazarin kernel binary embedding - accessor functions
// The actual binary data is generated at build time by incbin2goasm tool
// These are package-local functions (· prefix) that return via stack

// kmazarinBinaryStart returns the address of the embedded kmazarin binary start
// Returns: ret+0(FP) = address of kmazarin_binary_start
TEXT ·kmazarinBinaryStart(SB), NOSPLIT, $0-8
	MOVD $kmazarin_binary_start(SB), R0
	MOVD R0, ret+0(FP)
	RET

// kmazarinBinaryEnd returns the end address of the embedded kmazarin binary
// Returns: ret+0(FP) = address of kmazarin_binary_start + size
TEXT ·kmazarinBinaryEnd(SB), NOSPLIT, $0-8
	MOVD $kmazarin_binary_start(SB), R0
	MOVD kmazarin_binary_size(SB), R1  // Load size value (not address)
	ADD R1, R0
	MOVD R0, ret+0(FP)
	RET
