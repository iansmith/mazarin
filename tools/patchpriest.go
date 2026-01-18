//go:build !qemuvirt

// patchpriest patches a userspace ELF binary to set the PriestSyscallEntry
// function pointer to the address of priest's PriestSyscallEntry function.
//
// Usage: patchpriest <priest.elf> <userspace.elf> <output.elf>
//
// This tool:
// 1. Finds main.PriestSyscallEntry in priest.elf
// 2. Finds syscall.PriestSyscallEntry in userspace.elf
// 3. Patches the userspace binary with priest's function address
// 4. Handles BSS (NOBITS) sections by expanding the file if needed
package main

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s <priest.elf> <userspace.elf> <output.elf>\n", os.Args[0])
		os.Exit(1)
	}

	priestPath := os.Args[1]
	userspaceInputPath := os.Args[2]
	outputPath := os.Args[3]

	// Find PriestSyscallEntry address in priest
	priestAddr, err := findSymbolAddress(priestPath, "main.PriestSyscallEntry")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding main.PriestSyscallEntry in priest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found main.PriestSyscallEntry in priest at 0x%x\n", priestAddr)

	// Find syscall.PriestSyscallEntry in userspace binary
	userspaceSymAddr, section, err := findSymbolWithSection(userspaceInputPath, "syscall.PriestSyscallEntry")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding syscall.PriestSyscallEntry in userspace: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found syscall.PriestSyscallEntry in userspace at 0x%x (section: %s, type: %s)\n",
		userspaceSymAddr, section.Name, section.Type.String())

	// Copy the input file to output
	if err := copyFile(userspaceInputPath, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error copying file: %v\n", err)
		os.Exit(1)
	}

	// Patch the output file
	if err := patchELF(outputPath, userspaceSymAddr, priestAddr, section); err != nil {
		fmt.Fprintf(os.Stderr, "Error patching ELF: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully patched %s\n", outputPath)
	fmt.Printf("  syscall.PriestSyscallEntry (0x%x) = 0x%x\n", userspaceSymAddr, priestAddr)
}

// findSymbolAddress finds a symbol by name and returns its virtual address.
func findSymbolAddress(path, symbolName string) (uint64, error) {
	f, err := elf.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open ELF: %w", err)
	}
	defer f.Close()

	symbols, err := f.Symbols()
	if err != nil {
		return 0, fmt.Errorf("read symbols: %w", err)
	}

	for _, sym := range symbols {
		if sym.Name == symbolName {
			return sym.Value, nil
		}
	}

	return 0, fmt.Errorf("symbol %q not found", symbolName)
}

// findSymbolWithSection finds a symbol and returns its address and containing section.
func findSymbolWithSection(path, symbolName string) (uint64, *elf.Section, error) {
	f, err := elf.Open(path)
	if err != nil {
		return 0, nil, fmt.Errorf("open ELF: %w", err)
	}
	defer f.Close()

	symbols, err := f.Symbols()
	if err != nil {
		return 0, nil, fmt.Errorf("read symbols: %w", err)
	}

	var sym *elf.Symbol
	for i := range symbols {
		if symbols[i].Name == symbolName {
			sym = &symbols[i]
			break
		}
	}
	if sym == nil {
		return 0, nil, fmt.Errorf("symbol %q not found", symbolName)
	}

	// Find the section containing this symbol
	for _, section := range f.Sections {
		if section.Addr <= sym.Value && sym.Value < section.Addr+section.Size {
			return sym.Value, section, nil
		}
	}

	return 0, nil, fmt.Errorf("no section contains symbol %q at 0x%x", symbolName, sym.Value)
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// patchELF patches the ELF file at the given symbol address with the new value.
// If the symbol is in a NOBITS (BSS) section, it expands the file.
func patchELF(path string, symAddr, newValue uint64, section *elf.Section) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	// Read ELF header to determine endianness and class
	elfFile, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("parse ELF: %w", err)
	}
	byteOrder := elfFile.ByteOrder
	is64Bit := elfFile.Class == elf.ELFCLASS64
	elfFile.Close()

	if section.Type == elf.SHT_NOBITS {
		// BSS section - need to expand the file
		return patchNOBITS(f, symAddr, newValue, section, byteOrder, is64Bit)
	}

	// Regular section - calculate file offset and patch
	fileOffset := section.Offset + (symAddr - section.Addr)
	return patchAtOffset(f, fileOffset, newValue, byteOrder, is64Bit)
}

// patchAtOffset writes a value at the given file offset.
func patchAtOffset(f *os.File, offset, value uint64, byteOrder binary.ByteOrder, is64Bit bool) error {
	if _, err := f.Seek(int64(offset), 0); err != nil {
		return fmt.Errorf("seek to offset 0x%x: %w", offset, err)
	}

	if is64Bit {
		var buf [8]byte
		byteOrder.PutUint64(buf[:], value)
		if _, err := f.Write(buf[:]); err != nil {
			return fmt.Errorf("write value: %w", err)
		}
	} else {
		var buf [4]byte
		byteOrder.PutUint32(buf[:], uint32(value))
		if _, err := f.Write(buf[:]); err != nil {
			return fmt.Errorf("write value: %w", err)
		}
	}

	return nil
}

// patchNOBITS handles patching a symbol in a NOBITS (BSS) section.
// This requires:
// 1. Converting the section to PROGBITS
// 2. Expanding the file to include the section data
// 3. Writing the patch value
func patchNOBITS(f *os.File, symAddr, newValue uint64, section *elf.Section, byteOrder binary.ByteOrder, is64Bit bool) error {
	fmt.Printf("Symbol is in NOBITS section %s - expanding file\n", section.Name)

	// Get current file size
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	currentSize := fi.Size()

	// The new data will be appended at the end of the file
	newDataOffset := uint64(currentSize)

	// Calculate the offset within the BSS section where our symbol lives
	symbolOffsetInSection := symAddr - section.Addr

	// We need to write zeros for the BSS data up to our symbol, then our value
	// For simplicity, we'll write the entire BSS section as zeros, then patch
	bssData := make([]byte, section.Size)

	// Write our value at the appropriate offset
	valueOffset := symbolOffsetInSection
	if is64Bit {
		byteOrder.PutUint64(bssData[valueOffset:], newValue)
	} else {
		byteOrder.PutUint32(bssData[valueOffset:], uint32(newValue))
	}

	// Append the BSS data to the file
	if _, err := f.Seek(0, 2); err != nil { // Seek to end
		return fmt.Errorf("seek to end: %w", err)
	}
	if _, err := f.Write(bssData); err != nil {
		return fmt.Errorf("write BSS data: %w", err)
	}

	// Now we need to update the section header to:
	// 1. Change type from NOBITS to PROGBITS
	// 2. Set the file offset to our new data location
	if err := updateSectionHeader(f, section, newDataOffset, byteOrder, is64Bit); err != nil {
		return fmt.Errorf("update section header: %w", err)
	}

	return nil
}

// updateSectionHeader modifies a section header in place.
func updateSectionHeader(f *os.File, section *elf.Section, newOffset uint64, byteOrder binary.ByteOrder, is64Bit bool) error {
	// We need to find the section header in the file
	// Read the ELF header to find section header table
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}

	// Read e_ident
	var ident [16]byte
	if _, err := f.Read(ident[:]); err != nil {
		return err
	}

	var shoff uint64     // Section header table offset
	var shentsize uint16 // Section header entry size
	var shnum uint16     // Number of section headers
	var shstrndx uint16  // Section name string table index

	if is64Bit {
		// Read 64-bit ELF header
		if _, err := f.Seek(0x28, 0); err != nil { // e_shoff at offset 0x28
			return err
		}
		var buf [8]byte
		if _, err := f.Read(buf[:]); err != nil {
			return err
		}
		shoff = byteOrder.Uint64(buf[:])

		if _, err := f.Seek(0x3A, 0); err != nil { // e_shentsize at offset 0x3A
			return err
		}
		var buf2 [2]byte
		if _, err := f.Read(buf2[:]); err != nil {
			return err
		}
		shentsize = byteOrder.Uint16(buf2[:])

		if _, err := f.Read(buf2[:]); err != nil { // e_shnum at offset 0x3C
			return err
		}
		shnum = byteOrder.Uint16(buf2[:])

		if _, err := f.Read(buf2[:]); err != nil { // e_shstrndx at offset 0x3E
			return err
		}
		shstrndx = byteOrder.Uint16(buf2[:])
	} else {
		// Read 32-bit ELF header
		if _, err := f.Seek(0x20, 0); err != nil { // e_shoff at offset 0x20
			return err
		}
		var buf [4]byte
		if _, err := f.Read(buf[:]); err != nil {
			return err
		}
		shoff = uint64(byteOrder.Uint32(buf[:]))

		if _, err := f.Seek(0x2E, 0); err != nil { // e_shentsize at offset 0x2E
			return err
		}
		var buf2 [2]byte
		if _, err := f.Read(buf2[:]); err != nil {
			return err
		}
		shentsize = byteOrder.Uint16(buf2[:])

		if _, err := f.Read(buf2[:]); err != nil { // e_shnum at offset 0x30
			return err
		}
		shnum = byteOrder.Uint16(buf2[:])

		if _, err := f.Read(buf2[:]); err != nil { // e_shstrndx at offset 0x32
			return err
		}
		shstrndx = byteOrder.Uint16(buf2[:])
	}

	_ = shstrndx // Not needed for this operation

	// Find our section by matching address
	for i := uint16(0); i < shnum; i++ {
		shdrOffset := shoff + uint64(i)*uint64(shentsize)

		var shAddr uint64
		var shType uint32

		if is64Bit {
			// Read sh_type at offset 4 and sh_addr at offset 16
			if _, err := f.Seek(int64(shdrOffset)+4, 0); err != nil {
				return err
			}
			var typeBuf [4]byte
			if _, err := f.Read(typeBuf[:]); err != nil {
				return err
			}
			shType = byteOrder.Uint32(typeBuf[:])

			if _, err := f.Seek(int64(shdrOffset)+16, 0); err != nil {
				return err
			}
			var addrBuf [8]byte
			if _, err := f.Read(addrBuf[:]); err != nil {
				return err
			}
			shAddr = byteOrder.Uint64(addrBuf[:])
		} else {
			// 32-bit: sh_type at offset 4, sh_addr at offset 12
			if _, err := f.Seek(int64(shdrOffset)+4, 0); err != nil {
				return err
			}
			var typeBuf [4]byte
			if _, err := f.Read(typeBuf[:]); err != nil {
				return err
			}
			shType = byteOrder.Uint32(typeBuf[:])

			if _, err := f.Seek(int64(shdrOffset)+12, 0); err != nil {
				return err
			}
			var addrBuf [4]byte
			if _, err := f.Read(addrBuf[:]); err != nil {
				return err
			}
			shAddr = uint64(byteOrder.Uint32(addrBuf[:]))
		}

		// Check if this is our section (NOBITS and matching address)
		if shType == uint32(elf.SHT_NOBITS) && shAddr == section.Addr {
			fmt.Printf("Found section header at offset 0x%x, updating...\n", shdrOffset)

			// Update sh_type to PROGBITS (1)
			if _, err := f.Seek(int64(shdrOffset)+4, 0); err != nil {
				return err
			}
			var typeBuf [4]byte
			byteOrder.PutUint32(typeBuf[:], uint32(elf.SHT_PROGBITS))
			if _, err := f.Write(typeBuf[:]); err != nil {
				return err
			}

			// Update sh_offset
			if is64Bit {
				// sh_offset at offset 24 in 64-bit
				if _, err := f.Seek(int64(shdrOffset)+24, 0); err != nil {
					return err
				}
				var offBuf [8]byte
				byteOrder.PutUint64(offBuf[:], newOffset)
				if _, err := f.Write(offBuf[:]); err != nil {
					return err
				}
			} else {
				// sh_offset at offset 16 in 32-bit
				if _, err := f.Seek(int64(shdrOffset)+16, 0); err != nil {
					return err
				}
				var offBuf [4]byte
				byteOrder.PutUint32(offBuf[:], uint32(newOffset))
				if _, err := f.Write(offBuf[:]); err != nil {
					return err
				}
			}

			fmt.Printf("Section header updated: type=PROGBITS, offset=0x%x\n", newOffset)
			return nil
		}
	}

	return fmt.Errorf("could not find section header for section at 0x%x", section.Addr)
}
