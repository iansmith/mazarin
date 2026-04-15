//go:build amd64

// diplomat/main/main_amd64.go - x86_64-specific declarations

package main

// elfMachineExpected is the ELF machine type diplomat expects when loading a kernel.
const elfMachineExpected = elfMachineX64

// kernelFilePath is the path to the kernel on the FAT32 boot disk.
const kernelFilePath = "/EFI/Linux/kmazarin.elf"

// _efi_main_asm is the full UEFI entry point with Go runtime initialization
// Implemented in entry_amd64.s - sets up g0/m0 and calls DiplomatEntry
func _efi_main_asm()

// keepAlive keeps x86_64-specific entry points from being optimized away
// Called from main()
func keepAlive() {
	_efi_main_asm()
}
