// launch.go - Launch syscall implementation for loading and starting priests
package ksyscall

import (
	"kmazarin/console"
	"kmazarin/device"
	"kmazarin/device/virtio/gpu"
	"kmazarin/fs/fat32"
	"kmazarin/kmem"
	"unsafe"
	_ "unsafe" // for go:linkname
)

// Custom auxv types for Mazzy-specific values.
// Start at 512 to avoid conflicts with Linux auxv types (0-50).
// These must match mazarin/priest.AuxvType values.
const (
	AT_MAZZY_FB_ADDR   = 512 // Framebuffer virtual address in priest space
	AT_MAZZY_FB_WIDTH  = 513 // Framebuffer width in pixels
	AT_MAZZY_FB_HEIGHT = 514 // Framebuffer height in pixels
	AT_MAZZY_FB_PITCH  = 515 // Framebuffer pitch (bytes per row)
)

// ELF constants
const (
	ELF_MAGIC      = 0x464C457F // "\x7FELF"
	ELF_CLASS64    = 2
	ELF_DATA2LSB   = 1          // Little-endian
	ELF_MACHINE_AARCH64 = 0xB7  // ARM64

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

// Process represents a loaded userspace process
type Process struct {
	EntryPoint uint64
	StackTop   uint64
	StackBase  uint64
}

// currentProcess holds the currently loaded process (if any)
var currentProcess *Process

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
func SyscallLaunch(filenamePtr, _, _, _, _, _ uint64) int64 {
	// Read filename from kernel memory (TTBR1 high addresses)
	filename := readKernelString(uintptr(filenamePtr))
	if filename == "" {
		console.KWriteString("[Launch] ERROR: Invalid filename pointer\r\n")
		return -14 // EFAULT
	}

	console.KWriteString("\r\n[Launch] Loading ")
	console.KWriteString(filename)
	console.KWriteString("\r\n")

	// Get block device
	blk, ok := device.GetBlockDevice()
	if !ok {
		console.KWriteString("[Launch] ERROR: No block device found\r\n")
		return -1
	}

	// Mount FAT32 filesystem
	fs, err := fat32.Mount(blk)
	if err != nil {
		console.KWriteString("[Launch] ERROR: Failed to mount filesystem: ")
		console.KWriteString(err.Error())
		console.KWriteString("\r\n")
		return -2
	}

	// Open the ELF file
	file, err := fs.Open(filename)
	if err != nil {
		console.KWriteString("[Launch] ERROR: Failed to open file: ")
		console.KWriteString(err.Error())
		console.KWriteString("\r\n")
		return -3
	}
	defer file.Close()

	// Read entire file
	elfData, err := file.ReadAll()
	if err != nil {
		console.KWriteString("[Launch] ERROR: Failed to read file: ")
		console.KWriteString(err.Error())
		console.KWriteString("\r\n")
		return -4
	}

	console.KWriteString("[Launch] Read ")
	console.KPrintHex64(uint64(len(elfData)))
	console.KWriteString(" bytes\r\n")

	// DEBUG: Check raw ELF data at SizeClassToSize offset (0x170A00)
	// This verifies the FAT32 read is correct
	console.KWriteString("[Launch] ELF[0x170A00..0x170A10]: ")
	if len(elfData) > 0x170A10 {
		for i := 0; i < 16; i++ {
			console.KPrintHex64(uint64(elfData[0x170A00+i]))
			console.KWriteString(" ")
		}
	} else {
		console.KWriteString("(offset beyond file size)")
	}
	console.KWriteString("\r\n")

	// Create a FRESH page table for this process.
	// This avoids inheriting any leftover mappings from Cardinal's TTBR0
	// which could cause conflicts or cache coherency issues.
	processL0PA := kmem.CreateProcessPageTable()
	if processL0PA == 0 {
		console.KWriteString("[Launch] ERROR: Failed to create process page table\r\n")
		return -6
	}
	_ = processL0PA // Suppress unused variable warning (value stored in kmem global)

	// Map the framebuffer into priest address space for UI rendering
	if !kmem.MapUserFramebuffer() {
		console.KWriteString("[Launch] ERROR: Failed to map framebuffer to userspace\r\n")
		return -7
	}

	// Register the framebuffer as a span to prevent mmap collisions
	if !addSpan(UserFramebufferVA, UserFramebufferSize) {
		console.KWriteString("[Launch] WARNING: Failed to register framebuffer span\r\n")
		// Not fatal - continue anyway
	}

	// Parse and load ELF (now using the fresh process page table)
	proc, err := loadELF(elfData, filename)
	if err != nil {
		console.KWriteString("[Launch] ERROR: Failed to load ELF: ")
		console.KWriteString(err.Error())
		console.KWriteString("\r\n")
		return -5
	}

	// Store process info
	currentProcess = proc

	console.KWriteString("[Launch] Entry point: ")
	console.KPrintHex64(proc.EntryPoint)
	console.KWriteString("\r\n")

	// Verify entry point code was loaded correctly
	// Expected at 0x82fa0: e0 03 40 f9 e1 23 00 91 (ldr x0,[sp]; add x1,sp,#8)
	console.KWriteString("[Launch] Code@0x82fa0: ")
	for i := uintptr(0); i < 8; i++ {
		b, ok := kmem.ReadUserByte(0x82fa0 + i)
		if ok {
			console.KPrintHex64(uint64(b))
			console.KWriteString(" ")
		}
	}
	console.KWriteString("(expect: e0 03 40 f9 e1 23 00 91)\r\n")

	console.KWriteString("[Launch] Stack: ")
	console.KPrintHex64(proc.StackBase)
	console.KWriteString(" - ")
	console.KPrintHex64(proc.StackTop)
	console.KWriteString("\r\n")

	console.KWriteString("[Launch] Jumping to userspace...\r\n")

	// Final I-cache invalidation before userspace - ensure all loaded code is visible
	kmem.InvalidateAllICache()

	// DEBUG: Verify TTBR0 matches our internal tracking
	hwTTBR0 := kmem.ReadHWTTBR0()
	swTTBR0 := kmem.GetTTBR0L0PA()
	console.KWriteString("[Launch] TTBR0 HW=")
	console.KPrintHex64(uint64(hwTTBR0))
	console.KWriteString(" SW=")
	console.KPrintHex64(uint64(swTTBR0))
	if hwTTBR0 != swTTBR0 {
		console.KWriteString(" *** MISMATCH! ***")
	}
	console.KWriteString("\r\n")

	// Verify mPaddedSizeclass is still correct
	const mPaddedAddr = uintptr(0x180BC8)
	// Walk the page table to get the PA
	pa := kmem.WalkUserPageTable(mPaddedAddr)
	console.KWriteString("[Launch] VA ")
	console.KPrintHex64(uint64(mPaddedAddr))
	console.KWriteString(" -> PA ")
	console.KPrintHex64(uint64(pa))

	console.KWriteString("\r\n")

	// Read bytes around mPaddedSizeclass to verify data layout
	console.KWriteString("[Launch] mPadded@180BC0: ")
	for i := uintptr(0); i < 16; i++ {
		if b, ok := kmem.ReadUserByte(0x180BC0 + i); ok {
			console.KPrintHex64(uint64(b))
			console.KWriteString(" ")
		}
	}
	console.KWriteString("\r\n")

	// Verify SizeClassToSize array at 0x180A00 (from nm build/priest.elf)
	// It's uint16 array, so read 2 bytes per entry
	// Print indices 0-15 to catch any corruption at index 8
	console.KWriteString("[Launch] SizeClassToSize[0..15] @0x180A00:\r\n")
	for i := uint64(0); i < 16; i++ {
		addr := uintptr(0x180A00 + i*2)
		lo, ok1 := kmem.ReadUserByte(addr)
		hi, ok2 := kmem.ReadUserByte(addr + 1)
		if ok1 && ok2 {
			val := uint16(lo) | (uint16(hi) << 8)
			console.KWriteString("  [")
			console.KPrintHex64(i)
			console.KWriteString("] = ")
			console.KPrintHex64(uint64(val))
			console.KWriteString(" (")
			// Print decimal for clarity
			if val < 1000 {
				console.KPrintHex64(uint64(val))
			}
			console.KWriteString(")\r\n")
		}
	}

	// Verify SizeClassToSize[8] specifically - the error says sizeclass 8 gets 8 bytes
	// But SizeClassToSize[8] should be 96!
	console.KWriteString("[Launch] SizeClassToSize[8]@0x180A10 (scratch): ")
	lo, ok1 := kmem.ReadUserByte(0x180A10)
	hi, ok2 := kmem.ReadUserByte(0x180A11)
	if ok1 && ok2 {
		val := uint16(lo) | (uint16(hi) << 8)
		console.KPrintHex64(uint64(val))
	}
	console.KWriteString(" (expect: 0x60 = 96)\r\n")

	// Check SizeToSizeClass8 at 0x180960 (first 16 bytes)
	console.KWriteString("[Launch] SizeToSizeClass8[0..15]@0x180960: ")
	for i := uintptr(0); i < 16; i++ {
		b, ok := kmem.ReadUserByte(0x180960 + i)
		if ok {
			console.KPrintHex64(uint64(b))
			console.KWriteString(" ")
		}
	}
	console.KWriteString("\r\n")

	// Check SizeToSizeClass128 at 0x180C20 - this is what lockVerifyMSize reads!
	// The runtime does: ldrb w0, [x27, #3112] where x27 = 0x180000, so it reads 0x180C28
	console.KWriteString("[Launch] SizeToSizeClass128[0..15]@0x180C20: ")
	for i := uintptr(0); i < 16; i++ {
		b, ok := kmem.ReadUserByte(0x180C20 + i)
		if ok {
			console.KPrintHex64(uint64(b))
			console.KWriteString(" ")
		}
	}
	console.KWriteString("\r\n")

	// Specifically check the byte at 0x180C28 (index 8 into SizeToSizeClass128)
	console.KWriteString("[Launch] SizeToSizeClass128[8]@0x180C28 (scratch): ")
	if b, ok := kmem.ReadUserByte(0x180C28); ok {
		console.KPrintHex64(uint64(b))
	}
	console.KWriteString(" (expect: 0x26=38 decimal)\r\n")

	// Verify the ADRP instruction at 0x21DE8 (in lockVerifyMSize @ 0x21DD0)
	// Expected: f0000afb (ADRP x27, 180000) at 0x21DE8
	// Then:     3970a360 (ldrb w0, [x27, #3112]) at 0x21DEC
	console.KWriteString("[Launch] lockVerifyMSize@21DD0 instructions:\r\n")
	console.KWriteString("  @21DE8 (ADRP): ")
	for i := uintptr(0); i < 4; i++ {
		if b, ok := kmem.ReadUserByte(0x21DE8 + i); ok {
			console.KPrintHex64(uint64(b))
			console.KWriteString(" ")
		}
	}
	console.KWriteString("(expect: fb 0a 00 f0)\r\n")
	console.KWriteString("  @21DEC (LDRB): ")
	for i := uintptr(0); i < 4; i++ {
		if b, ok := kmem.ReadUserByte(0x21DEC + i); ok {
			console.KPrintHex64(uint64(b))
			console.KWriteString(" ")
		}
	}
	console.KWriteString("(expect: 60 a3 70 39)\r\n")

	// DEBUG: Print raw L3 PTE for VA 0x180000 (data segment page)
	console.KWriteString("[Launch] L3 PTE for VA 0x180000: ")
	l3pte := kmem.GetUserL3PTE(0x180000)
	console.KPrintHex64(l3pte)
	ptePA := l3pte & 0x0000FFFFFFFFF000
	console.KWriteString(" (PA=")
	console.KPrintHex64(ptePA)
	console.KWriteString(")\r\n")

	// CRITICAL DEBUG: Read from physical memory directly via QEMU
	// If the L3 PTE PA is correct, the data should be there
	console.KWriteString("[Launch] Reading SizeClassToSize[0..3] directly from PA ")
	console.KPrintHex64(ptePA + 0xA00)
	console.KWriteString(":\r\n")
	// Read via kernel scratch mapping
	scratchVA := kmem.MapPAToKernelScratch(uintptr(ptePA))
	if scratchVA != 0 {
		console.KWriteString("  via scratch: ")
		for i := uintptr(0); i < 8; i++ {
			b := *(*byte)(unsafe.Pointer(scratchVA + 0xA00 + i))
			console.KPrintHex64(uint64(b))
			console.KWriteString(" ")
		}
		console.KWriteString("\r\n")
	}

	// Also check L3 PTE for VA 0x1809A0 (SizeClassToSize) - should be same page
	console.KWriteString("[Launch] L3 PTE for VA 0x1809A0: ")
	l3pte2 := kmem.GetUserL3PTE(0x1809A0)
	console.KPrintHex64(l3pte2)
	console.KWriteString(" (should match above since same 4KB page)\r\n")

	// Enable userspace mmap allocator before jumping to userspace
	SetUserspaceActive()

	// CRITICAL: Final cache and TLB maintenance before userspace jump
	// This ensures all written data is visible to userspace instruction fetch and data access
	console.KWriteString("[Launch] Final cache/TLB sync before ERET...\r\n")
	kmem.FinalUserspaceSync()

	// POST-SYNC VERIFICATION: Re-read data after FinalUserspaceSync to confirm it's visible
	console.KWriteString("[Launch] POST-SYNC verification of SizeClassToSize:\r\n")
	// Get a fresh scratch mapping
	postPA := kmem.WalkUserPageTable(0x180000)
	console.KWriteString("  PA for VA 0x180000: ")
	console.KPrintHex64(uint64(postPA))
	console.KWriteString("\r\n")

	postScratch := kmem.MapPAToKernelScratch(postPA &^ 0xFFF)
	if postScratch != 0 {
		console.KWriteString("  SizeClassToSize[0..7] via fresh scratch: ")
		for i := uintptr(0); i < 16; i++ {
			b := *(*byte)(unsafe.Pointer(postScratch + 0xA00 + i))
			console.KPrintHex64(uint64(b))
			console.KWriteString(" ")
		}
		console.KWriteString("\r\n")

		// Specifically check index 8 (at offset 16-17)
		lo := *(*byte)(unsafe.Pointer(postScratch + 0xA10))
		hi := *(*byte)(unsafe.Pointer(postScratch + 0xA11))
		val := uint16(lo) | uint16(hi)<<8
		console.KWriteString("  SizeClassToSize[8] = ")
		console.KPrintHex64(uint64(val))
		console.KWriteString(" (expect 96=0x60)\r\n")

		// CRITICAL: Check SizeClassToSize[38] - THIS IS WHAT PRIEST READS WRONG!
		// SizeClassToSize[38] is at offset 38*2 = 76 = 0x4C from base 0xA00
		// So at scratch offset 0xA4C
		lo38 := *(*byte)(unsafe.Pointer(postScratch + 0xA4C))
		hi38 := *(*byte)(unsafe.Pointer(postScratch + 0xA4D))
		val38 := uint16(lo38) | uint16(hi38)<<8
		console.KWriteString("  SizeClassToSize[38]@0x180A4C = ")
		console.KPrintHex64(uint64(val38))
		console.KWriteString(" (expect 2048=0x800) lo=")
		console.KPrintHex64(uint64(lo38))
		console.KWriteString(" hi=")
		console.KPrintHex64(uint64(hi38))
		console.KWriteString("\r\n")

		// Check SizeToSizeClass128[8]
		sc128 := *(*byte)(unsafe.Pointer(postScratch + 0xC28))
		console.KWriteString("  SizeToSizeClass128[8] = ")
		console.KPrintHex64(uint64(sc128))
		console.KWriteString(" (expect 38=0x26)\r\n")
	}

	// CRITICAL: Switch TTBR0 to the process-specific page table before ERET.
	// This ensures priest runs with a clean address space, not Cardinal's leftover mappings.
	kmem.SwitchToProcessPageTable()

	// FINAL VERIFICATION: Read from the PHYSICAL ADDRESS directly via scratch mapping
	// to confirm the data is absolutely correct at the moment before ERET
	console.KWriteString("[Launch] FINAL physical memory check at PA 0x4513AC28:\r\n")
	finalPA := kmem.WalkUserPageTable(0x180C28)
	console.KWriteString("  VA 0x180C28 -> PA ")
	console.KPrintHex64(uint64(finalPA))
	if finalPA != 0 {
		finalScratch := kmem.MapPAToKernelScratch(finalPA &^ 0xFFF)
		if finalScratch != 0 {
			finalVal := *(*byte)(unsafe.Pointer(finalScratch + (finalPA & 0xFFF)))
			console.KWriteString(" value=")
			console.KPrintHex64(uint64(finalVal))
			console.KWriteString(" (MUST be 0x26=38)")
		}
	}
	console.KWriteString("\r\n")

	// Also verify a few surrounding bytes
	console.KWriteString("  Surrounding bytes at PA: ")
	if finalPA != 0 {
		finalScratch := kmem.MapPAToKernelScratch(finalPA &^ 0xFFF)
		if finalScratch != 0 {
			for i := uintptr(0); i < 16; i++ {
				b := *(*byte)(unsafe.Pointer(finalScratch + (finalPA&^0xFFF&0xFFF) + 0xC20 + i))
				console.KPrintHex64(uint64(b))
				console.KWriteString(" ")
			}
		}
	}
	console.KWriteString("\r\n")

	// CRITICAL: Enable timer IRQ before jumping to userspace
	// This ensures preemption works for the priest running at EL0
	EnableTimerIRQ()
	console.KWriteString("[Launch] Timer IRQ enabled for userspace preemption\r\n")

	// Jump to userspace (this does not return)
	jumpToUserspace(proc.EntryPoint, proc.StackTop)

	// Should never reach here
	return 0
}

// loadELF parses an ELF file and loads it into memory
// filename is passed through to setupUserStack for argv[0]
func loadELF(data []byte, filename string) (*Process, error) {
	if len(data) < 64 {
		return nil, &elfError{"file too small for ELF header"}
	}

	// Parse ELF header
	hdr := parseELFHeader(data)

	// Validate ELF magic
	if hdr.Magic != ELF_MAGIC {
		return nil, &elfError{"invalid ELF magic"}
	}

	// Validate architecture
	if hdr.Class != ELF_CLASS64 || hdr.Machine != ELF_MACHINE_AARCH64 {
		return nil, &elfError{"not an ARM64 ELF"}
	}

	console.KWriteString("[ELF] Valid ARM64 ELF64, entry=")
	console.KPrintHex64(hdr.Entry)
	console.KWriteString(", phnum=")
	console.KPrintHex64(uint64(hdr.Phnum))
	console.KWriteString("\r\n")

	// Process program headers and load segments
	for i := uint16(0); i < hdr.Phnum; i++ {
		phdrOffset := hdr.Phoff + uint64(i)*uint64(hdr.Phentsize)
		if phdrOffset+uint64(hdr.Phentsize) > uint64(len(data)) {
			return nil, &elfError{"program header out of bounds"}
		}

		phdr := parseProgramHeader(data[phdrOffset:])

		// Only process LOAD segments
		if phdr.Type != PT_LOAD {
			continue
		}

		console.KWriteString("[ELF] LOAD segment: vaddr=")
		console.KPrintHex64(phdr.Vaddr)
		console.KWriteString(" memsz=")
		console.KPrintHex64(phdr.Memsz)
		console.KWriteString(" filesz=")
		console.KPrintHex64(phdr.Filesz)
		console.KWriteString(" flags=")
		console.KPrintHex64(uint64(phdr.Flags))
		console.KWriteString("\r\n")

		// Load this segment into memory
		if err := loadSegment(data, &phdr); err != nil {
			return nil, err
		}
	}

	// Allocate a user stack (64KB at a fixed location for now)
	// Stack grows downward from high addresses to low addresses.
	// Stack region: 0x00007FFF00000000 - 0x00007FFF0000FFFF (mapped pages)
	// Located near the top of userspace VA to leave room for mmap below.
	stackBase := uint64(0x00007FFF00000000)
	stackSize := uint64(64 * 1024) // 64KB

	console.KWriteString("[ELF] Allocating user stack at ")
	console.KPrintHex64(stackBase)
	console.KWriteString("\r\n")

	if err := allocateUserStack(stackBase, stackSize); err != nil {
		return nil, err
	}

	// Set up the stack with argc, argv, envp, and auxv
	// This returns the final SP value pointing to argc on the stack
	stackTop, err := setupUserStack(stackBase, stackSize, filename)
	if err != nil {
		return nil, err
	}

	console.KWriteString("[ELF] Stack setup complete, SP=")
	console.KPrintHex64(stackTop)
	console.KWriteString("\r\n")

	// Debug: verify first few bytes at key addresses
	// For helloworld.maz: class_to_size @ 0x1809A0, mPaddedSizeclass @ 0x180BC8
	console.KWriteString("[ELF] class_to_size[0..3] at 0x1809A0: ")
	for i := uint64(0); i < 8; i++ {
		b, _ := kmem.ReadUserByte(uintptr(0x1809A0 + i))
		console.KPrintHex64(uint64(b))
		console.KWriteString(" ")
	}
	console.KWriteString("\r\n")

	console.KWriteString("[ELF] mPaddedSizeclass at 0x180BC8 (scratch): ")
	mps, _ := kmem.ReadUserByte(uintptr(0x180BC8))
	console.KPrintHex64(uint64(mps))
	console.KWriteString("\r\n")

	// Also verify class_to_size[38] at 0x1809EC
	console.KWriteString("[ELF] class_to_size[38] at 0x1809EC (scratch): ")
	lo, _ := kmem.ReadUserByte(uintptr(0x1809EC))
	hi, _ := kmem.ReadUserByte(uintptr(0x1809ED))
	console.KPrintHex64(uint64(lo) | (uint64(hi) << 8))
	console.KWriteString("\r\n")

	// Check ADRP instruction at 0x21d68 - should encode target 0x180000
	// Expected bytes: fb 0a 00 f0 (ADRP x27, 180000)
	console.KWriteString("[ELF] ADRP at 0x21d68: ")
	for i := uint64(0); i < 4; i++ {
		b, _ := kmem.ReadUserByte(uintptr(0x21d68 + i))
		console.KPrintHex64(uint64(b))
		console.KWriteString(" ")
	}
	console.KWriteString("(expect: fb 0a 00 f0)\r\n")

	// Check ldrb instruction at 0x21d6c - loads mPaddedSizeclass from [x27, #3016]
	// Expected bytes: 60 23 6f 39 (ldrb w0, [x27, #3016])
	console.KWriteString("[ELF] LDRB at 0x21d6c: ")
	for i := uint64(0); i < 4; i++ {
		b, _ := kmem.ReadUserByte(uintptr(0x21d6c + i))
		console.KPrintHex64(uint64(b))
		console.KWriteString(" ")
	}
	console.KWriteString("(expect: 60 23 6f 39, reads from x27+3016=x27+0xBC8)\r\n")

	// Check class_to_size table address calculation at 0x21d7c-0x21d83
	// ADRP x1, 180000 then ADD x1, x1, #0x9a0
	console.KWriteString("[ELF] class_to_size calc at 0x21d7c: ")
	for i := uint64(0); i < 8; i++ {
		b, _ := kmem.ReadUserByte(uintptr(0x21d7c + i))
		console.KPrintHex64(uint64(b))
		console.KWriteString(" ")
	}
	console.KWriteString("(expect: e1 0a 00 f0 21 80 26 91 = ADRP x1,180000; ADD x1,x1,#0x9a0)\r\n")

	return &Process{
		EntryPoint: hdr.Entry,
		StackTop:   stackTop,
		StackBase:  stackBase,
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

// loadSegment loads a single ELF segment into memory
// Uses kernel scratch mapping to copy data since kernel can't directly access
// userspace pages (PAN/permission restrictions on ARM64).
//
// IMPORTANT: Uses AllocAndMapUserPage which zeros all pages before use.
// This ensures any padding within pages is zero, not garbage data.
func loadSegment(elfData []byte, phdr *elf64Phdr) error {
	if phdr.Memsz == 0 {
		return nil
	}

	// Calculate page-aligned boundaries
	pageSize := uint64(4096)
	startPage := phdr.Vaddr &^ (pageSize - 1)
	endAddr := phdr.Vaddr + phdr.Memsz
	endPage := (endAddr + pageSize - 1) &^ (pageSize - 1)
	numPages := (endPage - startPage) / pageSize

	// Track physical addresses for each page so we can remap scratch VA later
	// AllocAndMapUserPage returns (framePA, scratchVA)
	pagePAs := make([]uintptr, numPages)

	// Allocate, map, and ZERO pages for userspace
	for page := uint64(0); page < numPages; page++ {
		pageVA := startPage + page*pageSize

		// AllocAndMapUserPage allocates from userspace pool, maps to user VA,
		// maps to kernel scratch VA, and zeros the page using DC ZVA.
		// Returns the framePA for later scratch remapping.
		framePA, _ := kmem.AllocAndMapUserPage(uintptr(pageVA), phdr.Flags)
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

	// Clean the data cache for all pages we wrote to.
	// This ensures the data is visible to userspace when it reads via TTBR0.
	// Without this, userspace may see stale/garbage data due to cache coherency.
	isExecutable := (phdr.Flags & PF_X) != 0
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
// Uses AllocAndMapUserPage which zeroes pages via DC ZVA
func allocateUserStack(base, size uint64) error {
	pageSize := uint64(4096)
	numPages := size / pageSize

	for page := uint64(0); page < numPages; page++ {
		pageVA := base + page*pageSize
		// Stack is RW (no execute)
		// AllocAndMapUserPage allocates, maps, and zeros the page using DC ZVA
		framePA, scratchVA := kmem.AllocAndMapUserPage(uintptr(pageVA), PF_R|PF_W)
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
// Stack layout (from high to low addresses):
//   [random bytes - 16 bytes for AT_RANDOM]
//   [program name string - null terminated]
//   [padding for 16-byte alignment]
//   [auxv entries - type/value pairs ending with AT_NULL]
//   [NULL - end of envp]
//   [NULL - end of argv]
//   [argv[0] - pointer to program name]
//   [argc = 1]
//   <- SP points here
func setupUserStack(stackBase, stackSize uint64, filename string) (uint64, error) {
	pageSize := uint64(4096)
	stackTop := stackBase + stackSize

	// Get the physical address of the top stack page
	topPageVA := (stackTop - 1) &^ (pageSize - 1) // Round down to page boundary
	topPA := kmem.WalkUserPageTable(uintptr(topPageVA))
	if topPA == 0 {
		return 0, &elfError{"stack page not mapped"}
	}

	// Map the stack page to kernel scratch for writing
	kernelVA := kmem.MapPAToKernelScratch(topPA &^ uintptr(pageSize-1))
	if kernelVA == 0 {
		return 0, &elfError{"failed to map stack to kernel scratch"}
	}

	// Start writing from the top of the stack
	sp := stackTop

	// 1. Write 16 random bytes at the top (for AT_RANDOM)
	sp -= 16
	randomAddr := sp
	// Just use zeros for now (not cryptographically secure, but works for testing)
	for i := uint64(0); i < 16; i++ {
		writeStackByte(stackBase, stackTop, kernelVA, sp+i, 0)
	}

	// 2. Write the program name string
	nameBytes := []byte(filename)
	nameLen := uint64(len(nameBytes)) + 1 // +1 for null terminator
	sp -= nameLen
	nameAddr := sp
	for i := 0; i < len(nameBytes); i++ {
		writeStackByte(stackBase, stackTop, kernelVA, sp+uint64(i), nameBytes[i])
	}
	writeStackByte(stackBase, stackTop, kernelVA, sp+uint64(len(nameBytes)), 0) // null terminator

	// 3. Align SP to 16 bytes
	sp = sp &^ 15

	// 4. Build auxv, envp, argv, argc from top down
	// We'll calculate the positions and write them

	// auxv entries (Go runtime needs these, plus Mazzy-specific framebuffer info)
	auxvEntries := [][2]uint64{
		{6, pageSize},                            // AT_PAGESZ = 6
		{25, randomAddr},                         // AT_RANDOM = 25 (pointer to 16 random bytes)
		{AT_MAZZY_FB_ADDR, UserFramebufferVA},    // Framebuffer VA in priest space
		{AT_MAZZY_FB_WIDTH, uint64(gpu.GetWidth())},   // Framebuffer width
		{AT_MAZZY_FB_HEIGHT, uint64(gpu.GetHeight())}, // Framebuffer height
		{AT_MAZZY_FB_PITCH, uint64(gpu.GetWidth() * 4)}, // Pitch = width * 4 bytes per pixel
		{0, 0},                                   // AT_NULL = 0 (terminator)
	}

	// Calculate total size needed:
	// argc (8) + argv[0] (8) + NULL (8) + NULL (8) + auxv entries
	numAuxv := uint64(len(auxvEntries))
	totalSize := 8 + 8 + 8 + 8 + (numAuxv * 16) // argc + argv[0] + argv NULL + envp NULL + auxv

	// Make sure we have 16-byte alignment after adding everything
	sp -= totalSize
	sp = sp &^ 15

	// Now write the stack contents from low address (SP) to high
	offset := uint64(0)

	// argc = 1
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, 1)
	offset += 8

	// argv[0] = pointer to program name
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, nameAddr)
	offset += 8

	// NULL (end of argv)
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, 0)
	offset += 8

	// NULL (end of envp)
	writeStackU64(stackBase, stackTop, kernelVA, sp+offset, 0)
	offset += 8

	// auxv entries
	for _, entry := range auxvEntries {
		writeStackU64(stackBase, stackTop, kernelVA, sp+offset, entry[0])
		offset += 8
		writeStackU64(stackBase, stackTop, kernelVA, sp+offset, entry[1])
		offset += 8
	}

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

// jumpToUserspace performs the transition from kernel (EL1) to userspace (EL0)
// This is implemented in assembly
func jumpToUserspace(entryPoint, stackPtr uint64)

// EnableTimerIRQ is provided by main package via go:linkname
// Enables the timer IRQ for preemption before jumping to userspace
//
//go:linkname EnableTimerIRQ main.EnableTimerIRQ
func EnableTimerIRQ()

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

// unsafePointer is a helper to convert uintptr to pointer
//go:linkname unsafePointer runtime.noescape
func unsafePointer(p uintptr) *byte

// elfError represents an ELF parsing error
type elfError struct {
	msg string
}

func (e *elfError) Error() string {
	return "elf: " + e.msg
}
