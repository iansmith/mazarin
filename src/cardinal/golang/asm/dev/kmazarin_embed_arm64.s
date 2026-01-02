#include "textflag.h"

// Kmazarin kernel binary embedding - accessor functions
// The actual binary data is generated at build time by bin2elf tool
// These are GLOBAL symbols (no · prefix) so they can be linked via //go:linkname
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Return values go in R0.

// KmazarinBinaryStart returns the address of the embedded kmazarin binary start
// Returns: R0 = address of kmazarin_binary_start
TEXT KmazarinBinaryStart(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $kmazarin_binary_start(SB), R0
	// Return value is in R0 (register ABI)
	RET

// KmazarinBinaryEnd returns the end address of the embedded kmazarin binary
// Returns: R0 = address of kmazarin_binary_start + size
TEXT KmazarinBinaryEnd(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $kmazarin_binary_start(SB), R0
	MOVD kmazarin_binary_size(SB), R1  // Load size value (not address)
	ADD R1, R0
	// Return value is in R0 (register ABI)
	RET
