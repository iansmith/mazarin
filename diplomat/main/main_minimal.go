// +build ignore
// This is a minimal diplomat implementation for Phase 0 toolchain validation
// It avoids the Go runtime entirely to eliminate kernel32.dll dependencies

package main

// Disable the Go runtime
//go:build windows
// +build windows

// Tell the linker we don't want the runtime
//go:linkname _rt0_amd64_windows_lib _rt0_amd64_windows_lib

// EFI Basic Types
type EFI_HANDLE uintptr
type EFI_STATUS uintptr
type CHAR16 uint16

// EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL
type EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL struct {
	Reset             uintptr
	OutputString      uintptr
	TestString        uintptr
	QueryMode         uintptr
	SetMode           uintptr
	SetAttribute      uintptr
	ClearScreen       uintptr
	SetCursorPosition uintptr
	EnableCursor      uintptr
	Mode              uintptr
}

// EFI_SYSTEM_TABLE
type EFI_SYSTEM_TABLE struct {
	Hdr                  [5]uintptr // Simplified header
	FirmwareVendor       *CHAR16
	FirmwareRevision     uint32
	_                    uint32 // Padding
	ConsoleInHandle      EFI_HANDLE
	ConIn                uintptr
	ConsoleOutHandle     EFI_HANDLE
	ConOut               *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL
	// ... rest omitted for Phase 0
}

var ST *EFI_SYSTEM_TABLE

// efi_main is the UEFI entry point
//
//go:export efi_main
//go:nosplit
//go:nowritebarrier
func efi_main(imageHandle EFI_HANDLE, systemTable *EFI_SYSTEM_TABLE) EFI_STATUS {
	ST = systemTable

	// Print message
	msg := []CHAR16{'H', 'e', 'l', 'l', 'o', ' ', 'f', 'r', 'o', 'm', ' ', 'D', 'i', 'p', 'l', 'o', 'm', 'a', 't', '!', '\r', '\n', 0}
	outputString(ST.ConOut, &msg[0])

	// Hang
	for {
	}

	return 0
}

// outputString calls UEFI OutputString
//
//go:nosplit
//go:nowritebarrier
func outputString(conout *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL, str *CHAR16)

// main is required but never called in UEFI
func main() {}
