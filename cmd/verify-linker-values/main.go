// verify-linker-values.go - Verify that linker values in cardinal.elf are correct
//
// This tool reads cardinal.elf and verifies that the Linker* variables contain
// the correct values by comparing them against:
// 1. Actual ELF section boundaries (for patched values)
// 2. Constants from constants/layout.go (for fixed values)
//
// Usage:
//   go run tools/verify-linker-values.go build/cardinal.elf [build/kmazarin.elf]
//
// Exit codes:
//   0 - All values correct
//   1 - Verification failed (values incorrect or missing)

package main

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"

	"cardinal/constants"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <cardinal.elf> [kmazarin.elf]\n", os.Args[0])
		os.Exit(1)
	}

	cardinalPath := os.Args[1]
	var kmazarinPath string
	if len(os.Args) > 2 {
		kmazarinPath = os.Args[2]
	}

	// Open cardinal ELF
	elfFile, err := elf.Open(cardinalPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening cardinal ELF: %v\n", err)
		os.Exit(1)
	}
	defer elfFile.Close()

	// Find symbol table
	symbols, err := elfFile.Symbols()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading symbols: %v\n", err)
		os.Exit(1)
	}

	// Build map of symbol names to addresses
	symMap := make(map[string]uint64)
	for _, sym := range symbols {
		symMap[sym.Name] = sym.Value
	}

	// Read the ELF file data to extract variable values
	data, err := os.ReadFile(cardinalPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading cardinal file: %v\n", err)
		os.Exit(1)
	}

	// Helper to read uint64 value at a symbol's address
	readSymbolValue := func(symName string) (uint64, error) {
		addr, ok := symMap["main."+symName]
		if !ok {
			return 0, fmt.Errorf("symbol not found: %s", symName)
		}

		// Find which section contains this address
		for _, section := range elfFile.Sections {
			if section.Addr <= addr && addr < section.Addr+section.Size {
				offset := section.Offset + (addr - section.Addr)
				if offset+8 > uint64(len(data)) {
					return 0, fmt.Errorf("offset out of bounds")
				}
				return binary.LittleEndian.Uint64(data[offset : offset+8]), nil
			}
		}
		return 0, fmt.Errorf("address not in any section")
	}

	fmt.Printf("Verifying linker values in %s\n\n", cardinalPath)

	passed := 0
	failed := 0

	// Helper to verify a value
	verify := func(name string, expected uint64, description string) {
		actual, err := readSymbolValue(name)
		if err != nil {
			fmt.Printf("❌ %s: %v\n", name, err)
			failed++
			return
		}
		if actual == expected {
			fmt.Printf("✅ %s = 0x%X (%s)\n", name, actual, description)
			passed++
		} else {
			fmt.Printf("❌ %s = 0x%X (expected 0x%X) - %s\n", name, actual, expected, description)
			failed++
		}
	}

	// Verify FIXED values (from constants package)
	fmt.Println("=== Fixed Values (from constants) ===")
	verify("LinkerRamStart", constants.BootAddress, "RAM start")
	verify("LinkerDtbBootAddr", constants.DtbStart, "DTB start")
	verify("LinkerDtbSize", constants.DtbSize, "DTB size")
	verify("LinkerCardinalEnd", constants.CardinalEnd, "Cardinal end")
	verify("LinkerCardinalAllocationSize", constants.CardinalAllocationSize, "Cardinal allocation")
	verify("LinkerKmazarinLoadAddr", constants.KmazarinLoadAddr, "Kmazarin load address")
	verify("LinkerPageTablesStart", constants.PageTableStart, "Page tables start")
	verify("LinkerPageTablesEnd", constants.PageTableEnd, "Page tables end")
	verify("LinkerStackTop", constants.G0StackTop, "g0 stack top")
	verify("LinkerG0StackBottom", constants.G0StackBottom, "g0 stack bottom")
	verify("LinkerExceptionStackTop", constants.ExceptionStackTop, "Exception stack top")
	verify("LinkerUartBase", constants.UartBase, "UART base")
	verify("LinkerGicBase", constants.GicBase, "GIC base")

	// Verify PATCHED values (section boundaries)
	fmt.Println("\n=== Patched Values (ELF sections) ===")

	if textSec := elfFile.Section(".text"); textSec != nil {
		verify("LinkerStart", textSec.Addr, ".text start")
		verify("LinkerTextStart", textSec.Addr, ".text start")
		verify("LinkerTextEnd", textSec.Addr+textSec.Size, ".text end")
	} else {
		fmt.Println("❌ .text section not found")
		failed++
	}

	if rodataSec := elfFile.Section(".rodata"); rodataSec != nil {
		verify("LinkerRodataStart", rodataSec.Addr, ".rodata start")
		// .rodata end includes .gopclntab
		var rodataEnd uint64 = rodataSec.Addr + rodataSec.Size
		if pclntab := elfFile.Section(".gopclntab"); pclntab != nil {
			rodataEnd = pclntab.Addr + pclntab.Size
		}
		verify("LinkerRodataEnd", rodataEnd, ".rodata end (including .gopclntab)")
	}

	if noptrdata := elfFile.Section(".noptrdata"); noptrdata != nil {
		verify("LinkerDataStart", noptrdata.Addr, ".noptrdata start")
	} else if dataSec := elfFile.Section(".data"); dataSec != nil {
		verify("LinkerDataStart", dataSec.Addr, ".data start")
	}

	if dataSec := elfFile.Section(".data"); dataSec != nil {
		verify("LinkerDataEnd", dataSec.Addr+dataSec.Size, ".data end")
	}

	if bssSec := elfFile.Section(".bss"); bssSec != nil {
		verify("LinkerBssStart", bssSec.Addr, ".bss start")
		var bssEnd uint64 = bssSec.Addr + bssSec.Size
		if noptrbss := elfFile.Section(".noptrbss"); noptrbss != nil {
			if noptrbss.Addr+noptrbss.Size > bssEnd {
				bssEnd = noptrbss.Addr + noptrbss.Size
			}
		}
		verify("LinkerBssEnd", bssEnd, ".bss end")
		verify("LinkerEnd", bssEnd, "kernel end")
	}

	// Verify kmazarin values if kmazarin.elf provided
	if kmazarinPath != "" {
		fmt.Println("\n=== Kmazarin Values ===")
		verify("LinkerKmazarinStart", constants.KmazarinLoadAddr, "Kmazarin start")

		// Calculate expected kmazarin size
		kmazarinElf, err := elf.Open(kmazarinPath)
		if err == nil {
			defer kmazarinElf.Close()

			var textStart, maxEnd uint64
			for _, section := range kmazarinElf.Sections {
				if section.Name == ".text" {
					textStart = section.Addr
				}
				if section.Addr != 0 && section.Size != 0 {
					sectionEnd := section.Addr + section.Size
					if sectionEnd > maxEnd {
						maxEnd = sectionEnd
					}
				}
			}

			if textStart > 0 && maxEnd > textStart {
				expectedSize := maxEnd - textStart
				verify("LinkerKmazarinSize", expectedSize, "Kmazarin size")
			}
		}
	}

	// Print summary
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Passed: %d\n", passed)
	fmt.Printf("Failed: %d\n", failed)

	if failed > 0 {
		fmt.Printf("\n❌ Verification FAILED - linker values are incorrect!\n")
		os.Exit(1)
	} else {
		fmt.Printf("\n✅ All linker values verified correct!\n")
		os.Exit(0)
	}
}
