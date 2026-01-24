#include "textflag.h"

// Kmazarin kernel binary embedding - accessor functions
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains accessor functions for the embedded kmazarin kernel binary.
// The actual binary data is generated at build time by incbin2goasm tool and
// stored in kmazarin_data_arm64.s.
//
// Functions:
//   - kmazarinBinaryStart() - Returns address of embedded kernel start
//   - kmazarinBinaryEnd()   - Returns address of embedded kernel end
//
// ABI NOTES:
// - These are package-local functions (· prefix) using stack-based ABI
// - Return values MUST be stored to ret+0(FP)
// - These reference kmazarin_binary_start symbol from kmazarin_data_arm64.s

// ============================================================================
// kmazarinBinaryStart() - Get Embedded Kernel Start Address
// ============================================================================
// Returns the address of the embedded kmazarin binary start
// Returns: ret+0(FP) = address of kmazarin_binary_start
//
// Segments:
//   1. Load address of binary start into R0
//   2. Store R0 to stack return location
//   3. Return to caller
//
TEXT ·kmazarinBinaryStart(SB), NOSPLIT, $0-8
	// Segment 1: Load binary start address
	MOVD $kmazarin_binary_start(SB), R0

	// Segment 2: Store to stack
	MOVD R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// kmazarinBinaryEnd() - Get Embedded Kernel End Address
// ============================================================================
// Returns the end address of the embedded kmazarin binary
// Returns: ret+0(FP) = address of kmazarin_binary_start + size
//
// Segments:
//   1. Load address of binary start into R0
//   2. Load binary size into R1
//   3. Add size to start address
//   4. Store result to stack return location
//   5. Return to caller
//
TEXT ·kmazarinBinaryEnd(SB), NOSPLIT, $0-8
	// Segment 1: Load binary start address
	MOVD $kmazarin_binary_start(SB), R0

	// Segment 2: Load size
	MOVD kmazarin_binary_size(SB), R1  // Load size value (not address)

	// Segment 3: Calculate end address
	ADD R1, R0

	// Segment 4: Store to stack
	MOVD R0, ret+0(FP)

	// Segment 5: Return
	RET
