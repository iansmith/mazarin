//go:build arm64

package mazdl

import (
	"debug/elf"
	"encoding/binary"
	"unsafe"
)

// applyRelative walks a .rela.dyn-style table and applies all
// R_AARCH64_RELATIVE entries. A RELATIVE reloc has no symbol — the
// addend is the offset within the module at which the target lives, and
// we write `base + addend` to *(base + r_offset). This is the
// self-contained pass that runs before any symbol resolution; after
// RELATIVE is applied, all intra-module pointer fields are valid.
//
// Other reloc types in .rela.dyn (GLOB_DAT, ABS64 against named syms)
// are skipped here and handled in applySymbolRelocs, which needs the
// resolved globalSyms table.
func applyRelative(base uintptr, rela []byte) error {
	for off := 0; off+24 <= len(rela); off += 24 {
		rOffset := binary.LittleEndian.Uint64(rela[off:])
		rInfo := binary.LittleEndian.Uint64(rela[off+8:])
		rAddend := int64(binary.LittleEndian.Uint64(rela[off+16:]))
		rType := uint32(rInfo & 0xffffffff)

		if elf.R_AARCH64(rType) != elf.R_AARCH64_RELATIVE {
			continue
		}
		target := base + uintptr(rOffset)
		val := uint64(int64(base) + rAddend)
		*(*uint64)(unsafe.Pointer(target)) = val
	}
	return nil
}

// applySymbolRelocs walks .rela.dyn and .rela.plt and resolves every
// non-RELATIVE entry by looking up its symbol name in globalSyms.
// Supported types:
//   - R_AARCH64_GLOB_DAT    : *(base + r_offset) = symaddr + addend
//   - R_AARCH64_JUMP_SLOT   : *(base + r_offset) = symaddr
//   - R_AARCH64_ABS64       : *(base + r_offset) = symaddr + addend
//
// Anything else is reported as an unsupported reloc and aborts the
// load — better to fail loudly than silently leave a slot with a garbage
// value that crashes the plugin later.
func applySymbolRelocs(base uintptr, rela []byte, syms []elf.Symbol) error {
	for off := 0; off+24 <= len(rela); off += 24 {
		rOffset := binary.LittleEndian.Uint64(rela[off:])
		rInfo := binary.LittleEndian.Uint64(rela[off+8:])
		rAddend := int64(binary.LittleEndian.Uint64(rela[off+16:]))
		rType := uint32(rInfo & 0xffffffff)
		rSym := uint32(rInfo >> 32)

		rt := elf.R_AARCH64(rType)
		if rt == elf.R_AARCH64_RELATIVE {
			continue
		}

		if rSym == 0 || rSym >= uint32(len(syms)+1) {
			return errorf("Open", "", "", "reloc at 0x%x references out-of-range sym index %d", rOffset, rSym)
		}
		// debug/elf returns dynsyms starting at index 1 (skipping the
		// STN_UNDEF entry at index 0), so rSym-1 is the right offset.
		sym := syms[rSym-1]

		// Resolution order:
		//  1. If the symbol is DEFINED in this plugin (section != UND),
		//     use base + sym.Value. A Go plugin can emit GLOB_DAT
		//     against its own data symbols (e.g. internal/strconv
		//     lookup tables) because the compiler threads the reference
		//     through an indirection regardless of who defines it.
		//  2. Otherwise (SHN_UNDEF) look the name up in globalSyms —
		//     this is where host-resident policy symbols land.
		var symAddr uintptr
		if sym.Section != elf.SHN_UNDEF {
			symAddr = base + uintptr(sym.Value)
		} else {
			e, ok := globalSyms[sym.Name]
			if !ok {
				return errorf("Open", "", sym.Name, "unresolved symbol (reloc type %v at 0x%x)", rt, rOffset)
			}
			symAddr = e.addr
		}
		target := base + uintptr(rOffset)

		switch rt {
		case elf.R_AARCH64_GLOB_DAT, elf.R_AARCH64_ABS64:
			*(*uint64)(unsafe.Pointer(target)) = uint64(int64(symAddr) + rAddend)
		case elf.R_AARCH64_JUMP_SLOT:
			*(*uint64)(unsafe.Pointer(target)) = uint64(symAddr)
		default:
			return errorf("Open", "", sym.Name, "unsupported reloc type %v at 0x%x", rt, rOffset)
		}
	}
	return nil
}
