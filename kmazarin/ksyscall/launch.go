
// launch.go - Launch syscall implementation for loading and starting shepherds
package ksyscall

import (
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ktime"
	"mazzy/kmazarin/proc"
)

// bootTimezone is the IANA timezone string from the boot config (e.g. "America/New_York").
// Set by the kernel during boot, passed to shepherds as the TZ env var.
var bootTimezone string

// SetBootTimezone stores the timezone string from the boot config.
func SetBootTimezone(tz string) {
	bootTimezone = tz
}

// suppressSerialStdioCopy is set from the boot config's SuppressSerialStdioCopy field.
// Passed to shepherds as the SUPPRESS_SERIAL_STDIO_COPY env var so the linux
// shepherd knows whether to echo delegated Write output to serial.
var suppressSerialStdioCopy bool

// SetSuppressSerialStdioCopy stores the suppress serial copy setting.
func SetSuppressSerialStdioCopy(v bool) {
	suppressSerialStdioCopy = v
}

// shepherdGCPercentage is the GOGC value for shepherd processes.
// 0 means use the default (100 = Go standard).
var shepherdGCPercentage int

// SetShepherdGCPercentage stores the GC percentage from the boot config.
func SetShepherdGCPercentage(v int) {
	shepherdGCPercentage = v
}

// shepherdMemLimitMB is the GOMEMLIMIT value (in MB) for shepherd processes.
// 0 means use the default (24MB).
var shepherdMemLimitMB int

// SetShepherdMemLimitMB stores the GOMEMLIMIT value from the boot config.
func SetShepherdMemLimitMB(v int) {
	shepherdMemLimitMB = v
}

// ELF constants
const (
	ELF_MAGIC      = 0x464C457F // "\x7FELF"
	ELF_CLASS64    = 2
	ELF_DATA2LSB   = 1          // Little-endian
	PT_LOAD = 1 // Loadable segment

	PF_X = 1 // Executable
	PF_W = 2 // Writable
	PF_R = 4 // Readable
)

// ELF64 header structure
type elf64Header struct {
	Magic      uint32
	Class      uint8
	Data       uint8
	Version    uint8
	OSABI      uint8
	ABIVersion uint8
	_          [7]uint8 // padding
	Type       uint16
	Machine    uint16
	Version2   uint32
	Entry      uint64
	Phoff      uint64 // Program header offset
	Shoff      uint64 // Section header offset
	Flags      uint32
	Ehsize     uint16
	Phentsize  uint16
	Phnum      uint16
	Shentsize  uint16
	Shnum      uint16
	Shstrndx   uint16
}

// ELF64 program header
type elf64Phdr struct {
	Type   uint32
	Flags  uint32
	Offset uint64
	Vaddr  uint64
	Paddr  uint64
	Filesz uint64
	Memsz  uint64
	Align  uint64
}

// ELF64 section header
type elf64Shdr struct {
	Name      uint32
	Type      uint32
	Flags     uint64
	Addr      uint64
	Offset    uint64
	Size      uint64
	Link      uint32
	Info      uint32
	Addralign uint64
	Entsize   uint64
}

// ELF64 symbol table entry
type elf64Sym struct {
	Name  uint32
	Info  uint8
	Other uint8
	Shndx uint16
	Value uint64
	Size  uint64
}

// Section header types
const (
	SHT_NULL     = 0  // Inactive
	SHT_SYMTAB   = 2  // Symbol table
	SHT_STRTAB   = 3  // String table
	SHT_DYNSYM   = 11 // Dynamic symbol table
)

// Process represents a loaded userspace process
type segSpan struct {
	VA   uint64
	Size uint64
}

type Process struct {
	EntryPoint   uint64
	StackTop     uint64
	StackBase    uint64
	SegmentCount int
	SegmentSpans [16]segSpan
}

// currentProcess holds the currently loaded process (if any)
var currentProcess *Process

// LaunchFromMemory loads and launches a shepherd from in-memory ELF data.
// Used for the embedded fs shepherd — no disk I/O needed.
func LaunchFromMemory(elfData []byte, name string) int64 {
	if len(elfData) < 64 {
		klog.Errf("[Launch] ERROR: embedded ELF too small\n")
		return -4
	}

	// Create a FRESH page table for this process.
	processL0PA := kmem.CreateProcessPageTable()
	if processL0PA == 0 {
		return -6
	}

	// Map the framebuffer into shepherd address space using explicit L0PA.
	// We do NOT switch TTBR0 here — this goroutine is preemptible and other
	// goroutines on the same M would see the wrong address space.
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	if !kmem.MapUserFramebufferWithL0(fbPA, fbSize, processL0PA) {
		return -7
	}

	// Map constraint shared pages read-only into shepherd address space.
	if !kmem.MapUserConstraintPagesWithL0(processL0PA) {
		return -8
	}

	// Initialize kernel attribute manager (once, on first shepherd launch).
	InitKernelAttrManager()

	// Build symbol table and track highest VA BEFORE loading
	hdr := parseELFHeader(elfData)
	shepherdSymTable := buildSymbolTable(elfData, &hdr)
	shepherdHighestVA := findHighestVA(elfData, &hdr)

	// Parse and load ELF using the fresh process page table
	filename := "/" + name + ".elf"
	loadedProc, err := loadELF(elfData, filename, processL0PA, 0, nil, nil)
	if err != nil {
		klog.Errf("[Launch] loadELF FAILED: %v\n", err)
		return -5
	}

	// Store process info
	currentProcess = loadedProc

	// Final I-cache invalidation before userspace
	kmem.InvalidateAllICache()

	// Enable userspace mmap allocator
	SetUserspaceActive()

	// Final cache and TLB maintenance
	kmem.FinalUserspaceSync()

	// Create a new thread for this process
	CreateUserspaceThread(loadedProc.EntryPoint, loadedProc.StackTop, processL0PA)

	// Cache symbol table, highest VA, filename, allocate IPC uring ring, and
	// register all kernel-allocated VA ranges in Spans so CleanupShepherdPages
	// Phase 1 correctly reclaims ELF, stack, framebuffer, and constraint pages.
	proc.Shepherds.ForEach(func(p *proc.Shepherd) bool {
		if p.PageTableL0PA != processL0PA {
			return true // keep iterating
		}
		p.SymbolTable = shepherdSymTable
		p.HighestVA = shepherdHighestVA
		p.Filename = filename

		// Allocate IPC uring ring for the new shepherd
		uringID := allocateUringID()
		p.UringID = uringID
		allocateUringIPCRing(p, 0)
		registerUringID(uringID, int16(p.PID))

		// Register kernel-allocated VA ranges that mmap syscall never sees.
		for j := 0; j < loadedProc.SegmentCount; j++ {
			p.Spans.Add(loadedProc.SegmentSpans[j].VA, loadedProc.SegmentSpans[j].Size)
		}
		p.Spans.Add(loadedProc.StackBase, 64*1024)
		p.Spans.Add(UserFramebufferVA, UserFramebufferSize)
		// UserConstraintPagesVA intentionally not in Spans — see runshepherd.go.

		return false // stop iteration — found our shepherd
	})

	return 0
}

// cloneExecLayout carries the faithful argv/envp for the execve→clone_exec
// child stack (MAZ-120). When non-nil, setupUserStack uses Argv verbatim
// (argv[0] = caller's program name, NO shepherd-number argv[1]) and lays out
// the caller's Envp merged with the mandatory mazzy runtime env (mandatory
// wins). When nil, setupUserStack keeps the legacy shepherd-launch layout
// ({filename, shepherdStr, extraArgs...} + mandatory-only env).
type cloneExecLayout struct {
	Argv [][]byte // faithful argv; Argv[0] is the program name
	Envp [][]byte // caller's envp (merged with mandatory env inside setupUserStack)
}

// loadELF parses an ELF file and loads it into memory
// filename is passed through to setupUserStack for argv[0]
// l0PA is the physical address of the L0 page table to use for mapping.
// CRITICAL: This must be passed explicitly to prevent race conditions with
// context switches that would otherwise corrupt the global processL0PA.
//
// faithful is non-nil only for the execve→clone_exec path (MAZ-120); it
// overrides the shepherd-launch argv/envv layout with the caller's faithful
// argv[0]+envp. extraArgs is ignored when faithful is set.
func loadELF(data []byte, filename string, l0PA uintptr, shepherdNum uint64, extraArgs []string, faithful *cloneExecLayout) (*Process, error) {
	if len(data) < 64 {
		return nil, &elfError{"file too small for ELF header"}
	}

	// Parse ELF header
	hdr := parseELFHeader(data)

	if hdr.Magic != ELF_MAGIC {
		return nil, &elfError{"invalid ELF magic"}
	}
	if hdr.Class != ELF_CLASS64 || hdr.Machine != elfExpectedMachine {
		return nil, &elfError{"ELF machine type mismatch"}
	}

	// Consult the shared-text cache. On hit, read-only PT_LOAD segments are
	// mapped from pre-loaded physical frames instead of allocated+copied,
	// saving ~4-5 MiB per subsequent launch of the same shepherd binary.
	// Writable segments (PF_W) always take the normal alloc+copy path.
	//
	// Scope: only plugin-host launches (shepherd.elf loading a .maz plugin)
	// are cacheable. The embedded fs shepherd and legacy ET_EXEC loads skip
	// the cache entirely so the single slot stays reserved for shepherd.elf.
	var cacheSnap cacheSnapshot
	cacheable := isCacheableLoad(extraArgs)
	if cacheable {
		cacheSnap = snapshotCache(data)
	}

	// On cache miss, record R-only segments here so we can populate the
	// cache after all segments have been successfully loaded.
	var newSegs []sharedSegment

	// Record all PT_LOAD VA ranges so the caller can register them in the
	// shepherd's Spans for correct cleanup on death.
	var segSpans [16]segSpan
	segCount := 0

	for i := uint16(0); i < hdr.Phnum; i++ {
		phdrOffset := hdr.Phoff + uint64(i)*uint64(hdr.Phentsize)
		if phdrOffset+uint64(hdr.Phentsize) > uint64(len(data)) {
			return nil, &elfError{"program header out of bounds"}
		}
		phdr := parseProgramHeader(data[phdrOffset:])
		if phdr.Type != PT_LOAD {
			continue
		}

		// Compute the same start/end rounding loadSegment uses, for cache key math.
		const pageSize = uint64(4096)
		startPage := phdr.Vaddr &^ (pageSize - 1)
		endPage := (phdr.Vaddr + phdr.Memsz + pageSize - 1) &^ (pageSize - 1)
		roundedSize := endPage - startPage

		// Record VA range for Spans registration after shepherd is created.
		if segCount < len(segSpans) {
			segSpans[segCount] = segSpan{VA: startPage, Size: roundedSize}
			segCount++
		}

		// Cache hit + cacheable segment → map existing frames, skip alloc+copy.
		if cacheSnap.hit && isCacheableSegment(phdr.Flags) {
			var cached *sharedSegment
			for j := range cacheSnap.segments {
				seg := &cacheSnap.segments[j]
				if seg.startVA == startPage && seg.memsz == roundedSize && seg.flags == phdr.Flags {
					cached = seg
					break
				}
			}
			if cached != nil {
				if err := mapSharedSegment(cached, l0PA); err != nil {
					return nil, err
				}
				continue
			}
			// Fingerprint matched but no segment entry for these bounds;
			// fall through to a normal load (won't be cached — slot is full).
		}

		pagePAs, err := loadSegment(data, &phdr, l0PA)
		if err != nil {
			return nil, err
		}

		// On cache miss, record R-only segments for post-load populate.
		if cacheable && !cacheSnap.hit && isCacheableSegment(phdr.Flags) && len(pagePAs) > 0 {
			newSegs = append(newSegs, sharedSegment{
				startVA: startPage,
				memsz:   roundedSize,
				flags:   phdr.Flags,
				pas:     pagePAs,
			})
		}
	}

	// Populate cache if this load brought in R-only segments and the slot is empty.
	if cacheable && !cacheSnap.hit && len(newSegs) > 0 {
		populateCache(cacheSnap.sizeBytes, cacheSnap.fp, newSegs)
	}

	// Allocate a user stack (64KB at a fixed location for now)
	// Stack grows downward from high addresses to low addresses.
	// Stack region: 0x00007FFF00000000 - 0x00007FFF0000FFFF (mapped pages)
	// Located near the top of userspace VA to leave room for mmap below.
	stackBase := uint64(0x00007FFF00000000)
	stackSize := uint64(64 * 1024) // 64KB

	if err := allocateUserStack(stackBase, stackSize, l0PA); err != nil {
		return nil, err
	}

	stackTop, err := setupUserStack(stackBase, stackSize, filename, l0PA, shepherdNum, extraArgs, faithful)
	if err != nil {
		return nil, err
	}

	return &Process{
		EntryPoint:   hdr.Entry,
		StackTop:     stackTop,
		StackBase:    stackBase,
		SegmentCount: segCount,
		SegmentSpans: segSpans,
	}, nil
}

// parseELFHeader extracts ELF header from raw bytes
func parseELFHeader(data []byte) elf64Header {
	return elf64Header{
		Magic:     readU32LE(data[0:4]),
		Class:     data[4],
		Data:      data[5],
		Version:   data[6],
		OSABI:     data[7],
		ABIVersion: data[8],
		Type:      readU16LE(data[16:18]),
		Machine:   readU16LE(data[18:20]),
		Version2:  readU32LE(data[20:24]),
		Entry:     readU64LE(data[24:32]),
		Phoff:     readU64LE(data[32:40]),
		Shoff:     readU64LE(data[40:48]),
		Flags:     readU32LE(data[48:52]),
		Ehsize:    readU16LE(data[52:54]),
		Phentsize: readU16LE(data[54:56]),
		Phnum:     readU16LE(data[56:58]),
		Shentsize: readU16LE(data[58:60]),
		Shnum:     readU16LE(data[60:62]),
		Shstrndx:  readU16LE(data[62:64]),
	}
}

// parseProgramHeader extracts program header from raw bytes
func parseProgramHeader(data []byte) elf64Phdr {
	return elf64Phdr{
		Type:   readU32LE(data[0:4]),
		Flags:  readU32LE(data[4:8]),
		Offset: readU64LE(data[8:16]),
		Vaddr:  readU64LE(data[16:24]),
		Paddr:  readU64LE(data[24:32]),
		Filesz: readU64LE(data[32:40]),
		Memsz:  readU64LE(data[40:48]),
		Align:  readU64LE(data[48:56]),
	}
}

// parseSectionHeader extracts section header from raw bytes
func parseSectionHeader(data []byte) elf64Shdr {
	return elf64Shdr{
		Name:      readU32LE(data[0:4]),
		Type:      readU32LE(data[4:8]),
		Flags:     readU64LE(data[8:16]),
		Addr:      readU64LE(data[16:24]),
		Offset:    readU64LE(data[24:32]),
		Size:      readU64LE(data[32:40]),
		Link:      readU32LE(data[40:44]),
		Info:      readU32LE(data[44:48]),
		Addralign: readU64LE(data[48:56]),
		Entsize:   readU64LE(data[56:64]),
	}
}

// parseSymbol extracts a symbol table entry from raw bytes
func parseSymbol(data []byte) elf64Sym {
	return elf64Sym{
		Name:  readU32LE(data[0:4]),
		Info:  data[4],
		Other: data[5],
		Shndx: readU16LE(data[6:8]),
		Value: readU64LE(data[8:16]),
		Size:  readU64LE(data[16:24]),
	}
}

// findSymbolAddress searches for a symbol by name and returns its address.
// Returns 0 if not found.
func findSymbolAddress(elfData []byte, hdr *elf64Header, symbolName string) uint64 {
	if hdr.Shnum == 0 || hdr.Shoff == 0 {
		return 0 // No section headers
	}

	// Find the symbol table and its associated string table
	var symtab *elf64Shdr
	var strtab *elf64Shdr

	for i := uint16(0); i < hdr.Shnum; i++ {
		shdrOffset := hdr.Shoff + uint64(i)*uint64(hdr.Shentsize)
		if shdrOffset+uint64(hdr.Shentsize) > uint64(len(elfData)) {
			continue
		}

		shdr := parseSectionHeader(elfData[shdrOffset:])

		if shdr.Type == SHT_SYMTAB {
			// Found symbol table - copy to heap so we can reference it
			symtabCopy := shdr
			symtab = &symtabCopy
		}
	}

	if symtab == nil {
		return 0 // No symbol table found
	}

	// Get the associated string table (symtab.Link points to it)
	if symtab.Link >= uint32(hdr.Shnum) {
		return 0 // Invalid string table index
	}

	strtabOffset := hdr.Shoff + uint64(symtab.Link)*uint64(hdr.Shentsize)
	if strtabOffset+uint64(hdr.Shentsize) > uint64(len(elfData)) {
		return 0
	}
	strtabHdr := parseSectionHeader(elfData[strtabOffset:])
	strtab = &strtabHdr

	// Validate string table bounds
	if strtab.Offset+strtab.Size > uint64(len(elfData)) {
		return 0
	}
	strtabData := elfData[strtab.Offset : strtab.Offset+strtab.Size]

	// Validate symbol table bounds
	if symtab.Offset+symtab.Size > uint64(len(elfData)) {
		return 0
	}
	symtabData := elfData[symtab.Offset : symtab.Offset+symtab.Size]

	// Iterate through symbols
	symSize := uint64(24) // sizeof(elf64Sym)
	numSyms := symtab.Size / symSize

	for i := uint64(0); i < numSyms; i++ {
		symOffset := i * symSize
		if symOffset+symSize > uint64(len(symtabData)) {
			break
		}

		sym := parseSymbol(symtabData[symOffset:])

		// Get symbol name from string table
		if sym.Name >= uint32(len(strtabData)) {
			continue
		}

		// Find the null-terminated string
		nameStart := sym.Name
		nameEnd := nameStart
		for nameEnd < uint32(len(strtabData)) && strtabData[nameEnd] != 0 {
			nameEnd++
		}

		name := string(strtabData[nameStart:nameEnd])

		if name == symbolName {
			return sym.Value
		}
	}

	return 0 // Symbol not found
}

// buildSymbolTable builds a complete name → VA address map from an ELF's .symtab.
// This is used to cache the shepherd's symbols for SysGetOwnExports /
// mazdl host-export registration (mazarin/mazdl/host_register_mazarin.go).
// Only includes FUNC and OBJECT symbols with non-zero values.
func buildSymbolTable(elfData []byte, hdr *elf64Header) map[string]uint64 {
	result := make(map[string]uint64)

	if hdr.Shnum == 0 || hdr.Shoff == 0 {
		return result
	}

	// Find the symbol table section
	var symtab *elf64Shdr
	for i := uint16(0); i < hdr.Shnum; i++ {
		shdrOffset := hdr.Shoff + uint64(i)*uint64(hdr.Shentsize)
		if shdrOffset+uint64(hdr.Shentsize) > uint64(len(elfData)) {
			continue
		}
		shdr := parseSectionHeader(elfData[shdrOffset:])
		if shdr.Type == SHT_SYMTAB {
			symtabCopy := shdr
			symtab = &symtabCopy
			break
		}
	}

	if symtab == nil {
		return result
	}

	// Get the associated string table
	if symtab.Link >= uint32(hdr.Shnum) {
		return result
	}
	strtabOffset := hdr.Shoff + uint64(symtab.Link)*uint64(hdr.Shentsize)
	if strtabOffset+uint64(hdr.Shentsize) > uint64(len(elfData)) {
		return result
	}
	strtabHdr := parseSectionHeader(elfData[strtabOffset:])

	if strtabHdr.Offset+strtabHdr.Size > uint64(len(elfData)) {
		return result
	}
	strtabData := elfData[strtabHdr.Offset : strtabHdr.Offset+strtabHdr.Size]

	if symtab.Offset+symtab.Size > uint64(len(elfData)) {
		return result
	}
	symtabData := elfData[symtab.Offset : symtab.Offset+symtab.Size]

	symSize := uint64(24) // sizeof(elf64Sym)
	numSyms := symtab.Size / symSize

	for i := uint64(0); i < numSyms; i++ {
		symOffset := i * symSize
		if symOffset+symSize > uint64(len(symtabData)) {
			break
		}

		sym := parseSymbol(symtabData[symOffset:])

		// Skip symbols with no value (undefined, etc.)
		if sym.Value == 0 {
			continue
		}

		// Only include FUNC and OBJECT symbols
		symType := sym.Info & 0x0F
		if symType != 1 && symType != 2 { // STT_OBJECT=1, STT_FUNC=2
			continue
		}

		// Get name from string table
		if sym.Name >= uint32(len(strtabData)) {
			continue
		}
		nameStart := sym.Name
		nameEnd := nameStart
		for nameEnd < uint32(len(strtabData)) && strtabData[nameEnd] != 0 {
			nameEnd++
		}
		name := string(strtabData[nameStart:nameEnd])
		if name == "" {
			continue
		}

		result[name] = sym.Value
	}

	return result
}

// findHighestVA returns the highest VA address used by loaded segments.
func findHighestVA(elfData []byte, hdr *elf64Header) uint64 {
	var highest uint64
	for i := uint16(0); i < hdr.Phnum; i++ {
		phdrOffset := hdr.Phoff + uint64(i)*uint64(hdr.Phentsize)
		if phdrOffset+uint64(hdr.Phentsize) > uint64(len(elfData)) {
			continue
		}
		phdr := parseProgramHeader(elfData[phdrOffset:])
		if phdr.Type != PT_LOAD {
			continue
		}
		end := phdr.Vaddr + phdr.Memsz
		if end > highest {
			highest = end
		}
	}
	// Page-align upward
	return (highest + 4095) &^ 4095
}

// loadSegment loads a single ELF segment into memory
// Uses kernel scratch mapping to copy data since kernel can't directly access
// userspace pages (PAN/permission restrictions on ARM64).
// l0PA is the explicit L0 page table PA to use for mapping.
//
// IMPORTANT: Uses AllocAndMapUserPageWithL0 which zeros all pages before use.
// This ensures any padding within pages is zero, not garbage data.
//
// Returns the list of allocated physical frames in VA order (starting at the
// page containing phdr.Vaddr), for the caller to record in the shared-text
// cache if this segment is read-only.
func loadSegment(elfData []byte, phdr *elf64Phdr, l0PA uintptr) ([]uintptr, error) {
	if phdr.Memsz == 0 {
		return nil, nil
	}

	// Calculate page-aligned boundaries
	pageSize := uint64(4096)
	startPage := phdr.Vaddr &^ (pageSize - 1)
	endAddr := phdr.Vaddr + phdr.Memsz
	endPage := (endAddr + pageSize - 1) &^ (pageSize - 1)
	numPages := (endPage - startPage) / pageSize

	// Track physical addresses for each page so we can remap scratch VA later
	// AllocAndMapUserPageWithL0 returns (framePA, scratchVA)
	pagePAs := make([]uintptr, numPages)

	// Allocate, map, and ZERO pages for userspace
	for page := uint64(0); page < numPages; page++ {
		pageVA := startPage + page*pageSize

		// AllocAndMapUserPageWithL0 allocates from userspace pool, maps to user VA
		// using the explicit l0PA, maps to kernel scratch VA, and zeros the page.
		// Using explicit l0PA prevents race conditions with context switches.
		framePA, _ := kmem.AllocAndMapUserPageWithL0(uintptr(pageVA), phdr.Flags, l0PA)
		if framePA == 0 {
			return nil, &elfError{"failed to alloc/map/zero user page"}
		}
		pagePAs[page] = framePA

	}

	// Copy file data to memory via kernel scratch mapping
	// We can't write to userspace VA directly, so we map each physical frame
	// to a kernel-accessible VA temporarily for copying
	if phdr.Filesz > 0 {
		srcOffset := phdr.Offset

		for i := uint64(0); i < phdr.Filesz; i++ {
			if srcOffset+i >= uint64(len(elfData)) {
				break
			}

			// Calculate which page this byte belongs to
			dstAddr := phdr.Vaddr + i
			pageIdx := (dstAddr - startPage) / pageSize
			pageOffset := dstAddr & (pageSize - 1)

			// Map the page's physical address to kernel scratch VA
			kernelVA := kmem.MapPAToKernelScratch(pagePAs[pageIdx])
			if kernelVA == 0 {
				return nil, &elfError{"failed to map PA to kernel scratch"}
			}

			// Write byte through kernel mapping
			*(*byte)(uintptrToPtr(kernelVA + uintptr(pageOffset))) = elfData[srcOffset+i]
		}
	}

	// BSS (memsz > filesz) is automatically zeroed by AllocAndMapUserPage
	// No explicit zeroing needed - DC ZVA already cleared the pages

	// Clean the data cache for all pages we wrote to.
	// This ensures the data is visible to userspace when it reads via TTBR0.
	// Without this, userspace may see stale/garbage data due to cache coherency.
	//
	// NOTE: Only D-cache clean here, NOT I-cache invalidation by VA.
	// IC IVAU requires the userVA to be mapped in the current TTBR0, which is
	// not guaranteed during LoadMaz/RunMaz (kernel worker goroutine may have a
	// different TTBR0). All callers perform global I-cache invalidation
	// (InvalidateAllICache + FinalUserspaceSync) after all segments are loaded.
	for page := uint64(0); page < numPages; page++ {
		kernelVA := kmem.MapPAToKernelScratch(pagePAs[page])
		if kernelVA != 0 {
			kmem.CleanPageCache(kernelVA)
		}
	}

	return pagePAs, nil
}

// allocateUserStack allocates pages for the user stack
// l0PA is the explicit L0 page table PA to use for mapping.
// Uses AllocAndMapUserPageWithL0 which zeroes pages via DC ZVA
func allocateUserStack(base, size uint64, l0PA uintptr) error {
	pageSize := uint64(4096)
	numPages := size / pageSize

	for page := uint64(0); page < numPages; page++ {
		pageVA := base + page*pageSize
		// Stack is RW (no execute)
		// AllocAndMapUserPageWithL0 allocates, maps, and zeros the page using DC ZVA
		// Using explicit l0PA prevents race conditions with context switches.
		framePA, scratchVA := kmem.AllocAndMapUserPageWithL0(uintptr(pageVA), PF_R|PF_W, l0PA)
		if framePA == 0 {
			return &elfError{"failed to alloc/map/zero stack page"}
		}

		// Clean cache to ensure zeros are visible to userspace
		kmem.CleanPageCache(scratchVA)
	}

	return nil
}

// setupUserStack maps the stack pages and uses ProcessEnv to lay out the
// argv/envp/auxv appropriate for launching a shepherd.
//
// faithful is non-nil only for the execve→clone_exec child (MAZ-120): its Argv
// replaces the {filename, shepherdStr, extraArgs...} layout verbatim and its
// Envp is merged with the mandatory env (mandatory wins) instead of using the
// mandatory env alone.
func setupUserStack(stackBase, stackSize uint64, filename string, l0PA uintptr, shepherdNum uint64, extraArgs []string, faithful *cloneExecLayout) (uint64, error) {
	pageSize := uint64(4096)
	stackTop := stackBase + stackSize

	// Map EVERY stack page to a kernel scratch VA so the layout can span more
	// than the top page — a faithful execve argv+envp can be far larger than
	// the 4 KB the single-page path historically supported (MAZ-120). The
	// scratch mapping is a fixed linear PA→VA offset, so non-contiguous physical
	// frames produce non-contiguous scratch VAs; StackWriter resolves per page.
	numPages := int(stackSize / pageSize)
	pageVAs := make([]uint64, 0, numPages)
	scratchVAs := make([]uintptr, 0, numPages)
	for i := 0; i < numPages; i++ {
		pageVA := stackBase + uint64(i)*pageSize
		pa := kmem.WalkUserPageTableWithL0(uintptr(pageVA), l0PA)
		if pa == 0 {
			return 0, &elfError{"stack page not mapped"}
		}
		scratchVA := kmem.MapPAToKernelScratch(pa &^ uintptr(pageSize-1))
		if scratchVA == 0 {
			return 0, &elfError{"failed to map stack to kernel scratch"}
		}
		pageVAs = append(pageVAs, pageVA)
		scratchVAs = append(scratchVAs, scratchVA)
	}
	// Top-page scratch VA, for the post-layout cache clean of the SP page.
	topPageVA := (stackTop - 1) &^ (pageSize - 1)
	kernelVA := scratchVAs[(topPageVA-stackBase)/pageSize]

	// Convert shepherd number to string (single digit 0-9)
	shepherdStr := "0"
	if shepherdNum < 10 {
		buf := []byte{'0' + byte(shepherdNum)}
		shepherdStr = string(buf)
	}

	penv := NewProcessEnv()
	penv.SetEnv("GODEBUG", "gccheckmark=1")
	gcVal := shepherdGCPercentage
	if gcVal <= 0 {
		gcVal = 100 // Go standard default
	}
	memLimitMB := shepherdMemLimitMB
	if memLimitMB <= 0 {
		memLimitMB = 24 // default
	}

	// Scan extraArgs for __MAZZY_ kernel directives.
	// These are per-shepherd runtime overrides consumed by the kernel and
	// stripped from the shepherd's argv — not passed to the process.
	var filteredArgs []string
	for _, a := range extraArgs {
		switch {
		case mazzyArgHasPrefix(a, "__MAZZY_GOMEMLIMIT="):
			if v := parseMazzyArgInt(a, len("__MAZZY_GOMEMLIMIT=")); v > 0 {
				memLimitMB = v
			}
		case mazzyArgHasPrefix(a, "__MAZZY_GCPERCENT="):
			if v := parseMazzyArgInt(a, len("__MAZZY_GCPERCENT=")); v > 0 {
				gcVal = v
			}
		default:
			filteredArgs = append(filteredArgs, a)
		}
	}

	// Convert gcVal to string (max 6 digits)
	var gcBuf [6]byte
	gcLen := 0
	tmp := gcVal
	for tmp > 0 {
		gcLen++
		tmp /= 10
	}
	if gcLen == 0 {
		gcLen = 1
		gcBuf[0] = '0'
	} else {
		tmp = gcVal
		for i := gcLen - 1; i >= 0; i-- {
			gcBuf[i] = byte('0' + tmp%10)
			tmp /= 10
		}
	}
	penv.SetEnv("GOGC", string(gcBuf[:gcLen]))
	// Convert memLimitMB to string (max 6 digits) + "MiB"
	var mlBuf [10]byte // "NNNNNNMiB\0"
	mlLen := 0
	tmp = memLimitMB
	for tmp > 0 {
		mlLen++
		tmp /= 10
	}
	if mlLen == 0 {
		mlLen = 1
		mlBuf[0] = '0'
	} else {
		tmp = memLimitMB
		for i := mlLen - 1; i >= 0; i-- {
			mlBuf[i] = byte('0' + tmp%10)
			tmp /= 10
		}
	}
	mlBuf[mlLen] = 'M'
	mlBuf[mlLen+1] = 'i'
	mlBuf[mlLen+2] = 'B'
	penv.SetEnv("GOMEMLIMIT", string(mlBuf[:mlLen+3]))
	penv.SetEnv("GOMAXPROCS", "1")
	if bootTimezone != "" {
		penv.SetEnv("TZ", bootTimezone)
	}
	if suppressSerialStdioCopy {
		penv.SetEnv("SUPPRESS_SERIAL_STDIO_COPY", "1")
	} else {
		penv.SetEnv("SUPPRESS_SERIAL_STDIO_COPY", "0")
	}

	// Pass boot epoch to userspace for walltime computation.
	// Shepherds use these to compute real wall clock time from CNTVCT_EL0
	// without needing a clock_gettime SVC on every nanotime/walltime call.
	bootSec, bootTicks, _ := ktime.GetBootEpoch()
	// RTC only provides whole-second resolution, so boot nsec is 0.
	penv.SetEnv("MAZZY_BOOT_SEC", uint64ToDecimal(bootSec))
	penv.SetEnv("MAZZY_BOOT_TICKS", uint64ToDecimal(bootTicks))
	penv.SetEnv("MAZZY_BOOT_NSEC", "0")

	penv.SetAuxv(6, 4096) // AT_PAGESZ

	sw := &StackWriter{
		StackBase:  stackBase,
		StackTop:   stackTop,
		KernelVA:   kernelVA,
		PageVAs:    pageVAs,
		ScratchVAs: scratchVAs,
	}

	var sp uint64
	var err error
	if faithful != nil {
		// Execve faithful-argv path (MAZ-120): caller's argv verbatim (argv[0] =
		// program name, NO shepherd-number argv[1] — that is a RunShepherd-launch
		// artifact a fork/exec'd Linux child must never see), and the caller's
		// envp merged with the mandatory mazzy runtime env (mandatory wins).
		merged := proc.MergeExecEnv(faithful.Envp, penv.EnvBytes())
		sp, err = penv.LayoutFaithful(faithful.Argv, merged, sw)
	} else {
		// Shepherd-launch path: argv = {filename, shepherdStr, extraArgs...},
		// env = mandatory only.
		argv := []string{filename, shepherdStr}
		argv = append(argv, filteredArgs...)
		sp, err = penv.Layout(argv, sw)
	}
	if err != nil {
		return 0, err
	}

	// Clean every stack page the layout may have written (the faithful path can
	// span multiple pages), not just the top page.
	for _, scratchVA := range scratchVAs {
		kmem.CleanPageCache(scratchVA)
	}

	return sp, nil
}

// Helper functions for reading little-endian values
func readU16LE(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

func readU32LE(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func readU64LE(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

// uintptrToPtr converts uintptr to unsafe.Pointer
//go:nosplit
func uintptrToPtr(p uintptr) *byte {
	return (*byte)(unsafePointer(p))
}

// uint64ToDecimal converts a uint64 to its decimal string representation.
func uint64ToDecimal(val uint64) string {
	if val == 0 {
		return "0"
	}
	var buf [20]byte // uint64 max is 20 digits
	pos := len(buf)
	for val > 0 {
		pos--
		buf[pos] = byte('0' + val%10)
		val /= 10
	}
	return string(buf[pos:])
}

// mazzyArgHasPrefix reports whether s starts with prefix.
// Kernel-safe: no strings package import needed.
func mazzyArgHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// parseMazzyArgInt parses a decimal integer starting at offset in s.
// Returns 0 if s is too short or contains no digits at offset.
// Kernel-safe: no strconv package import needed.
func parseMazzyArgInt(s string, offset int) int {
	n := 0
	for i := offset; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// elfError represents an ELF parsing error
type elfError struct {
	msg string
}

func (e *elfError) Error() string {
	return "elf: " + e.msg
}
