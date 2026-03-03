// loadmaz.go - SysLoadMaz syscall: load a .maz PIE ELF into a priest's address space.
//
// .maz programs are dynamically linked at load time: their thin runtime stubs
// are patched to call the host priest's real functions. This allows .maz binaries
// to be tiny (~100KB) while sharing the priest's full Go runtime.
//
// The heavy work (FAT32 I/O, ELF segment loading, PIE relocations) is offloaded
// to a kernel worker goroutine because the SVC handler runs on g0/SP_EL1 where
// the Go stack cannot grow. See loadmaz_bridge.go for the worker protocol.
package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/fs/fat32"
	"unsafe"
)

// MazLoadResult is the struct written back to the priest upon successful load.
// Layout must match mazarin/sys/loadmaz.go exactly.
type MazLoadResult struct {
	EntryPoint uint64 // Address of entry point (main.MazarinMain or main) in loaded .maz
	LoadBase   uint64 // Base VA where .maz was loaded
	LoadSize   uint64 // Total VA size of loaded segments
}

// LoadMazWorkRequest contains the parameters for a .maz load operation.
// Stored in the global LoadMazReq by SyscallLoadMaz, then read by the
// kernel worker goroutine.
type LoadMazWorkRequest struct {
	Filename   string
	ResultPtr  uint64
	Priest     *proc.Priest
	L0PA       uintptr
	BlockedTID int32 // Set by BlockForLoadMaz in main package
}

// LoadMazReq is the global request struct shared between the SVC handler
// (which writes it) and the kernel worker goroutine (which reads it).
// Only one LoadMaz request can be in flight at a time.
var LoadMazReq LoadMazWorkRequest

// SyscallLoadMaz loads a .maz PIE ELF into the calling priest's address space.
// This is a thin entry point that validates arguments, stores the request, and
// blocks the calling thread. The actual loading is done by the kernel worker
// goroutine (via DoLoadMazWork) which has a growable stack.
//
// arg0: pointer to null-terminated filename string (in priest's memory)
// arg1: pointer to MazLoadResult struct (in priest's writable memory)
// Returns: 0 on success, ErrorCode on failure
//
//go:noinline
func SyscallLoadMaz(filenamePtr, resultPtr, _, _, _, _ uint64) int64 {
	// === Validate arguments (lightweight, safe for g0 stack) ===
	if err := ValidateFilenamePtr(filenamePtr); err != 0 {
		return int64(err)
	}
	if err := ValidateWritablePtr(resultPtr); err != 0 {
		return int64(err)
	}

	filename := readNullTerminatedString(uintptr(filenamePtr))
	if filename == "" {
		return int64(errInvalidFilename)
	}

	console.KWriteString("[LoadMaz] ")
	console.KWriteString(filename)
	console.KWriteString("\r\n")

	// Find the calling priest
	priest := proc.CurrentPriest()
	if priest == nil {
		console.KWriteString("[LoadMaz] ERROR: not called from a priest\r\n")
		return int64(errNullPointer)
	}

	if priest.SymbolTable == nil {
		console.KWriteString("[LoadMaz] ERROR: priest has no symbol table\r\n")
		return int64(errNoSymbol)
	}

	// Store the request for the worker goroutine.
	LoadMazReq = LoadMazWorkRequest{
		Filename:  filename,
		ResultPtr: resultPtr,
		Priest:    priest,
		L0PA:      priest.PageTableL0PA,
	}

	// Block this thread and notify the worker goroutine.
	// BlockForLoadMaz sets LoadMazReq.BlockedTID and the loadMazPending atomic.
	ctxPtr := blockForLoadMaz()
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
	} else {
		// No other thread to switch to — cannot offload work.
		// This should be rare (other priests should be running).
		console.KWriteString("[LoadMaz] ERROR: no thread to switch to\r\n")
		return int64(errNoSpace)
	}

	// Return value doesn't matter — the worker will overwrite ThreadContext.X[0]
	// via SetReturnValue before waking this thread.
	return 0
}

// DoLoadMazWork performs the heavy .maz loading work. Called by the kernel
// worker goroutine (in loadmaz_bridge.go) on a normal growable stack.
func DoLoadMazWork(req *LoadMazWorkRequest) int64 {
	l0PA := req.L0PA
	priest := req.Priest

	// === Read the .maz file from disk ===
	blk, ok := device.GetBlockDevice()
	if !ok {
		console.KWriteString("[LoadMaz] ERROR: no block device\r\n")
		return int64(errFileNotFound)
	}

	fs, err := fat32.Mount(blk)
	if err != nil {
		console.KWriteString("[LoadMaz] ERROR: FAT32 mount failed\r\n")
		return int64(errFileNotFound)
	}

	file, err := fs.Open(req.Filename)
	if err != nil {
		console.KWriteString("[LoadMaz] ERROR: file not found\r\n")
		return int64(errFileNotFound)
	}
	defer file.Close()

	elfData, err := file.ReadAll()
	if err != nil {
		console.KWriteString("[LoadMaz] ERROR: ReadAll failed\r\n")
		return int64(errFileNotFound)
	}

	// === Parse ELF header ===
	if len(elfData) < 64 {
		return int64(errInvalidELF)
	}

	hdr := parseELFHeader(elfData)
	if hdr.Magic != ELF_MAGIC {
		return int64(errInvalidELF)
	}
	if hdr.Class != ELF_CLASS64 || hdr.Machine != elfExpectedMachine {
		return int64(errWrongArch)
	}

	// Verify it's a PIE (ET_DYN)
	if hdr.Type != 3 { // ET_DYN = 3
		console.KWriteString("[LoadMaz] ERROR: not a PIE binary (expected ET_DYN)\r\n")
		return int64(errInvalidELF)
	}

	// === Determine load base address ===
	loadBase := priest.HighestVA
	if loadBase == 0 {
		loadBase = 0x10000000
	}
	loadBase = (loadBase + 0x100000 + 4095) &^ 4095 // 1MB gap, page-aligned

	// Find the lowest and highest VA in the .maz's program headers
	var mazLowest, mazHighest uint64
	mazLowest = ^uint64(0)
	for i := uint16(0); i < hdr.Phnum; i++ {
		phdrOffset := hdr.Phoff + uint64(i)*uint64(hdr.Phentsize)
		if phdrOffset+uint64(hdr.Phentsize) > uint64(len(elfData)) {
			continue
		}
		phdr := parseProgramHeader(elfData[phdrOffset:])
		if phdr.Type != PT_LOAD || phdr.Memsz == 0 {
			continue
		}
		if phdr.Vaddr < mazLowest {
			mazLowest = phdr.Vaddr
		}
		end := phdr.Vaddr + phdr.Memsz
		if end > mazHighest {
			mazHighest = end
		}
	}
	if mazLowest == ^uint64(0) {
		return int64(errInvalidELF)
	}

	loadOffset := loadBase - mazLowest

	console.KWriteString("[LoadMaz] base=")
	console.KPrintHex64(loadBase)
	console.KWriteString(" offset=")
	console.KPrintHex64(loadOffset)
	console.KWriteString("\r\n")

	// === Load segments into priest's page table ===
	for i := uint16(0); i < hdr.Phnum; i++ {
		phdrOffset := hdr.Phoff + uint64(i)*uint64(hdr.Phentsize)
		if phdrOffset+uint64(hdr.Phentsize) > uint64(len(elfData)) {
			continue
		}
		phdr := parseProgramHeader(elfData[phdrOffset:])
		if phdr.Type != PT_LOAD || phdr.Memsz == 0 {
			continue
		}

		adjustedPhdr := phdr
		adjustedPhdr.Vaddr += loadOffset
		adjustedPhdr.Paddr += loadOffset

		if loadErr := loadSegment(elfData, &adjustedPhdr, l0PA); loadErr != nil {
			console.KWriteString("[LoadMaz] ERROR: loadSegment failed\r\n")
			return int64(errNoSpace)
		}
	}

	// === Apply PIE base relocations (.rela.dyn) ===
	reloCount := applyPIERelocations(elfData, &hdr, loadOffset, l0PA)

	// === Resolve .maz_imports ===
	importCount := resolveMazImports(elfData, &hdr, loadOffset, l0PA, priest.SymbolTable)

	console.KWriteString("[LoadMaz] relocs=")
	console.KPrintHex64(uint64(reloCount))
	console.KWriteString(" imports=")
	console.KPrintHex64(uint64(importCount))
	console.KWriteString("\r\n")

	// === Find entry point ===
	// Try "main.MazarinMain" first (convention), fall back to "main" (standard Go PIE).
	entrySymAddr := findSymbolAddress(elfData, &hdr, "main.MazarinMain")
	if entrySymAddr == 0 {
		entrySymAddr = findSymbolAddress(elfData, &hdr, "main")
	}
	if entrySymAddr == 0 {
		console.KWriteString("[LoadMaz] ERROR: no entry point symbol found\r\n")
		return int64(errNoSymbol)
	}
	entryPoint := entrySymAddr + loadOffset

	// === Update priest's highest VA ===
	newHighest := loadBase + (mazHighest - mazLowest)
	newHighest = (newHighest + 4095) &^ 4095
	if newHighest > priest.HighestVA {
		priest.HighestVA = newHighest
	}

	// === Cache maintenance ===
	kmem.InvalidateAllICache()
	kmem.FinalUserspaceSync()

	// === Write result back to priest ===
	loadSize := mazHighest - mazLowest

	console.KWriteString("[LoadMaz] writing result to ")
	console.KPrintHex64(req.ResultPtr)
	console.KWriteString(" l0PA=")
	console.KPrintHex64(uint64(l0PA))

	// Debug: check if the page is mapped
	pa0 := kmem.WalkUserPageTableWithL0(uintptr(req.ResultPtr), l0PA)
	console.KWriteString(" PA=")
	console.KPrintHex64(uint64(pa0))
	console.KWriteString("\r\n")

	writeU64ToUser(uintptr(req.ResultPtr), entryPoint, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+8), loadBase, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+16), loadSize, l0PA)

	console.KWriteString("[LoadMaz] OK entry=")
	console.KPrintHex64(entryPoint)
	console.KWriteString("\r\n")

	return 0
}

// writeU64ToUser writes a uint64 value to a userspace address via kernel scratch mapping.
func writeU64ToUser(userVA uintptr, val uint64, l0PA uintptr) {
	// WalkUserPageTableWithL0 returns PA with page offset already included.
	pa := kmem.WalkUserPageTableWithL0(userVA, l0PA)
	if pa == 0 {
		return
	}
	// MapPAToKernelScratch adds KernelMMIOOffset — PA already has the offset,
	// so kernelVA points directly to the target byte. Do NOT add pageOffset again.
	kernelVA := kmem.MapPAToKernelScratch(pa)
	if kernelVA == 0 {
		return
	}
	*(*uint64)(unsafe.Pointer(kernelVA)) = val
	kmem.CleanPageCache(kernelVA)
}

// applyPIERelocations processes .rela.dyn entries (R_*_RELATIVE) for PIE loading.
// Each R_*_RELATIVE relocation: *addr += loadOffset
func applyPIERelocations(elfData []byte, hdr *elf64Header, loadOffset uint64, l0PA uintptr) int {
	count := 0

	// Find .rela.dyn section by looking for SHT_RELA type
	for i := uint16(0); i < hdr.Shnum; i++ {
		shdrOffset := hdr.Shoff + uint64(i)*uint64(hdr.Shentsize)
		if shdrOffset+uint64(hdr.Shentsize) > uint64(len(elfData)) {
			continue
		}
		shdr := parseSectionHeader(elfData[shdrOffset:])

		// SHT_RELA = 4
		if shdr.Type != 4 {
			continue
		}

		if shdr.Offset+shdr.Size > uint64(len(elfData)) {
			continue
		}
		relaData := elfData[shdr.Offset : shdr.Offset+shdr.Size]

		// Each rela entry is 24 bytes: offset(8) + info(8) + addend(8)
		entrySize := uint64(24)
		numEntries := shdr.Size / entrySize

		for j := uint64(0); j < numEntries; j++ {
			off := j * entrySize
			if off+entrySize > uint64(len(relaData)) {
				break
			}

			relocOffset := readU64LE(relaData[off:])
			relocInfo := readU64LE(relaData[off+8:])
			relocAddend := readU64LE(relaData[off+16:])

			relocType := uint32(relocInfo & 0xFFFFFFFF)

			// Check for R_*_RELATIVE:
			// ARM64: R_AARCH64_RELATIVE = 1027
			// x86_64: R_X86_64_RELATIVE = 8
			// RISC-V: R_RISCV_RELATIVE = 3
			isRelative := false
			switch elfExpectedMachine {
			case 0xB7: // EM_AARCH64
				isRelative = (relocType == 1027)
			case 0x3E: // EM_X86_64
				isRelative = (relocType == 8)
			case 0xF3: // EM_RISCV
				isRelative = (relocType == 3)
			}

			if !isRelative {
				continue
			}

			// The relocation target address (after PIE adjustment)
			targetVA := relocOffset + loadOffset
			// New value = addend + loadOffset (PIE base)
			newVal := relocAddend + loadOffset

			// Write the relocated value through kernel scratch mapping.
			// WalkUserPageTableWithL0 returns PA with page offset included.
			pa := kmem.WalkUserPageTableWithL0(uintptr(targetVA), l0PA)
			if pa == 0 {
				continue
			}
			kernelVA := kmem.MapPAToKernelScratch(pa)
			if kernelVA == 0 {
				continue
			}
			*(*uint64)(unsafe.Pointer(kernelVA)) = newVal
			count++
		}
	}

	return count
}

// resolveMazImports reads the .maz_imports and .maz_import_strtab sections
// and patches call sites / data pointers to target the priest's real functions.
func resolveMazImports(elfData []byte, hdr *elf64Header, loadOffset uint64, l0PA uintptr, priestSyms map[string]uint64) int {
	// Find .maz_imports and .maz_import_strtab by name
	shstrData := getSectionHeaderStrings(elfData, hdr)
	if shstrData == nil {
		return 0
	}

	var importsSection, strtabSection *elf64Shdr
	for i := uint16(0); i < hdr.Shnum; i++ {
		shdrOffset := hdr.Shoff + uint64(i)*uint64(hdr.Shentsize)
		if shdrOffset+uint64(hdr.Shentsize) > uint64(len(elfData)) {
			continue
		}
		shdr := parseSectionHeader(elfData[shdrOffset:])

		name := getSectionName(shstrData, shdr.Name)
		if name == ".maz_imports" {
			shdrCopy := shdr
			importsSection = &shdrCopy
		} else if name == ".maz_import_strtab" {
			shdrCopy := shdr
			strtabSection = &shdrCopy
		}
	}

	if importsSection == nil || strtabSection == nil {
		console.KWriteString("[LoadMaz] no .maz_imports section found\r\n")
		return 0
	}

	if importsSection.Offset+importsSection.Size > uint64(len(elfData)) ||
		strtabSection.Offset+strtabSection.Size > uint64(len(elfData)) {
		return 0
	}

	importData := elfData[importsSection.Offset : importsSection.Offset+importsSection.Size]
	strtabData := elfData[strtabSection.Offset : strtabSection.Offset+strtabSection.Size]

	count := 0
	entrySize := uint64(16)
	numEntries := importsSection.Size / entrySize

	for i := uint64(0); i < numEntries; i++ {
		off := i * entrySize
		if off+8 > uint64(len(importData)) {
			break
		}

		segOffset := readU32LE(importData[off:])
		nameIdx := readU16LE(importData[off+4:])
		relocType := importData[off+6]

		// Read symbol name from strtab
		if uint64(nameIdx) >= uint64(len(strtabData)) {
			continue
		}
		nameEnd := uint16(nameIdx)
		for nameEnd < uint16(len(strtabData)) && strtabData[nameEnd] != 0 {
			nameEnd++
		}
		symbolName := string(strtabData[nameIdx:nameEnd])

		// Look up in priest's symbol table
		priestAddr, found := priestSyms[symbolName]
		if !found {
			continue
		}

		// The instruction/data is at segOffset + loadOffset
		targetVA := uint64(segOffset) + loadOffset

		switch relocType {
		case 0: // BL_ARM64
			patchBL_ARM64(targetVA, priestAddr, l0PA)
			count++
		case 1: // CALL_X86
			patchCALL_X86(targetVA, priestAddr, l0PA)
			count++
		case 2: // PTR64
			patchPTR64(targetVA, priestAddr, l0PA)
			count++
		case 3: // JAL_RISCV
			patchJAL_RISCV(targetVA, priestAddr, l0PA)
			count++
		}
	}

	return count
}

// patchBL_ARM64 rewrites an ARM64 BL instruction at instrVA to branch to targetAddr.
func patchBL_ARM64(instrVA, targetAddr uint64, l0PA uintptr) {
	offset := int64(targetAddr) - int64(instrVA)
	if offset < -(1<<27) || offset >= (1<<27) {
		return
	}

	imm26 := uint32((offset >> 2) & 0x03FFFFFF)
	insn := uint32(0x94000000) | imm26

	writeU32ToUser(uintptr(instrVA), insn, l0PA)
}

// patchCALL_X86 rewrites an x86_64 CALL (E8) instruction at instrVA to call targetAddr.
func patchCALL_X86(instrVA, targetAddr uint64, l0PA uintptr) {
	// CALL rel32: the offset is relative to the NEXT instruction (instrVA + 5)
	offset := int64(targetAddr) - int64(instrVA) - 5
	if offset < -(1<<31) || offset >= (1<<31) {
		return
	}

	// Write E8 opcode + 32-bit relative offset (5 bytes total)
	writeU8ToUser(uintptr(instrVA), 0xE8, l0PA)
	writeU32ToUser(uintptr(instrVA+1), uint32(offset), l0PA)
}

// patchPTR64 writes a 64-bit absolute address at the given VA.
func patchPTR64(ptrVA, targetAddr uint64, l0PA uintptr) {
	writeU64ToUser(uintptr(ptrVA), targetAddr, l0PA)
}

// patchJAL_RISCV rewrites a RISC-V JAL instruction at instrVA to jump to targetAddr.
func patchJAL_RISCV(instrVA, targetAddr uint64, l0PA uintptr) {
	offset := int64(targetAddr) - int64(instrVA)
	if offset < -(1<<20) || offset >= (1<<20) {
		return
	}

	imm := uint32(offset)
	// Encode J-immediate: imm[20|10:1|11|19:12]
	bit20 := (imm >> 20) & 1
	bits10_1 := (imm >> 1) & 0x3FF
	bit11 := (imm >> 11) & 1
	bits19_12 := (imm >> 12) & 0xFF

	// JAL rd=1 (ra), opcode = 0x6F
	insn := (bit20 << 31) | (bits10_1 << 21) | (bit11 << 20) | (bits19_12 << 12) | (1 << 7) | 0x6F

	writeU32ToUser(uintptr(instrVA), insn, l0PA)
}

// writeU32ToUser writes a uint32 value to a userspace address via kernel scratch mapping.
func writeU32ToUser(userVA uintptr, val uint32, l0PA uintptr) {
	// WalkUserPageTableWithL0 returns PA with page offset already included.
	pa := kmem.WalkUserPageTableWithL0(userVA, l0PA)
	if pa == 0 {
		return
	}
	kernelVA := kmem.MapPAToKernelScratch(pa)
	if kernelVA == 0 {
		return
	}
	*(*uint32)(unsafe.Pointer(kernelVA)) = val
}

// writeU8ToUser writes a single byte to a userspace address via kernel scratch mapping.
func writeU8ToUser(userVA uintptr, val uint8, l0PA uintptr) {
	// WalkUserPageTableWithL0 returns PA with page offset already included.
	pa := kmem.WalkUserPageTableWithL0(userVA, l0PA)
	if pa == 0 {
		return
	}
	kernelVA := kmem.MapPAToKernelScratch(pa)
	if kernelVA == 0 {
		return
	}
	*(*uint8)(unsafe.Pointer(kernelVA)) = val
}

// getSectionHeaderStrings returns the section header string table data.
func getSectionHeaderStrings(elfData []byte, hdr *elf64Header) []byte {
	if hdr.Shstrndx == 0 || hdr.Shstrndx >= hdr.Shnum {
		return nil
	}
	shstrOffset := hdr.Shoff + uint64(hdr.Shstrndx)*uint64(hdr.Shentsize)
	if shstrOffset+uint64(hdr.Shentsize) > uint64(len(elfData)) {
		return nil
	}
	shstr := parseSectionHeader(elfData[shstrOffset:])
	if shstr.Offset+shstr.Size > uint64(len(elfData)) {
		return nil
	}
	return elfData[shstr.Offset : shstr.Offset+shstr.Size]
}

// getSectionName reads a null-terminated name from the section header string table.
func getSectionName(shstrData []byte, nameOff uint32) string {
	if nameOff >= uint32(len(shstrData)) {
		return ""
	}
	end := nameOff
	for end < uint32(len(shstrData)) && shstrData[end] != 0 {
		end++
	}
	return string(shstrData[nameOff:end])
}
