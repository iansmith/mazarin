// layout.go - Memory layout variables for Cardinal on QEMU virt machine
//
// These variables provide memory layout information using constants from
// the constants/layout package (single source of truth).
//
// Variable categories:
// 1. FIXED CONSTANTS - Imported from constants package
// 2. SECTION BOUNDARIES - Initialized to 1, patched by compute-linker-values tool
// 3. EMBEDDED KMAZARIN - Initialized to 1, patched by compute-linker-values tool
//
// Variable naming: All symbols are prefixed with "Linker" to indicate they
// correspond to linker script symbols.

package main

import (
	"cardinal/asm"
	"shared/constants"
)

// ============================================================================
// Linker Symbol Variables
// ============================================================================

// DataTestMagic is a unique 64-byte pattern for debugging data section loading.
// This pattern helps locate where the data section was actually loaded in memory.
// The pattern uses values that are the same in big-endian and little-endian,
// making it easier to identify in memory dumps.
var DataTestMagic = [8]uint64{
	0xDEADBEEFDEADBEEF, // Magic[0] - "deadbeef" repeated
	0xCAFEBABECAFEBABE, // Magic[1] - "cafebabe" repeated
	0x0123456789ABCDEF, // Magic[2] - counting pattern
	0xFEDCBA9876543210, // Magic[3] - reverse counting
	0x5555555555555555, // Magic[4] - alternating bits
	0xAAAAAAAAAAAAAAAA, // Magic[5] - inverse alternating
	0x0F0F0F0F0F0F0F0F, // Magic[6] - nibble pattern
	0xF0F0F0F0F0F0F0F0, // Magic[7] - inverse nibble pattern
}

var (
	// Section boundaries - PATCHED POST-BUILD by compute-linker-values tool
	// These must be discovered from the actual ELF binary after linking.
	// Initialized to 1 (not 0) so they're placed in .data section, not .bss.
	// This allows the post-build tool to patch them with actual values.
	LinkerStart       uint64 = 1 // __start (patched)
	LinkerTextStart   uint64 = 1 // __text_start (patched)
	LinkerTextEnd     uint64 = 1 // __text_end (patched)
	LinkerRodataStart uint64 = 1 // __rodata_start (patched)
	LinkerRodataEnd   uint64 = 1 // __rodata_end (patched)
	LinkerDataStart   uint64 = 1 // __data_start (patched)
	LinkerDataEnd     uint64 = 1 // __data_end (patched)
	LinkerBssStart    uint64 = 1 // __bss_start (patched)
	LinkerBssEnd      uint64 = 1 // __bss_end (patched)
	LinkerEnd         uint64 = 1 // __end (patched)

	// Memory layout boundaries - FIXED VALUES from constants package
	LinkerRamStart               uint64 = constants.BootAddress
	LinkerDtbBootAddr            uint64 = constants.DtbStart
	LinkerDtbSize                uint64 = constants.DtbSize
	LinkerCardinalEnd            uint64 = constants.CardinalEnd
	LinkerCardinalAllocationSize uint64 = constants.CardinalAllocationSize
	LinkerKmazarinLoadAddr       uint64 = constants.KmazarinLoadAddr
	LinkerPageTablesStart        uint64 = constants.PageTableStart
	LinkerPageTablesEnd          uint64 = constants.PageTableEnd

	// Stack pointers - FIXED VALUES from constants package
	// g0 stack: 0x5EFF0000-0x5F000000 (64KB, SP_EL0)
	// Exception stack: 0x5F000000-0x5F020000 (128KB, SP_EL1)
	LinkerStackTop          uint64 = constants.G0StackTop           // SP_EL0 top
	LinkerG0StackBottom     uint64 = constants.G0StackBottom        // SP_EL0 bottom
	LinkerExceptionStackTop uint64 = constants.ExceptionStackTop    // SP_EL1 top

	// MMIO device addresses - FIXED VALUES from constants package (QEMU virt machine)
	LinkerGicBase          uint64 = constants.GicBase
	LinkerGicSize          uint64 = constants.GicSize
	LinkerUartBase         uint64 = constants.UartBase
	LinkerUartSize         uint64 = constants.UartSize
	LinkerRtcBase          uint64 = constants.RtcBase
	LinkerFwcfgBase        uint64 = constants.FwcfgBase
	LinkerFwcfgSize        uint64 = constants.FwcfgSize
	LinkerBochsDisplayBase uint64 = constants.BochsDisplayBase
	LinkerBochsDisplaySize uint64 = constants.BochsDisplaySize
	LinkerPciBarBase       uint64 = constants.PciBarBase
	LinkerPciBarSize       uint64 = constants.PciBarSize

	// Embedded kmazarin kernel - NOT YET IMPLEMENTED in go-native build
	// Initialized to 1 so they're in .data section for patching
	LinkerKmazarinStart uint64 = 1 // __kmazarin_start (patched)
	LinkerKmazarinSize  uint64 = 1 // __kmazarin_size (patched)
)

// linkerValuesSum is used to prevent dead-code elimination of Linker* variables.
// Go's linker will eliminate unused variables, so we create a dependency.
// This variable is never actually read at runtime.
var linkerValuesSum uint64

// init ensures all Linker* variables and critical symbols are kept by the linker.
// Without this, Go's dead-code elimination would remove unused variables and functions.
func init() {
	// Touch all variables to prevent elimination
	// The compiler can't prove these are unused since init() always runs
	linkerValuesSum = LinkerStart + LinkerTextStart + LinkerTextEnd +
		LinkerRodataStart + LinkerRodataEnd +
		LinkerDataStart + LinkerDataEnd +
		LinkerBssStart + LinkerBssEnd + LinkerEnd +
		LinkerRamStart + LinkerDtbBootAddr + LinkerDtbSize +
		LinkerCardinalEnd + LinkerCardinalAllocationSize +
		LinkerKmazarinLoadAddr + LinkerPageTablesStart + LinkerPageTablesEnd +
		LinkerStackTop + LinkerG0StackBottom + LinkerExceptionStackTop +
		LinkerGicBase + LinkerGicSize +
		LinkerUartBase + LinkerUartSize +
		LinkerRtcBase + LinkerFwcfgBase + LinkerFwcfgSize +
		LinkerBochsDisplayBase + LinkerBochsDisplaySize +
		LinkerPciBarBase + LinkerPciBarSize +
		LinkerKmazarinStart + LinkerKmazarinSize +
		DataTestMagic[0] + DataTestMagic[1] + DataTestMagic[2] + DataTestMagic[3] +
		DataTestMagic[4] + DataTestMagic[5] + DataTestMagic[6] + DataTestMagic[7]

	// Reference CardinalBoot to prevent dead-code elimination of the entry point.
	// This function is the bare-metal entry point that QEMU jumps to.
	// We store the function pointer in a global variable that the compiler can't
	// prove is unused, forcing it to keep the function.
	cardinalBootPtr = asm.CardinalBoot

	// Reference KernelMain to prevent dead-code elimination.
	// boot_arm64.s calls main·KernelMain which is implemented in abi_stubs_arm64.s.
	// Without this reference, the entire kernel code would be eliminated.
	// We store the function pointer in a global variable that the compiler can't
	// prove is unused, forcing it to keep the function.
	kernelMainPtr = KernelMain
}

// cardinalBootPtr stores a reference to CardinalBoot to prevent dead-code elimination.
// The Go compiler can't prove this variable is never read at runtime.
var cardinalBootPtr func()

// kernelMainPtr stores a reference to KernelMain to prevent dead-code elimination.
// The Go compiler can't prove this variable is never read at runtime.
var kernelMainPtr func(uint32, uint32, uint32)
