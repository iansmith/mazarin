//go:build amd64

// diplomat/main/main_amd64.go - x86_64-specific declarations

package main

// elfMachineExpected is the ELF machine type diplomat expects when loading a kernel.
const elfMachineExpected = elfMachineX64

// _minimal_uefi_test is a pure assembly entry point (no .abi0 wrapper needed)
// We declare it here so Go doesn't eliminate it as dead code
// The declaration has no body - it's implemented in minimal_test_amd64.s
func _minimal_uefi_test()

// _efi_main_asm is the full UEFI entry point with Go runtime initialization
// Implemented in entry_amd64.s - sets up g0/m0 and calls DiplomatEntry
func _efi_main_asm()

// keepAlive keeps x86_64-specific entry points from being optimized away
// Called from main()
func keepAlive() {
	_minimal_uefi_test()
	_efi_main_asm()
}
