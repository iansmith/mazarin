// compute-linker-values.go - Compute and patch linker symbol values in ELF binary
//
// This tool reads an ELF binary (built with Go's native toolchain) and computes
// the values that would normally be provided by the GNU linker script (linker.ld).
//
// The main.Linker* variables in layout.go need these values to be populated.
// With -patch flag, this tool writes the computed values directly into the binary.
//
// NOTE: Go's -T flag creates ELF files with "negative" program header offsets
// that the standard debug/elf package cannot handle. This tool parses the ELF
// manually to work around this limitation.
//
// Usage:
//   go run compute-linker-values.go <elf-file>                        # Print computed values
//   go run compute-linker-values.go -patch <elf-file>                 # Patch binary in place
//   go run compute-linker-values.go -patch -kmazarin <kelf> <elf>     # Patch with kmazarin info

package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"sort"
)

// ELF64 header structure
type Elf64Header struct {
	Magic      [4]byte
	Class      uint8
	Data       uint8
	Version    uint8
	OSABI      uint8
	ABIVersion uint8
	Pad        [7]byte
	Type       uint16
	Machine    uint16
	Version2   uint32
	Entry      uint64
	PhOff      uint64
	ShOff      uint64
	Flags      uint32
	EhSize     uint16
	PhEntSize  uint16
	PhNum      uint16
	ShEntSize  uint16
	ShNum      uint16
	ShStrNdx   uint16
}

// ELF64 section header
type Elf64SectionHeader struct {
	Name      uint32
	Type      uint32
	Flags     uint64
	Addr      uint64
	Offset    uint64
	Size      uint64
	Link      uint32
	Info      uint32
	AddrAlign uint64
	EntSize   uint64
}

// ELF64 symbol
type Elf64Sym struct {
	Name  uint32
	Info  uint8
	Other uint8
	Shndx uint16
	Value uint64
	Size  uint64
}

// Section types
const (
	SHT_NULL     = 0
	SHT_PROGBITS = 1
	SHT_SYMTAB   = 2
	SHT_STRTAB   = 3
	SHT_NOBITS   = 8
)

// LinkerValue represents a computed linker symbol value
type LinkerValue struct {
	Name       string // Go variable name (e.g., "LinkerTextStart")
	LinkerName string // Original linker symbol name (e.g., "__text_start")
	Value      uint64
	Source     string // How this value was computed
	SymbolAddr uint64 // Address of the Go variable in the binary
	NeedsPatch bool   // True if this value needs to be patched (section boundaries)
}

// Section represents a parsed ELF section
type Section struct {
	Name   string
	Type   uint32
	Addr   uint64
	Offset uint64
	Size   uint64
}

var patchMode bool
var kmazarinPath string

func main() {
	flag.BoolVar(&patchMode, "patch", false, "Patch the binary with computed values")
	flag.StringVar(&kmazarinPath, "kmazarin", "", "Path to kmazarin.elf for size calculation")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-patch] [-kmazarin <kmazarin.elf>] <elf-file>\n", os.Args[0])
		os.Exit(1)
	}

	elfPath := flag.Arg(0)

	// Calculate kmazarin size if path provided
	var kmazarinSize uint64
	if kmazarinPath != "" {
		var err error
		kmazarinSize, err = calculateKmazarinSize(kmazarinPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not calculate kmazarin size: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Kmazarin memory size: 0x%X (%d bytes)\n", kmazarinSize, kmazarinSize)
		}
	}

	data, err := os.ReadFile(elfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading ELF file: %v\n", err)
		os.Exit(1)
	}

	// Parse ELF header
	if len(data) < 64 {
		fmt.Fprintf(os.Stderr, "File too small to be an ELF\n")
		os.Exit(1)
	}

	// Check magic
	if data[0] != 0x7f || data[1] != 'E' || data[2] != 'L' || data[3] != 'F' {
		fmt.Fprintf(os.Stderr, "Not an ELF file\n")
		os.Exit(1)
	}

	// Check 64-bit
	if data[4] != 2 {
		fmt.Fprintf(os.Stderr, "Not a 64-bit ELF\n")
		os.Exit(1)
	}

	// Check little-endian
	if data[5] != 1 {
		fmt.Fprintf(os.Stderr, "Not little-endian\n")
		os.Exit(1)
	}

	// Parse header
	var hdr Elf64Header
	hdr.ShOff = binary.LittleEndian.Uint64(data[40:48])
	hdr.ShEntSize = binary.LittleEndian.Uint16(data[58:60])
	hdr.ShNum = binary.LittleEndian.Uint16(data[60:62])
	hdr.ShStrNdx = binary.LittleEndian.Uint16(data[62:64])

	// Parse section headers
	sections := make(map[string]*Section)

	// First, find the section string table
	shstrOff := hdr.ShOff + uint64(hdr.ShStrNdx)*uint64(hdr.ShEntSize)
	if shstrOff+64 > uint64(len(data)) {
		fmt.Fprintf(os.Stderr, "Section string table header out of bounds\n")
		os.Exit(1)
	}

	shstrOffset := binary.LittleEndian.Uint64(data[shstrOff+24 : shstrOff+32])
	shstrSize := binary.LittleEndian.Uint64(data[shstrOff+32 : shstrOff+40])

	if shstrOffset+shstrSize > uint64(len(data)) {
		fmt.Fprintf(os.Stderr, "Section string table out of bounds\n")
		os.Exit(1)
	}

	shstrtab := data[shstrOffset : shstrOffset+shstrSize]

	// Now parse all sections
	for i := uint16(0); i < hdr.ShNum; i++ {
		off := hdr.ShOff + uint64(i)*uint64(hdr.ShEntSize)
		if off+64 > uint64(len(data)) {
			continue
		}

		nameIdx := binary.LittleEndian.Uint32(data[off : off+4])
		secType := binary.LittleEndian.Uint32(data[off+4 : off+8])
		addr := binary.LittleEndian.Uint64(data[off+16 : off+24])
		offset := binary.LittleEndian.Uint64(data[off+24 : off+32])
		size := binary.LittleEndian.Uint64(data[off+32 : off+40])

		// Get section name from string table
		name := ""
		if nameIdx < uint32(len(shstrtab)) {
			end := nameIdx
			for end < uint32(len(shstrtab)) && shstrtab[end] != 0 {
				end++
			}
			name = string(shstrtab[nameIdx:end])
		}

		sec := &Section{
			Name:   name,
			Type:   secType,
			Addr:   addr,
			Offset: offset,
			Size:   size,
		}
		sections[name] = sec
	}

	// Find symbol table and string table
	symbolAddrs := make(map[string]uint64)

	symtab := sections[".symtab"]
	strtab := sections[".strtab"]

	if symtab != nil && strtab != nil && symtab.Type == SHT_SYMTAB {
		symtabData := data[symtab.Offset : symtab.Offset+symtab.Size]
		strtabData := data[strtab.Offset : strtab.Offset+strtab.Size]

		numSyms := symtab.Size / 24 // sizeof(Elf64_Sym) = 24
		for i := uint64(0); i < numSyms; i++ {
			off := i * 24
			if off+24 > uint64(len(symtabData)) {
				break
			}

			nameIdx := binary.LittleEndian.Uint32(symtabData[off : off+4])
			value := binary.LittleEndian.Uint64(symtabData[off+8 : off+16])

			// Get symbol name
			if nameIdx < uint32(len(strtabData)) {
				end := nameIdx
				for end < uint32(len(strtabData)) && strtabData[end] != 0 {
					end++
				}
				name := string(strtabData[nameIdx:end])
				symbolAddrs[name] = value
			}
		}
	}

	// Compute linker values
	values := computeLinkerValues(sections, symbolAddrs, kmazarinSize)

	if patchMode {
		// Patch the binary
		patched := patchBinary(data, values, sections)
		if err := os.WriteFile(elfPath, patched, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing patched file: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Patched %s with %d linker values\n", elfPath, countPatched(values))
	} else {
		// Print results
		printValues(values)
	}
}

func countPatched(values []LinkerValue) int {
	count := 0
	for _, v := range values {
		if v.NeedsPatch {
			count++
		}
	}
	return count
}

func printValues(values []LinkerValue) {
	fmt.Println("// Computed Linker Values for cardinal-go.elf")
	fmt.Println("// These values replace what would be provided by linker.ld")
	fmt.Println("//")
	fmt.Println("// Format: VariableName = Value (source) @ SymbolAddress")
	fmt.Println()

	// Group by category
	categories := map[string][]LinkerValue{
		"Section Boundaries": {},
		"Memory Layout":      {},
		"Stack":              {},
		"MMIO Devices":       {},
		"Embedded Kmazarin":  {},
	}

	for _, v := range values {
		switch v.Name {
		case "LinkerStart", "LinkerTextStart", "LinkerTextEnd",
			"LinkerRodataStart", "LinkerRodataEnd",
			"LinkerDataStart", "LinkerDataEnd",
			"LinkerBssStart", "LinkerBssEnd", "LinkerEnd":
			categories["Section Boundaries"] = append(categories["Section Boundaries"], v)
		case "LinkerRamStart", "LinkerDtbBootAddr", "LinkerDtbSize",
			"LinkerCardinalEnd", "LinkerCardinalAllocationSize",
			"LinkerKmazarinLoadAddr", "LinkerPageTablesStart", "LinkerPageTablesEnd":
			categories["Memory Layout"] = append(categories["Memory Layout"], v)
		case "LinkerStackTop", "LinkerG0StackBottom":
			categories["Stack"] = append(categories["Stack"], v)
		case "LinkerGicBase", "LinkerGicSize", "LinkerUartBase", "LinkerUartSize",
			"LinkerRtcBase", "LinkerFwcfgBase", "LinkerFwcfgSize",
			"LinkerBochsDisplayBase", "LinkerBochsDisplaySize",
			"LinkerPciBarBase", "LinkerPciBarSize":
			categories["MMIO Devices"] = append(categories["MMIO Devices"], v)
		case "LinkerKmazarinStart", "LinkerKmazarinSize":
			categories["Embedded Kmazarin"] = append(categories["Embedded Kmazarin"], v)
		}
	}

	// Print by category
	catOrder := []string{"Section Boundaries", "Memory Layout", "Stack", "MMIO Devices", "Embedded Kmazarin"}
	for _, cat := range catOrder {
		vals := categories[cat]
		if len(vals) == 0 {
			continue
		}
		fmt.Printf("// === %s ===\n", cat)
		for _, v := range vals {
			patchMark := ""
			if v.NeedsPatch {
				patchMark = " [PATCH]"
			}
			if v.SymbolAddr != 0 {
				fmt.Printf("// %s (%s) = 0x%016X  // %s @ 0x%X%s\n",
					v.Name, v.LinkerName, v.Value, v.Source, v.SymbolAddr, patchMark)
			} else {
				fmt.Printf("// %s (%s) = 0x%016X  // %s%s\n",
					v.Name, v.LinkerName, v.Value, v.Source, patchMark)
			}
		}
		fmt.Println()
	}

	// Print as Go code
	fmt.Println("// Go code to initialize these values:")
	fmt.Println("func initLinkerValues() {")
	for _, v := range values {
		fmt.Printf("\t%s = 0x%X\n", v.Name, v.Value)
	}
	fmt.Println("}")
}

func patchBinary(data []byte, values []LinkerValue, sections map[string]*Section) []byte {
	// Find the .data section to calculate file offsets
	dataSec := sections[".data"]
	noptrdataSec := sections[".noptrdata"]

	for _, v := range values {
		if !v.NeedsPatch || v.SymbolAddr == 0 {
			continue
		}

		// Find which section contains this symbol and calculate file offset
		var fileOffset uint64
		found := false

		// Check .noptrdata first (Go places most vars here)
		if noptrdataSec != nil && v.SymbolAddr >= noptrdataSec.Addr && v.SymbolAddr < noptrdataSec.Addr+noptrdataSec.Size {
			fileOffset = noptrdataSec.Offset + (v.SymbolAddr - noptrdataSec.Addr)
			found = true
		}
		// Then check .data
		if !found && dataSec != nil && v.SymbolAddr >= dataSec.Addr && v.SymbolAddr < dataSec.Addr+dataSec.Size {
			fileOffset = dataSec.Offset + (v.SymbolAddr - dataSec.Addr)
			found = true
		}

		if !found {
			fmt.Fprintf(os.Stderr, "Warning: Could not find section for %s @ 0x%X\n", v.Name, v.SymbolAddr)
			continue
		}

		if fileOffset+8 > uint64(len(data)) {
			fmt.Fprintf(os.Stderr, "Warning: File offset 0x%X for %s is out of bounds\n", fileOffset, v.Name)
			continue
		}

		// Write the value (little-endian)
		binary.LittleEndian.PutUint64(data[fileOffset:], v.Value)
		fmt.Printf("  Patched %s @ file:0x%X = 0x%X\n", v.Name, fileOffset, v.Value)
	}

	return data
}

// calculateKmazarinSize parses kmazarin.elf and returns the memory size needed
// This is from .text start to end of the last section (BSS/noptrbss)
func calculateKmazarinSize(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	if len(data) < 64 {
		return 0, fmt.Errorf("file too small")
	}

	// Check ELF magic
	if data[0] != 0x7f || data[1] != 'E' || data[2] != 'L' || data[3] != 'F' {
		return 0, fmt.Errorf("not an ELF file")
	}

	// Parse section headers to find .text start and last section end
	shOff := binary.LittleEndian.Uint64(data[40:48])
	shEntSize := binary.LittleEndian.Uint16(data[58:60])
	shNum := binary.LittleEndian.Uint16(data[60:62])
	shStrNdx := binary.LittleEndian.Uint16(data[62:64])

	// Get section string table
	shstrOff := shOff + uint64(shStrNdx)*uint64(shEntSize)
	if shstrOff+64 > uint64(len(data)) {
		return 0, fmt.Errorf("section string table out of bounds")
	}
	shstrOffset := binary.LittleEndian.Uint64(data[shstrOff+24 : shstrOff+32])
	shstrSize := binary.LittleEndian.Uint64(data[shstrOff+32 : shstrOff+40])
	if shstrOffset+shstrSize > uint64(len(data)) {
		return 0, fmt.Errorf("section string table data out of bounds")
	}
	shstrtab := data[shstrOffset : shstrOffset+shstrSize]

	var textStart uint64
	var maxEnd uint64

	// Parse sections to find .text start and highest end address
	for i := uint16(0); i < shNum; i++ {
		off := shOff + uint64(i)*uint64(shEntSize)
		if off+64 > uint64(len(data)) {
			continue
		}

		nameIdx := binary.LittleEndian.Uint32(data[off : off+4])
		addr := binary.LittleEndian.Uint64(data[off+16 : off+24])
		size := binary.LittleEndian.Uint64(data[off+32 : off+40])

		// Get section name
		name := ""
		if nameIdx < uint32(len(shstrtab)) {
			end := nameIdx
			for end < uint32(len(shstrtab)) && shstrtab[end] != 0 {
				end++
			}
			name = string(shstrtab[nameIdx:end])
		}

		// Find .text start
		if name == ".text" {
			textStart = addr
		}

		// Track highest end address (for loaded sections)
		// Include .bss and .noptrbss which have NOBITS but consume memory
		if addr != 0 && size != 0 {
			sectionEnd := addr + size
			if sectionEnd > maxEnd {
				maxEnd = sectionEnd
			}
		}
	}

	if textStart == 0 {
		return 0, fmt.Errorf(".text section not found")
	}

	// Size is from .text start to end of last section
	return maxEnd - textStart, nil
}

func computeLinkerValues(sections map[string]*Section, symbolAddrs map[string]uint64, kmazarinSize uint64) []LinkerValue {
	var values []LinkerValue

	// Helper to add a value
	add := func(name, linkerName string, value uint64, source string, needsPatch bool) {
		symAddr := symbolAddrs["main."+name]
		values = append(values, LinkerValue{
			Name:       name,
			LinkerName: linkerName,
			Value:      value,
			Source:     source,
			SymbolAddr: symAddr,
			NeedsPatch: needsPatch,
		})
	}

	// === Section Boundaries ===
	// These come from actual ELF sections and NEED PATCHING

	// .text section
	if sec := sections[".text"]; sec != nil {
		add("LinkerStart", "__start", sec.Addr, ".text section start", true)
		add("LinkerTextStart", "__text_start", sec.Addr, ".text section start", true)
		add("LinkerTextEnd", "__text_end", sec.Addr+sec.Size, ".text section end", true)
	}

	// .rodata section
	if sec := sections[".rodata"]; sec != nil {
		add("LinkerRodataStart", "__rodata_start", sec.Addr, ".rodata section start", true)
		// .rodata end includes typelink, itablink, gopclntab
		rodataEnd := sec.Addr + sec.Size
		if pclntab := sections[".gopclntab"]; pclntab != nil {
			rodataEnd = pclntab.Addr + pclntab.Size
		}
		add("LinkerRodataEnd", "__rodata_end", rodataEnd, "end of .gopclntab", true)
	}

	// .data section (includes .noptrdata)
	if noptrdata := sections[".noptrdata"]; noptrdata != nil {
		add("LinkerDataStart", "__data_start", noptrdata.Addr, ".noptrdata section start", true)
	} else if data := sections[".data"]; data != nil {
		add("LinkerDataStart", "__data_start", data.Addr, ".data section start", true)
	}
	if data := sections[".data"]; data != nil {
		add("LinkerDataEnd", "__data_end", data.Addr+data.Size, ".data section end", true)
	}

	// .bss section
	if bss := sections[".bss"]; bss != nil {
		add("LinkerBssStart", "__bss_start", bss.Addr, ".bss section start", true)
		bssEnd := bss.Addr + bss.Size
		// Include .noptrbss if present
		if noptrbss := sections[".noptrbss"]; noptrbss != nil {
			if noptrbss.Addr+noptrbss.Size > bssEnd {
				bssEnd = noptrbss.Addr + noptrbss.Size
			}
		}
		add("LinkerBssEnd", "__bss_end", bssEnd, "end of .bss/.noptrbss", true)
		add("LinkerEnd", "__end", bssEnd, "end of .bss/.noptrbss (same as bss_end)", true)
	}

	// === Memory Layout (from linker.ld constants) ===
	// These are FIXED values - no patching needed (already correct in binary)

	const (
		BOOT_ADDRESS        = 0x40000000
		DTB_SIZE            = 0x100000  // 1 MB
		CARDINAL_ALLOC_SIZE = 0xF00000  // 15 MB
		PAGE_TABLE_SIZE     = 0x800000  // 8 MB

		DTB_START          = BOOT_ADDRESS
		CARDINAL_START     = DTB_START + DTB_SIZE
		CARDINAL_END       = CARDINAL_START + CARDINAL_ALLOC_SIZE
		PAGE_TABLE_START   = CARDINAL_END
		PAGE_TABLE_END     = PAGE_TABLE_START + PAGE_TABLE_SIZE
		KMAZARIN_LOAD_ADDR = PAGE_TABLE_END
	)

	add("LinkerRamStart", "__ram_start", BOOT_ADDRESS, "QEMU virt RAM start", false)
	add("LinkerDtbBootAddr", "__dtb_boot_addr", DTB_START, "DTB location (QEMU fixed)", false)
	add("LinkerDtbSize", "__dtb_size", DTB_SIZE, "DTB allocation size", false)
	add("LinkerCardinalEnd", "__cardinal_end", CARDINAL_END, "cardinal allocation end", false)
	add("LinkerCardinalAllocationSize", "__cardinal_allocation_size", CARDINAL_ALLOC_SIZE, "cardinal allocation size", false)
	add("LinkerKmazarinLoadAddr", "__kmazarin_load_addr", KMAZARIN_LOAD_ADDR, "where kmazarin is loaded", false)
	add("LinkerPageTablesStart", "__page_tables_start", PAGE_TABLE_START, "page table region start", false)
	add("LinkerPageTablesEnd", "__page_tables_end", PAGE_TABLE_END, "page table region end", false)

	// === Stack (placeholders - calculated at runtime) ===
	// Fixed values - no patching needed
	add("LinkerStackTop", "__stack_top", 0x5F000000, "g0 stack top (from CLAUDE.md)", false)
	add("LinkerG0StackBottom", "__g0_stack_bottom", 0x5EFF0000, "g0 stack bottom (from CLAUDE.md)", false)

	// === MMIO Device Addresses (QEMU virt machine fixed) ===
	// Fixed values - no patching needed
	add("LinkerGicBase", "__gic_base", 0x08000000, "GIC base (QEMU fixed)", false)
	add("LinkerGicSize", "__gic_size", 0x00020000, "GIC size (128KB)", false)
	add("LinkerUartBase", "__uart_base", 0x09000000, "UART base (QEMU fixed)", false)
	add("LinkerUartSize", "__uart_size", 0x00010000, "UART size (64KB)", false)
	add("LinkerRtcBase", "__rtc_base", 0x09010000, "RTC base (QEMU fixed)", false)
	add("LinkerFwcfgBase", "__fwcfg_base", 0x09020000, "fw_cfg base (QEMU fixed)", false)
	add("LinkerFwcfgSize", "__fwcfg_size", 0x00010000, "fw_cfg size (64KB)", false)
	add("LinkerBochsDisplayBase", "__bochs_display_base", 0x10000000, "bochs-display base", false)
	add("LinkerBochsDisplaySize", "__bochs_display_size", 0x01000000, "bochs-display size (16MB)", false)
	add("LinkerPciBarBase", "__pci_bar_base", 0x11000000, "PCI BAR pool start", false)
	add("LinkerPciBarSize", "__pci_bar_size", 0x0F000000, "PCI BAR pool size (240MB)", false)

	// === Embedded Kmazarin ===
	// LinkerKmazarinStart is where kmazarin is loaded (same as KMAZARIN_LOAD_ADDR)
	// LinkerKmazarinSize is calculated from kmazarin.elf if -kmazarin flag provided
	add("LinkerKmazarinStart", "__kmazarin_start", KMAZARIN_LOAD_ADDR, "kmazarin load address (0x41800000)", true)
	if kmazarinSize > 0 {
		add("LinkerKmazarinSize", "__kmazarin_size", kmazarinSize, "calculated from kmazarin.elf", true)
	} else {
		add("LinkerKmazarinSize", "__kmazarin_size", 0, "not calculated (use -kmazarin flag)", true)
	}

	// Sort by address for readability
	sort.Slice(values, func(i, j int) bool {
		return values[i].Value < values[j].Value
	})

	return values
}
