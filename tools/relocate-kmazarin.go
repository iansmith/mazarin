// relocate-kmazarin.go - Relocate kmazarin to high memory
package main

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"time"
)

const (
	// Offset to add to all addresses
	highMemOffset = 0xFFFFFFFF00000000

	// Original kmazarin address range
	// Must cover all sections including .bss and .noptrbss
	kmazarinBase = 0x41800000
	kmazarinEnd  = 0x41C00000 // Extended to cover BSS (ends ~0x419B6000)
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <input.elf> <output.elf>\n", os.Args[0])
		os.Exit(1)
	}

	start := time.Now()

	// Read input ELF
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read input: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Read %d bytes from %s\n", len(data), os.Args[1])

	// Open ELF for parsing
	f, err := elf.Open(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse ELF: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	stats := &RelocStats{}

	// 1. Relocate ELF header entry point
	relocateEntryPoint(data, stats)

	// 2. Relocate program headers (segment VMAs)
	relocateProgramHeaders(data, f, stats)

	// 3. Scan .text for ADRP instructions and relocate
	relocateTextSection(data, f, stats)

	// 4. Scan data sections for pointer values
	relocateDataSections(data, f, stats)

	elapsed := time.Since(start)

	// Write output
	if err := os.WriteFile(os.Args[2], data, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write output: %v\n", err)
		os.Exit(1)
	}

	// Print statistics
	fmt.Printf("\nRelocation Statistics:\n")
	fmt.Printf("  Entry point:     %d relocated\n", stats.EntryPoint)
	fmt.Printf("  Program headers: %d segments relocated\n", stats.ProgHeaders)
	fmt.Printf("  ADRP instructions: %d relocated\n", stats.AdrpInstructions)
	fmt.Printf("  Data pointers:   %d relocated\n", stats.DataPointers)
	fmt.Printf("  Total relocations: %d\n", stats.Total())
	fmt.Printf("\nTime elapsed: %v\n", elapsed)
	fmt.Printf("Throughput: %.2f MB/s\n", float64(len(data))/elapsed.Seconds()/1024/1024)
}

type RelocStats struct {
	EntryPoint       int
	ProgHeaders      int
	AdrpInstructions int
	DataPointers     int
}

func (s *RelocStats) Total() int {
	return s.EntryPoint + s.ProgHeaders + s.AdrpInstructions + s.DataPointers
}

func relocateEntryPoint(data []byte, stats *RelocStats) {
	// ELF64 entry point is at offset 0x18 (8 bytes)
	entry := binary.LittleEndian.Uint64(data[0x18:0x20])
	if entry >= kmazarinBase && entry < kmazarinEnd {
		newEntry := entry + highMemOffset
		binary.LittleEndian.PutUint64(data[0x18:0x20], newEntry)
		fmt.Printf("Entry point: 0x%X -> 0x%X\n", entry, newEntry)
		stats.EntryPoint = 1
	}
}

func relocateProgramHeaders(data []byte, f *elf.File, stats *RelocStats) {
	// Program header offset is at 0x20
	phoff := binary.LittleEndian.Uint64(data[0x20:0x28])
	phentsize := binary.LittleEndian.Uint16(data[0x36:0x38])
	phnum := binary.LittleEndian.Uint16(data[0x38:0x3A])

	for i := uint16(0); i < phnum; i++ {
		offset := phoff + uint64(i)*uint64(phentsize)

		// p_vaddr at offset +16 (8 bytes)
		vaddr := binary.LittleEndian.Uint64(data[offset+16 : offset+24])
		if vaddr >= kmazarinBase && vaddr < kmazarinEnd {
			newVaddr := vaddr + highMemOffset
			binary.LittleEndian.PutUint64(data[offset+16:offset+24], newVaddr)

			// p_paddr at offset +24 (8 bytes) - also relocate
			paddr := binary.LittleEndian.Uint64(data[offset+24 : offset+32])
			if paddr >= kmazarinBase && paddr < kmazarinEnd {
				binary.LittleEndian.PutUint64(data[offset+24:offset+32], paddr+highMemOffset)
			}

			fmt.Printf("Segment %d: VMA 0x%X -> 0x%X\n", i, vaddr, newVaddr)
			stats.ProgHeaders++
		}
	}
}

func relocateTextSection(data []byte, f *elf.File, stats *RelocStats) {
	text := f.Section(".text")
	if text == nil {
		return
	}

	textData := data[text.Offset : text.Offset+text.Size]

	// Scan for ADRP instructions
	// ADRP format: 1xx1 0000 xxxx xxxx xxxx xxxx xxx xxxxx
	// Opcode pattern: 0x90 in bits 31-24 means ADRP
	for i := uint64(0); i < text.Size-3; i += 4 {
		insn := binary.LittleEndian.Uint32(textData[i : i+4])

		// Check if this is ADRP (opcode = 0x90xxxxxx)
		if (insn & 0x9F000000) == 0x90000000 {
			// Extract immlo (bits 30-29) and immhi (bits 23-5)
			immlo := (insn >> 29) & 0x3
			immhi := (insn >> 5) & 0x7FFFF

			// Combine into 21-bit signed immediate (page offset)
			imm := (immhi << 2) | immlo

			// Sign extend from 21 bits
			if (imm & 0x100000) != 0 {
				imm |= 0xFFE00000
			}

			// Calculate current target address (BEFORE relocation)
			// PC for ADRP is the address of the instruction itself
			pc := text.Addr + i
			targetPage := (pc & ^uint64(0xFFF)) + uint64(int64(int32(imm<<12)))

			// If target is in kmazarin range, we need to adjust
			if targetPage >= kmazarinBase && targetPage < kmazarinEnd {
				// ADRP is PC-relative, so we need to recalculate the offset
				// Original: target = (oldPC & ~0xFFF) + (imm << 12)
				// After relocation:
				//   - oldPC becomes newPC = oldPC + highMemOffset
				//   - target becomes newTarget = target + highMemOffset
				// We need: newTarget = (newPC & ~0xFFF) + (newImm << 12)
				// Therefore: newImm = ((newTarget - (newPC & ~0xFFF)) >> 12)

				newPC := pc + highMemOffset
				newTarget := targetPage + highMemOffset
				newPCPage := newPC & ^uint64(0xFFF)

				// Calculate new offset in pages
				offsetPages := int64(newTarget) - int64(newPCPage)
				newImm := offsetPages >> 12

				// Ensure new immediate fits in 21 bits (signed)
				if newImm < -0x100000 || newImm > 0xFFFFF {
					// Offset too large - this shouldn't happen for kmazarin
					continue
				}

				// Extract new immlo (bits 1-0) and immhi (bits 20-2)
				newImmlo := uint32(newImm) & 0x3
				newImmhi := (uint32(newImm) >> 2) & 0x7FFFF

				// Reconstruct instruction with new immediate
				newInsn := (insn & 0x9F00001F) | (newImmlo << 29) | (newImmhi << 5)
				binary.LittleEndian.PutUint32(textData[i:i+4], newInsn)

				stats.AdrpInstructions++
			}
		}
	}
}

func relocateDataSections(data []byte, f *elf.File, stats *RelocStats) {
	// CRITICAL: Include .text to relocate literal pool entries
	// When Go assembly does MOVD $symbol(SB), the assembler generates a PC-relative
	// load from a literal pool embedded in .text. These literal values must be relocated.
	sections := []string{".text", ".data", ".rodata", ".noptrdata", ".go.buildinfo", ".gopclntab", ".typelink", ".itablink"}

	for _, name := range sections {
		sect := f.Section(name)
		if sect == nil {
			continue
		}

		sectData := data[sect.Offset : sect.Offset+sect.Size]

		// Scan for 8-byte aligned pointers
		for i := uint64(0); i < sect.Size-7; i += 8 {
			ptr := binary.LittleEndian.Uint64(sectData[i : i+8])

			// Check if this looks like a kmazarin pointer
			if ptr >= kmazarinBase && ptr < kmazarinEnd {
				newPtr := ptr + highMemOffset
				binary.LittleEndian.PutUint64(sectData[i:i+8], newPtr)
				stats.DataPointers++
			}
		}
	}
}
