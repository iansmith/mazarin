package ksyscall

import (
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
	// === PHASE 1: Validate all arguments ===

	// Validate filename pointer (must be in accessible memory)
	if err := ValidateFilenamePtr(filenamePtr); err != 0 {
		return int64(err)
	}

	// Validate priest syscall entry address (must be in executable segment)
	if err := ValidateExecAddr(priestSyscallAddr); err != 0 {
		return int64(err)
	}

	// Validate ProgramControl pointer (must be in writable data)
	if err := ValidateWritablePtr(programControlPtr); err != 0 {
		return int64(err)
	}

	// Read the filename string
	filename := readNullTerminatedString(uintptr(filenamePtr))
	if filename == "" {
		return int64(encodeError(majorBadArg, minorInvalidFilename))
	}

	// === PHASE 2: Load the ELF file from disk ===

	// Get block device
	blk, ok := device.GetBlockDevice()
	if !ok {
		return int64(encodeError(majorNotFound, minorFileNotFound))
	}

	// Mount FAT32 filesystem
	fs, err := fat32.Mount(blk)
	if err != nil {
		return int64(encodeError(majorNotFound, minorFileNotFound))
	}

	// Open the ELF file
	file, err := fs.Open(filename)
	if err != nil {
		return int64(encodeError(majorNotFound, minorFileNotFound))
	}
	defer file.Close()

	// Read entire file
	elfData, err := file.ReadAll()
	if err != nil {
		return int64(encodeError(majorNotFound, minorFileNotFound))
	}

	// === PHASE 3: Parse ELF header (validation only for now) ===

	if len(elfData) < 64 {
		return int64(encodeError(majorBadArg, minorInvalidELF))
	}

	// Parse and validate ELF header
	hdr := parseELFHeader(elfData)

	if hdr.Magic != ELF_MAGIC {
		return int64(encodeError(majorBadArg, minorInvalidELF))
	}

	if hdr.Class != ELF_CLASS64 || hdr.Machine != ELF_MACHINE_AARCH64 {
		return int64(encodeError(majorBadArg, minorWrongArch))
	}

	// === PHASE 4: TODO - Full implementation ===
	// For now, return error indicating this isn't fully implemented yet
	// The next phases will:
	// - Find available load address (different from priest)
	// - Load ELF segments at that address
	// - Process relocations for PIE
	// - Find main.MazarinMain symbol
	// - Patch PriestSyscallEntry in the loaded program
	// - Fill in ProgramControl struct

	// Suppress unused variable warnings
	_ = filename
	_ = priestSyscallAddr
	_ = programControlPtr
	_ = hdr

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
