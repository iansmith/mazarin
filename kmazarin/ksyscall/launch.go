
// launch.go - Launch syscall implementation for loading and starting priests
package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device"
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/kmem"
	"mazzy/shared/fs/fat32"
	"unsafe"
)

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
type Process struct {
	EntryPoint       uint64
	StackTop         uint64
	StackBase        uint64
	AsyncPreemptAddr uint64 // Address of runtime.asyncPreempt (0 if not found)
}

// currentProcess holds the currently loaded process (if any)
var currentProcess *Process
var poolRangesPrinted bool

// readKernelString reads a null-terminated string from kernel memory (direct access)
// This is for reading strings passed from kernel code (high addresses, TTBR1)
//
//go:nosplit
func readKernelString(ptr uintptr) string {
	if ptr == 0 {
		return ""
	}
	maxLen := 256
	buf := make([]byte, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		b := *(*byte)(unsafe.Pointer(ptr + uintptr(i)))
		if b == 0 {
			break
		}
		buf = append(buf, b)
	}
	return string(buf)
}

// SyscallLaunch loads and launches a priest from an ELF file
// arg0: filename pointer (null-terminated string in kernel memory)
// arg1: priest number (passed as argv[1] to the program)
func SyscallLaunch(filenamePtr, priestNum, _, _, _, _ uint64) int64 {
	// Read filename from kernel memory (TTBR1 high addresses)
	filename := readKernelString(uintptr(filenamePtr))
	if filename == "" {
		return -14 // EFAULT
	}

	console.KWriteString("[Launch] ")
	console.KWriteString(filename)
	console.KWriteString("\r\n")
	// Get block device
	blk, ok := device.GetBlockDevice()
	if !ok {
		console.KPrintln("[Launch] ERROR: No block device found")
		return -1
	}

	// Mount FAT32 filesystem
	fs, err := fat32.Mount(blk)
	if err != nil {
		console.KPrintf("[Launch] ERROR: FAT32 mount failed: %v\n", err)
		return -2
	}

	// Open the ELF file
	file, err := fs.Open(filename)
	if err != nil {
		console.KPrintf("[Launch] ERROR: File open failed: %v\n", err)
		return -3
	}
	defer file.Close()

	// Allocate a buddy buffer for the file contents
	fileSize := uint64(file.Size())
	elfBuf := kmem.AllocBuffer(fileSize)
	if elfBuf == nil {
		return -4
	}
	defer kmem.FreeBuffer(elfBuf)

	// Read entire file into the buddy-allocated buffer
	elfData := elfBuf.Bytes()
	n, err := file.Read(elfData)
	if err != nil && n == 0 {
		return -4
	}
	elfData = elfData[:n]

	// Create a FRESH page table for this process.
	// This avoids inheriting any leftover mappings from Cardinal's TTBR0
	// which could cause conflicts or cache coherency issues.
	processL0PA := kmem.CreateProcessPageTable()
	if processL0PA == 0 {
		return -6
	}

	// CRITICAL: Switch TTBR0 to the new process page table BEFORE loading the ELF.
	// This ensures IC IVAU (instruction cache invalidate by VA) works correctly!
	// IC IVAU uses the current TTBR0 translation context. Without this switch,
	// IC IVAU during loadSegment would invalidate I-cache for the WRONG physical
	// pages (whatever TTBR0 was pointing to before, likely a different priest).
	// The kernel runs entirely via TTBR1 (high addresses), so switching TTBR0
	// does not affect kernel code execution.
	kmem.SwitchTTBR0WithASID(processL0PA, 0) // ASID=0 for now, will be set properly at thread schedule

	// Map the framebuffer into priest address space for UI rendering.
	// Use the GPU's actual framebuffer PA (dynamically allocated), not the
	// compile-time constant which may not match the actual allocation.
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	if !kmem.MapUserFramebuffer(fbPA, fbSize) {
		return -7
	}

	// Register the framebuffer as a span to prevent mmap collisions
	addSpan(UserFramebufferVA, UserFramebufferSize)

	// Parse and load ELF (now using the fresh process page table)
	// CRITICAL: Pass processL0PA explicitly to prevent race conditions!
	// Without this, context switches during ELF loading could cause the
	// global processL0PA to be overwritten, loading ELF data into the
	// WRONG page table.
	proc, err := loadELF(elfData, filename, processL0PA, priestNum)
	if err != nil {
		console.KPrintf("[Launch] loadELF FAILED: %v\n", err)
		return -5
	}

	// Store process info
	currentProcess = proc

	// Final I-cache invalidation before userspace - ensure all loaded code is visible
	kmem.InvalidateAllICache()

	// Enable userspace mmap allocator before jumping to userspace
	SetUserspaceActive()

	// Final cache and TLB maintenance before userspace jump
	// This ensures all written data is visible to userspace instruction fetch and data access
	kmem.FinalUserspaceSync()

	// NOTE: We do NOT switch TTBR0 here or enable timer IRQ.
	// The kernel runs using TTBR1, so TTBR0 switching is not needed during launch.
	// The context switch code will switch TTBR0 when this thread is scheduled.
	// Timer IRQ should be enabled by the caller after all launches complete,
	// to avoid race conditions where timer fires mid-launch and causes
	// context switches that corrupt global page table state.

	// Create a new thread for this process instead of jumping directly
	// The thread will be added to the ready queue and scheduled by the kernel
	// Pass the asyncPreempt address extracted from ELF symbols

	tid := CreateUserspaceThread(proc.EntryPoint, proc.StackTop, processL0PA, proc.AsyncPreemptAddr)
	_ = tid

	// Return to caller - the new thread will be scheduled later
	return 0
}

// loadELF parses an ELF file and loads it into memory
// filename is passed through to setupUserStack for argv[0]
// l0PA is the physical address of the L0 page table to use for mapping.
// CRITICAL: This must be passed explicitly to prevent race conditions with
// context switches that would otherwise corrupt the global processL0PA.
func loadELF(data []byte, filename string, l0PA uintptr, priestNum uint64) (*Process, error) {
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

	for i := uint16(0); i < hdr.Phnum; i++ {
		phdrOffset := hdr.Phoff + uint64(i)*uint64(hdr.Phentsize)
		if phdrOffset+uint64(hdr.Phentsize) > uint64(len(data)) {
			return nil, &elfError{"program header out of bounds"}
		}
		phdr := parseProgramHeader(data[phdrOffset:])
		if phdr.Type != PT_LOAD {
			continue
		}
		if err := loadSegment(data, &phdr, l0PA); err != nil {
			return nil, err
		}
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

	stackTop, err := setupUserStack(stackBase, stackSize, filename, l0PA, priestNum)
	if err != nil {
		return nil, err
	}

	// Find runtime.asyncPreempt symbol address for async preemption injection
	// Note: Go 1.25+ uses .abi0 suffix for ABI0-compatible symbols
	asyncPreemptAddr := findSymbolAddress(data, &hdr, "runtime.asyncPreempt.abi0")
	if asyncPreemptAddr == 0 {
		// Try without suffix (older Go versions)
		asyncPreemptAddr = findSymbolAddress(data, &hdr, "runtime.asyncPreempt")
	}

	return &Process{
		EntryPoint:       hdr.Entry,
		StackTop:         stackTop,
		StackBase:        stackBase,
		AsyncPreemptAddr: asyncPreemptAddr,
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

// loadSegment loads a single ELF segment into memory
// Uses kernel scratch mapping to copy data since kernel can't directly access
// userspace pages (PAN/permission restrictions on ARM64).
// l0PA is the explicit L0 page table PA to use for mapping.
//
// IMPORTANT: Uses AllocAndMapUserPageWithL0 which zeros all pages before use.
// This ensures any padding within pages is zero, not garbage data.
func loadSegment(elfData []byte, phdr *elf64Phdr, l0PA uintptr) error {
	if phdr.Memsz == 0 {
		return nil
	}

	// Calculate page-aligned boundaries
	pageSize := uint64(4096)
	startPage := phdr.Vaddr &^ (pageSize - 1)
	endAddr := phdr.Vaddr + phdr.Memsz
	endPage := (endAddr + pageSize - 1) &^ (pageSize - 1)
	numPages := (endPage - startPage) / pageSize

	isExecutable := (phdr.Flags & PF_X) != 0

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
			return &elfError{"failed to alloc/map/zero user page"}
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
				return &elfError{"failed to map PA to kernel scratch"}
			}

			// Write byte through kernel mapping
			*(*byte)(uintptrToPtr(kernelVA + uintptr(pageOffset))) = elfData[srcOffset+i]
		}
	}

	// BSS (memsz > filesz) is automatically zeroed by AllocAndMapUserPage
	// No explicit zeroing needed - DC ZVA already cleared the pages

	// DEBUG: Verify copy via linear map (bypass scratch VA entirely)
	// Clean the data cache for all pages we wrote to.
	// This ensures the data is visible to userspace when it reads via TTBR0.
	// Without this, userspace may see stale/garbage data due to cache coherency.
	// isExecutable was set earlier in the function
	for page := uint64(0); page < numPages; page++ {
		kernelVA := kmem.MapPAToKernelScratch(pagePAs[page])
		if kernelVA != 0 {
			if isExecutable {
				// For executable pages, we need to sync both D-cache and I-cache
				// to ensure the loaded code is visible to instruction fetch
				userVA := uintptr(startPage + page*pageSize)
				kmem.SyncExecutablePage(kernelVA, userVA)
			} else {
				// For non-executable pages, just clean the data cache
				kmem.CleanPageCache(kernelVA)
			}
		}
	}

	return nil
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

// setupUserStack sets up the initial stack for a userspace program.
// Returns the adjusted stack pointer.
//
// Stack layout for Go rt0 (from low to high addresses):
//
//	SP+0:   argc = 2
//	SP+8:   argv[0] → pointer to filename string
//	SP+16:  argv[1] → pointer to priest number string
//	SP+24:  NULL (end of argv)
//	SP+32:  NULL (end of envp)
//	SP+40:  AT_PAGESZ (6)
//	SP+48:  4096
//	SP+56:  AT_NULL (0)
//	SP+64:  0
//	SP+72:  filename string data (null-terminated)
//	SP+72+len(filename)+1: priest number string data (null-terminated)
func setupUserStack(stackBase, stackSize uint64, filename string, l0PA uintptr, priestNum uint64) (uint64, error) {
	pageSize := uint64(4096)
	stackTop := stackBase + stackSize

	// Get the physical address of the top stack page
	topPageVA := (stackTop - 1) &^ (pageSize - 1)
	topPA := kmem.WalkUserPageTableWithL0(uintptr(topPageVA), l0PA)
	if topPA == 0 {
		return 0, &elfError{"stack page not mapped"}
	}

	// Map the stack page to kernel scratch for writing
	kernelVA := kmem.MapPAToKernelScratch(topPA &^ uintptr(pageSize-1))
	if kernelVA == 0 {
		return 0, &elfError{"failed to map stack to kernel scratch"}
	}

	// Convert priest number to string (single digit 0-9 for now)
	priestStr := "0"
	if priestNum < 10 {
		buf := []byte{'0' + byte(priestNum), 0}
		priestStr = string(buf[:1])
	}

	// Calculate string data sizes (including null terminators)
	// Remove leading '/' from filename for argv[0] display, but keep full path
	filenameLen := uint64(len(filename)) + 1 // +1 for null terminator
	priestStrLen := uint64(len(priestStr)) + 1

	// Environment variable for GC debugging
	godebugStr := "GODEBUG=gctrace=1"
	godebugLen := uint64(len(godebugStr)) + 1

	// Layout: 10 uint64s (pointers/values) + string data, 16-byte aligned
	// argc + argv[0] + argv[1] + NULL(argv) + envp[0] + NULL(envp) + auxv(4)
	pointerAreaSize := uint64(10 * 8)
	stringDataSize := filenameLen + priestStrLen + godebugLen
	totalSize := pointerAreaSize + stringDataSize
	totalSize = (totalSize + 15) &^ 15 // 16-byte align

	// SP will be near the top of the stack, 16-byte aligned
	sp := (stackTop - totalSize) &^ 15

	// String data starts after the pointer area
	stringBase := sp + pointerAreaSize
	argv0Addr := stringBase
	argv1Addr := stringBase + filenameLen
	godebugAddr := stringBase + filenameLen + priestStrLen

	// Write the stack layout
	offset := uint64(0)

	// argc = 2
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, 2)
	offset += 8

	// argv[0] → pointer to filename string
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, argv0Addr)
	offset += 8

	// argv[1] → pointer to priest number string
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, argv1Addr)
	offset += 8

	// NULL (end of argv)
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, 0)
	offset += 8

	// envp[0] → pointer to GODEBUG string
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, godebugAddr)
	offset += 8

	// NULL (end of envp)
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, 0)
	offset += 8

	// AT_PAGESZ = 6
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, 6)
	offset += 8

	// page size value = 4096
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, pageSize)
	offset += 8

	// AT_NULL = 0 (auxv terminator)
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, 0)
	offset += 8

	// 0 (auxv terminator value)
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, 0)
	offset += 8

	// Write filename string data (null-terminated)
	for i := 0; i < len(filename); i++ {
		writeStackByte(stackBase, stackTop, kernelVA, argv0Addr+uint64(i), filename[i])
	}
	writeStackByte(stackBase, stackTop, kernelVA, argv0Addr+uint64(len(filename)), 0)

	// Write priest number string data (null-terminated)
	for i := 0; i < len(priestStr); i++ {
		writeStackByte(stackBase, stackTop, kernelVA, argv1Addr+uint64(i), priestStr[i])
	}
	writeStackByte(stackBase, stackTop, kernelVA, argv1Addr+uint64(len(priestStr)), 0)

	// Write GODEBUG environment variable string data (null-terminated)
	for i := 0; i < len(godebugStr); i++ {
		writeStackByte(stackBase, stackTop, kernelVA, godebugAddr+uint64(i), godebugStr[i])
	}
	writeStackByte(stackBase, stackTop, kernelVA, godebugAddr+uint64(len(godebugStr)), 0)

	// Clean cache for the stack page we wrote to
	kmem.CleanPageCache(kernelVA)

	return sp, nil
}

// writeStackByte writes a byte to the userspace stack via kernel scratch mapping
func writeStackByte(stackBase, stackTop uint64, kernelVA uintptr, addr uint64, val byte) {
	if addr < stackBase || addr >= stackTop {
		return // Out of bounds
	}
	pageSize := uint64(4096)
	topPageVA := (stackTop - 1) &^ (pageSize - 1)
	if addr >= topPageVA && addr < topPageVA+pageSize {
		// Same page as top - use existing kernel mapping
		offset := addr - topPageVA
		*(*byte)(uintptrToPtr(kernelVA + uintptr(offset))) = val
	}
	// TODO: Handle multiple pages if needed
}

// writeStackU64 writes a uint64 to the userspace stack via kernel scratch mapping
func writeStackU64(stackBase, stackTop uint64, kernelVA uintptr, addr uint64, val uint64) {
	// Write as 8 bytes in little-endian order
	for i := uint64(0); i < 8; i++ {
		writeStackByte(stackBase, stackTop, kernelVA, addr+i, byte(val>>(i*8)))
	}
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

// elfError represents an ELF parsing error
type elfError struct {
	msg string
}

func (e *elfError) Error() string {
	return "elf: " + e.msg
}
