// diplomat/main/elf_loader.go
// ELF64 loader for loading kmazarin kernel
package main

import (
	"mazzy/shared/fs/fat32"
	"unsafe"
)

// ELF64 header constants
const (
	ELF_MAGIC      = 0x464C457F // "\x7FELF"
	ELFCLASS64     = 2
	ELFDATA2LSB    = 1 // Little endian
	EM_X86_64      = 62
	PT_LOAD        = 1
	PT_NULL        = 0
	PF_X           = 1 // Executable
	PF_W           = 2 // Writable
	PF_R           = 4 // Readable
)

// ELF64 header structure (64 bytes)
type Elf64Ehdr struct {
	Ident     [16]byte // Magic number and other info
	Type      uint16   // Object file type
	Machine   uint16   // Architecture
	Version   uint32   // Object file version
	Entry     uint64   // Entry point virtual address
	Phoff     uint64   // Program header table file offset
	Shoff     uint64   // Section header table file offset
	Flags     uint32   // Processor-specific flags
	Ehsize    uint16   // ELF header size
	Phentsize uint16   // Program header table entry size
	Phnum     uint16   // Program header table entry count
	Shentsize uint16   // Section header table entry size
	Shnum     uint16   // Section header table entry count
	Shstrndx  uint16   // Section header string table index
}

// ELF64 program header structure (56 bytes)
type Elf64Phdr struct {
	Type   uint32 // Segment type
	Flags  uint32 // Segment flags
	Offset uint64 // Segment file offset
	Vaddr  uint64 // Segment virtual address
	Paddr  uint64 // Segment physical address
	Filesz uint64 // Segment size in file
	Memsz  uint64 // Segment size in memory
	Align  uint64 // Segment alignment
}

// LoadedKernel contains information about the loaded kernel
type LoadedKernel struct {
	Entry       uint64   // Entry point address
	LowestAddr  uint64   // Lowest loaded address
	HighestAddr uint64   // Highest loaded address (exclusive)
}

// Global buffer for file reading (avoid allocation)
var elfReadBuf [4096]byte

// LoadKernel loads an ELF kernel from the filesystem into memory
func LoadKernel(fs *fat32.FileSystem, path string) (*LoadedKernel, error) {
	// Find the kernel file
	file, err := findFile(fs, path)
	if err != nil {
		return nil, err
	}

	// Read ELF header
	n, err := readFileAt(fs, file, 0, elfReadBuf[:64])
	if err != nil || n < 64 {
		return nil, &blockDevError{"failed to read ELF header"}
	}

	// Parse ELF header
	ehdr := (*Elf64Ehdr)(unsafe.Pointer(&elfReadBuf[0]))

	// Validate magic
	magic := uint32(ehdr.Ident[0]) | uint32(ehdr.Ident[1])<<8 | uint32(ehdr.Ident[2])<<16 | uint32(ehdr.Ident[3])<<24
	if magic != ELF_MAGIC {
		return nil, &blockDevError{"invalid ELF magic"}
	}

	// Validate class (64-bit)
	if ehdr.Ident[4] != ELFCLASS64 {
		return nil, &blockDevError{"not ELF64"}
	}

	// Validate endianness (little)
	if ehdr.Ident[5] != ELFDATA2LSB {
		return nil, &blockDevError{"not little endian"}
	}

	// Validate machine (x86_64)
	if ehdr.Machine != EM_X86_64 {
		return nil, &blockDevError{"not x86_64"}
	}

	// Save header values BEFORE we reuse elfReadBuf for program headers
	// (elfReadBuf is used for both, so ehdr gets overwritten on each phdr read)
	entryPoint := ehdr.Entry
	phoff := ehdr.Phoff
	phentsize := uint64(ehdr.Phentsize)
	phnum := ehdr.Phnum

	// Load program headers and segments - use bump allocator
	result := dNew[LoadedKernel]()
	if result == nil {
		return nil, &blockDevError{"allocation failed"}
	}
	result.Entry = entryPoint
	result.LowestAddr = 0xFFFFFFFFFFFFFFFF
	result.HighestAddr = 0

	for i := uint16(0); i < phnum; i++ {
		phdrOffset := phoff + uint64(i)*phentsize

		// Read program header
		n, err := readFileAt(fs, file, phdrOffset, elfReadBuf[:56])
		if err != nil || n < 56 {
			return nil, &blockDevError{"failed to read program header"}
		}

		phdr := (*Elf64Phdr)(unsafe.Pointer(&elfReadBuf[0]))

		// Skip non-LOAD segments
		if phdr.Type != PT_LOAD || phdr.Memsz == 0 {
			continue
		}

		// Track memory range
		if phdr.Vaddr < result.LowestAddr {
			result.LowestAddr = phdr.Vaddr
		}
		endAddr := phdr.Vaddr + phdr.Memsz
		if endAddr > result.HighestAddr {
			result.HighestAddr = endAddr
		}

		// Zero the memory region first (for .bss)
		zeroMemory(phdr.Vaddr, phdr.Memsz)

		// Copy file data to memory
		if phdr.Filesz > 0 {
			err = copySegmentToMemory(fs, file, phdr.Offset, phdr.Vaddr, phdr.Filesz)
			if err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}

// findFile finds a file by path (e.g., "/EFI/Linux/kmazarin.elf")
// Currently hardcoded to find /EFI/Linux/kmazarin.elf
func findFile(fs *fat32.FileSystem, path string) (*SimpleFile, error) {
	// Start at root
	cluster := fs.RootCluster()

	// Find EFI directory
	entry, err := findInDir(fs, cluster, "EFI")
	if err != nil {
		return nil, err
	}
	if !entry.IsDir {
		return nil, &blockDevError{"EFI is not a directory"}
	}
	cluster = entry.Cluster

	// Find Linux directory
	entry, err = findInDir(fs, cluster, "LINUX")
	if err != nil {
		return nil, err
	}
	if !entry.IsDir {
		return nil, &blockDevError{"Linux is not a directory"}
	}
	cluster = entry.Cluster

	// Find kmazarin.elf
	entry, err = findInDir(fs, cluster, "KMAZARIN.ELF")
	if err != nil {
		return nil, err
	}

	// Use bump allocator - Go heap allocation crashes in UEFI
	sf := dNew[SimpleFile]()
	if sf == nil {
		return nil, &blockDevError{"dNew SimpleFile failed"}
	}
	sf.Cluster = entry.Cluster
	sf.Size = entry.Size
	return sf, nil
}

// SimpleFile represents a file for reading
type SimpleFile struct {
	Cluster uint32
	Size    uint32
}

// findInDir finds an entry in a directory by name (case-insensitive)
func findInDir(fs *fat32.FileSystem, cluster uint32, name string) (*SimpleDirEntry, error) {
	var found *SimpleDirEntry

	err := WalkDir(fs, cluster, func(e *SimpleDirEntry) bool {
		// Compare names (case-insensitive)
		if matchName(e, name) {
			// Use bump allocator - Go heap allocation crashes in UEFI
			found = dNew[SimpleDirEntry]()
			if found == nil {
				return false
			}
			*found = *e
			return false // stop walking
		}
		return true
	})

	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, &blockDevError{"file not found"}
	}
	return found, nil
}

// matchName compares a directory entry name with a target (case-insensitive)
func matchName(e *SimpleDirEntry, target string) bool {
	if int(e.NameLen) != len(target) {
		return false
	}
	for i := 0; i < len(target); i++ {
		a := e.Name[i]
		b := target[i]
		// Convert to uppercase for comparison
		if a >= 'a' && a <= 'z' {
			a -= 32
		}
		if b >= 'a' && b <= 'z' {
			b -= 32
		}
		if a != b {
			return false
		}
	}
	return true
}

// readFileAt reads from a file at a given offset
func readFileAt(fs *fat32.FileSystem, file *SimpleFile, offset uint64, buf []byte) (int, error) {
	if offset >= uint64(file.Size) {
		return 0, nil
	}

	bytesPerClus := uint64(fs.BytesPerCluster())
	cluster := file.Cluster
	clusterOffset := offset / bytesPerClus

	// Skip to the right cluster
	for i := uint64(0); i < clusterOffset; i++ {
		next, err := fs.ReadFATEntry(cluster)
		if err != nil {
			return 0, err
		}
		if fat32.IsEOF(next) {
			return 0, nil
		}
		cluster = next
	}

	// Read data
	inClusterOffset := offset % bytesPerClus
	totalRead := 0
	remaining := len(buf)

	for remaining > 0 && cluster >= 2 && !fat32.IsEOF(cluster) {
		// Read cluster into temp buffer
		if err := fs.ReadCluster(cluster, dirClusterBuf[:bytesPerClus]); err != nil {
			return totalRead, err
		}

		// Copy from cluster to output buffer
		toCopy := int(bytesPerClus - inClusterOffset)
		if toCopy > remaining {
			toCopy = remaining
		}
		if uint64(totalRead)+uint64(toCopy) > uint64(file.Size)-offset {
			toCopy = int(uint64(file.Size) - offset - uint64(totalRead))
		}
		if toCopy <= 0 {
			break
		}

		copy(buf[totalRead:], dirClusterBuf[inClusterOffset:inClusterOffset+uint64(toCopy)])
		totalRead += toCopy
		remaining -= toCopy
		inClusterOffset = 0

		// Next cluster
		next, err := fs.ReadFATEntry(cluster)
		if err != nil {
			return totalRead, err
		}
		cluster = next
	}

	return totalRead, nil
}

// copySegmentToMemory copies file data to a memory address
func copySegmentToMemory(fs *fat32.FileSystem, file *SimpleFile, fileOffset, memAddr, size uint64) error {
	dest := unsafe.Pointer(uintptr(memAddr))
	remaining := size
	offset := fileOffset

	for remaining > 0 {
		toRead := uint64(len(elfReadBuf))
		if toRead > remaining {
			toRead = remaining
		}

		n, err := readFileAt(fs, file, offset, elfReadBuf[:toRead])
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}

		// Copy to destination memory
		copyToMem(dest, elfReadBuf[:n])
		dest = unsafe.Pointer(uintptr(dest) + uintptr(n))
		offset += uint64(n)
		remaining -= uint64(n)
	}

	return nil
}

// copyToMem copies bytes to a memory address
func copyToMem(dest unsafe.Pointer, src []byte) {
	for i := 0; i < len(src); i++ {
		*(*byte)(unsafe.Pointer(uintptr(dest) + uintptr(i))) = src[i]
	}
}

// zeroMemory zeros a memory region
func zeroMemory(addr, size uint64) {
	ptr := unsafe.Pointer(uintptr(addr))
	for i := uint64(0); i < size; i++ {
		*(*byte)(unsafe.Pointer(uintptr(ptr) + uintptr(i))) = 0
	}
}

// JumpToKernel transfers control to the loaded kernel
// This should be called after ExitBootServices
func JumpToKernel(entry uint64) {
	jumpToEntry(entry)
}

// jumpToEntry is implemented in assembly
func jumpToEntry(entry uint64)
