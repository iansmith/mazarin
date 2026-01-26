// diplomat/main/main.go - Minimal UEFI bootloader entry point
package main

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
	Hdr                  EFI_TABLE_HEADER
	FirmwareVendor       *uint16
	FirmwareRevision     uint32
	ConsoleInHandle      EFI_HANDLE
	ConIn                uintptr
	ConsoleOutHandle     EFI_HANDLE
	ConOut               *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL
	StandardErrorHandle  EFI_HANDLE
	StdErr               *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL
	RuntimeServices      uintptr
	BootServices         *EFI_BOOT_SERVICES
	NumberOfTableEntries uintptr
	ConfigurationTable   uintptr
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

// Global system table - set by efi_main
var systemTable *EFI_SYSTEM_TABLE
var imageHandle EFI_HANDLE

// main is required by the Go runtime
// For Windows PE/UEFI, the Go runtime doesn't actually call this
// The UEFI firmware calls efi_main directly
func main() {
	// This should never be reached in a UEFI environment
	// UEFI firmware calls efi_main directly
}

// efi_main is the entry point called by UEFI firmware
// It receives the image handle and system table
//
//go:export efi_main
func efi_main(imgHandle EFI_HANDLE, st *EFI_SYSTEM_TABLE) EFI_STATUS {
	imageHandle = imgHandle
	systemTable = st

	// Initialize memory span tracking for mmap
	// This must happen before Go runtime tries to allocate heap
	if !InitializeSpans() {
		printString("FATAL: Failed to initialize memory spans\r\n")
		for {}
	}

	// Print hello message via UEFI ConOut
	printString("Diplomat UEFI Bootloader\r\n")
	printString("Memory spans initialized\r\n")
	printString("Phase 1: Go Runtime + mmap working\r\n")
	printString("\r\n")

	// Test that we can allocate
	printString("Testing Go heap allocation...\r\n")

	// Try to allocate a simple slice - this will test mmap
	testSlice := make([]byte, 1024)
	if len(testSlice) == 1024 {
		printString("Heap allocation successful!\r\n")
	}

	printString("\r\n")
	printString("Next: Implement FAT32/ELF loading\r\n")

	// Hang (for now)
	// Next phases: FAT32 filesystem, ELF loading, kmazarin launch
	for {
	}

	return EFI_SUCCESS
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
func ueficall_OutputString(conout *EFI_SIMPLE_TEXT_OUTPUT_PROTOCOL, char uint16)
