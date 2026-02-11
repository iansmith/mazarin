//go:build riscv64

// diplomat/main/main_riscv64.go - RISC-V 64-bit specific declarations

package main

// elfMachineExpected is the ELF machine type diplomat expects when loading a kernel.
const elfMachineExpected = elfMachineRISCV64

// kernelFilePath is the path to the kernel on the FAT32 disk.
// On RISC-V, files are in the root directory (no /EFI/Linux/ UEFI structure).
const kernelFilePath = "KMAZARIN.ELF"

// keepAlive is called from main() to prevent the linker from removing symbols.
// On RISC-V with OpenSBI boot, there are no UEFI entry points to keep alive.
func keepAlive() {
	// Nothing to keep alive on RISC-V
}
