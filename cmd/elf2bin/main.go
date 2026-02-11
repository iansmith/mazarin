// elf2bin - Convert ELF file to raw binary
//
// Extracts PT_LOAD segments from an ELF file and writes them to a raw binary.
// The binary is sized to cover from the lowest to highest segment address.
//
// Usage: elf2bin <input.elf> <output.bin>
package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <input.elf> <output.bin>\n", os.Args[0])
		os.Exit(1)
	}

	if err := elf2bin(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func elf2bin(inputPath, outputPath string) error {
	// Read ELF file
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", inputPath, err)
	}

	// Verify ELF magic
	if len(data) < 64 || string(data[:4]) != "\x7fELF" {
		return fmt.Errorf("%s is not an ELF file", inputPath)
	}

	// Check 64-bit
	if data[4] != 2 {
		return fmt.Errorf("%s is not a 64-bit ELF", inputPath)
	}

	// Determine endianness
	var order binary.ByteOrder
	if data[5] == 1 {
		order = binary.LittleEndian
	} else if data[5] == 2 {
		order = binary.BigEndian
	} else {
		return fmt.Errorf("unknown endianness")
	}

	// Read program header info
	e_phoff := order.Uint64(data[32:40])
	e_phentsize := order.Uint16(data[54:56])
	e_phnum := order.Uint16(data[56:58])

	// Find lowest and highest addresses in PT_LOAD segments
	var minAddr, maxAddr uint64 = ^uint64(0), 0
	var segments []struct {
		offset uint64
		vaddr  uint64
		filesz uint64
	}

	for i := uint16(0); i < e_phnum; i++ {
		phOffset := e_phoff + uint64(i)*uint64(e_phentsize)
		if phOffset+56 > uint64(len(data)) {
			continue
		}

		p_type := order.Uint32(data[phOffset : phOffset+4])
		if p_type != 1 { // PT_LOAD
			continue
		}

		p_offset := order.Uint64(data[phOffset+8 : phOffset+16])
		p_vaddr := order.Uint64(data[phOffset+16 : phOffset+24])
		p_filesz := order.Uint64(data[phOffset+32 : phOffset+40])
		p_memsz := order.Uint64(data[phOffset+40 : phOffset+48])

		fmt.Printf("Segment: vaddr=0x%08x filesz=0x%x memsz=0x%x offset=0x%x\n",
			p_vaddr, p_filesz, p_memsz, p_offset)

		segments = append(segments, struct {
			offset uint64
			vaddr  uint64
			filesz uint64
		}{p_offset, p_vaddr, p_filesz})

		if p_vaddr < minAddr {
			minAddr = p_vaddr
		}
		if p_vaddr+p_memsz > maxAddr {
			maxAddr = p_vaddr + p_memsz
		}
	}

	if len(segments) == 0 {
		return fmt.Errorf("no PT_LOAD segments found")
	}

	// Create output binary
	binSize := maxAddr - minAddr
	fmt.Printf("Binary: base=0x%08x size=0x%x (%d bytes)\n", minAddr, binSize, binSize)

	binary := make([]byte, binSize)

	// Copy each segment into the binary
	for _, seg := range segments {
		relAddr := seg.vaddr - minAddr
		if seg.offset+seg.filesz > uint64(len(data)) {
			return fmt.Errorf("segment extends beyond file")
		}
		if relAddr+seg.filesz > uint64(len(binary)) {
			return fmt.Errorf("segment extends beyond binary")
		}
		copy(binary[relAddr:], data[seg.offset:seg.offset+seg.filesz])
		fmt.Printf("  Copied 0x%x bytes from file offset 0x%x to binary offset 0x%x\n",
			seg.filesz, seg.offset, relAddr)
	}

	// Write output
	if err := os.WriteFile(outputPath, binary, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", outputPath, err)
	}

	fmt.Printf("Written %s (%d bytes)\n", outputPath, len(binary))
	return nil
}
