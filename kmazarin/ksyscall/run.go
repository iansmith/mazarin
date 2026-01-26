package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/shared/fs/fat32"
)

// SyscallRun loads a .maz program into the calling priest's address space.
// This is called by priest via SVC after receiving a syscall from a program.
//
// arg0: Pointer to null-terminated filename string
// arg1: Address of priest's PriestSyscallEntry function
// arg2: Pointer to ProgramControl struct (in priest's writable memory)
//
// Returns: 0 on success, encoded error on failure
func SyscallRun(filenamePtr, priestSyscallAddr, programControlPtr, _, _, _ uint64) int64 {
	console.KWriteString("\r\n[SysRun] Starting...\r\n")

	// === PHASE 1: Validate all arguments ===

	// Validate filename pointer (must be in accessible memory)
	if err := ValidateFilenamePtr(filenamePtr); err != 0 {
		console.KWriteString("[SysRun] ERROR: Invalid filename pointer\r\n")
		return int64(err)
	}

	// Validate priest syscall entry address (must be in executable segment)
	if err := ValidateExecAddr(priestSyscallAddr); err != 0 {
		console.KWriteString("[SysRun] ERROR: PriestSyscallEntry not in exec segment\r\n")
		return int64(err)
	}

	// Validate ProgramControl pointer (must be in writable data)
	if err := ValidateWritablePtr(programControlPtr); err != 0 {
		console.KWriteString("[SysRun] ERROR: ProgramControl not in writable memory\r\n")
		return int64(err)
	}

	// Read the filename string
	filename := readNullTerminatedString(uintptr(filenamePtr))
	if filename == "" {
		console.KWriteString("[SysRun] ERROR: Empty filename\r\n")
		return int64(encodeError(majorBadArg, minorInvalidFilename))
	}

	console.KWriteString("[SysRun] Loading: ")
	console.KWriteString(filename)
	console.KWriteString("\r\n")

	console.KWriteString("[SysRun] PriestSyscallEntry at: ")
	console.KPrintHex64(priestSyscallAddr)
	console.KWriteString("\r\n")

	console.KWriteString("[SysRun] ProgramControl at: ")
	console.KPrintHex64(programControlPtr)
	console.KWriteString("\r\n")

	// === PHASE 2: Load the ELF file from disk ===

	// Get block device
	blk, ok := device.GetBlockDevice()
	if !ok {
		console.KWriteString("[SysRun] ERROR: No block device found\r\n")
		return int64(encodeError(majorNotFound, minorFileNotFound))
	}

	// Mount FAT32 filesystem
	fs, err := fat32.Mount(blk)
	if err != nil {
		console.KWriteString("[SysRun] ERROR: Failed to mount filesystem\r\n")
		return int64(encodeError(majorNotFound, minorFileNotFound))
	}

	// Open the ELF file
	file, err := fs.Open(filename)
	if err != nil {
		console.KWriteString("[SysRun] ERROR: File not found: ")
		console.KWriteString(filename)
		console.KWriteString("\r\n")
		return int64(encodeError(majorNotFound, minorFileNotFound))
	}
	defer file.Close()

	// Read entire file
	elfData, err := file.ReadAll()
	if err != nil {
		console.KWriteString("[SysRun] ERROR: Failed to read file\r\n")
		return int64(encodeError(majorNotFound, minorFileNotFound))
	}

	console.KWriteString("[SysRun] Read ")
	console.KPrintHex64(uint64(len(elfData)))
	console.KWriteString(" bytes\r\n")

	// === PHASE 3: Parse ELF header (validation only for now) ===

	if len(elfData) < 64 {
		console.KWriteString("[SysRun] ERROR: File too small for ELF header\r\n")
		return int64(encodeError(majorBadArg, minorInvalidELF))
	}

	// Parse and validate ELF header
	hdr := parseELFHeader(elfData)

	if hdr.Magic != ELF_MAGIC {
		console.KWriteString("[SysRun] ERROR: Invalid ELF magic\r\n")
		return int64(encodeError(majorBadArg, minorInvalidELF))
	}

	if hdr.Class != ELF_CLASS64 || hdr.Machine != ELF_MACHINE_AARCH64 {
		console.KWriteString("[SysRun] ERROR: Not an ARM64 ELF\r\n")
		return int64(encodeError(majorBadArg, minorWrongArch))
	}

	console.KWriteString("[SysRun] Valid ARM64 ELF64\r\n")
	console.KWriteString("[SysRun] Entry point: ")
	console.KPrintHex64(hdr.Entry)
	console.KWriteString("\r\n")

	// === PHASE 4: TODO - Full implementation ===
	// For now, return error indicating this isn't fully implemented yet
	// The next phases will:
	// - Find available load address (different from priest)
	// - Load ELF segments at that address
	// - Process relocations for PIE
	// - Find main.MazarinMain symbol
	// - Patch PriestSyscallEntry in the loaded program
	// - Fill in ProgramControl struct

	console.KWriteString("[SysRun] TODO: Full loading not yet implemented\r\n")
	console.KWriteString("[SysRun] Returning NotFound:FileNotFound as placeholder\r\n")

	return int64(encodeError(majorNotFound, minorFileNotFound))
}

// Error encoding helpers (mirror client-side codes from mazarin/error)
const (
	majorBadArg   uint32 = 0x00010000
	majorNotFound uint32 = 0x00020000
	majorNoMem    uint32 = 0x00040000

	minorNone              uint32 = 0
	minorNotWritableData   uint32 = 1
	minorNotInExec         uint32 = 2
	minorNotAccessibleMem  uint32 = 3
	minorInvalidFilename   uint32 = 5
	minorInvalidELF        uint32 = 8
	minorWrongArch         uint32 = 9
	minorNoSymbol          uint32 = 10
	minorNoSpace           uint32 = 12
	minorFileNotFound      uint32 = 13
)

//go:nosplit
func encodeError(major, minor uint32) uint64 {
	return (uint64(major) << 32) | uint64(minor)
}
