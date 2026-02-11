//go:build riscv64

// diplomat/main/main_riscv64.go - RISC-V 64-bit specific declarations

package main

// Forward declarations of assembly entry points
// Using underscore prefix like AMD64 to prevent dead code elimination
func _diplomatBootstrap()  // Pure assembly bootstrap (no Go runtime, just UART loop)
func _diplomatRealEntry()  // Full entry with MMU setup (not used yet)

// elfMachineExpected is the ELF machine type diplomat expects when loading a kernel.
const elfMachineExpected = elfMachineRISCV64

// kernelFilePath is the path to the kernel on the FAT32 disk.
// On RISC-V, files are in the root directory (no /EFI/Linux/ UEFI structure).
const kernelFilePath = "KMAZARIN.ELF"

// keepAlive is called from main() to prevent the linker from removing symbols.
// These functions are never actually executed (main() never runs), but must exist
// in the binary for fix-go-elf to patch the entry point.
func keepAlive() {
	_diplomatBootstrap()
	_diplomatRealEntry()
}
