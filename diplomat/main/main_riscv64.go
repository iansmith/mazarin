//go:build riscv64

// diplomat/main/main_riscv64.go - RISC-V 64-bit specific declarations

package main

// elfMachineExpected is the ELF machine type diplomat expects when loading a kernel.
const elfMachineExpected = elfMachineRISCV64

// _efi_main_riscv64 is the full UEFI entry point for RISC-V 64-bit
// Implemented in entry_riscv64.s - sets up g0/m0 and calls DiplomatEntry
func _efi_main_riscv64()

// _minimal_uefi_test_riscv64 is a minimal test entry point for RISC-V 64-bit
// Implemented in entry_riscv64.s
func _minimal_uefi_test_riscv64()

// keepAlive keeps RISC-V-specific entry points from being optimized away
// Called from main()
func keepAlive() {
	_efi_main_riscv64()
	_minimal_uefi_test_riscv64()
}
