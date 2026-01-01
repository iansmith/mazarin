// weaken-symbols: Modify ELF object file to weaken specified symbols
//
// Usage: weaken-symbols -o output.o input.o symbol1 symbol2 ...
//
// This tool reads an ELF object file, changes the binding of specified
// symbols from STB_GLOBAL to STB_WEAK, and writes the result.
//
// This replaces the need for: objcopy --weaken-symbol=X input.o output.o
package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
)

func main() {
	outputFile := flag.String("o", "", "Output file (required)")
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 || *outputFile == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -o output.o input.o symbol1 [symbol2 ...]\n", os.Args[0])
		os.Exit(1)
	}

	inputFile := args[0]
	symbolsToWeaken := make(map[string]bool)
	for _, sym := range args[1:] {
		symbolsToWeaken[sym] = true
	}

	if err := weakenSymbols(inputFile, *outputFile, symbolsToWeaken); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func weakenSymbols(inputPath, outputPath string, symbols map[string]bool) error {
	// Read entire file into memory
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading input: %w", err)
	}

	// Parse ELF to find symbol table
	r := bytes.NewReader(data)
	f, err := elf.NewFile(r)
	if err != nil {
		return fmt.Errorf("parsing ELF: %w", err)
	}
	defer f.Close()

	// Find .symtab section
	symtab := f.Section(".symtab")
	if symtab == nil {
		return fmt.Errorf("no .symtab section found")
	}

	// Find .strtab section (symbol string table)
	strtab := f.Section(".strtab")
	if strtab == nil {
		return fmt.Errorf("no .strtab section found")
	}

	// Read string table
	strData, err := strtab.Data()
	if err != nil {
		return fmt.Errorf("reading strtab: %w", err)
	}

	// Determine symbol entry size based on ELF class
	var symSize int
	if f.Class == elf.ELFCLASS64 {
		symSize = 24 // Elf64_Sym size
	} else {
		symSize = 16 // Elf32_Sym size
	}

	// Calculate number of symbols
	numSyms := int(symtab.Size) / symSize

	// Process each symbol
	weakened := 0
	for i := 0; i < numSyms; i++ {
		symOffset := int(symtab.Offset) + i*symSize

		var nameIdx uint32
		var infoOffset int

		if f.Class == elf.ELFCLASS64 {
			// Elf64_Sym: st_name(4), st_info(1), st_other(1), st_shndx(2), st_value(8), st_size(8)
			nameIdx = binary.LittleEndian.Uint32(data[symOffset : symOffset+4])
			infoOffset = symOffset + 4
		} else {
			// Elf32_Sym: st_name(4), st_value(4), st_size(4), st_info(1), st_other(1), st_shndx(2)
			nameIdx = binary.LittleEndian.Uint32(data[symOffset : symOffset+4])
			infoOffset = symOffset + 12
		}

		// Get symbol name from string table
		if int(nameIdx) >= len(strData) {
			continue
		}
		nameEnd := bytes.IndexByte(strData[nameIdx:], 0)
		if nameEnd < 0 {
			continue
		}
		name := string(strData[nameIdx : nameIdx+uint32(nameEnd)])

		// Check if this symbol should be weakened
		if !symbols[name] {
			continue
		}

		// Read current info byte
		info := data[infoOffset]
		bind := info >> 4
		typ := info & 0xf

		// Only weaken if currently GLOBAL (1)
		if bind != uint8(elf.STB_GLOBAL) {
			fmt.Printf("Symbol %q: binding is %d (not GLOBAL), skipping\n", name, bind)
			continue
		}

		// Change binding to WEAK (2)
		newInfo := (uint8(elf.STB_WEAK) << 4) | typ
		data[infoOffset] = newInfo
		weakened++
		fmt.Printf("Weakened symbol: %s\n", name)
	}

	if weakened == 0 {
		fmt.Println("Warning: No symbols were weakened")
	} else {
		fmt.Printf("Weakened %d symbol(s)\n", weakened)
	}

	// Write modified data
	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// getString extracts a null-terminated string from a byte slice
func getString(data []byte, offset int) string {
	if offset < 0 || offset >= len(data) {
		return ""
	}
	end := bytes.IndexByte(data[offset:], 0)
	if end < 0 {
		return string(data[offset:])
	}
	return string(data[offset : offset+end])
}
