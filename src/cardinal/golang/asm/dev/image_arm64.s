#include "textflag.h"

// Image data accessor functions
//
// ============================================================================
// OVERVIEW
// ============================================================================
// This file contains accessor functions for embedded binary image data.
// The actual binary data is generated at build time by bin2elf tool.
//
// Functions:
//   - imageDataStart() - Returns address of embedded image start
//   - imageDataEnd()   - Returns address of embedded image end
//
// ABI NOTES:
// - These are GLOBAL symbols (no · prefix) so they can be linked via //go:linkname
// - These functions use Go 1.17+ register-based calling convention
// - Return values go in R0 (register ABI)

// ============================================================================
// imageDataStart() - Get Embedded Image Start Address
// ============================================================================
// Returns the address of the image data start
// Returns: R0 = address of _binary_boot_mazarin_bin_start
//
// Segments:
//   1. Load address of image start into R0
//   2. Return to caller
//
TEXT imageDataStart(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Load image start address
	MOVD $_binary_boot_mazarin_bin_start(SB), R0

	// Segment 2: Return
	// Return value is in R0 (register ABI)
	RET

// ============================================================================
// imageDataEnd() - Get Embedded Image End Address
// ============================================================================
// Returns the address of the image data end
// Returns: R0 = address of _binary_boot_mazarin_bin_end
//
// Segments:
//   1. Load address of image end into R0
//   2. Return to caller
//
TEXT imageDataEnd(SB), NOSPLIT|NOFRAME, $0-8
	// Segment 1: Load image end address
	MOVD $_binary_boot_mazarin_bin_end(SB), R0

	// Segment 2: Return
	// Return value is in R0 (register ABI)
	RET
