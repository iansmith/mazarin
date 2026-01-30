package bootloader

// ELF64 constants
const (
	elfMagic    = 0x464C457F // "\x7FELF" as little-endian uint32
	elfClass64  = 2
	elfDataLSB  = 1
	elfTypeExec = 2
	elfTypeDyn  = 3
	ElfPTLoad   = 1 // PT_LOAD segment type

	elfEhdrSize = 64 // ELF64 header size
	elfPhdrSize = 56 // ELF64 program header size

	// Segment flags
	PF_X = 0x1
	PF_W = 0x2
	PF_R = 0x4

	// Maximum number of LOAD segments we can return
	MaxLoadSegments = 32
)

// ELFError is a pre-allocated error for ELF parsing failures.
type ELFError struct {
	Msg string
}

func (e *ELFError) Error() string {
	return "elf: " + e.Msg
}

// Pre-allocated errors to avoid heap allocation on error paths.
var (
	ErrELFTooSmall    = &ELFError{"file too small"}
	ErrELFBadMagic    = &ELFError{"not an ELF file"}
	ErrELFNot64LE     = &ELFError{"not ELF64 little-endian"}
	ErrELFTooManyPhdrs = &ELFError{"too many program headers"}
	ErrELFNoLoadSegs  = &ELFError{"no LOAD segments"}
	ErrELFPhdrOOB     = &ELFError{"program header out of bounds"}
)

// ParseELFHeaders parses ELF64 headers from raw bytes (in-memory ELF).
// Returns the kernel metadata and LOAD segment descriptors.
// Does NOT allocate memory or copy segment data — the caller handles that.
//
// The segments slice is backed by a caller-provided array to avoid heap allocation.
// Pass a pointer to a [MaxLoadSegments]LoadSegment array.
func ParseELFHeaders(data []byte, segBuf *[MaxLoadSegments]LoadSegment) (kernel LoadedKernel, nsegs int, err error) {
	if len(data) < elfEhdrSize {
		return kernel, 0, ErrELFTooSmall
	}

	// Validate ELF magic
	magic := le32(data[0:4])
	if magic != elfMagic {
		return kernel, 0, ErrELFBadMagic
	}
	if data[4] != elfClass64 || data[5] != elfDataLSB {
		return kernel, 0, ErrELFNot64LE
	}

	// Parse header fields
	kernel.Entry = le64(data[0x18:0x20])
	phoff := le64(data[0x20:0x28])
	phentsize := le16(data[0x36:0x38])
	phnum := le16(data[0x38:0x3A])

	if phnum > MaxLoadSegments {
		return kernel, 0, ErrELFTooManyPhdrs
	}

	// Scan program headers for PT_LOAD segments
	lowestVirt := uint64(0xFFFFFFFFFFFFFFFF)
	highestVirt := uint64(0)

	for i := uint16(0); i < phnum; i++ {
		off := phoff + uint64(i)*uint64(phentsize)
		if off+uint64(elfPhdrSize) > uint64(len(data)) {
			return kernel, 0, ErrELFPhdrOOB
		}
		ph := data[off:]

		pType := le32(ph[0:4])
		if pType != ElfPTLoad {
			continue
		}

		seg := &segBuf[nsegs]
		seg.Flags = le32(ph[4:8])
		seg.Offset = le64(ph[8:16])
		seg.Vaddr = le64(ph[16:24])
		seg.Paddr = le64(ph[24:32])
		seg.Filesz = le64(ph[32:40])
		seg.Memsz = le64(ph[40:48])
		nsegs++

		if seg.Memsz == 0 {
			continue
		}
		if seg.Vaddr < lowestVirt {
			lowestVirt = seg.Vaddr
		}
		end := seg.Vaddr + seg.Memsz
		if end > highestVirt {
			highestVirt = end
		}
	}

	if lowestVirt >= highestVirt {
		return kernel, 0, ErrELFNoLoadSegs
	}

	kernel.LowestVirt = lowestVirt
	kernel.HighestVirt = highestVirt
	return kernel, nsegs, nil
}

// IsNegativeOffset returns true if the ELF file offset looks "negative" —
// a quirk of Go's linker when using the -T flag. The first LOAD segment
// has an offset > 0x8000000000000000 which indicates a 64KB header region
// that should be zero-filled, with .text starting at file offset 0x1000.
func IsNegativeOffset(offset uint64) bool {
	return offset > 0x8000000000000000
}

// Little-endian integer readers (no heap allocation).

func le16(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func le64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}
