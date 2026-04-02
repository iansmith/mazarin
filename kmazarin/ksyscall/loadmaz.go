// loadmaz.go - SysLoadMaz syscall: load a .maz PIE ELF into a shepherd's address space.
//
// .maz programs are dynamically linked at load time: their thin runtime stubs
// are patched to call the host shepherd's real functions. This allows .maz binaries
// to be tiny (~100KB) while sharing the shepherd's full Go runtime.
//
// The heavy work (FAT32 I/O, ELF segment loading, PIE relocations) is offloaded
// to a kernel worker goroutine because the SVC handler runs on g0/SP_EL1 where
// the Go stack cannot grow. See loadmaz_bridge.go for the worker protocol.
package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"unsafe"
)

// MazLoadResult is the struct written back to the shepherd upon successful load.
// Layout must match mazarin/sys/loadmaz.go exactly.
type MazLoadResult struct {
	EntryPoint     uint64 // Address of entry point (main.MazarinMain or main) in loaded .maz
	LoadBase       uint64 // Base VA where .maz was loaded
	LoadSize       uint64 // Total VA size of loaded segments
	ModuledataAddr uint64 // Address of runtime.firstmoduledata in loaded .maz (0 if not found)
	ShepherdInitAddr uint64 // Address of main.MazarinShepherd in loaded .maz (0 if not found)
}

// LoadMazWorkRequest contains the parameters for a .maz load operation.
type LoadMazWorkRequest struct {
	Filename string
	ResultPtr uint64
	Shepherd  *proc.Shepherd
	L0PA      uintptr
	DataVA    uintptr // 0 = read from disk, non-zero = pre-loaded pages
	DataLen   uint64  // Length of pre-loaded ELF data in bytes
}

// SyscallLoadMaz loads a .maz PIE ELF into the calling shepherd's address space.
// This is a thin entry point that validates arguments, stores the request, and
// blocks the calling thread. The actual loading is done by the kernel worker
// goroutine (via DoLoadMazWork) which has a growable stack.
//
// arg0: pointer to null-terminated filename string (in shepherd's memory)
// arg1: pointer to MazLoadResult struct (in shepherd's writable memory)
// arg2: dataVA — if non-zero, ELF data is pre-loaded at this VA (delegate path)
// arg3: dataLen — length of pre-loaded ELF data
// Returns: 0 on success, ErrorCode on failure
//
//go:noinline
func SyscallLoadMaz(filenamePtr, resultPtr, dataVA, dataLen, _, _ uint64) int64 {
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

	// Find the calling shepherd
	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		console.KWriteString("[LoadMaz] ERROR: not called from a shepherd\r\n")
		return int64(errNullPointer)
	}

	if shepherd.SymbolTable == nil {
		console.KWriteString("[LoadMaz] ERROR: shepherd has no symbol table\r\n")
		return int64(errNoSymbol)
	}

	// Submit to kernel worker goroutine. Submit handles the busy CAS guard,
	// blocks the calling thread, and sets the pending flag for thread 0 relay.
	ctxPtr := submitLoadMaz(LoadMazWorkRequest{
		Filename:  filename,
		ResultPtr: resultPtr,
		Shepherd:  shepherd,
		L0PA:      shepherd.PageTableL0PA,
		DataVA:    uintptr(dataVA),
		DataLen:   dataLen,
	})
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
	} else {
		console.KWriteString("[LoadMaz] ERROR: busy or no thread to switch to\r\n")
		return int64(errNoSpace)
	}

	// Return value doesn't matter — the worker will overwrite ThreadContext.X[0]
	// via SetReturnValue before waking this thread.
	return 0
}

// DoLoadMazWork performs the heavy .maz loading work. Called by the kernel
// worker goroutine (in loadmaz_bridge.go) on a normal growable stack.
//
// Two data paths:
//   - Direct (DataVA == 0): mounts FAT32 via kernel block device, reads file from disk.
//   - Pre-loaded (DataVA != 0): reads ELF data from caller's pages via linear map.
//     Used when the file was loaded via the LoadFile delegate (fs.maz → async DMA).
func DoLoadMazWork(req *LoadMazWorkRequest) int64 {
	l0PA := req.L0PA
	shepherd := req.Shepherd

	var elfData []byte

	if req.DataVA != 0 && req.DataLen > 0 {
		// Pre-loaded mode: read ELF from caller's pages via kernel linear map
		elfData = readUserData(req.DataVA, req.DataLen, l0PA)
		if elfData == nil {
			console.KWriteString("[LoadMaz] ERROR: failed to read pre-loaded data\r\n")
			return int64(errFileNotFound)
		}
	} else {
		// Previously, there was a backup way to load a file here, called
		// loadMazInternal, but it has been removed. All .maz loading must
		// go through the LoadFile delegate (fs shepherd) which provides
		// pre-loaded pages via DataVA/DataLen.
		console.KWriteString("[LoadMaz] ERROR: direct disk load path removed — use LoadFile delegate\r\n")
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
	} else {
		// ET_DYN (PIE): relocate above shepherd's current highest VA
		loadBase = shepherd.HighestVA
		if loadBase == 0 {
			loadBase = 0x10000000
		}
		loadBase = (loadBase + 0x100000 + 4095) &^ 4095 // 1MB gap, page-aligned
		loadOffset = loadBase - mazLowest
	}

	// === Load segments into shepherd's page table ===
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
	importCount := resolveMazImports(elfData, &hdr, loadOffset, l0PA, shepherd.SymbolTable)

	_ = reloCount
	_ = importCount

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
	}

	// === Find main.MazarinShepherd for interface injection ===
	shepherdInitSymAddr := findSymbolAddress(elfData, &hdr, "main.MazarinShepherd")
	var shepherdInitVA uint64
	if shepherdInitSymAddr != 0 {
		shepherdInitVA = shepherdInitSymAddr + loadOffset
	}

	// === Update shepherd's highest VA ===
	newHighest := loadBase + (mazHighest - mazLowest)
	newHighest = (newHighest + 4095) &^ 4095
	if newHighest > shepherd.HighestVA {
		shepherd.HighestVA = newHighest
	}

	// === Cache maintenance ===
	kmem.InvalidateAllICache()
	kmem.FinalUserspaceSync()

	// === Write result back to shepherd ===
	loadSize := mazHighest - mazLowest

	writeU64ToUser(uintptr(req.ResultPtr), entryPoint, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+8), loadBase, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+16), loadSize, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+24), moduledataVA, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+32), shepherdInitVA, l0PA)

	return 0
}

// readUserData reads data from a userspace address range into a kernel buffer.
// Used by the pre-loaded LoadMaz path to copy ELF data from pages that were
// transferred to the caller by the LoadFile delegate (fs.maz).
func readUserData(dataVA uintptr, dataLen uint64, l0PA uintptr) []byte {
	buf := make([]byte, dataLen)
	offset := uint64(0)
	for offset < dataLen {
		va := dataVA + uintptr(offset)
		pa := kmem.WalkUserPageTableWithL0(va, l0PA)
		if pa == 0 {
			console.KWriteString("[LoadMaz] readUserData: unmapped VA ")
			console.KPrintHex64(uint64(va))
			console.KWriteString("\r\n")
			return nil
		}
		// MapPAToKernelScratch converts PA (with byte offset) to kernel VA
		kernelVA := kmem.MapPAToKernelScratch(pa)
		if kernelVA == 0 {
			return nil
		}
		// Calculate bytes until next page boundary
		pageOffset := uintptr(va) & (kmem.PageSize - 1)
		canCopy := uint64(kmem.PageSize - pageOffset)
		remaining := dataLen - offset
		if canCopy > remaining {
			canCopy = remaining
		}
		src := unsafe.Slice((*byte)(unsafe.Pointer(kernelVA)), int(canCopy))
		copy(buf[offset:offset+canCopy], src)
		offset += canCopy
	}
	return buf
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
// and patches call sites / data pointers to target the shepherd's real functions.
func resolveMazImports(elfData []byte, hdr *elf64Header, loadOffset uint64, l0PA uintptr, shepherdSyms map[string]uint64) int {
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

		// Look up in shepherd's symbol table
		shepherdAddr, found := shepherdSyms[symbolName]
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
			if patchBL_ARM64(targetVA, shepherdAddr, l0PA) {
				count++
			}
		case 1: // CALL_X86
			patchCALL_X86(targetVA, shepherdAddr, l0PA)
			count++
		case 2: // PTR64
			patchPTR64(targetVA, shepherdAddr, l0PA)
			count++
		case 3: // JAL_RISCV
			patchJAL_RISCV(targetVA, shepherdAddr, l0PA)
			count++
		case 4: // B_ARM64 — body trampoline (unconditional branch, no link)
			if patchB_ARM64(targetVA, shepherdAddr, l0PA) {
				count++
			}
		case 5: // JMP_X86 — body trampoline (E9 JMP rel32)
			patchJMP_X86(targetVA, shepherdAddr, l0PA)
			count++
		case 6: // J_RISCV — body trampoline (JAL x0)
			patchJ_RISCV(targetVA, shepherdAddr, l0PA)
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

// patchBL_ARM64 patches an ARM64 BL import by replacing the stub body
// with a B (unconditional branch) trampoline to the host function.
//
// Instead of patching the call-site BL instruction (which only fixes
// direct calls found by maz-reloc), we decode the BL to find the stub
// address and replace the stub body with a B trampoline. This ensures
// ALL calls to the stub — direct BL, indirect calls through funcvals,
// interface dispatch, etc. — are redirected to the host function.
//
// This follows the same pattern as RISC-V's patchJAL_RISCV and x86_64's
// patchCALL_X86, which decode the call-site instruction, find the stub,
// and patch the stub body.
func patchBL_ARM64(instrVA, targetAddr uint64, l0PA uintptr) bool {
	// Read the BL instruction at the call site to find the stub.
	pa := kmem.WalkUserPageTableWithL0(uintptr(instrVA), l0PA)
	if pa == 0 {
		return false
	}
	kva := kmem.MapPAToKernelScratch(pa)
	if kva == 0 {
		return false
	}
	insn := *(*uint32)(unsafe.Pointer(kva))

	// Verify this is a BL instruction (opcode [31:26] = 100101).
	if insn&0xFC000000 != 0x94000000 {
		return false
	}

	// Decode 26-bit signed offset (in units of 4 bytes).
	imm26 := int32(insn & 0x03FFFFFF)
	if imm26&0x02000000 != 0 {
		imm26 |= ^int32(0x03FFFFFF) // sign extend from bit 25
	}
	stubVA := uint64(int64(instrVA) + int64(imm26)*4)

	// Patch the stub body with B (tail jump) to the host function.
	return patchB_ARM64(stubVA, targetAddr, l0PA)
}

// patchB_ARM64 writes an ARM64 B (unconditional branch, no link) at funcVA
// to trampoline to targetAddr. Used to patch .maz's runtime.morestack body
// so it jumps to the host shepherd's working morestack.
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

// patchCALL_X86 patches an x86_64 CALL (E8) import by replacing the stub
// body with a JMP (E9) trampoline to the host function.
//
// Instead of patching the call-site CALL instruction (which only fixes
// direct calls found by maz-reloc), we decode the CALL to find the stub
// address and replace the stub body with a JMP trampoline. This ensures
// ALL calls to the stub — direct CALL, indirect CALL through funcvals,
// interface dispatch, etc. — are redirected to the host function.
//
// This follows the same pattern as RISC-V's patchJAL_RISCV, which
// decodes the JAL to find the stub and patches the stub body with
// AUIPC+JALR. The stub body is always at least 5 bytes (Go function
// prologue + CALL to _thinStubPanic), so the 5-byte JMP fits.
func patchCALL_X86(instrVA, targetAddr uint64, l0PA uintptr) {
	// Read the CALL (E8) instruction at the call site to find the stub.
	pa := kmem.WalkUserPageTableWithL0(uintptr(instrVA), l0PA)
	if pa == 0 {
		return
	}
	kva := kmem.MapPAToKernelScratch(pa)
	if kva == 0 {
		return
	}
	opcode := *(*uint8)(unsafe.Pointer(kva))
	if opcode != 0xE8 {
		return
	}

	// Read the 32-bit relative offset (at instrVA+1).
	rel32PA := kmem.WalkUserPageTableWithL0(uintptr(instrVA+1), l0PA)
	if rel32PA == 0 {
		return
	}
	rel32KVA := kmem.MapPAToKernelScratch(rel32PA)
	if rel32KVA == 0 {
		return
	}
	rel32 := *(*int32)(unsafe.Pointer(rel32KVA))

	// Compute stub address: CALL target = instrVA + 5 + rel32
	stubVA := uint64(int64(instrVA) + 5 + int64(rel32))

	// Patch the stub body with JMP (E9 + rel32) to the host function.
	patchJMP_X86(stubVA, targetAddr, l0PA)
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
