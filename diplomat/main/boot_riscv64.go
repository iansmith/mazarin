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
