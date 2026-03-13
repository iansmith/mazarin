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

// Active function pointer tables — initialized in DiplomatEntry
var plat PlatformOps
var boot BootSequence

// TLS block for storing g pointer (required by Go .abi0 wrappers)
// The .abi0 wrappers load g from %fs:-0x8, so we need:
//   - TLS block with g0 address at offset 0
//   - FS segment base set to tlsBlock address
var tlsBlock [256]byte

// uefiReturnAddr saves UEFI's return address so _efi_main_arm64 can
// call through a TOPFRAME wrapper and still return to UEFI.
var uefiReturnAddr uintptr

// main is required by Go linker but never executes
// The actual entry point is architecture-specific assembly.
// This function calls referenced functions to keep them from being optimized away
func main() {
	// This never executes - assembly entry point is the PE entry point
	// We call functions here to prevent dead code elimination
	DiplomatEntry()

	// Keep assembly functions alive
	ueficall_OutputString(nil, 0)

	// Keep architecture-specific entry points alive
	// (implemented in main_amd64.go / main_arm64.go)
	keepAlive()

	for {}
}

// DiplomatEntry is called by assembly efi_main() after g0/m0 initialization.
// The assembly entry point (entry_amd64.s) saves ImageHandle and SystemTable
// to globals, then calls this function.

// DiplomatEntry is called by assembly wrapper after g0/m0 initialization.
func DiplomatEntry() {
	// imageHandle and systemTable already set by assembly entry point

	// Initialize function pointer tables
	plat = defaultPlatform
	boot = defaultBootSequence

	printString("Diplomat UEFI Bootloader\r\n")

	// Get block device for boot partition
	blockDev, err := boot.GetBlockDevice()
	if err != nil {
		printString("ERROR: block device: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}

	// Mount FAT32 filesystem
	printString("Mounting FAT32...\r\n")
	// Platform-specific mount: ARM64/AMD64 use normal error-returning mount,
	// RISC-V uses allocation-free version to avoid error interfaces during early boot
	fs := mountFAT32OrDie(blockDev)
	printString("FAT32 mounted OK\r\n")

	// Load the kernel into physical memory
	kernel, err := boot.LoadKernel(fs, "/EFI/Linux/kmazarin.elf")
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

	// Phase 4: Read configuration
	config, err := boot.ReadConfig(fs)
	if err != nil {
		printString("ERROR: config: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}
	printString("Config: cpus=")
	printHex(config.MinCPUs)
	printString("-")
	printHex(config.MaxCPUs)
	printString(" ram=")
	printHex(config.MinRAMMB)
	printString("-")
	printHex(config.MaxRAMMB)
	printString("MB\r\n")

	// Phase 5: Query hardware
	hw, err := boot.QueryHardware(config)
	if err != nil {
		printString("ERROR: hardware: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}
	printString("Hardware: ")
	printHex(hw.CPUCount)
	printString(" CPUs, ")
	printHex(hw.RAMSize / (1024 * 1024))
	printString("MB RAM @ ")
	printHex(hw.RAMBase)
	printString("\r\n")

	// Phase 7: Prepare kernel VM (TTBR1, linear map, stacks, page pool)
	printString("Preparing kernel VM...\r\n")
	vm, err := boot.PrepareKernelVM(hw, kernel)
	if err != nil {
		printString("ERROR: kernelvm: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}

	// Phase 8: Install demand paging fault handler.
	// On ARM64, kmazarin's VBAR is set at jump time so this is a no-op.
	// On x86_64, we install an IDT with a #PF handler because kmazarin's Go
	// runtime generates page faults during init (before kmazarin can set its own IDT).
	if err := boot.InstallFaultHandler(vm); err != nil {
		printString("ERROR: fault handler: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}

	// Phase 9: Build startup environment (auxv on g0 stack)
	stackPtr, err := boot.BuildStartupEnv(vm, hw, kernel, config)
	if err != nil {
		printString("ERROR: startup env: ")
		printString(err.Error())
		printString("\r\n")
		for {
		}
	}

	// Look up the platform-specific exception handler symbol from the loaded kernel.
	// ARM64/AMD64: ExceptionVectorTable. RISC-V: trapEntry.
	// The relocator updates ELF header/code/data but NOT the symbol table,
	// so add the relocation delta (high 32 bits of LowestVirt) to get the real VA.
	relocDelta := kernel.LowestVirt - (kernel.LowestVirt & 0x00000000FFFFFFFF)
	excVecAddr := getExceptionHandlerForJump(kernel, relocDelta)

	// Update diplomat's IDT with kmazarin's real ISR entry points.
	// On x86_64: replaces diplomat's stub syscall handler (IDT[128]) with
	// kmazarin's isr128 so clone/futex/sched_yield go through real dispatch.
	// On ARM64/RISC-V: no-op.
	updateIDTWithKmazarinISRs(kernel, relocDelta)

	// Phase 10: Jump to kernel with proper stack setup and kmazarin's VBAR.
	// Kmazarin's exception handler handles:
	//   - Data aborts (demand paging for heap at 0xFFFF000100000000+)
	//   - SVC (clone for thread creation via SyscallClone)
	printString("Jumping to kmazarin...\r\n")
	boot.JumpToKernelWithEnv(kernel.Entry, stackPtr, vm.ExcStackTopVA, excVecAddr)

	// Does not return
	for {
	}
}

// findKernelSymbol looks up a symbol by name in the loaded kernel's symbol table.
// Returns the symbol's value (ELF VA) or 0 if not found.
func findKernelSymbol(kernel *LoadedKernel, name string) uint64 {
	for i := 0; i < kernel.NumSymbols; i++ {
		ks := &kernel.Symbols[i]
		match := true
		for j := 0; j < len(name); j++ {
			if j >= 64 || ks.Name[j] != name[j] {
				match = false
				break
			}
		}
		if match && (len(name) >= 64 || ks.Name[len(name)] == 0) {
			return ks.Value
		}
	}
	return 0
}

// printString outputs a string to the console (UEFI or direct UART)
func printString(s string) {
	// On RISC-V with -bios none, systemTable is nil but we can still
	// print via direct UART using plat.PrintChar
	if plat.PrintChar != nil {
		// Use platform-specific print (UEFI or direct UART)
		for _, r := range s {
			plat.PrintChar(uint16(r))
		}
		return
	}

	// Fallback: UEFI only (when plat not initialized yet)
	if systemTable == nil || systemTable.ConOut == nil {
		return
	}

	// Convert Go string to UCS-2 (UTF-16LE) and print via UEFI
	for _, r := range s {
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

// diplomatAllocator is the FAT32 allocator callback that uses our bump allocator
func diplomatAllocator(size uintptr) unsafe.Pointer {
	return dMalloc(size)
}

// Global FileSystem for RISC-V to avoid local allocation
var globalFS fat32.FileSystem

// Global buffer for boot sector read (to avoid touching FileSystem during read)
var bootSectorBuf [512]byte

// fat32Mount mounts FAT32 using diplomat's bump allocator (no Go runtime heap)
//
//go:noinline
func fat32Mount(dev blockdev.BlockDevice) (*fat32.FileSystem, error) {
	plat.DebugPortOut('Z') // entered fat32Mount
	return fat32.MountWith(dev, diplomatAllocator)
}

// fat32MountOrDie is a RISC-V-specific wrapper that avoids error interface returns.
// On RISC-V, error interface conversions trigger heap allocation which fails during
// early boot. This function inlines the mount logic and halts on error instead
// of returning an error interface.
//
//go:noinline
func fat32MountOrDie(dev blockdev.BlockDevice) *fat32.FileSystem {
	plat.DebugPortOut('1') // entered fat32MountOrDie

	// Zero the global FS
	globalFS = fat32.FileSystem{}
	plat.DebugPortOut('2') // zeroed global

	// Set allocator
	globalFS.SetAlloc(diplomatAllocator)
	plat.DebugPortOut('3') // set allocator

	// Initialize in place - we inline the init logic to avoid error interfaces
	// Check block size
	if dev.BlockSize() != 512 {
		printString("ERROR: Only 512-byte sectors supported\r\n")
		for {
		}
	}
	plat.DebugPortOut('4') // block size OK

	// Read boot sector into separate buffer (avoid touching FileSystem during read)
	plat.DebugPortOut('5') // about to read boot sector

	// Call ReadBlockVirtIONoError directly - avoid accessing plat struct!
	ReadBlockVirtIONoError(0, bootSectorBuf[:])
	plat.DebugPortOut('6') // read completed

	// Check boot signature
	if bootSectorBuf[510] != 0x55 || bootSectorBuf[511] != 0xAA {
		printString("ERROR: Invalid boot signature\r\n")
		for {
		}
	}
	plat.DebugPortOut('7') // signature OK

	// Copy boot sector to FileSystem buffer
	fat32.CopyBootSector(&globalFS, &bootSectorBuf)
	plat.DebugPortOut('8') // copied to FS

	// Set device
	globalFS.SetDevice(dev)
	plat.DebugPortOut('9') // device set

	// Parse and validate BPB using standalone function
	if !fat32.ParseBPBStandalone(&globalFS) {
		printString("ERROR: Invalid FAT32 BPB\r\n")
		for {
		}
	}
	plat.DebugPortOut('A') // BPB parsed

	plat.DebugPortOut('B') // returning
	return &globalFS
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
			// Use plat.PrintChar to support both UEFI and direct UART modes
			if plat.PrintChar != nil {
				plat.PrintChar(uint16(hexChars[digit]))
			}
			started = true
		}
	}
}
