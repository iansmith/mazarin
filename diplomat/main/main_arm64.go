//go:build arm64

// diplomat/main/main_arm64.go - ARM64-specific declarations

package main

// elfMachineExpected is the ELF machine type diplomat expects when loading a kernel.
const elfMachineExpected = elfMachineARM64

// _efi_main_arm64 is the full UEFI entry point for ARM64
// Implemented in entry_arm64.s - sets up g0/m0 and calls DiplomatEntry
func _efi_main_arm64()

// _minimal_uefi_test_arm64 is a minimal test entry point for ARM64
// Implemented in entry_arm64.s
func _minimal_uefi_test_arm64()

// keepAlive keeps ARM64-specific entry points from being optimized away
// Called from main()
func keepAlive() {
	_efi_main_arm64()
	_minimal_uefi_test_arm64()
}
