#include "textflag.h"

// Image data accessor functions
// The actual binary data is generated at build time by bin2elf tool
// These are GLOBAL symbols (no · prefix) so they can be linked via //go:linkname
//
// NOTE: These functions use Go 1.17+ register-based calling convention.
// Return values go in R0.

// imageDataStart returns the address of the image data start
// Returns: R0 = address of _binary_boot_mazarin_bin_start
TEXT imageDataStart(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $_binary_boot_mazarin_bin_start(SB), R0
	// Return value is in R0 (register ABI)
	RET

// imageDataEnd returns the address of the image data end
// Returns: R0 = address of _binary_boot_mazarin_bin_end
TEXT imageDataEnd(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $_binary_boot_mazarin_bin_end(SB), R0
	// Return value is in R0 (register ABI)
	RET
