#include "textflag.h"

// Image data accessor functions
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains accessor functions for embedded binary image data.
// The actual binary data is generated at build time by incbin2goasm tool.
//
// Functions:
//   - imageDataStart() - Returns address of embedded image start
//   - imageDataEnd()   - Returns address of embedded image end
//
// ABI NOTES:
// - These are GLOBAL symbols (no · prefix) so they can be linked via //go:linkname
// - These functions use STACK-BASED ABI (return via ret+0(FP))
// - The Go compiler generates .abi0 wrappers that expect stack returns

// ============================================================================
// imageDataStart() - Get Embedded Image Start Address
// ============================================================================
// Returns the address of the image data start
// Returns: ret+0(FP) = address of _binary_boot_mazarin_bin_start
//
// Segments:
//   1. Load address of image start into R0
//   2. Store to stack return location
//   3. Return to caller
//
TEXT imageDataStart(SB), NOSPLIT, $0-8
	// Segment 1: Load image start address
	MOVD $_binary_boot_mazarin_bin_start(SB), R0

	// Segment 2: Store to stack
	MOVD R0, ret+0(FP)

	// Segment 3: Return
	RET

// ============================================================================
// imageDataEnd() - Get Embedded Image End Address
// ============================================================================
// Returns the address of the image data end (start + size)
// Returns: ret+0(FP) = _binary_boot_mazarin_bin_start + size
//
// Segments:
//   1. Load start address
//   2. Load size value
//   3. Add size to start address
//   4. Store result to stack return location
//   5. Return to caller
//
TEXT imageDataEnd(SB), NOSPLIT, $0-8
	// Segment 1: Load image start address
	MOVD $_binary_boot_mazarin_bin_start(SB), R0

	// Segment 2: Load size value (not address)
	MOVD _binary_boot_mazarin_bin_size(SB), R1

	// Segment 3: Calculate end address
	ADD R1, R0

	// Segment 4: Store to stack
	MOVD R0, ret+0(FP)

	// Segment 5: Return
	RET
