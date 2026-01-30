// diplomat/main/main.go - Minimal UEFI bootloader entry point
package main

import (
	"mazzy/shared/blockdev"
	"mazzy/shared/fs/fat32"
	"unsafe"
)

// EFI Basic Types
type EFI_HANDLE uintptr
type EFI_STATUS uintptr

// EFI Status Codes
const (
	EFI_SUCCESS               EFI_STATUS = 0
	EFI_LOAD_ERROR            EFI_STATUS = 1
	EFI_INVALID_PARAMETER     EFI_STATUS = 2
	EFI_UNSUPPORTED           EFI_STATUS = 3
	EFI_BAD_BUFFER_SIZE       EFI_STATUS = 4
	EFI_BUFFER_TOO_SMALL      EFI_STATUS = 5
	EFI_NOT_READY             EFI_STATUS = 6
	EFI_DEVICE_ERROR          EFI_STATUS = 7
	EFI_WRITE_PROTECTED       EFI_STATUS = 8
	EFI_OUT_OF_RESOURCES      EFI_STATUS = 9
	EFI_VOLUME_CORRUPTED      EFI_STATUS = 10
	EFI_VOLUME_FULL           EFI_STATUS = 11
	EFI_NO_MEDIA              EFI_STATUS = 12
	EFI_MEDIA_CHANGED         EFI_STATUS = 13
	EFI_NOT_FOUND             EFI_STATUS = 14
	EFI_ACCESS_DENIED         EFI_STATUS = 15
	EFI_NO_RESPONSE           EFI_STATUS = 16
	EFI_NO_MAPPING            EFI_STATUS = 17
	EFI_TIMEOUT               EFI_STATUS = 18
	EFI_NOT_STARTED           EFI_STATUS = 19
	EFI_ALREADY_STARTED       EFI_STATUS = 20
	EFI_ABORTED               EFI_STATUS = 21
	EFI_PROTOCOL_ERROR        EFI_STATUS = 24
	EFI_INCOMPATIBLE_VERSION  EFI_STATUS = 25
	EFI_SECURITY_VIOLATION    EFI_STATUS = 26
	EFI_CRC_ERROR             EFI_STATUS = 27
	EFI_END_OF_MEDIA          EFI_STATUS = 28
	EFI_END_OF_FILE           EFI_STATUS = 31
	EFI_INVALID_LANGUAGE      EFI_STATUS = 32
	EFI_COMPROMISED_DATA      EFI_STATUS = 33
	EFI_WARN_UNKNOWN_GLYPH    EFI_STATUS = 1
	EFI_WARN_DELETE_FAILURE   EFI_STATUS = 2
	EFI_WARN_WRITE_FAILURE    EFI_STATUS = 3
	EFI_WARN_BUFFER_TOO_SMALL EFI_STATUS = 4
	EFI_WARN_STALE_DATA       EFI_STATUS = 5
	EFI_WARN_FILE_SYSTEM      EFI_STATUS = 6
)

// EFI_TABLE_HEADER - Common header for EFI tables
type EFI_TABLE_HEADER struct {
	Signature  uint64
	Revision   uint32
	HeaderSize uint32
	CRC32      uint32
	Reserved   uint32
}

// EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL - Console output protocol
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

// EFI_BOOT_SERVICES - Boot time services
type EFI_BOOT_SERVICES struct {
	Hdr EFI_TABLE_HEADER

	// Task Priority Services (4 functions)
	RaiseTPL  uintptr
	RestoreTPL uintptr

	// Memory Services (9 functions)
	AllocatePages   uintptr
	FreePages       uintptr
	GetMemoryMap    uintptr
	AllocatePool    uintptr
	FreePool        uintptr

	// Event & Timer Services (6 functions)
	CreateEvent          uintptr
	SetTimer             uintptr
	WaitForEvent         uintptr
	SignalEvent          uintptr
	CloseEvent           uintptr
	CheckEvent           uintptr

	// Protocol Handler Services (9 functions)
	InstallProtocolInterface    uintptr
	ReinstallProtocolInterface  uintptr
	UninstallProtocolInterface  uintptr
	HandleProtocol              uintptr
	Reserved                    uintptr
	RegisterProtocolNotify      uintptr
	LocateHandle                uintptr
	LocateDevicePath            uintptr
	InstallConfigurationTable   uintptr

	// Image Services (5 functions)
	LoadImage                   uintptr
	StartImage                  uintptr
	Exit                        uintptr
	UnloadImage                 uintptr
	ExitBootServices            uintptr

	// Miscellaneous Services (10 functions)
	GetNextMonotonicCount       uintptr
	Stall                       uintptr
	SetWatchdogTimer            uintptr

	// DriverSupport Services (3 functions)
	ConnectController           uintptr
	DisconnectController        uintptr

	// Open and Close Protocol Services (5 functions)
	OpenProtocol                uintptr
	CloseProtocol               uintptr
	OpenProtocolInformation     uintptr

	// Library Services (4 functions)
	ProtocolsPerHandle          uintptr
	LocateHandleBuffer          uintptr
	LocateProtocol              uintptr
	InstallMultipleProtocolInterfaces   uintptr
	UninstallMultipleProtocolInterfaces uintptr

	// 32-bit CRC Services
	CalculateCrc32              uintptr

	// Miscellaneous Services (continuation)
	CopyMem                     uintptr
	SetMem                      uintptr
	CreateEventEx               uintptr
}

// EFI_SYSTEM_TABLE - Main system table passed to bootloader
type EFI_SYSTEM_TABLE struct {
	Hdr                  EFI_TABLE_HEADER  // +0 (24 bytes)
	FirmwareVendor       *uint16            // +24 (8 bytes)
	FirmwareRevision     uint32             // +32 (4 bytes)
	_                    uint32             // +36 (4 bytes padding for alignment)
	ConsoleInHandle      EFI_HANDLE         // +40 (8 bytes)
	ConIn                uintptr            // +48 (8 bytes)
	ConsoleOutHandle     EFI_HANDLE         // +56 (8 bytes)
	ConOut               *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL  // +64 (8 bytes)
	StandardErrorHandle  EFI_HANDLE         // +72 (8 bytes)
	StdErr               *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL  // +80 (8 bytes)
	RuntimeServices      uintptr            // +88 (8 bytes)
	BootServices         *EFI_BOOT_SERVICES // +96 (8 bytes)
	NumberOfTableEntries uintptr            // +104 (8 bytes)
	ConfigurationTable   uintptr            // +112 (8 bytes)
}

// EFI Memory Types
const (
	EfiReservedMemoryType      = 0
	EfiLoaderCode              = 1
	EfiLoaderData              = 2
	EfiBootServicesCode        = 3
	EfiBootServicesData        = 4
	EfiRuntimeServicesCode     = 5
	EfiRuntimeServicesData     = 6
	EfiConventionalMemory      = 7
	EfiUnusableMemory          = 8
	EfiACPIReclaimMemory       = 9
	EfiACPIMemoryNVS           = 10
	EfiMemoryMappedIO          = 11
	EfiMemoryMappedIOPortSpace = 12
	EfiPalCode                 = 13
	EfiPersistentMemory        = 14
	EfiMaxMemoryType           = 15
)

// EFI Allocate Type
const (
	AllocateAnyPages   = 0
	AllocateMaxAddress = 1
	AllocateAddress    = 2
	MaxAllocateType    = 3
)

// Global system table - set by assembly entry point (efi_main in entry_amd64.s)
var systemTable *EFI_SYSTEM_TABLE
var imageHandle EFI_HANDLE

// TLS block for storing g pointer (required by Go .abi0 wrappers)
// The .abi0 wrappers load g from %fs:-0x8, so we need:
//   - TLS block with g0 address at offset 0
//   - FS segment base set to tlsBlock address
var tlsBlock [256]byte

// executionMarker is set by assembly entry point to prove code executed
// Check from QEMU monitor: x/1gx &main.executionMarker
var executionMarker uint64

// main is required by Go linker but never executes
// The actual entry point is _minimal_uefi_test in minimal_test_amd64.s
// This function calls referenced functions to keep them from being optimized away
func main() {
	// This never executes - _minimal_uefi_test is the PE entry point
	// We call functions here to prevent dead code elimination
	DiplomatEntry()

	// Keep assembly functions alive
	ueficall_OutputString(nil, 0)
	_minimal_uefi_test() // Keep the minimal test entry point symbol alive
	_efi_main_asm()      // Keep the full entry point symbol alive

	for {}
}

// DiplomatEntry is called by assembly efi_main() after g0/m0 initialization.
// The assembly entry point (entry_amd64.s) saves ImageHandle and SystemTable
// to globals, then calls this function.
//
//go:nosplit
func DiplomatEntry() {
	// imageHandle and systemTable already set by assembly entry point

	printString("Diplomat UEFI Bootloader\r\n")
	printString("DBG: before InitializeSpans\r\n")

	// Initialize memory span tracking for mmap
	if !InitializeSpans() {
		printString("FATAL: Failed to initialize memory spans\r\n")
		for {
		}
	}

	printString("DBG: spans OK\r\n")

	// Get block device for boot partition
	debugPortOut('G')
	blockDev, err := GetBootDeviceBlockIO()
	debugPortOut('R')
	if err != nil {
		printString("ERROR: block device: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}

	// Mount FAT32 filesystem
	debugPortOut('M')
	printString("Mounting FAT32...\r\n")
	fs, err := fat32Mount(blockDev)
	debugPortOut('N')
	if err != nil {
		printString("ERROR: FAT32 mount: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}
	debugPortOut('O')
	printString("FAT32 mounted OK\r\n")

	// Load the kernel into physical memory
	debugPortOut('L')
	kernel, err := LoadKernel(fs, "/EFI/Linux/kmazarin.elf")
	if err != nil {
		printString("ERROR: kernel load: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}

	printString("Kernel loaded: virt=")
	printHex(kernel.LowestVirt)
	printString("-")
	printHex(kernel.HighestVirt)
	printString(" phys=")
	printHex(kernel.PhysBase)
	printString(" entry=")
	printHex(kernel.Entry)
	printString("\r\n")

	// Add kernel mapping to UEFI's existing page tables.
	// Building separate page tables fails because UEFI corrupts them.
	// Instead, read the current CR3 and graft our kernel mapping into
	// UEFI's page table hierarchy.
	printString("Adding kernel mapping to UEFI page tables...\r\n")
	err = addKernelMappingToCurrentPT(kernel.LowestVirt, kernel.PhysBase, DefaultKernelMemSize)
	if err != nil {
		printString("ERROR: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}
	printString("Kernel mapped, jumping...\r\n")
	// Jump using the current CR3 — just jump directly, no CR3 switch needed
	JumpToKernel(kernel.Entry)

	// Does not return
	for {
	}
}

// printString outputs a string to the UEFI console
func printString(s string) {
	if systemTable == nil || systemTable.ConOut == nil {
		return
	}

	// Convert Go string to UCS-2 (UTF-16LE) and print via UEFI
	for _, r := range s {
		// For ASCII, direct conversion works
		// For full Unicode support, we'd need proper UTF-16 encoding
		printChar(uint16(r))
	}
}

// printChar outputs a single character via UEFI OutputString
//
//go:nosplit
func printChar(c uint16) {
	if systemTable == nil || systemTable.ConOut == nil {
		return
	}

	// Call UEFI OutputString with a single-character string
	// The assembly helper creates a UCS-2 string on the stack
	ueficall_OutputString(systemTable.ConOut, c)
}

// ueficall_OutputString is implemented in assembly (uefi_calls.s)
// It calls the UEFI OutputString function using the MS x64 calling convention
//
//go:noescape
func ueficall_OutputString(conout *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL, char uint16)

// debugPortOut writes a byte to QEMU debug port 0xE9 (implemented in assembly)
func debugPortOut(c byte)

// setupTLS is implemented in assembly (tls_amd64.s)
// It sets the FS segment base for TLS support
func setupTLS(tlsAddr uintptr)

// _minimal_uefi_test is a pure assembly entry point (no .abi0 wrapper needed)
// We declare it here so Go doesn't eliminate it as dead code
// The declaration has no body - it's implemented in minimal_test_amd64.s
func _minimal_uefi_test()

// _efi_main_asm is the full UEFI entry point with Go runtime initialization
// Implemented in entry_amd64.s - sets up g0/m0 and calls DiplomatEntry
func _efi_main_asm()

// diplomatAllocator is the FAT32 allocator callback that uses our bump allocator
func diplomatAllocator(size uintptr) unsafe.Pointer {
	return dMalloc(size)
}

// fat32Mount mounts FAT32 using diplomat's bump allocator (no Go runtime heap)
//
//go:noinline
func fat32Mount(dev blockdev.BlockDevice) (*fat32.FileSystem, error) {
	return fat32.MountWith(dev, diplomatAllocator)
}

// printHex prints a uint64 as hex
func printHex(val uint64) {
	hexChars := "0123456789ABCDEF"
	printString("0x")
	// Print at least one digit
	started := false
	for i := 60; i >= 0; i -= 4 {
		digit := (val >> uint(i)) & 0xF
		if digit != 0 || started || i == 0 {
			printChar(uint16(hexChars[digit]))
			started = true
		}
	}
}
