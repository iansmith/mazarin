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
	elfShdrSize    = 64 // ELF64 section header size
	elfSymSize     = 24 // ELF64 symbol table entry size

	// Section header types
	elfSHT_SYMTAB = 2  // Symbol table
	elfSHT_STRTAB = 3  // String table

	// Symbol binding/type
	elfSTT_FUNC = 2
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

// elf64Shdr is the ELF64 section header (64 bytes)
type elf64Shdr struct {
	Name      uint32
	Type      uint32
	Flags     uint64
	Addr      uint64
	Offset    uint64
	Size      uint64
	Link      uint32 // For SYMTAB: index of associated STRTAB section
	Info      uint32
	Addralign uint64
	Entsize   uint64
}

// elf64Sym is the ELF64 symbol table entry (24 bytes)
type elf64Sym struct {
	Name  uint32 // Index into string table
	Info  byte   // Type and binding
	Other byte
	Shndx uint16
	Value uint64
	Size  uint64
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

	// Build cluster map for O(1) random access (critical for symbol table scanning)
	buildClusterMap(fsys, file)

	// Read ELF header
	var ehdr elf64Ehdr
	n, err := readFileAt(fsys, file, 0, (*[elfEhdrSize]byte)(unsafe.Pointer(&ehdr))[:])
	if err != nil || n < elfEhdrSize {
		return nil, &errFailedReadELFHeader
	}
	debugPortOut('d')

	// Validate ELF
	magic := *(*uint32)(unsafe.Pointer(&ehdr.Ident[0]))
	if magic != elfMagic {
		return nil, &errNotAnELF
	}
	if ehdr.Ident[4] != elfClass64 || ehdr.Ident[5] != elfDataLSB {
		return nil, &errNotELF64LE
	}
	if ehdr.Machine != elfMachineExpected {
		return nil, &errMachineTypeMismatch
	}
	debugPortOut('e')

	printString("ELF: entry=")
	printHex(ehdr.Entry)
	printString(" phdrs=")
	printHex(uint64(ehdr.Phnum))
	printString("\r\n")

	// Read program headers
	if ehdr.Phnum > 32 {
		return nil, &errTooManyProgramHeaders
	}
	var phdrs [32]elf64Phdr
	phdrBytes := int(ehdr.Phnum) * elfPhdrSize
	n, err = readFileAt(fsys, file, ehdr.Phoff, (*[32 * elfPhdrSize]byte)(unsafe.Pointer(&phdrs[0]))[:phdrBytes])
	if err != nil || n < phdrBytes {
		return nil, &errFailedReadProgramHeaders
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
		return nil, &errNoLOADSegments
	}
	debugPortOut('g')

	printString("ELF: virt=")
	printHex(lowestVirt)
	printString("-")
	printHex(highestVirt)
	printString("\r\n")

	// Allocate physical memory (extra 2MB for alignment)
	printString("ELF: allocating memory...\r\n")
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
	printString("ELF: zeroing ")
	printHex(DefaultKernelMemSize)
	printString(" @ ")
	printHex(physBase)
	printString("\r\n")
	plat.ZeroMemory(physBase, DefaultKernelMemSize)
	debugPortOut('i')

	// Pass 2: Load segments
	printString("ELF: loading segments\r\n")
	for i := uint16(0); i < ehdr.Phnum; i++ {
		ph := &phdrs[i]
		if ph.Type != elfPTLoad || ph.Memsz == 0 {
			continue
		}
		physDest := physBase + (ph.Vaddr - lowestVirt)
		printString("  seg[")
		printHex(uint64(i))
		printString("] off=")
		printHex(ph.Offset)
		printString(" dest=")
		printHex(physDest)
		printString(" fsz=")
		printHex(ph.Filesz)
		printString(" msz=")
		printHex(ph.Memsz)
		printString("\r\n")
		if ph.Filesz > 0 {
			err = copySegmentToMemory(fsys, file, ph.Offset, physDest, ph.Filesz)
			if err != nil {
				return nil, err
			}
			printString("  seg[")
			printHex(uint64(i))
			printString("] done\r\n")
		}
	}
	debugPortOut('j')

	result := dNew[LoadedKernel]()
	if result == nil {
		return nil, &errAllocationFailed
	}
	result.Entry = ehdr.Entry
	result.LowestVirt = lowestVirt
	result.HighestVirt = highestVirt
	result.PhysBase = physBase

	// Extract important symbols from the ELF symbol table
	extractSymbols(fsys, file, &ehdr, result)

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
		return nil, &errEFINotDir
	}
	cluster = entry.Cluster

	// Find Linux directory
	entry, err = findInDir(fs, cluster, "LINUX")
	if err != nil {
		return nil, err
	}
	if !entry.IsDir {
		return nil, &errLinuxNotDir
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
		return nil, &errDNewSimpleFileFailed
	}
	sf.Cluster = entry.Cluster
	sf.Size = entry.Size
	return sf, nil
}

// SimpleFile represents a file for reading
type SimpleFile struct {
	Cluster    uint32
	Size       uint32
	HasMap     bool   // true if clusterMap is populated
	MapLen     uint32 // number of entries in cluster map
}

// clusterMap caches the FAT32 cluster chain for a file.
// Supports files up to 1024 clusters (4MB with 4KB clusters).
var clusterMap [1024]uint32

// buildClusterMap walks the FAT32 cluster chain once and stores all cluster
// numbers, enabling O(1) random access to any part of the file.
func buildClusterMap(fs *fat32.FileSystem, file *SimpleFile) {
	bytesPerClus := uint64(fs.BytesPerCluster())
	numClusters := (uint64(file.Size) + bytesPerClus - 1) / bytesPerClus
	if numClusters > uint64(len(clusterMap)) {
		return // file too large for our map
	}

	cluster := file.Cluster
	for i := uint64(0); i < numClusters && cluster >= 2 && !fat32.IsEOF(cluster); i++ {
		clusterMap[i] = cluster
		file.MapLen = uint32(i + 1)
		next, err := fs.ReadFATEntry(cluster)
		if err != nil {
			return
		}
		cluster = next
	}
	file.HasMap = true
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
		return nil, &errFileNotFound
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
	clusterIdx := offset / bytesPerClus

	var cluster uint32
	if file.HasMap && uint32(clusterIdx) < file.MapLen {
		// O(1) indexed access via cached cluster map
		cluster = clusterMap[clusterIdx]
	} else {
		// Fallback: walk the chain from the beginning
		cluster = file.Cluster
		for i := uint64(0); i < clusterIdx; i++ {
			next, err := fs.ReadFATEntry(cluster)
			if err != nil {
				return 0, err
			}
			if fat32.IsEOF(next) {
				return 0, nil
			}
			cluster = next
		}
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
		clusterIdx++

		// Next cluster
		if file.HasMap && uint32(clusterIdx) < file.MapLen {
			cluster = clusterMap[clusterIdx]
		} else {
			next, err := fs.ReadFATEntry(cluster)
			if err != nil {
				return totalRead, err
			}
			cluster = next
		}
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

// Symbols we want to extract from kmazarin's ELF.
// Initialized explicitly byte-by-byte to avoid depending on Go init() running.
var wantedSymbols [5][32]byte

func initWantedSymbols() {
	names := [5]string{
		"main.ExceptionVectorTable",
		"main.isr14",
		"main.isr48",
		"main.isr128",
		"main.isr255",
	}
	for w, s := range names {
		for i := 0; i < len(s); i++ {
			wantedSymbols[w][i] = s[i]
		}
	}
}

// symNameBuf is a reusable buffer for reading symbol names from the string table.
var symNameBuf [128]byte

// shdrBuf holds section headers read from the ELF.
var shdrBuf [64]elf64Shdr

// extractSymbols reads the ELF section headers to find .symtab and .strtab,
// then scans for specific symbols needed by diplomat (e.g., ExceptionVectorTable).
func extractSymbols(fsys *fat32.FileSystem, file *SimpleFile, ehdr *elf64Ehdr, kernel *LoadedKernel) {
	initWantedSymbols()

	if ehdr.Shoff == 0 || ehdr.Shnum == 0 {
		printString("ELF: no section headers\r\n")
		return
	}
	if ehdr.Shnum > 64 {
		printString("ELF: too many sections\r\n")
		return
	}

	// Read section headers
	shdrBytes := int(ehdr.Shnum) * elfShdrSize
	n, err := readFileAt(fsys, file, ehdr.Shoff, (*[64 * elfShdrSize]byte)(unsafe.Pointer(&shdrBuf[0]))[:shdrBytes])
	if err != nil || n < shdrBytes {
		printString("ELF: failed to read section headers\r\n")
		return
	}

	// Find .symtab section
	var symtab *elf64Shdr
	for i := uint16(0); i < ehdr.Shnum; i++ {
		if shdrBuf[i].Type == elfSHT_SYMTAB {
			symtab = &shdrBuf[i]
			break
		}
	}
	if symtab == nil {
		printString("ELF: no .symtab\r\n")
		return
	}

	// The .symtab's Link field points to its associated .strtab section
	if symtab.Link >= uint32(ehdr.Shnum) {
		printString("ELF: bad strtab link\r\n")
		return
	}
	strtab := &shdrBuf[symtab.Link]

	// Scan symbol table entries for wanted symbols
	numSyms := symtab.Size / uint64(elfSymSize)
	found := 0

	printString("ELF: symtab at off=")
	printHex(symtab.Offset)
	printString(" ")
	printHex(numSyms)
	printString(" syms, strtab at off=")
	printHex(strtab.Offset)
	printString("\r\n")

	// Read symbols in chunks using elfReadBuf (4096 bytes = 170 symbols per chunk)
	symsPerChunk := uint64(len(elfReadBuf)) / uint64(elfSymSize)

	for offset := uint64(0); offset < numSyms && found < len(wantedSymbols); offset += symsPerChunk {
		remaining := numSyms - offset
		if remaining > symsPerChunk {
			remaining = symsPerChunk
		}
		readSize := int(remaining * uint64(elfSymSize))
		fileOff := symtab.Offset + offset*uint64(elfSymSize)

		n, err := readFileAt(fsys, file, fileOff, elfReadBuf[:readSize])
		if err != nil || n < readSize {
			break
		}

		// Process each symbol in this chunk
		for i := uint64(0); i < remaining; i++ {
			sym := (*elf64Sym)(unsafe.Pointer(&elfReadBuf[i*uint64(elfSymSize)]))
			if sym.Name == 0 || sym.Value == 0 {
				continue
			}

			// Read symbol name from string table
			nameOff := strtab.Offset + uint64(sym.Name)
			nn, err := readFileAt(fsys, file, nameOff, symNameBuf[:])
			if err != nil || nn == 0 {
				continue
			}

			// Check against wanted symbols
			for w := 0; w < len(wantedSymbols); w++ {
				if matchSymName(symNameBuf[:], wantedSymbols[w]) {
					// Found it — store in kernel.Symbols
					if kernel.NumSymbols < len(kernel.Symbols) {
						ks := &kernel.Symbols[kernel.NumSymbols]
						for j := 0; j < 64 && wantedSymbols[w][j] != 0; j++ {
							ks.Name[j] = wantedSymbols[w][j]
						}
						ks.Value = sym.Value
						kernel.NumSymbols++
						found++

						printString("ELF: found ")
						printSymName(wantedSymbols[w][:])
						printString(" = ")
						printHex(sym.Value)
						printString("\r\n")
					}
				}
			}
		}
	}
}

// matchSymName checks if the name read from the string table matches a wanted name.
// The strtab name may have a ".abi0" suffix that we ignore.
func matchSymName(strtabName []byte, wanted [32]byte) bool {
	// Find length of wanted name
	wantLen := 0
	for wantLen < 32 && wanted[wantLen] != 0 {
		wantLen++
	}
	if wantLen == 0 {
		return false
	}

	// Compare first wantLen bytes
	for i := 0; i < wantLen; i++ {
		if i >= len(strtabName) || strtabName[i] != wanted[i] {
			return false
		}
	}

	// After the wanted name, must be NUL or ".abi0" suffix
	if wantLen < len(strtabName) {
		next := strtabName[wantLen]
		if next == 0 {
			return true
		}
		// Check for ".abi0" suffix
		if next == '.' && wantLen+5 <= len(strtabName) {
			return strtabName[wantLen+1] == 'a' &&
				strtabName[wantLen+2] == 'b' &&
				strtabName[wantLen+3] == 'i' &&
				strtabName[wantLen+4] == '0'
		}
		return false
	}
	return true
}

// printSymName prints a symbol name (null-terminated byte array)
func printSymName(name []byte) {
	for _, b := range name {
		if b == 0 {
			break
		}
		printChar(uint16(b))
	}
}
