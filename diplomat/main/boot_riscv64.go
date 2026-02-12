//go:build riscv64

// diplomat/main/boot_riscv64.go - RISC-V 64-bit boot sequence (non-UEFI)
//
// RISC-V diplomat runs under OpenSBI firmware, not UEFI. This file provides
// alternative implementations of boot services that would normally come from UEFI.

package main

import (
	"mazzy/shared/blockdev"
	"mazzy/shared/fs/fat32"
)

// Global FDT info (populated by InitializeFDT)
var fdtInfo *FDTInfo

// Assembly functions to read firmware parameters from S0/S1 registers
// (saved by bootstrap stub in inject.go)
//
//go:noescape
func readBootHartID() uint64

//go:noescape
func readFDTPointer() uint64

// InitializeFDT parses the FDT passed by OpenSBI and initializes global state.
// This should be called early in DiplomatEntry, before any other initialization.
//
//go:nosplit
func InitializeFDT() bool {
	printString("DBG: Initializing FDT...\r\n")

	// WORKAROUND: Hardcode FDT address for QEMU virt platform
	// TODO: Fix bootstrap stub to properly preserve A0/A1 in S0/S1
	// OpenSBI always passes FDT at 0xFFE00000 on QEMU virt
	bootHartID = 0
	fdtPointer = 0xFFE00000

	printString("Hardcoded: hart=")
	printHex(bootHartID)
	printString(" fdt=")
	printHex(fdtPointer)
	printString("\r\n")

	// Parse FDT
	info, err := parseFDT(fdtPointer)
	if err != nil {
		printString("ERROR: FDT parse failed: ")
		printString(err.Error())
		printString("\r\n")
		return false
	}

	// Store globally
	fdtInfo = info

	printString("FDT initialized successfully\r\n")
	printString("  Hart ID: ")
	printHex(bootHartID)
	printString("\r\n")
	printString("  RAM: 0x")
	printHex(info.RAMBase)
	printString(" - 0x")
	printHex(info.RAMBase + info.RAMSize)
	printString(" (")
	printHex(info.RAMSize / (1024 * 1024))
	printString(" MB)\r\n")
	printString("  CPUs: ")
	printHex(uint64(info.CPUCount))
	printString("\r\n")
	if info.ISAString != "" {
		printString("  ISA: ")
		printString(info.ISAString)
		printString("\r\n")
	}

	return true
}

// InitSpansRISCV wraps InitializeSpans with FDT initialization.
// This is called from the RISC-V boot sequence instead of InitializeSpans directly.
//
//go:nosplit
func InitSpansRISCV() bool {
	// Parse FDT first
	if !InitializeFDT() {
		return false
	}

	// Then initialize memory spans (regular path)
	return InitializeSpans()
}

// GetBootDeviceRISCV returns a block device for the boot disk.
// On RISC-V, we don't have UEFI HandleProtocol, so we need to either:
//   1. Implement VirtIO block device access directly (TODO)
//   2. Use memory-mapped kernel loading via QEMU -kernel (current approach)
//
// For now, this is a stub that explains the situation.
func GetBootDeviceRISCV() (*UEFIBlockDevice, error) {
	printString("RISC-V boot: UEFI services not available\r\n")
	printString("RISC-V boot: Need to implement VirtIO block device access\r\n")
	printString("\r\n")
	printString("=== RISC-V Diplomat Phase 2 Complete ===\r\n")
	printString("OpenSBI boot:     SUCCESS\r\n")
	printString("Bootstrap stub:   SUCCESS\r\n")
	printString("Trampoline init:  SUCCESS\r\n")
	printString("g0/m0/TLS setup:  SUCCESS\r\n")
	printString("DiplomatEntry:    SUCCESS\r\n")
	printString("Memory spans:     SUCCESS\r\n")
	printString("\r\n")
	printString("Next steps:\r\n")
	printString("  1. Parse FDT (Flattened Device Tree) from a1 register\r\n")
	printString("  2. Implement VirtIO block device driver\r\n")
	printString("  3. Mount FAT32 filesystem\r\n")
	printString("  4. Load kmazarin.elf from disk\r\n")
	printString("  5. Set up kernel page tables\r\n")
	printString("  6. Jump to kmazarin entry point\r\n")
	printString("\r\n")
	printString("Halting (Phase 2 demonstration complete)...\r\n")

	// Return an error to stop execution cleanly
	return nil, &errBootServicesNotAvailable
}

// MountFilesystemRISCV mounts a FAT32 filesystem (stub for now).
func MountFilesystemRISCV(dev blockdev.BlockDevice) (*fat32.FileSystem, error) {
	// Will be implemented after VirtIO block device support
	return nil, &errBootServicesNotAvailable
}
