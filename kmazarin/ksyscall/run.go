package ksyscall

import (
	"mazzy/kmazarin/device"
	"mazzy/shared/fs/fat32"
)

// SyscallBootstrapRunElf loads an ELF from disk into the calling shepherd's
// address space. This is a bootstrap-only syscall used to get the disk
// manager off the disk; once the disk manager is running, programs are
// loaded through it instead.
//
// arg0: Pointer to null-terminated filename string
// arg1: Address of shepherd's ShepherdSyscallEntry function
// arg2: Pointer to ProgramControl struct (in shepherd's writable memory)
//
// Returns: 0 on success, ErrorCode on failure
func SyscallBootstrapRunElf(filenamePtr, shepherdSyscallAddr, programControlPtr, _, _, _ uint64) int64 {
	// === PHASE 1: Validate all arguments ===

	// Validate filename pointer (must be in accessible memory)
	if err := ValidateFilenamePtr(filenamePtr); err != 0 {
		return int64(err)
	}

	// Validate shepherd syscall entry address (must be in executable segment)
	if err := ValidateExecAddr(shepherdSyscallAddr); err != 0 {
		return int64(err)
	}

	// Validate ProgramControl pointer (must be in writable data)
	if err := ValidateWritablePtr(programControlPtr); err != 0 {
		return int64(err)
	}

	// Read the filename string
	filename := readNullTerminatedString(uintptr(filenamePtr))
	if filename == "" {
		return int64(errInvalidFilename)
	}

	// === PHASE 2: Load the ELF file from disk ===

	// Get block device
	blk, ok := device.GetBlockDevice()
	if !ok {
		return int64(errFileNotFound)
	}

	// Mount FAT32 filesystem
	fs, err := fat32.Mount(blk)
	if err != nil {
		return int64(errFileNotFound)
	}

	// Open the ELF file
	file, err := fs.Open(filename)
	if err != nil {
		return int64(errFileNotFound)
	}
	defer file.Close()

	// Read entire file
	elfData, err := file.ReadAll()
	if err != nil {
		return int64(errFileNotFound)
	}

	// === PHASE 3: Parse ELF header (validation only for now) ===

	if len(elfData) < 64 {
		return int64(errInvalidELF)
	}

	// Parse and validate ELF header
	hdr := parseELFHeader(elfData)

	if hdr.Magic != ELF_MAGIC {
		return int64(errInvalidELF)
	}

	if hdr.Class != ELF_CLASS64 || hdr.Machine != elfExpectedMachine {
		return int64(errWrongArch)
	}

	// === PHASE 4: TODO - Full implementation ===
	// For now, return error indicating this isn't fully implemented yet
	// The next phases will:
	// - Find available load address (different from shepherd)
	// - Load ELF segments at that address
	// - Process relocations for PIE
	// - Find main.MazarinMain symbol
	// - Patch ShepherdSyscallEntry in the loaded program
	// - Fill in ProgramControl struct

	// Suppress unused variable warnings
	_ = filename
	_ = shepherdSyscallAddr
	_ = programControlPtr
	_ = hdr

	return int64(errFileNotFound)
}

// Mazzy ErrorCode values — must stay in sync with mazarin/error/codes.go.
// These are returned directly as syscall results (not negative errno).
const (
	errNotWritableData     uint32 = 0x1000
	errNotInExec           uint32 = 0x1001
	errNotAccessibleMemory uint32 = 0x1002
	errNullPointer         uint32 = 0x1003
	errInvalidFilename     uint32 = 0x1004
	errTooLarge            uint32 = 0x1005
	errTooSmall            uint32 = 0x1006
	errInvalidELF          uint32 = 0x1007
	errWrongArch           uint32 = 0x1008
	errNoSymbol            uint32 = 0x1009
	errAlreadyLoaded       uint32 = 0x100A
	errNoSpace             uint32 = 0x100B
	errFileNotFound        uint32 = 0x100C
	errNoDelegate          uint32 = 0x100D
	errNotReady            uint32 = 0x100E
	errTransferFailed      uint32 = 0x100F
)
