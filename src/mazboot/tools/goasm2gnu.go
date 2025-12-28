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
	"strconv"
	"strings"
)

type Symbol struct {
	Name    string
	Offset  uint64 // Offset within the text section
	Size    int
	Bytes   []byte
	Section string // .text, .rodata, etc.
}

var (
	// Matches: TEXT <unlinkable>.sync_el1_handler(SB) /path/file.s
	textHeaderRe = regexp.MustCompile(`^TEXT\s+(?:<[^>]+>\.)?(\S+)\(SB\)`)

	// Matches: file.s:5  0x3a0  d2824680  MOVD $4660, R0
	objdumpLineRe = regexp.MustCompile(`^\s+\S+:\d+\s+(0x[0-9a-fA-F]+)\s+([0-9a-fA-F]+)\s+`)
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

	// Step 2: Extract symbols and bytes using go tool objdump
	symbols, err := extractSymbols(objFile, *verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error extracting symbols: %v\n", err)
		os.Exit(1)
	}

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
			// Convert · to _ for GNU asm compatibility
			name = strings.ReplaceAll(name, "·", "_")

			currentSym = &Symbol{
				Name:    name,
				Bytes:   make([]byte, 0),
				Section: ".text",
			}
			continue
		}

		// Check for instruction line with hex bytes
		if currentSym != nil {
			if matches := objdumpLineRe.FindStringSubmatch(line); matches != nil {
				offsetStr := matches[1]
				hexBytes := matches[2]

				// Parse offset
				offset, _ := strconv.ParseUint(offsetStr[2:], 16, 64)
				if currentSym.Offset == 0 && offset > 0 {
					currentSym.Offset = offset
				}

				// Parse hex bytes (they come as a single hex number, e.g., "d2824680")
				// Convert to little-endian bytes
				val, err := strconv.ParseUint(hexBytes, 16, 32)
				if err == nil {
					// ARM64 is little-endian, so we need to output bytes in LE order
					b := make([]byte, 4)
					binary.LittleEndian.PutUint32(b, uint32(val))
					currentSym.Bytes = append(currentSym.Bytes, b...)
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

	// Calculate sizes
	var textData []byte
	symOffsets := make(map[string]uint64)
	for _, sym := range symbols {
		symOffsets[sym.Name] = uint64(len(textData))
		textData = append(textData, sym.Bytes...)
	}

	// Build string table
	strtab := []byte{0} // Start with null byte
	shstrtab := []byte{0}

	// Section names
	shstrtabIdx := len(shstrtab)
	shstrtab = append(shstrtab, ".shstrtab\x00"...)
	textIdx := len(shstrtab)
	shstrtab = append(shstrtab, section[1:]...) // Remove leading '.'
	shstrtab = append(shstrtab, 0)
	symtabIdx := len(shstrtab)
	shstrtab = append(shstrtab, ".symtab\x00"...)
	strtabSectionIdx := len(shstrtab)
	shstrtab = append(shstrtab, ".strtab\x00"...)

	// Symbol names
	symNameOffsets := make(map[string]uint32)
	for _, sym := range symbols {
		symNameOffsets[sym.Name] = uint32(len(strtab))
		strtab = append(strtab, sym.Name...)
		strtab = append(strtab, 0)
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
		Shoff:     64, // Section headers start after ELF header (will update)
		Flags:     0,
		Ehsize:    64,
		Phentsize: 0,
		Phnum:     0,
		Shentsize: 64,
		Shnum:     5, // null + .text + .symtab + .strtab + .shstrtab
		Shstrndx:  4, // .shstrtab is section 4
	}

	// Calculate offsets
	// Layout: ELF header | .text | .symtab | .strtab | .shstrtab | section headers
	textOff := uint64(64)
	symtabOff := textOff + uint64(len(textData))
	// Align symtab to 8 bytes
	if symtabOff%8 != 0 {
		symtabOff += 8 - (symtabOff % 8)
	}

	// Symbol table: null + one per symbol
	symtabEntries := 1 + len(symbols)
	symtabSize := uint64(symtabEntries * 24) // Elf64_Sym is 24 bytes

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
			Info:      1, // First non-local symbol
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

	// Write ELF header
	if err := binary.Write(f, binary.LittleEndian, &ehdr); err != nil {
		return err
	}

	// Write .text section
	if _, err := f.Write(textData); err != nil {
		return err
	}

	// Pad to symtab offset
	currentPos := textOff + uint64(len(textData))
	if currentPos < symtabOff {
		padding := make([]byte, symtabOff-currentPos)
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

	// Symbols
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

	// Write .strtab
	if _, err := f.Write(strtab); err != nil {
		return err
	}

	// Write .shstrtab
	if _, err := f.Write(shstrtab); err != nil {
		return err
	}

	// Pad to section header offset
	currentPosInt, _ := f.Seek(0, io.SeekCurrent)
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
