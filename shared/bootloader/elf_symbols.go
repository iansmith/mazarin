package bootloader

// ELF section header constants
const (
	shtNull   = 0
	shtSymtab = 2
	shtStrtab = 3

	elfShdrSize = 64 // ELF64 section header size
	elfSymSize  = 24 // Elf64_Sym size
)

// ExtractSymbols parses .symtab/.strtab from an in-memory ELF and fills
// kernel.Symbols for any symbol whose name matches one of the requested names.
//
// names is a fixed-size array of [64]byte entries, each containing a
// null-terminated symbol name to search for. numNames is the count of
// valid entries in names.
//
// Symbol values are stored as-is from the ELF; the caller applies any
// address offset (e.g., high-memory relocation).
func ExtractSymbols(elfData []byte, kernel *LoadedKernel, names *[16][64]byte, numNames int) {
	if len(elfData) < elfEhdrSize {
		return
	}

	// Parse section header table location
	shoff := le64(elfData[0x28:0x30])
	shentsize := le16(elfData[0x3A:0x3C])
	shnum := le16(elfData[0x3C:0x3E])
	shstrndx := le16(elfData[0x3E:0x40])

	if shoff == 0 || shnum == 0 || shentsize < elfShdrSize {
		return
	}

	// Get section header string table (.shstrtab)
	shstrHdr := shoff + uint64(shstrndx)*uint64(shentsize)
	if shstrHdr+elfShdrSize > uint64(len(elfData)) {
		return
	}
	shstrOffset := le64(elfData[shstrHdr+24 : shstrHdr+32])
	shstrSize := le64(elfData[shstrHdr+32 : shstrHdr+40])
	if shstrOffset+shstrSize > uint64(len(elfData)) {
		return
	}

	// Find .symtab and .strtab sections
	var symtabOff, symtabSize, strtabOff, strtabSize uint64

	for i := uint16(0); i < shnum; i++ {
		off := shoff + uint64(i)*uint64(shentsize)
		if off+elfShdrSize > uint64(len(elfData)) {
			continue
		}
		secType := le32(elfData[off+4 : off+8])
		nameIdx := le32(elfData[off : off+4])

		if secType == shtSymtab {
			symtabOff = le64(elfData[off+24 : off+32])
			symtabSize = le64(elfData[off+32 : off+40])
			continue
		}

		// Match .strtab by name (not .shstrtab)
		if secType == shtStrtab && uint64(nameIdx) < shstrSize {
			nameStart := shstrOffset + uint64(nameIdx)
			if nameStart+7 <= uint64(len(elfData)) {
				n := elfData[nameStart:]
				if n[0] == '.' && n[1] == 's' && n[2] == 't' && n[3] == 'r' &&
					n[4] == 't' && n[5] == 'a' && n[6] == 'b' &&
					(nameStart+7 >= uint64(len(elfData)) || n[7] == 0) {
					strtabOff = le64(elfData[off+24 : off+32])
					strtabSize = le64(elfData[off+32 : off+40])
				}
			}
		}
	}

	if symtabOff == 0 || symtabSize == 0 || strtabOff == 0 || strtabSize == 0 {
		return
	}
	if symtabOff+symtabSize > uint64(len(elfData)) || strtabOff+strtabSize > uint64(len(elfData)) {
		return
	}

	// Iterate symbols
	numSyms := symtabSize / elfSymSize
	for i := uint64(0); i < numSyms; i++ {
		symOff := symtabOff + i*elfSymSize
		if symOff+elfSymSize > uint64(len(elfData)) {
			break
		}

		nameIdx := le32(elfData[symOff : symOff+4])
		value := le64(elfData[symOff+8 : symOff+16])

		if uint64(nameIdx) >= strtabSize || value == 0 {
			continue
		}

		// Get symbol name from string table
		nameStart := strtabOff + uint64(nameIdx)
		if nameStart >= uint64(len(elfData)) {
			continue
		}
		sym := elfData[nameStart:]

		// Compute symbol name length (null-terminated, max 100)
		symLen := 0
		for symLen < 100 && symLen < len(sym) && sym[symLen] != 0 {
			symLen++
		}

		// Match against requested names
		for j := 0; j < numNames; j++ {
			if matchName64(sym, symLen, &names[j]) {
				if kernel.NumSymbols < len(kernel.Symbols) {
					kernel.Symbols[kernel.NumSymbols].Name = names[j]
					kernel.Symbols[kernel.NumSymbols].Value = value
					kernel.NumSymbols++
				}
				break
			}
		}
	}
}

// matchName64 compares a null-terminated ELF symbol name (sym[:symLen]) against
// a null-terminated name in a [64]byte array. No heap allocation.
func matchName64(sym []byte, symLen int, name *[64]byte) bool {
	// Find length of name (null-terminated)
	nameLen := 0
	for nameLen < 64 && name[nameLen] != 0 {
		nameLen++
	}
	if nameLen != symLen {
		return false
	}
	for i := 0; i < nameLen; i++ {
		if sym[i] != name[i] {
			return false
		}
	}
	return true
}

// MakeSymbolName converts a Go string to a [64]byte null-terminated name
// suitable for use with ExtractSymbols. The string is truncated at 63 bytes.
func MakeSymbolName(s string) [64]byte {
	var name [64]byte
	n := len(s)
	if n > 63 {
		n = 63
	}
	for i := 0; i < n; i++ {
		name[i] = s[i]
	}
	return name
}
