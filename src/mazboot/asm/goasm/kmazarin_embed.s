#include "textflag.h"

// Kmazarin kernel binary embedding - accessor functions
// The actual binary data is generated at build time by bin2elf tool
// These are GLOBAL symbols (no · prefix) so they can be linked via //go:linkname

// KmazarinBinaryStart returns the address of the embedded kmazarin binary start
// Returns: address of kmazarin_binary_start
TEXT KmazarinBinaryStart(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $kmazarin_binary_start(SB), R0
	MOVD R0, ret+0(FP)
	RET

// KmazarinBinaryEnd returns the address of the embedded kmazarin binary end
// Returns: address of kmazarin_binary_end
TEXT KmazarinBinaryEnd(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $kmazarin_binary_end(SB), R0
	MOVD R0, ret+0(FP)
	RET
