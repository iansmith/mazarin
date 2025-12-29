// goasm2gnu - Transpile Go assembly to GNU assembly
//
// This tool allows writing ARM64 assembly in Go's Plan 9 syntax while
// producing output that can be linked with GCC/ld.
//
// Usage:
//   go run goasm2gnu.go input.s > output.s          # Generate GNU assembly
//   go run goasm2gnu.go -elf -o output.o input.s    # Generate ELF directly
//
// The tool:
// 1. Runs `go tool asm` on the input to create a Go object file
// 2. Uses `go tool objdump` to extract verified machine code
// 3. Generates GNU assembly with .byte directives OR writes ELF directly
//
// Using Go's assembler means:
// - Full syntax checking and error reporting
// - Access to Go's PCALIGN, WORD, etc. directives
// - Verified machine code (no transcription errors)

package main

import (
	"bufio"
	"bytes"
	"debug/elf"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Relocation represents an ELF relocation entry
type Relocation struct {
	Offset     uint64 // Offset within the section
	Type       uint32 // ELF relocation type (R_AARCH64_*)
	SymbolName string // Target symbol name
	Addend     int64  // Addend value
}

type Symbol struct {
	Name        string
	Offset      uint64 // Offset within the text section
	Size        int
	Bytes       []byte
	Section     string        // .text, .rodata, etc.
	Relocations []Relocation  // Relocations for this symbol
}

// ARM64 ELF relocation types
const (
	R_AARCH64_NONE              = 0
	R_AARCH64_ABS64             = 257
	R_AARCH64_ABS32             = 258
	R_AARCH64_CALL26            = 283
	R_AARCH64_JUMP26            = 282
	R_AARCH64_ADR_PREL_PG_HI21  = 275
	R_AARCH64_ADD_ABS_LO12_NC   = 277
	R_AARCH64_LDST64_ABS_LO12_NC = 286
)

var (
	// Matches: TEXT <unlinkable>.sync_el1_handler(SB) /path/file.s
	textHeaderRe = regexp.MustCompile(`^TEXT\s+(?:<[^>]+>\.)?(\S+)\(SB\)`)

	// Matches: file.s:5  0x3a0  d2824680  MOVD $4660, R0  [reloc info]
	// The relocation info is optional: [start:size]R_TYPE:symbol
	objdumpLineRe = regexp.MustCompile(`^\s+\S+:\d+\s+(0x[0-9a-fA-F]+)\s+([0-9a-fA-F]+)\s+`)

	// Matches relocation info: [0:8]R_ADDRARM64:runtime.g0
	relocRe = regexp.MustCompile(`\[(\d+):(\d+)\](R_\w+):(\S+)`)
)

func main() {
	section := flag.String("section", ".text", "Output section name")
	global := flag.Bool("global", true, "Make symbols global")
	elfOutput := flag.Bool("elf", false, "Output ELF file directly instead of GNU assembly")
	outputFile := flag.String("o", "", "Output file (default: stdout for asm, required for -elf)")
	verbose := flag.Bool("v", false, "Verbose output")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] input.s\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s input.s > output.s              # GNU assembly to stdout\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -elf -o output.o input.s        # ELF object file\n", os.Args[0])
		os.Exit(1)
	}

	if *elfOutput && *outputFile == "" {
		fmt.Fprintf(os.Stderr, "Error: -o is required when using -elf\n")
		os.Exit(1)
	}

	inputFile := flag.Arg(0)

	// Step 1: Assemble with go tool asm
	objFile, cleanup, err := assembleGoAsm(inputFile, *verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error assembling: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	// Step 2: Extract symbols and bytes using go tool objdump (for text)
	symbols, err := extractSymbols(objFile, *verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting symbols: %v\n", err)
		os.Exit(1)
	}

	// Step 2b: Extract data symbols from the Go object file
	dataSymbols, err := extractDataSymbols(objFile, *verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting data symbols: %v\n", err)
		os.Exit(1)
	}
	symbols = append(symbols, dataSymbols...)

	if len(symbols) == 0 {
		fmt.Fprintf(os.Stderr, "Warning: no symbols found in %s\n", inputFile)
	}

	// Step 3: Output
	if *elfOutput {
		err = writeELF(*outputFile, symbols, *section)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing ELF: %v\n", err)
			os.Exit(1)
		}
		if *verbose {
			fmt.Fprintf(os.Stderr, "Wrote ELF to %s\n", *outputFile)
		}
	} else {
		var w io.Writer = os.Stdout
		if *outputFile != "" {
			f, err := os.Create(*outputFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating output: %v\n", err)
				os.Exit(1)
			}
			defer f.Close()
			w = f
		}
		generateGNUAsm(w, symbols, *section, *global)
	}
}

func assembleGoAsm(inputFile string, verbose bool) (objFile string, cleanup func(), err error) {
	// Find Go's include paths
	goroot := os.Getenv("GOROOT")
	if goroot == "" {
		cmd := exec.Command("go", "env", "GOROOT")
		out, err := cmd.Output()
		if err != nil {
			return "", nil, fmt.Errorf("cannot find GOROOT: %v", err)
		}
		goroot = strings.TrimSpace(string(out))
	}

	// Create temp file for output
	tmpDir, err := os.MkdirTemp("", "goasm2gnu")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(tmpDir) }

	objFile = filepath.Join(tmpDir, "output.o")

	includePaths := []string{
		filepath.Join(goroot, "src", "runtime"),
		filepath.Join(goroot, "pkg", "include"),
	}

	// Build command: go tool asm -o output.o input.s
	args := []string{"tool", "asm", "-o", objFile}
	for _, inc := range includePaths {
		args = append(args, "-I", inc)
	}
	args = append(args, inputFile)

	if verbose {
		fmt.Fprintf(os.Stderr, "Running: go %s\n", strings.Join(args, " "))
	}

	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOARCH=arm64", "GOOS=linux")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("%v: %s", err, stderr.String())
	}

	return objFile, cleanup, nil
}

func extractSymbols(objFile string, verbose bool) ([]Symbol, error) {
	// Run go tool objdump on the object file
	cmd := exec.Command("go", "tool", "objdump", objFile)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, stderr.String())
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "objdump output:\n%s\n", stdout.String())
	}

	return parseObjdumpOutput(stdout.Bytes())
}

func parseObjdumpOutput(data []byte) ([]Symbol, error) {
	var symbols []Symbol
	var currentSym *Symbol
	var firstOffset uint64 // First offset in the file (to compute relative offsets)
	var gotFirstOffset bool

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()

		// Check for TEXT header line
		if matches := textHeaderRe.FindStringSubmatch(line); matches != nil {
			// Save previous symbol
			if currentSym != nil && len(currentSym.Bytes) > 0 {
				symbols = append(symbols, *currentSym)
			}

			name := matches[1]
			// Remove parentheses if present
			name = strings.TrimSuffix(name, "(SB)")
			// Convert · to . for ELF symbol compatibility (e.g., runtime·g0 -> runtime.g0)
			name = strings.ReplaceAll(name, "·", ".")

			currentSym = &Symbol{
				Name:        name,
				Bytes:       make([]byte, 0),
				Section:     ".text",
				Relocations: make([]Relocation, 0),
			}
			gotFirstOffset = false
			continue
		}

		// Check for instruction line with hex bytes
		if currentSym != nil {
			if matches := objdumpLineRe.FindStringSubmatch(line); matches != nil {
				offsetStr := matches[1]
				hexBytes := matches[2]

				// Parse offset
				offset, _ := strconv.ParseUint(offsetStr[2:], 16, 64)
				if !gotFirstOffset {
					firstOffset = offset
					gotFirstOffset = true
				}

				// Compute relative offset within this symbol's bytes
				relativeOffset := offset - firstOffset

				// Parse hex bytes (they come as a single hex number, e.g., "d2824680")
				// Convert to little-endian bytes
				val, err := strconv.ParseUint(hexBytes, 16, 32)
				if err == nil {
					// ARM64 is little-endian, so we need to output bytes in LE order
					b := make([]byte, 4)
					binary.LittleEndian.PutUint32(b, uint32(val))
					currentSym.Bytes = append(currentSym.Bytes, b...)
				}

				// Check for relocation info in the line
				if relocMatches := relocRe.FindAllStringSubmatch(line, -1); relocMatches != nil {
					for _, rm := range relocMatches {
						// rm[1] = start, rm[2] = size, rm[3] = type, rm[4] = symbol
						relocType := rm[3]
						relocSymbol := rm[4]

						// Convert Go relocation types to ELF types
						// R_ADDRARM64 maps to ADRP+ADD pair
						// R_CALLARM64 maps to CALL26
						var elfType uint32
						switch relocType {
						case "R_ADDRARM64":
							// This is the ADRP instruction, ADD follows
							elfType = R_AARCH64_ADR_PREL_PG_HI21
						case "R_CALLARM64":
							elfType = R_AARCH64_CALL26
						case "R_ARM64_PCREL_LDST8", "R_ARM64_PCREL_LDST16",
						     "R_ARM64_PCREL_LDST32", "R_ARM64_PCREL_LDST64":
							elfType = R_AARCH64_LDST64_ABS_LO12_NC
						default:
							// Unknown type, skip
							continue
						}

						// Clean up symbol name (remove trailing whitespace, convert · to .)
						relocSymbol = strings.TrimSpace(relocSymbol)
						relocSymbol = strings.ReplaceAll(relocSymbol, "·", ".")

						currentSym.Relocations = append(currentSym.Relocations, Relocation{
							Offset:     relativeOffset,
							Type:       elfType,
							SymbolName: relocSymbol,
							Addend:     0,
						})

						// For R_ADDRARM64, we also need to add the ADD relocation
						// The ADD instruction follows immediately (offset + 4)
						if relocType == "R_ADDRARM64" {
							currentSym.Relocations = append(currentSym.Relocations, Relocation{
								Offset:     relativeOffset + 4,
								Type:       R_AARCH64_ADD_ABS_LO12_NC,
								SymbolName: relocSymbol,
								Addend:     0,
							})
						}
					}
				}
			}
		}
	}

	// Don't forget the last symbol
	if currentSym != nil && len(currentSym.Bytes) > 0 {
		symbols = append(symbols, *currentSym)
	}

	return symbols, scanner.Err()
}

func extractDataSymbols(objFile string, verbose bool) ([]Symbol, error) {
	// Read the entire Go object file
	data, err := os.ReadFile(objFile)
	if err != nil {
		return nil, err
	}

	// Use go tool nm to find data symbols
	cmd := exec.Command("go", "tool", "nm", objFile)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go tool nm failed: %v", err)
	}

	var symbols []Symbol
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: "  offset type name"
		// We're looking for 'R' (read-only data) and 'D' (data) symbols
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		symbolType := parts[1]
		symbolName := parts[2]

		// Only process read-only data symbols
		if symbolType != "R" {
			continue
		}

		// Parse offset
		offsetStr := parts[0]
		offset, err := strconv.ParseUint(offsetStr, 16, 64)
		if err != nil {
			continue
		}

		// Clean up the symbol name
		symbolName = strings.TrimPrefix(symbolName, "<unlinkable>.")
		symbolName = strings.ReplaceAll(symbolName, "·", "_")

		// The Go object file contains the data at the symbol's offset
		// We need to find the size by looking at the next symbol or end of data
		// For now, we'll extract the data by reading the object file
		// This is a simplification - proper implementation would parse the Go object format

		if verbose {
			fmt.Fprintf(os.Stderr, "Found data symbol: %s at offset 0x%x\n", symbolName, offset)
		}

		// Read data from the Go object file
		// The Go object file format has data sections after the header
		// For simplicity, we'll extract by finding the data in the file
		symbolBytes, err := extractDataFromGoObj(data, offset, symbolName)
		if err != nil || len(symbolBytes) == 0 {
			if verbose {
				fmt.Fprintf(os.Stderr, "Warning: could not extract data for %s: %v\n", symbolName, err)
			}
			continue
		}

		symbols = append(symbols, Symbol{
			Name:    symbolName,
			Offset:  offset,
			Bytes:   symbolBytes,
			Section: ".rodata",
		})
	}

	return symbols, scanner.Err()
}

func extractDataFromGoObj(objData []byte, offset uint64, name string) ([]byte, error) {
	// Go object files are in a custom format
	// The data section starts after the symbol table
	// This is a simplified extraction that reads the actual data bytes

	// Search for the data pattern in the object file
	// Go stores data sequentially after the header and symbol table
	// For DATA directives, the actual bytes are embedded in the object file

	// Since we can't easily parse the Go object format without using internal packages,
	// we'll use a heuristic: the data should be present in the file
	// We'll search for likely data sections

	// For now, try to find data by reading from a reasonable offset
	// The Go object format typically has data after offset 0x200 or so
	if int(offset) >= len(objData) {
		return nil, fmt.Errorf("offset beyond file size")
	}

	// Try to find the actual data size by looking for the next symbol or zero padding
	// This is approximate - a proper implementation would parse the object file format
	dataStart := int(offset)
	dataEnd := dataStart

	// Scan forward to find the extent of the data
	// Stop at excessive zeros or end of file
	zeroCount := 0
	maxZeros := 16 // Allow some internal zeros
	for dataEnd < len(objData) && dataEnd-dataStart < 2*1024*1024 { // Max 2MB per symbol
		if objData[dataEnd] == 0 {
			zeroCount++
			if zeroCount > maxZeros {
				break
			}
		} else {
			zeroCount = 0
		}
		dataEnd++
	}

	// Back up past the trailing zeros
	for dataEnd > dataStart && objData[dataEnd-1] == 0 {
		dataEnd--
	}

	if dataEnd <= dataStart {
		return nil, fmt.Errorf("no data found")
	}

	return objData[dataStart:dataEnd], nil
}

func generateGNUAsm(w io.Writer, symbols []Symbol, section string, global bool) {
	fmt.Fprintf(w, "// Generated by goasm2gnu from Go assembly\n")
	fmt.Fprintf(w, "// DO NOT EDIT - regenerate from source\n\n")
	fmt.Fprintf(w, ".section \"%s\"\n\n", section)

	for _, sym := range symbols {
		// Global directive
		if global {
			fmt.Fprintf(w, ".global %s\n", sym.Name)
		}

		// Label
		fmt.Fprintf(w, "%s:\n", sym.Name)

		// Bytes in groups of 16
		for i := 0; i < len(sym.Bytes); i += 16 {
			end := i + 16
			if end > len(sym.Bytes) {
				end = len(sym.Bytes)
			}

			fmt.Fprintf(w, "    .byte ")
			for j := i; j < end; j++ {
				if j > i {
					fmt.Fprintf(w, ", ")
				}
				fmt.Fprintf(w, "0x%02x", sym.Bytes[j])
			}
			fmt.Fprintf(w, "\n")
		}

		fmt.Fprintf(w, "\n")
	}
}

// writeELF writes an ELF relocatable object file directly
func writeELF(filename string, symbols []Symbol, section string) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// Calculate sizes and collect all relocations
	var textData []byte
	symOffsets := make(map[string]uint64)
	var allRelocations []Relocation

	for _, sym := range symbols {
		symOffset := uint64(len(textData))
		symOffsets[sym.Name] = symOffset
		textData = append(textData, sym.Bytes...)

		// Adjust relocation offsets to be relative to section start
		for _, r := range sym.Relocations {
			adjustedReloc := r
			adjustedReloc.Offset += symOffset
			allRelocations = append(allRelocations, adjustedReloc)
		}
	}

	// Collect unique external symbols referenced by relocations
	externalSymbols := make(map[string]bool)
	for _, r := range allRelocations {
		externalSymbols[r.SymbolName] = true
	}

	// Remove defined symbols from external list
	for _, sym := range symbols {
		delete(externalSymbols, sym.Name)
	}

	// Create ordered list of external symbols
	var extSymList []string
	for name := range externalSymbols {
		extSymList = append(extSymList, name)
	}
	// Sort for deterministic output
	sort.Strings(extSymList)

	// Build string table
	strtab := []byte{0} // Start with null byte
	shstrtab := []byte{0}

	// Section names
	shstrtabIdx := len(shstrtab)
	shstrtab = append(shstrtab, ".shstrtab\x00"...)
	textIdx := len(shstrtab)
	shstrtab = append(shstrtab, section...) // Keep full section name including '.'
	shstrtab = append(shstrtab, 0)
	symtabIdx := len(shstrtab)
	shstrtab = append(shstrtab, ".symtab\x00"...)
	strtabSectionIdx := len(shstrtab)
	shstrtab = append(shstrtab, ".strtab\x00"...)

	// Add .rela section name if we have relocations
	var relaIdx int
	hasRelocations := len(allRelocations) > 0
	if hasRelocations {
		relaIdx = len(shstrtab)
		relaName := ".rela" + section
		shstrtab = append(shstrtab, relaName...)
		shstrtab = append(shstrtab, 0)
	}

	// Symbol names - defined symbols first, then external
	symNameOffsets := make(map[string]uint32)
	symIndices := make(map[string]uint32) // 1-based index in symbol table

	symIndex := uint32(1) // 0 is null symbol

	// Add defined symbols
	for _, sym := range symbols {
		symNameOffsets[sym.Name] = uint32(len(strtab))
		symIndices[sym.Name] = symIndex
		strtab = append(strtab, sym.Name...)
		strtab = append(strtab, 0)
		symIndex++
	}

	// Add external symbols
	for _, name := range extSymList {
		symNameOffsets[name] = uint32(len(strtab))
		symIndices[name] = symIndex
		strtab = append(strtab, name...)
		strtab = append(strtab, 0)
		symIndex++
	}

	// Total symbols: null + defined + external
	totalSymbols := 1 + len(symbols) + len(extSymList)

	// Number of section headers
	numSections := 5 // null + .text + .symtab + .strtab + .shstrtab
	if hasRelocations {
		numSections++ // + .rela.text
	}

	// ELF Header
	ehdr := elf.Header64{
		Ident: [16]byte{
			0x7f, 'E', 'L', 'F', // Magic
			2,    // 64-bit
			1,    // Little endian
			1,    // ELF version
			0,    // OS/ABI
			0, 0, 0, 0, 0, 0, 0, 0,
		},
		Type:      uint16(elf.ET_REL),
		Machine:   uint16(elf.EM_AARCH64),
		Version:   1,
		Entry:     0,
		Phoff:     0,
		Shoff:     64, // Will update later
		Flags:     0,
		Ehsize:    64,
		Phentsize: 0,
		Phnum:     0,
		Shentsize: 64,
		Shnum:     uint16(numSections),
		Shstrndx:  4, // .shstrtab is section 4
	}

	// Calculate offsets
	// Layout: ELF header | .text | .rela.text | .symtab | .strtab | .shstrtab | section headers
	textOff := uint64(64)

	// Relocation section (if any)
	var relaOff uint64
	var relaSize uint64
	if hasRelocations {
		relaOff = textOff + uint64(len(textData))
		// Align to 8 bytes
		if relaOff%8 != 0 {
			relaOff += 8 - (relaOff % 8)
		}
		relaSize = uint64(len(allRelocations) * 24) // Elf64_Rela is 24 bytes
	}

	// Symbol table
	var symtabOff uint64
	if hasRelocations {
		symtabOff = relaOff + relaSize
	} else {
		symtabOff = textOff + uint64(len(textData))
	}
	// Align symtab to 8 bytes
	if symtabOff%8 != 0 {
		symtabOff += 8 - (symtabOff % 8)
	}

	symtabSize := uint64(totalSymbols * 24) // Elf64_Sym is 24 bytes

	strtabOff := symtabOff + symtabSize
	shstrtabOff := strtabOff + uint64(len(strtab))
	shdrOff := shstrtabOff + uint64(len(shstrtab))
	// Align section headers to 8 bytes
	if shdrOff%8 != 0 {
		shdrOff += 8 - (shdrOff % 8)
	}

	ehdr.Shoff = shdrOff

	// Section headers
	shdrs := []elf.Section64{
		// 0: Null section
		{},
		// 1: .text
		{
			Name:      uint32(textIdx),
			Type:      uint32(elf.SHT_PROGBITS),
			Flags:     uint64(elf.SHF_ALLOC | elf.SHF_EXECINSTR),
			Addr:      0,
			Off:       textOff,
			Size:      uint64(len(textData)),
			Link:      0,
			Info:      0,
			Addralign: 128, // For exception vector alignment
			Entsize:   0,
		},
		// 2: .symtab
		{
			Name:      uint32(symtabIdx),
			Type:      uint32(elf.SHT_SYMTAB),
			Flags:     0,
			Addr:      0,
			Off:       symtabOff,
			Size:      symtabSize,
			Link:      3, // .strtab
			Info:      1, // Index of first GLOBAL symbol (symbol 0 is null/LOCAL, symbol 1+ are GLOBAL)
			Addralign: 8,
			Entsize:   24,
		},
		// 3: .strtab
		{
			Name:      uint32(strtabSectionIdx),
			Type:      uint32(elf.SHT_STRTAB),
			Flags:     0,
			Addr:      0,
			Off:       strtabOff,
			Size:      uint64(len(strtab)),
			Link:      0,
			Info:      0,
			Addralign: 1,
			Entsize:   0,
		},
		// 4: .shstrtab
		{
			Name:      uint32(shstrtabIdx),
			Type:      uint32(elf.SHT_STRTAB),
			Flags:     0,
			Addr:      0,
			Off:       shstrtabOff,
			Size:      uint64(len(shstrtab)),
			Link:      0,
			Info:      0,
			Addralign: 1,
			Entsize:   0,
		},
	}

	// Add .rela.text section if we have relocations
	if hasRelocations {
		shdrs = append(shdrs, elf.Section64{
			Name:      uint32(relaIdx),
			Type:      uint32(elf.SHT_RELA),
			Flags:     uint64(elf.SHF_INFO_LINK),
			Addr:      0,
			Off:       relaOff,
			Size:      relaSize,
			Link:      2, // .symtab
			Info:      1, // .text section
			Addralign: 8,
			Entsize:   24,
		})
	}

	// Write ELF header
	if err := binary.Write(f, binary.LittleEndian, &ehdr); err != nil {
		return err
	}

	// Write .text section
	if _, err := f.Write(textData); err != nil {
		return err
	}

	// Write .rela.text section if we have relocations
	if hasRelocations {
		// Pad to rela offset
		currentPos := textOff + uint64(len(textData))
		if currentPos < relaOff {
			padding := make([]byte, relaOff-currentPos)
			if _, err := f.Write(padding); err != nil {
				return err
			}
		}

		// Write relocations
		for _, r := range allRelocations {
			symIdx := symIndices[r.SymbolName]
			// Elf64_Rela: offset (8) + info (8) + addend (8)
			// info = (sym << 32) | type
			info := (uint64(symIdx) << 32) | uint64(r.Type)

			if err := binary.Write(f, binary.LittleEndian, r.Offset); err != nil {
				return err
			}
			if err := binary.Write(f, binary.LittleEndian, info); err != nil {
				return err
			}
			if err := binary.Write(f, binary.LittleEndian, r.Addend); err != nil {
				return err
			}
		}
	}

	// Pad to symtab offset
	currentPosInt, _ := f.Seek(0, io.SeekCurrent)
	if uint64(currentPosInt) < symtabOff {
		padding := make([]byte, symtabOff-uint64(currentPosInt))
		if _, err := f.Write(padding); err != nil {
			return err
		}
	}

	// Write symbol table
	// Null symbol
	nullSym := elf.Sym64{}
	if err := binary.Write(f, binary.LittleEndian, &nullSym); err != nil {
		return err
	}

	// Defined symbols (local)
	for _, sym := range symbols {
		s := elf.Sym64{
			Name:  symNameOffsets[sym.Name],
			Info:  uint8(elf.STB_GLOBAL)<<4 | uint8(elf.STT_FUNC),
			Other: 0,
			Shndx: 1, // .text section
			Value: symOffsets[sym.Name],
			Size:  uint64(len(sym.Bytes)),
		}
		if err := binary.Write(f, binary.LittleEndian, &s); err != nil {
			return err
		}
	}

	// External symbols (undefined)
	for _, name := range extSymList {
		s := elf.Sym64{
			Name:  symNameOffsets[name],
			Info:  uint8(elf.STB_GLOBAL)<<4 | uint8(elf.STT_NOTYPE),
			Other: 0,
			Shndx: 0, // SHN_UNDEF
			Value: 0,
			Size:  0,
		}
		if err := binary.Write(f, binary.LittleEndian, &s); err != nil {
			return err
		}
	}

	// Write .strtab
	if _, err := f.Write(strtab); err != nil {
		return err
	}

	// Write .shstrtab
	if _, err := f.Write(shstrtab); err != nil {
		return err
	}

	// Pad to section header offset
	currentPosInt, _ = f.Seek(0, io.SeekCurrent)
	if uint64(currentPosInt) < shdrOff {
		padding := make([]byte, shdrOff-uint64(currentPosInt))
		if _, err := f.Write(padding); err != nil {
			return err
		}
	}

	// Write section headers
	for _, shdr := range shdrs {
		if err := binary.Write(f, binary.LittleEndian, &shdr); err != nil {
			return err
		}
	}

	return nil
}
