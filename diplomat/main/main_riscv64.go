//go:build riscv64

// diplomat/main/main_riscv64.go - RISC-V 64-bit specific declarations

package main

// Forward declarations of assembly entry points
// Using underscore prefix like AMD64 to prevent dead code elimination
func _diplomatBootstrap()     // Pure assembly bootstrap (no Go runtime, just UART loop)
func diplomatEntry()           // Full entry with MMU setup (in entry_riscv64.s)
func diplomatEntryWrapper()    // Wrapper that writes debug marker and calls DiplomatEntry

// elfMachineExpected is the ELF machine type diplomat expects when loading a kernel.
const elfMachineExpected = elfMachineRISCV64

// kernelFilePath is the path to the kernel on the FAT32 disk.
// On RISC-V, files are in the root directory (no /EFI/Linux/ UEFI structure).
const kernelFilePath = "KMAZARIN.ELF"

// Early boot parameters passed by firmware
var bootHartID uint64  // Hart (hardware thread) ID from a0
var fdtPointer uint64  // FDT (Flattened Device Tree) pointer from a1

// bootStack provides a small stack for early initialization (64KB)
// Stack grows down, so entry point sets SP to bootStack + 65536
var bootStack [65536]byte

// keepAlive is called from main() to prevent the linker from removing symbols.
// These functions are never actually executed (main() never runs), but must exist
// in the binary for fix-go-elf to patch the entry point.
func keepAlive() {
	_diplomatBootstrap()
	diplomatEntry()
	diplomatEntryWrapper()
}

// uartWriteDirect writes a single byte to UART using inline assembly
// This is RISC-V specific and avoids any Go runtime overhead
//
//go:nosplit
//go:noinline
func uartWriteDirect(c byte)
