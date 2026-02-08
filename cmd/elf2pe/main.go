// elf2pe converts an ELF executable to PE format for UEFI
//
// This tool takes a GOOS=linux GOARCH={amd64,arm64} ELF binary and converts it
// to a UEFI-compatible PE/COFF executable. The machine code is identical -
// only the file format wrapper changes.
//
// Usage:
//
//	elf2pe input.elf output.efi
//
// Supported architectures:
//   - x86_64 (amd64) -> PE32+ with IMAGE_FILE_MACHINE_AMD64
//   - aarch64 (arm64) -> PE32+ with IMAGE_FILE_MACHINE_ARM64
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
	IMAGE_FILE_MACHINE_AMD64  = 0x8664
	IMAGE_FILE_MACHINE_ARM64  = 0xAA64
	IMAGE_FILE_MACHINE_RISCV64 = 0x5064

	// PE Section characteristics
	IMAGE_SCN_CNT_CODE               = 0x00000020
	IMAGE_SCN_CNT_INITIALIZED_DATA   = 0x00000040
	IMAGE_SCN_CNT_UNINITIALIZED_DATA = 0x00000080
	IMAGE_SCN_MEM_EXECUTE            = 0x20000000
	IMAGE_SCN_MEM_READ               = 0x40000000
	IMAGE_SCN_MEM_WRITE              = 0x80000000

	// PE Optional Header magic
	PE32PLUS_MAGIC = 0x20b // PE32+ (64-bit)

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
		fmt.Fprintf(os.Stderr, "\nConverts a Linux ELF executable (amd64 or arm64) to UEFI PE format.\n")
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

	// Determine PE machine type based on ELF machine
	var peMachine uint16
	var archName string
	var entrySymbols []string

	switch elfFile.Machine {
	case elf.EM_X86_64:
		peMachine = IMAGE_FILE_MACHINE_AMD64
		archName = "x86-64"
		// x86_64 entry point symbols
		entrySymbols = []string{
			"main._efi_main_asm",
			"main._efi_main_asm.abi0",
			"main._minimal_uefi_test",
			"main._minimal_uefi_test.abi0",
		}
	case elf.EM_AARCH64:
		peMachine = IMAGE_FILE_MACHINE_ARM64
		archName = "aarch64"
		// ARM64 entry point symbols
		entrySymbols = []string{
			"main._efi_main_arm64",
			"main._efi_main_arm64.abi0",
			"main._minimal_uefi_test_arm64",
			"main._minimal_uefi_test_arm64.abi0",
		}
	case elf.EM_RISCV:
		peMachine = IMAGE_FILE_MACHINE_RISCV64
		archName = "riscv64"
		// RISC-V entry point symbols
		entrySymbols = []string{
			"main._efi_main_riscv64",
			"main._efi_main_riscv64.abi0",
			"main._minimal_uefi_test_riscv64",
			"main._minimal_uefi_test_riscv64.abi0",
		}
	default:
		return fmt.Errorf("unsupported ELF machine type: %v (only x86_64, aarch64, and riscv64 are supported)", elfFile.Machine)
	}

	fmt.Printf("Input: %s\n", inputPath)
	fmt.Printf("  Type: ELF64 %s\n", archName)
	fmt.Printf("  Entry: 0x%x\n", elfFile.Entry)
	var efiMainAddr uint64
	var foundSym string
	for _, sym := range entrySymbols {
		efiMainAddr = findSymbolAddress(elfFile, sym)
		if efiMainAddr != 0 {
			foundSym = sym
			break
		}
	}
	if efiMainAddr != 0 {
		fmt.Printf("  Found %s: 0x%x (using as PE entry point)\n", foundSym, efiMainAddr)
		elfFile.Entry = efiMainAddr
	} else {
		fmt.Printf("  WARNING: No UEFI entry point found!\n")
	}

	// Compute image base from ELF section layout
	imageBase := computeImageBase(elfFile)
	fmt.Printf("  ImageBase: 0x%x\n", imageBase)

	// Extract sections from ELF
	peSections, err := extractELFSections(elfFile, imageBase)
	if err != nil {
		return fmt.Errorf("failed to extract ELF sections: %w", err)
	}

	fmt.Printf("  Sections: %d\n", len(peSections))
	for _, sec := range peSections {
		fmt.Printf("    %-8s VA=0x%08x Size=0x%08x Chars=0x%08x\n",
			sec.Name, sec.VirtualAddress, sec.VirtualSize, sec.Characteristics)
	}

	// Create PE file
	peData, err := createPEFile(elfFile, peSections, peMachine, imageBase)
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

// findUEFIEntryPoint finds the UEFI entry point (efi_main) via binary analysis.
// Strategy:
//   1. Find main.DiplomatEntry symbol (this exports)
//   2. Scan backwards in .text to find a CALL instruction targeting DiplomatEntry
//   3. The function containing that CALL is efi_main
//   4. Return its address as the entry point
func findUEFIEntryPoint(elfFile *elf.File) uint64 {
	// Find main.DiplomatEntry symbol
	diplomatEntryAddr := findSymbolAddress(elfFile, "main.DiplomatEntry")
	if diplomatEntryAddr == 0 {
		// Not a diplomat binary, use default entry
		return 0
	}

	// Get .text section
	textSection := elfFile.Section(".text")
	if textSection == nil {
		return 0
	}

	textData, err := textSection.Data()
	if err != nil {
		return 0
	}

	textStart := textSection.Addr
	textEnd := textStart + textSection.Size

	// Calculate where DiplomatEntry is within .text
	if diplomatEntryAddr < textStart || diplomatEntryAddr >= textEnd {
		return 0
	}

	diplomatEntryOffset := diplomatEntryAddr - textStart

	// Scan backwards from DiplomatEntry looking for a CALL instruction
	// that targets it. X86_64 CALL: E8 [4-byte relative offset]
	// Search backwards up to 256 bytes (reasonable function size)
	searchStart := int(diplomatEntryOffset) - 256
	if searchStart < 0 {
		searchStart = 0
	}

	for i := int(diplomatEntryOffset) - 5; i >= searchStart; i-- {
		// Check if this looks like a CALL instruction (E8)
		if textData[i] != 0xE8 {
			continue
		}

		// Extract the 4-byte relative offset
		offset := int32(textData[i+1]) |
			int32(textData[i+2])<<8 |
			int32(textData[i+3])<<16 |
			int32(textData[i+4])<<24

		// Calculate absolute target address
		// CALL at offset i, next instruction at i+5
		callAddr := textStart + uint64(i)
		nextInstr := callAddr + 5
		targetAddr := uint64(int64(nextInstr) + int64(offset))

		// Check if this CALL targets DiplomatEntry
		if targetAddr == diplomatEntryAddr {
			// Found the CALL! Now find the function start.
			// efi_main is small (< 32 bytes), so scan backwards for function boundary.
			// Look for a RET (C3) or start of section.
			funcStart := i
			for funcStart > 0 && (i-funcStart) < 32 {
				// Check for RET instruction - previous function ends here
				if textData[funcStart-1] == 0xC3 {
					// Function starts right after the RET
					break
				}
				funcStart--
			}

			efiMainAddr := textStart + uint64(funcStart)
			return efiMainAddr
		}
	}

	// Couldn't find efi_main, use default
	return 0
}

// computeImageBase determines the PE image base from the ELF's section layout.
// It finds the minimum virtual address across all sections we care about and
// aligns it down to 64KB (standard PE section alignment for image base).
// This avoids the hardcoded IMAGE_BASE=0x400000 which causes unsigned underflow
// on ARM64 where Go places sections starting at 0x10000.
func computeImageBase(elfFile *elf.File) uint64 {
	sectionNames := []string{
		".text", ".rodata", ".typelink", ".itablink", ".gosymtab", ".gopclntab",
		".go.buildinfo", ".go.fipsinfo", ".noptrdata", ".data", ".bss", ".noptrbss",
	}

	var minAddr uint64 = ^uint64(0) // max uint64
	for _, name := range sectionNames {
		sec := elfFile.Section(name)
		if sec != nil && sec.Size > 0 && sec.Addr < minAddr {
			minAddr = sec.Addr
		}
	}

	if minAddr == ^uint64(0) {
		// Fallback if no sections found
		return 0x400000
	}

	// Align down to 64KB boundary (0x10000)
	return minAddr &^ 0xFFFF
}

// extractELFSections reads ELF sections and converts them to PE sections
func extractELFSections(elfFile *elf.File, imageBase uint64) ([]*PESection, error) {
	var peSections []*PESection

	// Go generates multiple data sections that need to be merged:
	// - .data and .noptrdata -> merged into PE .data
	// - .bss and .noptrbss -> merged into PE .bss (uninitialized)
	// - .rodata -> PE .rodata
	// - .text -> PE .text
	//
	// CRITICAL: We must preserve the ELF virtual addresses because Go uses
	// PC-relative addressing compiled against those addresses. The PE RVA
	// (Relative Virtual Address) must equal (ELF addr - IMAGE_BASE).

	// Helper to collect and merge ELF sections
	type sectionGroup struct {
		peName   string
		elfNames []string
		chars    uint32
		isBSS    bool // true for uninitialized data
	}

	// Go also generates runtime metadata sections that must be included:
	// - .typelink, .itablink, .gosymtab, .gopclntab -> merge with .rodata (read-only)
	// - .go.buildinfo, .go.fipsinfo -> merge with .data (writable)
	// These are critical for the Go runtime (GC, stack unwinding, interface dispatch).
	groups := []sectionGroup{
		{".text", []string{".text"}, IMAGE_SCN_CNT_CODE | IMAGE_SCN_MEM_EXECUTE | IMAGE_SCN_MEM_READ, false},
		{".rodata", []string{".rodata", ".typelink", ".itablink", ".gosymtab", ".gopclntab"}, IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ, false},
		{".data", []string{".go.buildinfo", ".go.fipsinfo", ".noptrdata", ".data"}, IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ | IMAGE_SCN_MEM_WRITE, false},
		{".bss", []string{".bss", ".noptrbss"}, IMAGE_SCN_CNT_UNINITIALIZED_DATA | IMAGE_SCN_MEM_READ | IMAGE_SCN_MEM_WRITE, true},
	}

	for _, group := range groups {
		var combinedData []byte
		var totalVirtualSize uint64

		// Collect all ELF sections for this group, sorted by address
		type secInfo struct {
			sec  *elf.Section
			addr uint64
		}
		var sections []secInfo

		for _, elfName := range group.elfNames {
			elfSec := elfFile.Section(elfName)
			if elfSec != nil && elfSec.Size > 0 {
				sections = append(sections, secInfo{elfSec, elfSec.Addr})
			}
		}

		if len(sections) == 0 {
			continue
		}

		// Sort by address
		for i := 0; i < len(sections)-1; i++ {
			for j := i + 1; j < len(sections); j++ {
				if sections[j].addr < sections[i].addr {
					sections[i], sections[j] = sections[j], sections[i]
				}
			}
		}

		// Calculate virtual size spanning all sections
		firstAddr := sections[0].addr
		lastSec := sections[len(sections)-1]
		totalVirtualSize = (lastSec.addr - firstAddr) + lastSec.sec.Size

		// PE virtual address must match ELF address (minus IMAGE_BASE)
		// This is critical because Go compiles with PC-relative addressing
		peVirtualAddr := uint32(firstAddr - imageBase)

		// For initialized sections, read and merge data
		if !group.isBSS {
			combinedData = make([]byte, totalVirtualSize)
			for _, si := range sections {
				offset := si.addr - firstAddr
				data, err := si.sec.Data()
				if err != nil {
					return nil, fmt.Errorf("failed to read section %s: %w", si.sec.Name, err)
				}
				copy(combinedData[offset:], data)
			}
		}

		virtualSize := uint32(totalVirtualSize)
		rawSize := uint32(len(combinedData))

		// Align raw size to file alignment
		rawSize = alignTo(rawSize, FILE_ALIGNMENT)

		// Pad data to raw size
		if len(combinedData) < int(rawSize) {
			combinedData = append(combinedData, make([]byte, int(rawSize)-len(combinedData))...)
		}

		peSec := &PESection{
			Name:            group.peName,
			VirtualSize:     virtualSize,
			VirtualAddress:  peVirtualAddr,
			RawSize:         rawSize,
			Characteristics: group.chars,
			Data:            combinedData,
		}

		peSections = append(peSections, peSec)
	}

	// Generate base relocations by scanning data sections for absolute pointers.
	// Go's linker hardlinks absolute addresses (function pointers, itabs, type
	// metadata) into .rodata and .data. UEFI loads PE images at arbitrary
	// addresses, so these pointers must be adjusted via base relocations.
	relocData := generateBaseRelocations(peSections, imageBase)

	// Place .reloc after the last section
	var relocVA uint32
	if len(peSections) > 0 {
		lastSec := peSections[len(peSections)-1]
		relocVA = alignTo(lastSec.VirtualAddress+lastSec.VirtualSize, SECTION_ALIGNMENT)
	} else {
		relocVA = SECTION_ALIGNMENT
	}
	relocVirtualSize := uint32(len(relocData)) // Exact size of relocation blocks
	relocRawSize := alignTo(relocVirtualSize, FILE_ALIGNMENT)
	if len(relocData) < int(relocRawSize) {
		relocData = append(relocData, make([]byte, int(relocRawSize)-len(relocData))...)
	}
	peSections = append(peSections, &PESection{
		Name:            ".reloc",
		VirtualSize:     relocVirtualSize, // Must be exact — EDK2 rejects SizeOfBlock==0 in padding
		VirtualAddress:  relocVA,
		RawSize:         relocRawSize,
		Characteristics: IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ | 0x01000000, // DISCARDABLE
		Data:            relocData,
	})

	return peSections, nil
}

// generateBaseRelocations scans data sections for absolute pointers and generates
// PE base relocation entries. Go's internal linker resolves all relocations at
// link time, embedding absolute virtual addresses in data sections (itabs, type
// metadata, function tables, pclntab). When UEFI loads the PE at a different
// address, these pointers must be adjusted.
//
// Strategy: any 8-byte aligned value in .rodata or .data that falls within the
// ELF's virtual address range [imageBase, imageBase+sizeOfImage) is treated as
// an absolute pointer needing relocation. False positive rate is negligible
// because this is a narrow 64-bit range (upper bytes must be zero).
func generateBaseRelocations(sections []*PESection, imageBase uint64) []byte {
	// Determine the image's VA range for pointer detection
	var maxVA uint64
	for _, sec := range sections {
		end := uint64(sec.VirtualAddress) + uint64(sec.VirtualSize)
		if end > maxVA {
			maxVA = end
		}
	}
	// Absolute address range: [imageBase, imageBase + maxVA)
	minAbsAddr := imageBase
	maxAbsAddr := imageBase + maxVA

	// Collect RVAs of all absolute pointers in data sections
	var relocRVAs []uint32
	for _, sec := range sections {
		// Only scan initialized data sections, not code or BSS
		if sec.Name == ".text" || sec.Name == ".bss" || sec.Name == ".reloc" {
			continue
		}
		for off := 0; off+8 <= len(sec.Data); off += 8 {
			val := binary.LittleEndian.Uint64(sec.Data[off : off+8])
			if val >= minAbsAddr && val < maxAbsAddr {
				relocRVAs = append(relocRVAs, sec.VirtualAddress+uint32(off))
			}
		}
	}

	fmt.Printf("  Relocations: %d absolute pointers found in data sections\n", len(relocRVAs))

	if len(relocRVAs) == 0 {
		// Return minimal valid reloc section (empty block)
		return make([]byte, FILE_ALIGNMENT)
	}

	// Generate PE base relocation blocks, grouped by 4KB page.
	// Each block: { uint32 PageRVA, uint32 BlockSize, uint16[] entries }
	// Entry format: type(4 bits) | offset(12 bits)
	// Type 10 = IMAGE_REL_BASED_DIR64 (64-bit pointer)
	const relocTypeDIR64 = 10

	var result []byte
	blockStart := 0
	for blockStart < len(relocRVAs) {
		pageRVA := relocRVAs[blockStart] &^ 0xFFF // 4KB page
		blockEnd := blockStart

		// Collect all entries on this page
		for blockEnd < len(relocRVAs) && (relocRVAs[blockEnd]&^0xFFF) == pageRVA {
			blockEnd++
		}

		numEntries := blockEnd - blockStart
		// Pad to even number of entries (block size must be 4-byte aligned)
		padded := numEntries
		if padded%2 != 0 {
			padded++
		}
		blockSize := uint32(8 + padded*2) // 8-byte header + 2 bytes per entry

		// Write block header
		var hdr [8]byte
		binary.LittleEndian.PutUint32(hdr[0:4], pageRVA)
		binary.LittleEndian.PutUint32(hdr[4:8], blockSize)
		result = append(result, hdr[:]...)

		// Write entries
		for i := blockStart; i < blockEnd; i++ {
			offset := relocRVAs[i] & 0xFFF
			entry := uint16(relocTypeDIR64<<12) | uint16(offset)
			var buf [2]byte
			binary.LittleEndian.PutUint16(buf[:], entry)
			result = append(result, buf[:]...)
		}

		// Padding entry (type 0 = IMAGE_REL_BASED_ABSOLUTE = no-op)
		if numEntries%2 != 0 {
			result = append(result, 0, 0)
		}

		blockStart = blockEnd
	}

	return result
}

// createPEFile builds the complete PE file
func createPEFile(elfFile *elf.File, sections []*PESection, machineType uint16, imageBase uint64) ([]byte, error) {
	buf := make([]byte, 0, 1024*1024) // Pre-allocate 1MB

	// DOS Header
	buf = append(buf, dosHeader...)
	buf = append(buf, dosStub...)

	// PE Signature (at offset 0x80)
	buf = append(buf, 'P', 'E', 0, 0)

	// COFF Header (20 bytes)
	coffHeader := make([]byte, 20)
	binary.LittleEndian.PutUint16(coffHeader[0:2], machineType)                // Machine
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
	entryRVA := uint32(elfFile.Entry - imageBase)
	binary.LittleEndian.PutUint32(optHeader[16:20], entryRVA)                  // AddressOfEntryPoint
	binary.LittleEndian.PutUint32(optHeader[20:24], baseOfCode)                // BaseOfCode

	// NT additional fields (PE32+)
	binary.LittleEndian.PutUint64(optHeader[24:32], imageBase)                  // ImageBase
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
	// BaseReloc data directory (index 5): point at .reloc section with its
	// actual data size. The .reloc section contains PE base relocations
	// generated by scanning Go's data sections for absolute pointers.
	for _, sec := range sections {
		if sec.Name == ".reloc" {
			binary.LittleEndian.PutUint32(optHeader[152:156], sec.VirtualAddress)  // RVA
			binary.LittleEndian.PutUint32(optHeader[156:160], sec.VirtualSize)     // Size
			break
		}
	}

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
