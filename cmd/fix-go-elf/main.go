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

var noBootstrap bool

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-no-bootstrap] <elf-file> [output-file]\n", os.Args[0])
		os.Exit(1)
	}

	args := os.Args[1:]
	for len(args) > 0 && args[0][0] == '-' {
		switch args[0] {
		case "-no-bootstrap":
			noBootstrap = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[0])
			os.Exit(1)
		}
		args = args[1:]
	}

	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-no-bootstrap] <elf-file> [output-file]\n", os.Args[0])
		os.Exit(1)
	}

	inputPath := args[0]
	outputPath := inputPath
	if len(args) >= 2 {
		outputPath = args[1]
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
	hasNegativeOffset := false

	// For RISC-V: First pass to check if binary has negative offset (from -T flag)
	// If it does, the binary is already at the correct address and we should skip
	// relocation (only handle negative offset fix below)
	if isRISCV {
		for i := uint16(0); i < e_phnum; i++ {
			phOffset := e_phoff + uint64(i)*uint64(e_phentsize)
			if phOffset+56 > uint64(len(data)) {
				continue
			}

			p_type := order.Uint32(data[phOffset : phOffset+4])
			p_offset := order.Uint64(data[phOffset+8 : phOffset+16])
			p_vaddr := order.Uint64(data[phOffset+16 : phOffset+24])

			// PT_LOAD = 1
			if p_type == 1 {
				// Check for negative offset (wrapped around as large positive)
				// This indicates the binary was linked with -T flag at the correct address
				if p_offset > 0x8000000000000000 {
					hasNegativeOffset = true
					fmt.Printf("RISC-V binary has negative offset (linked with -T flag at 0x%x), skipping relocation\n", p_vaddr)
					break
				}

				// First PT_LOAD without negative offset: determine if we need relocation
				if !relocOffsetSet && !hasNegativeOffset {
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
		// UNLESS the binary has negative offset (linked with -T flag at correct address)
		if isRISCV && isPTLOAD && relocOffsetSet && !hasNegativeOffset && p_vaddr < 0x80000000 {
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

			// Reduce filesz and memsz to remove the zero-fill padding region
			var newFilesz, newMemsz uint64
			if p_filesz >= adjustment {
				newFilesz = p_filesz - adjustment
			}
			if p_memsz >= adjustment {
				newMemsz = p_memsz - adjustment
			}

			fmt.Printf("Fixing segment %d:\n", i)
			fmt.Printf("  vaddr:  0x%08x -> 0x%08x\n", p_vaddr, newVaddr)
			fmt.Printf("  paddr:  0x%08x -> 0x%08x\n", p_paddr, newPaddr)
			fmt.Printf("  offset: 0x%x -> 0x%x\n", p_offset, newOffset)
			fmt.Printf("  filesz: 0x%x -> 0x%x\n", p_filesz, newFilesz)
			fmt.Printf("  memsz:  0x%x -> 0x%x\n", p_memsz, newMemsz)

			// Write the fixed values
			order.PutUint64(data[phOffset+8:phOffset+16], newOffset)
			order.PutUint64(data[phOffset+16:phOffset+24], newVaddr)
			order.PutUint64(data[phOffset+24:phOffset+32], newPaddr)
			order.PutUint64(data[phOffset+32:phOffset+40], newFilesz)
			order.PutUint64(data[phOffset+40:phOffset+48], newMemsz)

			// Do NOT adjust entry point! The Go linker already placed it at the correct vaddr.
			// The entry point vaddr (e.g., 0x80053DE0) is in the code region, not the zero-fill region.

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

	// For RISC-V with -T flag: inject bootstrap stub at segment start
	// OpenSBI/QEMU jumps to the beginning of the loaded segment, not the ELF entry point
	// We need a stub at file offset 0x1000 (segment start) that jumps to the actual entry point
	//
	// NOTE: The bootstrap stub OVERWRITES the first function in .text (typically
	// internal/abi.NoEscape). This is fine for diplomat (never uses Go runtime),
	// but FATAL for kmazarin (IS the Go runtime). Use -no-bootstrap for kmazarin.
	if isRISCV && hasNegativeOffset && e_entry != 0 && !noBootstrap {
		// Find the first PT_LOAD segment to get its vaddr (where bootstrap should be)
		var bootstrapAddr uint64
		for i := uint16(0); i < e_phnum; i++ {
			phOffset := e_phoff + uint64(i)*uint64(e_phentsize)
			if phOffset+56 > uint64(len(data)) {
				continue
			}
			p_type := order.Uint32(data[phOffset : phOffset+4])
			p_vaddr := order.Uint64(data[phOffset+16 : phOffset+24])
			if p_type == 1 { // PT_LOAD
				bootstrapAddr = p_vaddr
				break
			}
		}

		if bootstrapAddr != 0 {
			if err := injectBootstrapStub(data, order, e_entry); err != nil {
				return fmt.Errorf("injecting bootstrap stub: %w", err)
			}
			fmt.Printf("Bootstrap stub will be placed at segment start: 0x%08x -> 0x%08x\n", bootstrapAddr, e_entry)
		}
	}

	// Write the fixed file
	if err := os.WriteFile(outputPath, data, 0755); err != nil {
		return fmt.Errorf("writing %s: %w", outputPath, err)
	}

	fmt.Printf("Fixed ELF written to %s\n", outputPath)
	return nil
}
