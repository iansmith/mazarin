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
	EntryPoint     uint64 // Address of entry point (main.MazarinMain or main) in loaded .maz
	LoadBase       uint64 // Base VA where .maz was loaded
	LoadSize       uint64 // Total VA size of loaded segments
	ModuledataAddr uint64 // Address of runtime.firstmoduledata in loaded .maz (0 if not found)
	PriestInitAddr uint64 // Address of main.MazarinPriest in loaded .maz (0 if not found)
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

	// Check ELF type: ET_EXEC (2) = fixed-address .mzr, ET_DYN (3) = PIE .maz
	isFixedAddr := hdr.Type == 2 // ET_EXEC
	isPIE := hdr.Type == 3       // ET_DYN
	if !isFixedAddr && !isPIE {
		console.KWriteString("[LoadMaz] ERROR: unsupported ELF type (expected ET_EXEC or ET_DYN)\r\n")
		return int64(errInvalidELF)
	}

	// Find the lowest and highest VA in the program headers
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

	// === Determine load base and offset ===
	var loadBase, loadOffset uint64
	if isFixedAddr {
		// ET_EXEC: binary runs at its linked addresses, no relocation
		loadBase = mazLowest
		loadOffset = 0
		console.KWriteString("[LoadMaz] ET_EXEC base=")
	} else {
		// ET_DYN (PIE): relocate above priest's current highest VA
		loadBase = priest.HighestVA
		if loadBase == 0 {
			loadBase = 0x10000000
		}
		loadBase = (loadBase + 0x100000 + 4095) &^ 4095 // 1MB gap, page-aligned
		loadOffset = loadBase - mazLowest
		console.KWriteString("[LoadMaz] ET_DYN base=")
	}
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

	// === Apply PIE base relocations (.rela.dyn) — only for ET_DYN ===
	reloCount := 0
	if isPIE {
		reloCount = applyPIERelocations(elfData, &hdr, loadOffset, l0PA)
	}

	// === Resolve .maz_imports (works for both ET_EXEC and ET_DYN) ===
	importCount := resolveMazImports(elfData, &hdr, loadOffset, l0PA, priest.SymbolTable)

	console.KWriteString("[LoadMaz] relocs=")
	console.KPrintHex64(uint64(reloCount))
	console.KWriteString(" imports=")
	console.KPrintHex64(uint64(importCount))
	console.KWriteString("\r\n")

	// === Find entry point ===
	entrySymAddr := findSymbolAddress(elfData, &hdr, "main.MazarinMain")
	if entrySymAddr == 0 {
		entrySymAddr = findSymbolAddress(elfData, &hdr, "main")
	}
	if entrySymAddr == 0 {
		console.KWriteString("[LoadMaz] ERROR: no entry point symbol found\r\n")
		return int64(errNoSymbol)
	}
	entryPoint := entrySymAddr + loadOffset

	// === Find runtime.firstmoduledata for pclntab registration ===
	moduledataSymAddr := findSymbolAddress(elfData, &hdr, "runtime.firstmoduledata")
	var moduledataVA uint64
	if moduledataSymAddr != 0 {
		moduledataVA = moduledataSymAddr + loadOffset
		console.KWriteString("[LoadMaz] moduledata=")
		console.KPrintHex64(moduledataVA)
		console.KWriteString("\r\n")
	} else {
		console.KWriteString("[LoadMaz] no runtime.firstmoduledata found\r\n")
	}

	// === Find main.MazarinPriest for interface injection ===
	priestInitSymAddr := findSymbolAddress(elfData, &hdr, "main.MazarinPriest")
	var priestInitVA uint64
	if priestInitSymAddr != 0 {
		priestInitVA = priestInitSymAddr + loadOffset
		console.KWriteString("[LoadMaz] MazarinPriest=")
		console.KPrintHex64(priestInitVA)
		console.KWriteString("\r\n")
	}

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
	writeU64ToUser(uintptr(req.ResultPtr+24), moduledataVA, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+32), priestInitVA, l0PA)

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
	unresolved := 0
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
			unresolved++
			if unresolved <= 5 {
				console.KWriteString("[LoadMaz] UNRESOLVED import: ")
				console.KWriteString(symbolName)
				console.KWriteString("\r\n")
			}
			continue
		}

		// The instruction/data is at segOffset + loadOffset
		targetVA := uint64(segOffset) + loadOffset

		switch relocType {
		case 0: // BL_ARM64
			if patchBL_ARM64(targetVA, priestAddr, l0PA) {
				count++
			}
		case 1: // CALL_X86
			patchCALL_X86(targetVA, priestAddr, l0PA)
			count++
		case 2: // PTR64
			patchPTR64(targetVA, priestAddr, l0PA)
			count++
		case 3: // JAL_RISCV
			patchJAL_RISCV(targetVA, priestAddr, l0PA)
			count++
		case 4: // B_ARM64 — body trampoline (unconditional branch, no link)
			if patchB_ARM64(targetVA, priestAddr, l0PA) {
				count++
			}
		case 5: // JMP_X86 — body trampoline (E9 JMP rel32)
			patchJMP_X86(targetVA, priestAddr, l0PA)
			count++
		case 6: // J_RISCV — body trampoline (JAL x0)
			patchJ_RISCV(targetVA, priestAddr, l0PA)
			count++
		}
	}

	if unresolved > 0 {
		console.KWriteString("[LoadMaz] unresolved: ")
		console.KPrintHex64(uint64(unresolved))
		console.KWriteString(" of ")
		console.KPrintHex64(numEntries)
		console.KWriteString(" total\r\n")
	}

	return count
}

// patchBL_ARM64 rewrites an ARM64 BL instruction at instrVA to branch to targetAddr.
func patchBL_ARM64(instrVA, targetAddr uint64, l0PA uintptr) bool {
	offset := int64(targetAddr) - int64(instrVA)
	if offset < -(1<<27) || offset >= (1<<27) {
		console.KWriteString("[LoadMaz] BL range exceeded: from=")
		console.KPrintHex64(instrVA)
		console.KWriteString(" to=")
		console.KPrintHex64(targetAddr)
		console.KWriteString(" offset=")
		console.KPrintHex64(uint64(offset))
		console.KWriteString("\r\n")
		return false
	}

	imm26 := uint32((offset >> 2) & 0x03FFFFFF)
	insn := uint32(0x94000000) | imm26

	writeU32ToUser(uintptr(instrVA), insn, l0PA)
	return true
}

// patchB_ARM64 writes an ARM64 B (unconditional branch, no link) at funcVA
// to trampoline to targetAddr. Used to patch .maz's runtime.morestack body
// so it jumps to the host priest's working morestack.
func patchB_ARM64(funcVA, targetAddr uint64, l0PA uintptr) bool {
	offset := int64(targetAddr) - int64(funcVA)
	if offset < -(1<<27) || offset >= (1<<27) {
		console.KWriteString("[LoadMaz] B range exceeded: from=")
		console.KPrintHex64(funcVA)
		console.KWriteString(" to=")
		console.KPrintHex64(targetAddr)
		console.KWriteString("\r\n")
		return false
	}

	imm26 := uint32((offset >> 2) & 0x03FFFFFF)
	insn := uint32(0x14000000) | imm26 // B (not BL)

	writeU32ToUser(uintptr(funcVA), insn, l0PA)
	return true
}

// patchJMP_X86 writes an x86_64 JMP rel32 (E9) at funcVA to trampoline
// to targetAddr. Used for morestack body patching.
func patchJMP_X86(funcVA, targetAddr uint64, l0PA uintptr) {
	// JMP rel32: offset relative to next instruction (funcVA + 5)
	offset := int64(targetAddr) - int64(funcVA) - 5
	if offset < -(1<<31) || offset >= (1<<31) {
		return
	}
	writeU8ToUser(uintptr(funcVA), 0xE9, l0PA)
	writeU32ToUser(uintptr(funcVA+1), uint32(offset), l0PA)
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

// patchJAL_RISCV is defined in loadmaz_riscv64.go (real implementation)
// and loadmaz_arm64.go / loadmaz_amd64.go (empty stubs).

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
