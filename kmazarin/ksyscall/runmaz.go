// runmaz.go - SysRunMaz syscall: load a .maz ELF from the caller's pages.
//
// Unlike SysLoadMaz (which reads from disk), RunMaz reads ELF data from pages
// already mapped in the caller's address space (e.g., loaded via LoadFile).
// After processing, the raw ELF pages are implicitly unmapped from the caller.
package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"sync/atomic"
	"unsafe"
)

// RunMazWorkRequest contains the parameters for a RunMaz operation.
type RunMazWorkRequest struct {
	StartVA    uintptr
	NumPages   int
	TotalBytes int
	ResultPtr  uint64
	Shepherd     *proc.Shepherd
	L0PA       uintptr
	BlockedTID int32 // Set by BlockForRunMaz in main package
}

// RunMazReq is the global request struct shared between the SVC handler
// and the kernel worker goroutine. One request at a time.
// RunMazBusy guards against concurrent access (latent SMP hazard).
var RunMazReq RunMazWorkRequest
var RunMazBusy int32

// SyscallRunMaz processes pages in the caller's address space as a .maz ELF.
// The ELF is parsed, segments are loaded, relocations applied, and imports resolved.
// The raw ELF pages are implicitly unmapped from the caller.
//
// arg0 = startVA (page-aligned, where raw ELF data is mapped)
// arg1 = numPages (number of 4KB pages)
// arg2 = totalBytes (actual file size, may be less than numPages * 4096)
// arg3 = pointer to MazLoadResult struct (in caller's writable memory)
//
// Returns: 0 on success, ErrorCode on failure.
//
//go:noinline
func SyscallRunMaz(arg0, arg1, arg2, arg3, _, _ uint64) int64 {
	startVA := uintptr(arg0)
	numPages := int(arg1)
	totalBytes := int(arg2)
	resultPtr := arg3

	// Validate arguments
	if startVA == 0 || startVA&0xFFF != 0 {
		return int64(errNullPointer)
	}
	// RunMaz uses copyPagesFromUser (byte-by-byte), not the fixed-size PA
	// array in TransferPages, so it can handle larger .maz files.
	const maxRunMazPages = 4096 // 16MB
	if numPages < 1 || numPages > maxRunMazPages {
		return -22 // EINVAL
	}
	if totalBytes < 64 || totalBytes > numPages*4096 {
		return int64(errInvalidELF)
	}
	if resultPtr == 0 {
		return int64(errNullPointer)
	}

	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return int64(errNullPointer)
	}
	if shepherd.SymbolTable == nil {
		return int64(errNoSymbol)
	}

	// Guard against concurrent access (latent SMP hazard).
	if !atomic.CompareAndSwapInt32(&RunMazBusy, 0, 1) {
		console.KWriteString("[RunMaz] ERROR: concurrent request\r\n")
		return -16 // EBUSY
	}

	// Store request for the worker goroutine
	RunMazReq = RunMazWorkRequest{
		StartVA:    startVA,
		NumPages:   numPages,
		TotalBytes: totalBytes,
		ResultPtr:  resultPtr,
		Shepherd:     shepherd,
		L0PA:       shepherd.PageTableL0PA,
	}

	// Block and dispatch to worker goroutine (needs growable stack)
	ctxPtr := blockForRunMaz()
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
	} else {
		console.KWriteString("[RunMaz] ERROR: no thread to switch to\r\n")
		return int64(errNoSpace)
	}

	return 0
}

// DoRunMazWork performs the heavy .maz loading from the caller's pages.
// Called by the kernel worker goroutine on a normal growable stack.
func DoRunMazWork(req *RunMazWorkRequest) int64 {
	l0PA := req.L0PA
	shepherd := req.Shepherd

	// Copy ELF data from the caller's pages into a contiguous kernel buffer.
	elfData := copyPagesFromUser(req.StartVA, req.TotalBytes, l0PA)
	if elfData == nil {
		console.KWriteString("[RunMaz] ERROR: failed to copy pages from user\r\n")
		return int64(errNullPointer)
	}

	// Unmap the raw ELF pages from the caller (implicit cleanup).
	unmapUserPages(req.StartVA, req.NumPages, l0PA, int16(shepherd.PID))

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

	isFixedAddr := hdr.Type == 2 // ET_EXEC
	isPIE := hdr.Type == 3       // ET_DYN
	if !isFixedAddr && !isPIE {
		return int64(errInvalidELF)
	}

	// Find lowest/highest VA in program headers
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

	// Determine load base and offset
	var loadBase, loadOffset uint64
	if isFixedAddr {
		loadBase = mazLowest
		loadOffset = 0
	} else {
		loadBase = shepherd.HighestVA
		if loadBase == 0 {
			loadBase = 0x10000000
		}
		loadBase = (loadBase + 0x100000 + 4095) &^ 4095
		loadOffset = loadBase - mazLowest
	}

	console.KWriteString("[RunMaz] base=")
	console.KPrintHex64(loadBase)
	console.KWriteString(" offset=")
	console.KPrintHex64(loadOffset)
	console.KWriteString("\r\n")

	// Load segments into shepherd's page table
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
			console.KWriteString("[RunMaz] ERROR: loadSegment failed\r\n")
			return int64(errNoSpace)
		}
	}

	// Apply PIE relocations (ET_DYN only)
	reloCount := 0
	if isPIE {
		reloCount = applyPIERelocations(elfData, &hdr, loadOffset, l0PA)
	}

	// Resolve .maz_imports
	importCount := resolveMazImports(elfData, &hdr, loadOffset, l0PA, shepherd.SymbolTable)

	console.KWriteString("[RunMaz] relocs=")
	console.KPrintHex64(uint64(reloCount))
	console.KWriteString(" imports=")
	console.KPrintHex64(uint64(importCount))
	console.KWriteString("\r\n")

	// Find entry point
	entrySymAddr := findSymbolAddress(elfData, &hdr, "main.MazarinMain")
	if entrySymAddr == 0 {
		entrySymAddr = findSymbolAddress(elfData, &hdr, "main")
	}
	if entrySymAddr == 0 {
		return int64(errNoSymbol)
	}
	entryPoint := entrySymAddr + loadOffset

	// Find moduledata
	moduledataSymAddr := findSymbolAddress(elfData, &hdr, "runtime.firstmoduledata")
	var moduledataVA uint64
	if moduledataSymAddr != 0 {
		moduledataVA = moduledataSymAddr + loadOffset
	}

	// Find MazarinShepherd
	shepherdInitSymAddr := findSymbolAddress(elfData, &hdr, "main.MazarinShepherd")
	var shepherdInitVA uint64
	if shepherdInitSymAddr != 0 {
		shepherdInitVA = shepherdInitSymAddr + loadOffset
	}

	// Update shepherd's highest VA
	newHighest := loadBase + (mazHighest - mazLowest)
	newHighest = (newHighest + 4095) &^ 4095
	if newHighest > shepherd.HighestVA {
		shepherd.HighestVA = newHighest
	}

	// Cache maintenance
	kmem.InvalidateAllICache()
	kmem.FinalUserspaceSync()

	// Write result back to shepherd
	writeU64ToUser(uintptr(req.ResultPtr), entryPoint, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+8), loadBase, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+16), mazHighest-mazLowest, l0PA) // LoadSize
	writeU64ToUser(uintptr(req.ResultPtr+24), moduledataVA, l0PA)
	writeU64ToUser(uintptr(req.ResultPtr+32), shepherdInitVA, l0PA)

	console.KWriteString("[RunMaz] OK entry=")
	console.KPrintHex64(entryPoint)
	console.KWriteString("\r\n")

	return 0
}

// copyPagesFromUser copies data from a contiguous range of user pages into
// a kernel-allocated buffer. Returns the buffer or nil on failure.
func copyPagesFromUser(startVA uintptr, totalBytes int, l0PA uintptr) []byte {
	buf := make([]byte, totalBytes)
	offset := 0
	for offset < totalBytes {
		va := startVA + uintptr(offset)
		pageRem := 4096 - int(va&0xFFF)
		chunk := pageRem
		if chunk > totalBytes-offset {
			chunk = totalBytes - offset
		}
		pa := kmem.DemandMapUserPage(va, l0PA)
		if pa == 0 {
			return nil
		}
		kernelVA := kmem.MapPAToKernelScratch(pa)
		if kernelVA == 0 {
			return nil
		}
		src := unsafe.Slice((*byte)(unsafe.Pointer(kernelVA)), chunk)
		copy(buf[offset:offset+chunk], src)
		offset += chunk
	}
	return buf
}

// unmapUserPages unmaps a range of user pages and releases them.
func unmapUserPages(startVA uintptr, numPages int, l0PA uintptr, ownerSID int16) {
	for i := 0; i < numPages; i++ {
		va := startVA + uintptr(i)*4096
		pa := kmem.UnmapUserPageWithL0(va, l0PA)
		if pa != 0 {
			kmem.ReleasePageByPA(pa &^ 0xFFF)
		}
	}
	// Remove span from shepherd
	callerShepherd := proc.FindShepherdBySID(proc.ShepherdId(ownerSID))
	if callerShepherd != nil {
		callerShepherd.Spans.Remove(uint64(startVA), uint64(numPages)*4096)
	}
}
