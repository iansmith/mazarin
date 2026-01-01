// layout.go - Memory layout variables for Cardinal on QEMU virt machine
//
// These variables provide memory layout information that was previously
// supplied by the GNU linker script (linker.ld). With Go's native toolchain,
// we initialize constants directly and patch section boundaries post-build.
//
// Variable categories:
// 1. FIXED CONSTANTS - Initialized here (QEMU virt machine addresses, memory layout)
// 2. SECTION BOUNDARIES - Initialized to 0, patched by compute-linker-values tool
// 3. EMBEDDED KMAZARIN - Not yet implemented in go-native build
//
// Variable naming: All symbols are prefixed with "Linker" to indicate they
// correspond to linker script symbols.

package main

import "cardinal/asm"

// ============================================================================
// Memory Layout Constants (from linker.ld)
// ============================================================================
const (
	// Base addresses and sizes from QEMU virt machine / linker.ld
	BootAddress            = 0x40000000 // RAM start on QEMU virt
	DtbSize                = 0x100000   // 1 MB for Device Tree Blob
	CardinalAllocationSize = 0xF00000   // 15 MB for cardinal
	PageTableSize          = 0x800000   // 8 MB for page tables

	// Calculated boundaries
	DtbStart         = BootAddress
	CardinalStart    = DtbStart + DtbSize
	CardinalEnd      = CardinalStart + CardinalAllocationSize
	PageTableStart   = CardinalEnd
	PageTableEnd     = PageTableStart + PageTableSize
	KmazarinLoadAddr = PageTableEnd
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

	// Memory layout boundaries - FIXED VALUES from constants above
	LinkerRamStart               uint64 = BootAddress
	LinkerDtbBootAddr            uint64 = DtbStart
	LinkerDtbSize                uint64 = DtbSize
	LinkerCardinalEnd            uint64 = CardinalEnd
	LinkerCardinalAllocationSize uint64 = CardinalAllocationSize
	LinkerKmazarinLoadAddr       uint64 = KmazarinLoadAddr
	LinkerPageTablesStart        uint64 = PageTableStart
	LinkerPageTablesEnd          uint64 = PageTableEnd

	// Stack pointers - FIXED VALUES from CLAUDE.md memory map
	// g0 stack: 0x5EFF0000-0x5F000000 (64KB)
	// Exception stack: 0x5F000000-0x5F020000 (128KB)
	LinkerStackTop      uint64 = 0x5F000000
	LinkerG0StackBottom uint64 = 0x5EFF0000

	// MMIO device addresses - FIXED VALUES from QEMU virt machine
	LinkerGicBase          uint64 = 0x08000000 // GIC base
	LinkerGicSize          uint64 = 0x00020000 // GIC size (128KB)
	LinkerUartBase         uint64 = 0x09000000 // UART PL011
	LinkerUartSize         uint64 = 0x00010000 // UART size (64KB)
	LinkerRtcBase          uint64 = 0x09010000 // PL031 RTC
	LinkerFwcfgBase        uint64 = 0x09020000 // QEMU fw_cfg device
	LinkerFwcfgSize        uint64 = 0x00010000 // fw_cfg size (64KB)
	LinkerBochsDisplayBase uint64 = 0x10000000 // bochs-display framebuffer
	LinkerBochsDisplaySize uint64 = 0x01000000 // bochs-display size (16MB)
	LinkerPciBarBase       uint64 = 0x11000000 // PCI BAR allocation start
	LinkerPciBarSize       uint64 = 0x0F000000 // PCI BAR pool size (240MB)

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
		LinkerStackTop + LinkerG0StackBottom +
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
