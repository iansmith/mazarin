// bin2elf.go - Convert binary file directly to ELF object with data symbols
package main

import (
	"debug/elf"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
)

func main() {
	symbolName := flag.String("sym", "binaryData", "Symbol name for the data")
	output := flag.String("o", "", "Output ELF file")
	sectionName := flag.String("section", ".rodata", "Section name (default: .rodata)")
	flag.Parse()

	if flag.NArg() < 1 || *output == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s -sym name [-section name] -o output.o input.bin\n", os.Args[0])
		os.Exit(1)
	}

	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	if err := writeELF(*output, *symbolName, *sectionName, data); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing ELF: %v\n", err)
		os.Exit(1)
	}
}

func writeELF(filename, symbolName, sectionName string, data []byte) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// Build string tables
	strtab := []byte{0} // Start with null byte
	shstrtab := []byte{0}

	// Section names in shstrtab
	shstrtabIdx := len(shstrtab)
	shstrtab = append(shstrtab, ".shstrtab\x00"...)
	dataSectionIdx := len(shstrtab)
	shstrtab = append(shstrtab, sectionName+"\x00"...)
	symtabIdx := len(shstrtab)
	shstrtab = append(shstrtab, ".symtab\x00"...)
	strtabSectionIdx := len(shstrtab)
	shstrtab = append(shstrtab, ".strtab\x00"...)

	// Symbol names in strtab
	startSymOffset := uint32(len(strtab))
	strtab = append(strtab, symbolName+"_start\x00"...)
	endSymOffset := uint32(len(strtab))
	strtab = append(strtab, symbolName+"_end\x00"...)

	// ELF Header
	ehdr := struct {
		Ident     [16]byte
		Type      uint16
		Machine   uint16
		Version   uint32
		Entry     uint64
		Phoff     uint64
		Shoff     uint64
		Flags     uint32
		Ehsize    uint16
		Phentsize uint16
		Phnum     uint16
		Shentsize uint16
		Shnum     uint16
		Shstrndx  uint16
	}{
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
		Shoff:     0, // Will update
		Flags:     0,
		Ehsize:    64,
		Phentsize: 0,
		Phnum:     0,
		Shentsize: 64,
		Shnum:     5, // null + .rodata + .symtab + .strtab + .shstrtab
		Shstrndx:  4, // .shstrtab is section 4
	}

	// Calculate offsets
	// Layout: ELF header | .rodata | .symtab | .strtab | .shstrtab | section headers
	rodataOff := uint64(64)
	symtabOff := rodataOff + uint64(len(data))
	// Align to 8 bytes
	if symtabOff%8 != 0 {
		symtabOff += 8 - (symtabOff % 8)
	}

	// Symbol table: null + start + end
	symtabSize := uint64(3 * 24) // 3 Elf64_Sym entries, 24 bytes each
	strtabOff := symtabOff + symtabSize
	shstrtabOff := strtabOff + uint64(len(strtab))
	shdrOff := shstrtabOff + uint64(len(shstrtab))
	// Align to 8 bytes
	if shdrOff%8 != 0 {
		shdrOff += 8 - (shdrOff % 8)
	}

	ehdr.Shoff = shdrOff

	// Write ELF header
	if err := binary.Write(f, binary.LittleEndian, &ehdr); err != nil {
		return err
	}

	// Write .rodata section (the binary data)
	if _, err := f.Write(data); err != nil {
		return err
	}

	// Pad to symtab offset
	currentOff, _ := f.Seek(0, 1)
	for uint64(currentOff) < symtabOff {
		f.Write([]byte{0})
		currentOff, _ = f.Seek(0, 1)
	}

	// Write symbol table
	// Null symbol
	nullSym := make([]byte, 24)
	f.Write(nullSym)

	// start symbol
	startSym := struct {
		Name  uint32
		Info  uint8
		Other uint8
		Shndx uint16
		Value uint64
		Size  uint64
	}{
		Name:  startSymOffset,
		Info:  uint8(elf.STB_GLOBAL)<<4 | uint8(elf.STT_OBJECT),
		Other: 0,
		Shndx: 1, // .rodata section
		Value: 0,
		Size:  uint64(len(data)),
	}
	binary.Write(f, binary.LittleEndian, &startSym)

	// end symbol
	endSym := struct {
		Name  uint32
		Info  uint8
		Other uint8
		Shndx uint16
		Value uint64
		Size  uint64
	}{
		Name:  endSymOffset,
		Info:  uint8(elf.STB_GLOBAL)<<4 | uint8(elf.STT_OBJECT),
		Other: 0,
		Shndx: 1, // .rodata section
		Value: uint64(len(data)),
		Size:  0,
	}
	binary.Write(f, binary.LittleEndian, &endSym)

	// Write .strtab
	f.Write(strtab)

	// Write .shstrtab
	f.Write(shstrtab)

	// Pad to section header offset
	currentOff2, _ := f.Seek(0, 1)
	for uint64(currentOff2) < shdrOff {
		f.Write([]byte{0})
		currentOff2, _ = f.Seek(0, 1)
	}

	// Write section headers
	shdrs := []struct {
		Name      uint32
		Type      uint32
		Flags     uint64
		Addr      uint64
		Off       uint64
		Size      uint64
		Link      uint32
		Info      uint32
		Addralign uint64
		Entsize   uint64
	}{
		// 0: Null section
		{},
		// 1: .rodata
		{
			Name:      uint32(dataSectionIdx),
			Type:      uint32(elf.SHT_PROGBITS),
			Flags:     uint64(elf.SHF_ALLOC),
			Addr:      0,
			Off:       rodataOff,
			Size:      uint64(len(data)),
			Link:      0,
			Info:      0,
			Addralign: 4,
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
			Info:      1, // One local symbol (null)
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

	for _, shdr := range shdrs {
		if err := binary.Write(f, binary.LittleEndian, &shdr); err != nil {
			return err
		}
	}

	return nil
}
