// patch-entry.go - Patches the ELF entry point to a specified symbol address
//
// Usage: go run patch-entry.go <elf-file> <symbol-name> [output-file]
//
// This tool reads an ELF file, finds the specified symbol, and patches
// the ELF header's e_entry field to that symbol's address.
//
// If output-file is not specified, the input file is modified in place.
//
// Note: This tool handles Go's quirky ELF format where the -T flag creates
// program headers with "negative" offsets that confuse standard ELF parsers.
// We read the raw file and parse sections/symbols manually.

package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

// ELF64 header offsets (little-endian)
const (
	EI_MAG0       = 0  // ELF magic: 0x7f
	EI_MAG1       = 1  // 'E'
	EI_MAG2       = 2  // 'L'
	EI_MAG3       = 3  // 'F'
	EI_CLASS      = 4  // 1=32-bit, 2=64-bit
	EI_DATA       = 5  // 1=LE, 2=BE
	E_ENTRY       = 24 // Entry point (8 bytes for ELF64)
	E_SHOFF       = 40 // Section header table offset
	E_SHENTSIZE   = 58 // Section header entry size
	E_SHNUM       = 60 // Number of section headers
	E_SHSTRNDX    = 62 // Section name string table index
	ELF64_EHDR_SZ = 64
)

// Section header offsets (ELF64)
const (
	SH_NAME      = 0
	SH_TYPE      = 4
	SH_OFFSET    = 24
	SH_SIZE      = 32
	SH_LINK      = 40
	SH_ENTSIZE   = 56
	ELF64_SHDR_SZ = 64
)

// Section types
const (
	SHT_SYMTAB = 2
	SHT_STRTAB = 3
)

// Symbol table entry offsets (ELF64)
const (
	ST_NAME      = 0
	ST_VALUE     = 8
	ELF64_SYM_SZ = 24
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <elf-file> <symbol-name> [output-file]\n", os.Args[0])
		os.Exit(1)
	}

	elfPath := os.Args[1]
	symbolName := os.Args[2]
	outputPath := elfPath
	if len(os.Args) >= 4 {
		outputPath = os.Args[3]
	}

	// Read the entire file
	data, err := os.ReadFile(elfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Verify ELF magic
	if len(data) < ELF64_EHDR_SZ || data[EI_MAG0] != 0x7f || data[EI_MAG1] != 'E' || data[EI_MAG2] != 'L' || data[EI_MAG3] != 'F' {
		fmt.Fprintf(os.Stderr, "Not a valid ELF file\n")
		os.Exit(1)
	}

	// Check 64-bit
	if data[EI_CLASS] != 2 {
		fmt.Fprintf(os.Stderr, "Only 64-bit ELF files are supported\n")
		os.Exit(1)
	}

	// Check little-endian
	if data[EI_DATA] != 1 {
		fmt.Fprintf(os.Stderr, "Only little-endian ELF files are supported\n")
		os.Exit(1)
	}

	// Parse section headers
	shoff := binary.LittleEndian.Uint64(data[E_SHOFF : E_SHOFF+8])
	shentsize := binary.LittleEndian.Uint16(data[E_SHENTSIZE : E_SHENTSIZE+2])
	shnum := binary.LittleEndian.Uint16(data[E_SHNUM : E_SHNUM+2])
	shstrndx := binary.LittleEndian.Uint16(data[E_SHSTRNDX : E_SHSTRNDX+2])

	if shoff == 0 || shnum == 0 {
		fmt.Fprintf(os.Stderr, "No section headers found\n")
		os.Exit(1)
	}

	// Find .shstrtab (section header string table)
	shstrBase := shoff + uint64(shstrndx)*uint64(shentsize)
	if shstrBase+ELF64_SHDR_SZ > uint64(len(data)) {
		fmt.Fprintf(os.Stderr, "Section header string table out of bounds\n")
		os.Exit(1)
	}
	shstrOffset := binary.LittleEndian.Uint64(data[shstrBase+SH_OFFSET : shstrBase+SH_OFFSET+8])
	shstrSize := binary.LittleEndian.Uint64(data[shstrBase+SH_SIZE : shstrBase+SH_SIZE+8])
	shstrtab := data[shstrOffset : shstrOffset+shstrSize]

	// Find .symtab and .strtab sections
	var symtabOff, symtabSize, symtabLink uint64
	var strtabOff, strtabSize uint64
	foundSymtab := false
	foundStrtab := false

	for i := uint16(0); i < shnum; i++ {
		shBase := shoff + uint64(i)*uint64(shentsize)
		if shBase+ELF64_SHDR_SZ > uint64(len(data)) {
			continue
		}

		shType := binary.LittleEndian.Uint32(data[shBase+SH_TYPE : shBase+SH_TYPE+4])
		shNameIdx := binary.LittleEndian.Uint32(data[shBase+SH_NAME : shBase+SH_NAME+4])

		// Get section name
		var secName string
		if uint64(shNameIdx) < uint64(len(shstrtab)) {
			end := shNameIdx
			for end < uint32(len(shstrtab)) && shstrtab[end] != 0 {
				end++
			}
			secName = string(shstrtab[shNameIdx:end])
		}

		if shType == SHT_SYMTAB || secName == ".symtab" {
			symtabOff = binary.LittleEndian.Uint64(data[shBase+SH_OFFSET : shBase+SH_OFFSET+8])
			symtabSize = binary.LittleEndian.Uint64(data[shBase+SH_SIZE : shBase+SH_SIZE+8])
			symtabLink = uint64(binary.LittleEndian.Uint32(data[shBase+SH_LINK : shBase+SH_LINK+4]))
			foundSymtab = true
		}
		if secName == ".strtab" {
			strtabOff = binary.LittleEndian.Uint64(data[shBase+SH_OFFSET : shBase+SH_OFFSET+8])
			strtabSize = binary.LittleEndian.Uint64(data[shBase+SH_SIZE : shBase+SH_SIZE+8])
			foundStrtab = true
		}
	}

	// If we didn't find .strtab by name, use symtab's sh_link
	if foundSymtab && !foundStrtab && symtabLink > 0 && symtabLink < uint64(shnum) {
		linkBase := shoff + symtabLink*uint64(shentsize)
		strtabOff = binary.LittleEndian.Uint64(data[linkBase+SH_OFFSET : linkBase+SH_OFFSET+8])
		strtabSize = binary.LittleEndian.Uint64(data[linkBase+SH_SIZE : linkBase+SH_SIZE+8])
		foundStrtab = true
	}

	if !foundSymtab {
		fmt.Fprintf(os.Stderr, "Symbol table (.symtab) not found\n")
		os.Exit(1)
	}
	if !foundStrtab {
		fmt.Fprintf(os.Stderr, "String table (.strtab) not found\n")
		os.Exit(1)
	}

	// Read string table
	if strtabOff+strtabSize > uint64(len(data)) {
		fmt.Fprintf(os.Stderr, "String table out of bounds\n")
		os.Exit(1)
	}
	strtab := data[strtabOff : strtabOff+strtabSize]

	// Search symbol table
	numSyms := symtabSize / ELF64_SYM_SZ
	var symbolAddr uint64
	found := false

	for i := uint64(0); i < numSyms; i++ {
		symBase := symtabOff + i*ELF64_SYM_SZ
		if symBase+ELF64_SYM_SZ > uint64(len(data)) {
			continue
		}

		nameIdx := binary.LittleEndian.Uint32(data[symBase+ST_NAME : symBase+ST_NAME+4])
		value := binary.LittleEndian.Uint64(data[symBase+ST_VALUE : symBase+ST_VALUE+8])

		// Get symbol name from string table
		if uint64(nameIdx) >= uint64(len(strtab)) {
			continue
		}
		end := nameIdx
		for end < uint32(len(strtab)) && strtab[end] != 0 {
			end++
		}
		name := string(strtab[nameIdx:end])

		if name == symbolName {
			symbolAddr = value
			found = true
			break
		}
	}

	if !found {
		fmt.Fprintf(os.Stderr, "Symbol '%s' not found in ELF file\n", symbolName)
		os.Exit(1)
	}

	fmt.Printf("Found symbol '%s' at address 0x%08x\n", symbolName, symbolAddr)

	// Get current entry point
	currentEntry := binary.LittleEndian.Uint64(data[E_ENTRY : E_ENTRY+8])
	fmt.Printf("Current entry point: 0x%08x\n", currentEntry)

	// Patch the entry point
	binary.LittleEndian.PutUint64(data[E_ENTRY:E_ENTRY+8], symbolAddr)
	fmt.Printf("New entry point: 0x%08x\n", symbolAddr)

	// Write the patched file
	err = os.WriteFile(outputPath, data, 0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
		os.Exit(1)
	}

	if outputPath == elfPath {
		fmt.Printf("Patched %s in place\n", elfPath)
	} else {
		fmt.Printf("Wrote patched file to %s\n", outputPath)
	}
}
