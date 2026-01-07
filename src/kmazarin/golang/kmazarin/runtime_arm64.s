//go:build qemuvirt && aarch64

// runtime_arm64.s - Runtime support functions for kmazarin

#include "textflag.h"

// ============================================================================
// getAuxval - Get auxiliary vector value by tag
// ============================================================================
// func getAuxval(tag uint64) uint64
//
// For now, we return hardcoded values for known tags.
// TODO: Implement proper auxv parsing from initial stack
//
// Cardinal passes auxv with these custom entries:
//   AT_DTB_PHYS (0x1000) = 0x40000000 (DTB physical address)
//   AT_DTB_SIZE (0x1001) = DTB size
//   AT_KMAZARIN_PHYS (0x1002) = kmazarin physical base
//   etc.

TEXT ·getAuxval(SB), NOSPLIT, $0-16
	MOVD	tag+0(FP), R0		// Load tag parameter

	// Check for AT_DTB_PHYS (0x1000)
	MOVD	$0x1000, R1
	CMP	R0, R1
	BEQ	return_dtb_addr

	// Unknown tag - return 0
	MOVD	$0, R0
	MOVD	R0, ret+8(FP)
	RET

return_dtb_addr:
	// Return DTB high-memory address (0xFFFFFFFF40000000)
	// Physical 0x40000000 is mapped to kernel VA via TTBR1
	MOVD	$0xFFFFFFFF40000000, R0
	MOVD	R0, ret+8(FP)
	RET
