// fix-go-elf.go - Fix Go ELF binaries with negative file offsets
//
// Go's linker with -T flag produces ELF files with negative (wrapped around)
// file offsets in LOAD segments. QEMU 9.0+ mishandles these, causing crashes
// in address_space_write_rom during rom_reset.
//
// This tool fixes the ELF by adjusting the problematic segment:
//   - Changes vaddr from 0x400f0000 to 0x40100000
//   - Changes offset from -0xf000 to 0x1000
//   - Adjusts filesz and memsz accordingly
//
// Usage:
//
//	go run fix-go-elf.go <elf-file>           # Fix in-place
//	go run fix-go-elf.go <elf-file> <output>  # Write to new file
package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

const (
	elfMagic = "\x7fELF"
	elfClass64 = 2
	elfDataLSB = 1
	elfDataMSB = 2
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <elf-file> [output-file]\n", os.Args[0])
		os.Exit(1)
	}

	inputPath := os.Args[1]
	outputPath := inputPath
	if len(os.Args) >= 3 {
		outputPath = os.Args[2]
	}

	if err := fixELF(inputPath, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func fixELF(inputPath, outputPath string) error {
	// Read the file
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", inputPath, err)
	}

	// Verify ELF magic
	if len(data) < 64 || string(data[:4]) != elfMagic {
		return fmt.Errorf("%s is not an ELF file", inputPath)
	}

	// Check 64-bit
	if data[4] != elfClass64 {
		return fmt.Errorf("%s is not a 64-bit ELF", inputPath)
	}

	// Determine endianness
	var order binary.ByteOrder
	switch data[5] {
	case elfDataLSB:
		order = binary.LittleEndian
	case elfDataMSB:
		order = binary.BigEndian
	default:
		return fmt.Errorf("%s has unknown endianness", inputPath)
	}

	// Check machine type (e_machine at offset 18, 2 bytes)
	e_machine := order.Uint16(data[18:20])
	isRISCV := (e_machine == 0xF3) // EM_RISCV = 243

	// Read ELF header fields
	// e_entry at offset 24 (8 bytes)
	// e_phoff at offset 32 (8 bytes)
	// e_phentsize at offset 54 (2 bytes)
	// e_phnum at offset 56 (2 bytes)
	e_entry := order.Uint64(data[24:32])
	e_phoff := order.Uint64(data[32:40])
	e_phentsize := order.Uint16(data[54:56])
	e_phnum := order.Uint16(data[56:58])

	fixed := false
	var relocOffset uint64
	relocOffsetSet := false
	var firstSegmentOffsetDelta int64 // Track p_offset change for entry point adjustment

	// For RISC-V: First pass to determine relocation offset from first PT_LOAD
	if isRISCV {
		for i := uint16(0); i < e_phnum; i++ {
			phOffset := e_phoff + uint64(i)*uint64(e_phentsize)
			if phOffset+56 > uint64(len(data)) {
				continue
			}

			p_type := order.Uint32(data[phOffset : phOffset+4])
			p_vaddr := order.Uint64(data[phOffset+16 : phOffset+24])

			// PT_LOAD = 1
			if p_type == 1 && !relocOffsetSet {
				// Calculate offset to relocate first PT_LOAD to 0x80000000
				// We use -bios none, so diplomat runs as firmware (not kernel under OpenSBI)
				if p_vaddr < 0x80000000 {
					relocOffset = uint64(0x80000000) - p_vaddr
					relocOffsetSet = true
					fmt.Printf("RISC-V relocation offset: 0x%x\n", relocOffset)
					break
				}
			}
		}
	}

	// Scan program headers
	for i := uint16(0); i < e_phnum; i++ {
		phOffset := e_phoff + uint64(i)*uint64(e_phentsize)

		if phOffset+56 > uint64(len(data)) {
			return fmt.Errorf("program header %d extends beyond file", i)
		}

		// Read program header fields
		// Phdr64: p_type(4), p_flags(4), p_offset(8), p_vaddr(8), p_paddr(8), p_filesz(8), p_memsz(8), p_align(8)
		p_type := order.Uint32(data[phOffset : phOffset+4])
		p_offset := order.Uint64(data[phOffset+8 : phOffset+16])
		p_vaddr := order.Uint64(data[phOffset+16 : phOffset+24])
		p_paddr := order.Uint64(data[phOffset+24 : phOffset+32])
		p_filesz := order.Uint64(data[phOffset+32 : phOffset+40])
		p_memsz := order.Uint64(data[phOffset+40 : phOffset+48])

		// PT_LOAD = 1
		isPTLOAD := (p_type == 1)

		// For RISC-V ELFs: relocate ALL PT_LOAD segments by the same offset
		if isRISCV && isPTLOAD && relocOffsetSet && p_vaddr < 0x80000000 {
			newVaddr := p_vaddr + relocOffset
			newPaddr := p_paddr + relocOffset

			// Also fix p_offset for first segment to skip ELF headers
			// This ensures code at file offset 0x1000 maps to vaddr 0x80200000
			newOffset := p_offset
			if p_offset == 0 && p_vaddr < 0x20000 {
				// First segment starts at file offset 0 (ELF header)
				// Relocate it to start at 0x1000 (where actual code begins)
				newOffset = 0x1000
				// Track the offset change for entry point adjustment
				firstSegmentOffsetDelta = int64(newOffset) - int64(p_offset)
			}

			fmt.Printf("Relocating RISC-V segment %d for OpenSBI:\n", i)
			fmt.Printf("  offset: 0x%08x -> 0x%08x\n", p_offset, newOffset)
			fmt.Printf("  vaddr:  0x%08x -> 0x%08x\n", p_vaddr, newVaddr)
			fmt.Printf("  paddr:  0x%08x -> 0x%08x\n", p_paddr, newPaddr)

			// Write the fixed segment values
			order.PutUint64(data[phOffset+8:phOffset+16], newOffset)
			order.PutUint64(data[phOffset+16:phOffset+24], newVaddr)
			order.PutUint64(data[phOffset+24:phOffset+32], newPaddr)

			fixed = true
		}

		// Check for negative offset (wrapped around as large positive)
		if p_offset > 0x8000000000000000 {
			// This is a negative offset
			signedOffset := int64(p_offset)

			// Calculate the fix
			// The negative offset tells us how much zero-fill there is
			zeroFillSize := uint64(-signedOffset)

			// Align to 64KB (0x10000)
			var adjustment uint64
			if zeroFillSize < 0x10000 {
				adjustment = 0x10000
			} else {
				adjustment = (zeroFillSize + 0xFFFF) & ^uint64(0xFFFF)
			}

			newVaddr := p_vaddr + adjustment
			newPaddr := p_paddr + adjustment
			newOffset := uint64(0x1000) // .text section starts at file offset 0x1000

			var newFilesz, newMemsz uint64
			if p_filesz >= adjustment {
				newFilesz = p_filesz - adjustment
			}
			if p_memsz >= adjustment {
				newMemsz = p_memsz - adjustment
			}

			fmt.Printf("Fixing segment %d:\n", i)
			fmt.Printf("  vaddr:  0x%08x -> 0x%08x\n", p_vaddr, newVaddr)
			fmt.Printf("  offset: 0x%x -> 0x%x\n", p_offset, newOffset)
			fmt.Printf("  filesz: 0x%x -> 0x%x\n", p_filesz, newFilesz)
			fmt.Printf("  memsz:  0x%x -> 0x%x\n", p_memsz, newMemsz)

			// Write the fixed values
			order.PutUint64(data[phOffset+8:phOffset+16], newOffset)
			order.PutUint64(data[phOffset+16:phOffset+24], newVaddr)
			order.PutUint64(data[phOffset+24:phOffset+32], newPaddr)
			order.PutUint64(data[phOffset+32:phOffset+40], newFilesz)
			order.PutUint64(data[phOffset+40:phOffset+48], newMemsz)

			fixed = true
		}
	}

	// Update entry point for RISC-V if we relocated segments
	if isRISCV && relocOffsetSet && fixed {
		// Account for p_offset change in the first segment
		// When p_offset increases (e.g., 0 -> 0x1000), virtual addresses shift down by that amount
		// Example: e_entry 0x64b30 is at file offset 0x54b30 (0x64b30 - 0x10000)
		// After relocation, file offset 0x54b30 with segment at (vaddr=0x80000000, p_offset=0x1000)
		// maps to vaddr 0x80000000 + (0x54b30 - 0x1000) = 0x80053b30
		newEntry := e_entry + relocOffset - uint64(firstSegmentOffsetDelta)
		if firstSegmentOffsetDelta != 0 {
			fmt.Printf("Updating entry point:  0x%08x -> 0x%08x (reloc +0x%x, p_offset -%d)\n",
				e_entry, newEntry, relocOffset, firstSegmentOffsetDelta)
		} else {
			fmt.Printf("Updating entry point:  0x%08x -> 0x%08x\n", e_entry, newEntry)
		}
		order.PutUint64(data[24:32], newEntry)
		e_entry = newEntry // Update for bootstrap injection
	}

	if !fixed {
		fmt.Println("No segments need fixing")
		return nil
	}

	// Inject bootstrap stub for RISC-V (jumps from load address to actual entry)
	// Bootstrap is placed at the start of first LOAD segment (0x80200000)
	// and jumps to the original entry point (trampoline)
	if isRISCV && e_entry != 0 {
		if err := injectBootstrapStub(data, order, e_entry); err != nil {
			return fmt.Errorf("injecting bootstrap stub: %w", err)
		}

		// Update e_entry to point to bootstrap, not trampoline
		// QEMU with -bios none jumps to e_entry (or lowest segment address)
		bootstrapAddr := uint64(0x80000000)
		fmt.Printf("Setting entry point to bootstrap: 0x%08x -> 0x%08x\n", e_entry, bootstrapAddr)
		order.PutUint64(data[24:32], bootstrapAddr)
	}

	// Write the fixed file
	if err := os.WriteFile(outputPath, data, 0755); err != nil {
		return fmt.Errorf("writing %s: %w", outputPath, err)
	}

	fmt.Printf("Fixed ELF written to %s\n", outputPath)
	return nil
}
