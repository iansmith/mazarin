// diplomat/main/elf_loader.go
// ELF64 loader for loading kmazarin kernel
// Parses ELF manually to avoid Go heap allocations (Go runtime heap not available)
package main

import (
	"mazzy/shared/fs/fat32"
	"unsafe"
)

// LoadedKernel is bootloader.LoadedKernel (re-exported via type alias in platform.go)

// Global buffer for file reading (avoid allocation)
var elfReadBuf [4096]byte

// ELF64 header constants
const (
	elfMagic       = 0x464C457F // "\x7FELF"
	elfClass64     = 2
	elfDataLSB     = 1
	elfTypeExec    = 2
	elfTypeDyn     = 3
	elfMachineX64    = 0x3E
	elfMachineARM64 = 0xB7
	elfPTLoad        = 1
	elfEhdrSize    = 64 // ELF64 header size
	elfPhdrSize    = 56 // ELF64 program header size
)

// elf64Ehdr is the ELF64 file header (64 bytes)
type elf64Ehdr struct {
	Ident     [16]byte
	Type      uint16
	Machine   uint16
	Version   uint32
	Entry     uint64
	Phoff     uint64 // Program header table offset
	Shoff     uint64
	Flags     uint32
	Ehsize    uint16
	Phentsize uint16
	Phnum     uint16
	Shentsize uint16
	Shnum     uint16
	Shstrndx  uint16
}

// elf64Phdr is the ELF64 program header (56 bytes)
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

// LoadKernel loads an ELF kernel from the filesystem into physical memory.
func LoadKernel(fsys *fat32.FileSystem, path string) (*LoadedKernel, error) {
	debugPortOut('a')
	file, err := findFile(fsys, path)
	debugPortOut('b')
	if err != nil {
		return nil, err
	}
	debugPortOut('c')
	printString("Kernel file found\r\n")

	// Read ELF header
	var ehdr elf64Ehdr
	n, err := readFileAt(fsys, file, 0, (*[elfEhdrSize]byte)(unsafe.Pointer(&ehdr))[:])
	if err != nil || n < elfEhdrSize {
		return nil, &blockDevError{"failed to read ELF header"}
	}
	debugPortOut('d')

	// Validate ELF
	magic := *(*uint32)(unsafe.Pointer(&ehdr.Ident[0]))
	if magic != elfMagic {
		return nil, &blockDevError{"not an ELF file"}
	}
	if ehdr.Ident[4] != elfClass64 || ehdr.Ident[5] != elfDataLSB {
		return nil, &blockDevError{"not ELF64 little-endian"}
	}
	if ehdr.Machine != elfMachineExpected {
		return nil, &blockDevError{"ELF machine type mismatch"}
	}
	debugPortOut('e')

	printString("ELF: entry=")
	printHex(ehdr.Entry)
	printString(" phdrs=")
	printHex(uint64(ehdr.Phnum))
	printString("\r\n")

	// Read program headers
	if ehdr.Phnum > 32 {
		return nil, &blockDevError{"too many program headers"}
	}
	var phdrs [32]elf64Phdr
	phdrBytes := int(ehdr.Phnum) * elfPhdrSize
	n, err = readFileAt(fsys, file, ehdr.Phoff, (*[32 * elfPhdrSize]byte)(unsafe.Pointer(&phdrs[0]))[:phdrBytes])
	if err != nil || n < phdrBytes {
		return nil, &blockDevError{"failed to read program headers"}
	}
	debugPortOut('f')

	// Pass 1: Find virtual address range
	lowestVirt := uint64(0xFFFFFFFFFFFFFFFF)
	highestVirt := uint64(0)
	for i := uint16(0); i < ehdr.Phnum; i++ {
		ph := &phdrs[i]
		if ph.Type != elfPTLoad || ph.Memsz == 0 {
			continue
		}
		if ph.Vaddr < lowestVirt {
			lowestVirt = ph.Vaddr
		}
		end := ph.Vaddr + ph.Memsz
		if end > highestVirt {
			highestVirt = end
		}
	}
	if lowestVirt >= highestVirt {
		return nil, &blockDevError{"no LOAD segments"}
	}
	debugPortOut('g')

	printString("ELF: virt=")
	printHex(lowestVirt)
	printString("-")
	printHex(highestVirt)
	printString("\r\n")

	// Allocate physical memory (extra 2MB for alignment)
	allocSize := uint64(DefaultKernelMemSize) + Page2MBSize
	physPages := allocSize / PageSize
	rawPhys, err := allocatePhysPages(physPages)
	if err != nil {
		return nil, err
	}
	// Align up to 2MB boundary for 2MB page table entries
	physBase := (rawPhys + Page2MBSize - 1) &^ (Page2MBSize - 1)
	debugPortOut('h')

	// Zero the physical region
	plat.ZeroMemory(physBase, DefaultKernelMemSize)
	debugPortOut('i')

	// Pass 2: Load segments
	for i := uint16(0); i < ehdr.Phnum; i++ {
		ph := &phdrs[i]
		if ph.Type != elfPTLoad || ph.Memsz == 0 {
			continue
		}
		physDest := physBase + (ph.Vaddr - lowestVirt)
		if ph.Filesz > 0 {
			err = copySegmentToMemory(fsys, file, ph.Offset, physDest, ph.Filesz)
			if err != nil {
				return nil, err
			}
		}
	}
	debugPortOut('j')

	result := dNew[LoadedKernel]()
	if result == nil {
		return nil, &blockDevError{"allocation failed"}
	}
	result.Entry = ehdr.Entry
	result.LowestVirt = lowestVirt
	result.HighestVirt = highestVirt
	result.PhysBase = physBase

	printString("Kernel loaded OK\r\n")
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
