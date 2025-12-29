#include "textflag.h"

// Image data accessor functions
// The actual binary data is generated at build time by bin2elf tool
// These are GLOBAL symbols (no · prefix) so they can be linked via //go:linkname

// imageDataStart returns the address of the image data start
// Returns: address of _binary_boot_mazarin_bin_start
TEXT imageDataStart(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $_binary_boot_mazarin_bin_start(SB), R0
	MOVD R0, ret+0(FP)
	RET

// imageDataEnd returns the address of the image data end
// Returns: address of _binary_boot_mazarin_bin_end
TEXT imageDataEnd(SB), NOSPLIT|NOFRAME, $0-8
	MOVD $_binary_boot_mazarin_bin_end(SB), R0
	MOVD R0, ret+0(FP)
	RET
