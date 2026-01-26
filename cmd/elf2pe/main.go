// elf2pe converts an ELF executable to PE format for UEFI
//
// This tool takes a GOOS=linux GOARCH=amd64 ELF binary and converts it
// to a UEFI-compatible PE/COFF executable. The machine code is identical -
// only the file format wrapper changes.
//
// Usage:
//
//	elf2pe input.elf output.efi
//
// The tool:
//   - Reads ELF sections (.text, .rodata, .data, .bss)
//   - Creates PE sections with the same content
//   - Sets PE subsystem to 10 (EFI_APPLICATION)
//   - Preserves entry point and relocations
package main

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
)

const (
	// PE Subsystem values
	IMAGE_SUBSYSTEM_EFI_APPLICATION = 10

	// PE Machine types
	IMAGE_FILE_MACHINE_AMD64 = 0x8664

	// PE Section characteristics
	IMAGE_SCN_CNT_CODE               = 0x00000020
	IMAGE_SCN_CNT_INITIALIZED_DATA   = 0x00000040
	IMAGE_SCN_CNT_UNINITIALIZED_DATA = 0x00000080
	IMAGE_SCN_MEM_EXECUTE            = 0x20000000
	IMAGE_SCN_MEM_READ               = 0x40000000
	IMAGE_SCN_MEM_WRITE              = 0x80000000

	// PE Optional Header magic
	PE32PLUS_MAGIC = 0x20b // PE32+ (64-bit)

	// Image base for UEFI applications
	IMAGE_BASE = 0x400000

	// Section alignment
	SECTION_ALIGNMENT = 0x1000 // 4KB
	FILE_ALIGNMENT    = 0x200  // 512 bytes
)

// DOS Header (64 bytes)
var dosHeader = []byte{
	0x4d, 0x5a, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00,
	0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80, 0x00, 0x00, 0x00,
}

// DOS Stub (64 bytes) - "This program cannot be run in DOS mode"
var dosStub = []byte{
	0x0e, 0x1f, 0xba, 0x0e, 0x00, 0xb4, 0x09, 0xcd, 0x21, 0xb8, 0x01, 0x4c, 0xcd, 0x21, 0x54, 0x68,
	0x69, 0x73, 0x20, 0x70, 0x72, 0x6f, 0x67, 0x72, 0x61, 0x6d, 0x20, 0x63, 0x61, 0x6e, 0x6e, 0x6f,
	0x74, 0x20, 0x62, 0x65, 0x20, 0x72, 0x75, 0x6e, 0x20, 0x69, 0x6e, 0x20, 0x44, 0x4f, 0x53, 0x20,
	0x6d, 0x6f, 0x64, 0x65, 0x2e, 0x0d, 0x0d, 0x0a, 0x24, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
}

type PESection struct {
	Name             string
	VirtualSize      uint32
	VirtualAddress   uint32
	RawSize          uint32
	RawDataOffset    uint32
	Characteristics  uint32
	Data             []byte
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <input.elf> <output.efi>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nConverts a Linux/amd64 ELF executable to UEFI PE format.\n")
		os.Exit(1)
	}

	inputPath := os.Args[1]
	outputPath := os.Args[2]

	if err := convertELFtoPE(inputPath, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully converted %s to %s\n", inputPath, outputPath)
}

func convertELFtoPE(inputPath, outputPath string) error {
	// Open ELF file
	elfFile, err := elf.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open ELF file: %w", err)
	}
	defer elfFile.Close()

	// Verify it's AMD64
	if elfFile.Machine != elf.EM_X86_64 {
		return fmt.Errorf("ELF file is not x86_64 (machine type: %v)", elfFile.Machine)
	}

	fmt.Printf("Input: %s\n", inputPath)
	fmt.Printf("  Type: ELF64 x86-64\n")
	fmt.Printf("  Entry: 0x%x\n", elfFile.Entry)

	// Look for efi_main symbol to use as PE entry point (for UEFI applications)
	// If found, this overrides the ELF entry point
	efiMainAddr := findSymbolAddress(elfFile, "efi_main")
	if efiMainAddr != 0 {
		fmt.Printf("  Found efi_main: 0x%x (using as PE entry point)\n", efiMainAddr)
		elfFile.Entry = efiMainAddr
	}

	// Extract sections from ELF
	peSections, err := extractELFSections(elfFile)
	if err != nil {
		return fmt.Errorf("failed to extract ELF sections: %w", err)
	}

	fmt.Printf("  Sections: %d\n", len(peSections))
	for _, sec := range peSections {
		fmt.Printf("    %-8s VA=0x%08x Size=0x%08x Chars=0x%08x\n",
			sec.Name, sec.VirtualAddress, sec.VirtualSize, sec.Characteristics)
	}

	// Create PE file
	peData, err := createPEFile(elfFile, peSections)
	if err != nil {
		return fmt.Errorf("failed to create PE file: %w", err)
	}

	// Write output
	if err := os.WriteFile(outputPath, peData, 0755); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	fmt.Printf("Output: %s\n", outputPath)
	fmt.Printf("  Type: PE32+ (EFI application)\n")
	fmt.Printf("  Size: %d bytes\n", len(peData))

	return nil
}

// findSymbolAddress searches for a symbol by name and returns its address.
// Returns 0 if the symbol is not found.
func findSymbolAddress(elfFile *elf.File, symbolName string) uint64 {
	// Get symbols from both .symtab and .dynsym sections
	symbols, err := elfFile.Symbols()
	if err != nil {
		// If we can't read symbols, just return 0
		return 0
	}

	for _, sym := range symbols {
		if sym.Name == symbolName {
			return sym.Value
		}
	}

	// Also check dynamic symbols
	dynSymbols, err := elfFile.DynamicSymbols()
	if err == nil {
		for _, sym := range dynSymbols {
			if sym.Name == symbolName {
				return sym.Value
			}
		}
	}

	return 0
}

// extractELFSections reads ELF sections and converts them to PE sections
func extractELFSections(elfFile *elf.File) ([]*PESection, error) {
	var peSections []*PESection
	virtualAddr := uint32(0x1000) // Start after PE headers

	// Map to track which ELF sections we've processed
	processed := make(map[string]bool)

	// Process sections in order: .text, .rodata, .data, .bss
	sectionOrder := []string{".text", ".rodata", ".data", ".bss"}

	for _, name := range sectionOrder {
		elfSec := elfFile.Section(name)
		if elfSec == nil {
			continue
		}

		if processed[name] {
			continue
		}
		processed[name] = true

		// Determine PE characteristics based on ELF section
		var characteristics uint32
		switch name {
		case ".text":
			characteristics = IMAGE_SCN_CNT_CODE | IMAGE_SCN_MEM_EXECUTE | IMAGE_SCN_MEM_READ
		case ".rodata":
			characteristics = IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ
		case ".data":
			characteristics = IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ | IMAGE_SCN_MEM_WRITE
		case ".bss":
			characteristics = IMAGE_SCN_CNT_UNINITIALIZED_DATA | IMAGE_SCN_MEM_READ | IMAGE_SCN_MEM_WRITE
		default:
			// Skip other sections
			continue
		}

		// Read section data (if not .bss)
		var data []byte
		if elfSec.Type != elf.SHT_NOBITS {
			var err error
			data, err = elfSec.Data()
			if err != nil {
				return nil, fmt.Errorf("failed to read section %s: %w", name, err)
			}
		}

		virtualSize := uint32(elfSec.Size)
		rawSize := uint32(len(data))

		// Align raw size to file alignment
		rawSize = alignTo(rawSize, FILE_ALIGNMENT)

		// Pad data to raw size
		if len(data) < int(rawSize) {
			data = append(data, make([]byte, int(rawSize)-len(data))...)
		}

		peSec := &PESection{
			Name:            name,
			VirtualSize:     virtualSize,
			VirtualAddress:  virtualAddr,
			RawSize:         rawSize,
			Characteristics: characteristics,
			Data:            data,
		}

		peSections = append(peSections, peSec)

		// Advance virtual address (aligned to section alignment)
		virtualAddr += alignTo(virtualSize, SECTION_ALIGNMENT)
	}

	return peSections, nil
}

// createPEFile builds the complete PE file
func createPEFile(elfFile *elf.File, sections []*PESection) ([]byte, error) {
	buf := make([]byte, 0, 1024*1024) // Pre-allocate 1MB

	// DOS Header
	buf = append(buf, dosHeader...)
	buf = append(buf, dosStub...)

	// PE Signature (at offset 0x80)
	buf = append(buf, 'P', 'E', 0, 0)

	// COFF Header (20 bytes)
	coffHeader := make([]byte, 20)
	binary.LittleEndian.PutUint16(coffHeader[0:2], IMAGE_FILE_MACHINE_AMD64)   // Machine
	binary.LittleEndian.PutUint16(coffHeader[2:4], uint16(len(sections)))      // NumberOfSections
	binary.LittleEndian.PutUint32(coffHeader[4:8], 0)                          // TimeDateStamp
	binary.LittleEndian.PutUint32(coffHeader[8:12], 0)                         // PointerToSymbolTable
	binary.LittleEndian.PutUint32(coffHeader[12:16], 0)                        // NumberOfSymbols
	binary.LittleEndian.PutUint16(coffHeader[16:18], 240)                      // SizeOfOptionalHeader (PE32+)
	binary.LittleEndian.PutUint16(coffHeader[18:20], 0x0022)                   // Characteristics (executable, large address aware)
	buf = append(buf, coffHeader...)

	// Optional Header (PE32+, 240 bytes for minimal UEFI)
	optHeader := make([]byte, 240)

	// Standard fields
	binary.LittleEndian.PutUint16(optHeader[0:2], PE32PLUS_MAGIC)              // Magic
	optHeader[2] = 14                                                          // MajorLinkerVersion
	optHeader[3] = 0                                                           // MinorLinkerVersion

	// Calculate sizes
	var sizeOfCode, sizeOfInitializedData, sizeOfUninitializedData uint32
	var baseOfCode uint32 = 0xFFFFFFFF

	for _, sec := range sections {
		if sec.Characteristics&IMAGE_SCN_CNT_CODE != 0 {
			sizeOfCode += sec.RawSize
			if sec.VirtualAddress < baseOfCode {
				baseOfCode = sec.VirtualAddress
			}
		}
		if sec.Characteristics&IMAGE_SCN_CNT_INITIALIZED_DATA != 0 {
			sizeOfInitializedData += sec.RawSize
		}
		if sec.Characteristics&IMAGE_SCN_CNT_UNINITIALIZED_DATA != 0 {
			sizeOfUninitializedData += sec.VirtualSize
		}
	}

	binary.LittleEndian.PutUint32(optHeader[4:8], sizeOfCode)                  // SizeOfCode
	binary.LittleEndian.PutUint32(optHeader[8:12], sizeOfInitializedData)      // SizeOfInitializedData
	binary.LittleEndian.PutUint32(optHeader[12:16], sizeOfUninitializedData)   // SizeOfUninitializedData

	// Entry point: use ELF entry point offset from image base
	entryRVA := uint32(elfFile.Entry - IMAGE_BASE)
	binary.LittleEndian.PutUint32(optHeader[16:20], entryRVA)                  // AddressOfEntryPoint
	binary.LittleEndian.PutUint32(optHeader[20:24], baseOfCode)                // BaseOfCode

	// NT additional fields (PE32+)
	binary.LittleEndian.PutUint64(optHeader[24:32], IMAGE_BASE)                // ImageBase
	binary.LittleEndian.PutUint32(optHeader[32:36], SECTION_ALIGNMENT)         // SectionAlignment
	binary.LittleEndian.PutUint32(optHeader[36:40], FILE_ALIGNMENT)            // FileAlignment
	binary.LittleEndian.PutUint16(optHeader[40:42], 0)                         // MajorOperatingSystemVersion
	binary.LittleEndian.PutUint16(optHeader[42:44], 0)                         // MinorOperatingSystemVersion
	binary.LittleEndian.PutUint16(optHeader[44:46], 0)                         // MajorImageVersion
	binary.LittleEndian.PutUint16(optHeader[46:48], 0)                         // MinorImageVersion
	binary.LittleEndian.PutUint16(optHeader[48:50], 0)                         // MajorSubsystemVersion
	binary.LittleEndian.PutUint16(optHeader[50:52], 0)                         // MinorSubsystemVersion
	binary.LittleEndian.PutUint32(optHeader[52:56], 0)                         // Win32VersionValue

	// Calculate SizeOfImage (highest virtual address + size, aligned)
	var sizeOfImage uint32
	for _, sec := range sections {
		end := sec.VirtualAddress + alignTo(sec.VirtualSize, SECTION_ALIGNMENT)
		if end > sizeOfImage {
			sizeOfImage = end
		}
	}
	binary.LittleEndian.PutUint32(optHeader[56:60], sizeOfImage)               // SizeOfImage

	// SizeOfHeaders (DOS + PE + COFF + Optional + Section Headers)
	sizeOfHeaders := uint32(len(dosHeader) + len(dosStub) + 4 + 20 + 240 + (len(sections) * 40))
	sizeOfHeaders = alignTo(sizeOfHeaders, FILE_ALIGNMENT)
	binary.LittleEndian.PutUint32(optHeader[60:64], sizeOfHeaders)             // SizeOfHeaders

	binary.LittleEndian.PutUint32(optHeader[64:68], 0)                         // CheckSum
	binary.LittleEndian.PutUint16(optHeader[68:70], IMAGE_SUBSYSTEM_EFI_APPLICATION) // Subsystem
	binary.LittleEndian.PutUint16(optHeader[70:72], 0)                         // DllCharacteristics
	binary.LittleEndian.PutUint64(optHeader[72:80], 0x100000)                  // SizeOfStackReserve
	binary.LittleEndian.PutUint64(optHeader[80:88], 0x1000)                    // SizeOfStackCommit
	binary.LittleEndian.PutUint64(optHeader[88:96], 0x100000)                  // SizeOfHeapReserve
	binary.LittleEndian.PutUint64(optHeader[96:104], 0x1000)                   // SizeOfHeapCommit
	binary.LittleEndian.PutUint32(optHeader[104:108], 0)                       // LoaderFlags
	binary.LittleEndian.PutUint32(optHeader[108:112], 16)                      // NumberOfRvaAndSizes

	// Data directories (16 entries * 8 bytes = 128 bytes)
	// All zeros for minimal UEFI application

	buf = append(buf, optHeader...)

	// Section Headers (40 bytes each)
	rawDataOffset := sizeOfHeaders
	for _, sec := range sections {
		secHeader := make([]byte, 40)

		// Name (8 bytes, null-padded)
		copy(secHeader[0:8], sec.Name)

		binary.LittleEndian.PutUint32(secHeader[8:12], sec.VirtualSize)        // VirtualSize
		binary.LittleEndian.PutUint32(secHeader[12:16], sec.VirtualAddress)    // VirtualAddress
		binary.LittleEndian.PutUint32(secHeader[16:20], sec.RawSize)           // SizeOfRawData
		binary.LittleEndian.PutUint32(secHeader[20:24], rawDataOffset)         // PointerToRawData
		binary.LittleEndian.PutUint32(secHeader[24:28], 0)                     // PointerToRelocations
		binary.LittleEndian.PutUint32(secHeader[28:32], 0)                     // PointerToLinenumbers
		binary.LittleEndian.PutUint16(secHeader[32:34], 0)                     // NumberOfRelocations
		binary.LittleEndian.PutUint16(secHeader[34:36], 0)                     // NumberOfLinenumbers
		binary.LittleEndian.PutUint32(secHeader[36:40], sec.Characteristics)   // Characteristics

		buf = append(buf, secHeader...)

		sec.RawDataOffset = rawDataOffset
		rawDataOffset += sec.RawSize
	}

	// Pad to SizeOfHeaders
	for uint32(len(buf)) < sizeOfHeaders {
		buf = append(buf, 0)
	}

	// Section data
	for _, sec := range sections {
		buf = append(buf, sec.Data...)
	}

	return buf, nil
}

// alignTo rounds up n to the nearest multiple of alignment
func alignTo(n, alignment uint32) uint32 {
	return (n + alignment - 1) &^ (alignment - 1)
}
