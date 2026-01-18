
package dtb

import "unsafe"

// DTB Header Format (Flattened Device Tree)
const (
	FDT_MAGIC            = 0xd00dfeed
	FDT_HEADER_SIZE      = 40

	// Offsets in header
	OFFSET_MAGIC         = 0x00
	OFFSET_TOTALSIZE     = 0x04
	OFFSET_OFF_DT_STRUCT = 0x08
	OFFSET_OFF_DT_STRINGS = 0x0C
	OFFSET_OFF_MEM_RSVMAP = 0x10
	OFFSET_VERSION       = 0x14
	OFFSET_LAST_COMP_VERSION = 0x18
	OFFSET_BOOT_CPUID_PHYS = 0x1C
	OFFSET_SIZE_DT_STRINGS = 0x20
	OFFSET_SIZE_DT_STRUCT = 0x24
)

// DTB Structure Block Tokens
const (
	FDT_BEGIN_NODE = 0x00000001
	FDT_END_NODE   = 0x00000002
	FDT_PROP       = 0x00000003
	FDT_NOP        = 0x00000004
	FDT_END        = 0x00000009
)

// DTBHeader represents the Device Tree Blob header
type DTBHeader struct {
	dtbAddr uintptr // Base address of DTB in memory
}

// NewDTBHeader creates a DTB header reader at the given address
//
//go:nosplit
func NewDTBHeader(addr uintptr) *DTBHeader {
	return &DTBHeader{dtbAddr: addr}
}

// Validate checks if this is a valid DTB
//
//go:nosplit
func (h *DTBHeader) Validate() bool {
	magic := h.readU32BE(OFFSET_MAGIC)
	return magic == FDT_MAGIC
}

// GetStructOffset returns the offset to the structure block
//
//go:nosplit
func (h *DTBHeader) GetStructOffset() uint32 {
	return h.readU32BE(OFFSET_OFF_DT_STRUCT)
}

// GetStringsOffset returns the offset to the strings block
//
//go:nosplit
func (h *DTBHeader) GetStringsOffset() uint32 {
	return h.readU32BE(OFFSET_OFF_DT_STRINGS)
}

// GetTotalSize returns the total size of the DTB
//
//go:nosplit
func (h *DTBHeader) GetTotalSize() uint32 {
	return h.readU32BE(OFFSET_TOTALSIZE)
}

// readU32BE reads a big-endian 32-bit value at offset
//
//go:nosplit
func (h *DTBHeader) readU32BE(offset uint32) uint32 {
	addr := h.dtbAddr + uintptr(offset)
	val := *(*uint32)(unsafe.Pointer(addr))
	return swapU32(val)
}

// readU64BE reads a big-endian 64-bit value at offset
//
//go:nosplit
func (h *DTBHeader) readU64BE(offset uint32) uint64 {
	addr := h.dtbAddr + uintptr(offset)
	val := *(*uint64)(unsafe.Pointer(addr))
	return swapU64(val)
}

// ReadStringBuf reads a null-terminated string at offset into provided buffer
// Returns the length (excluding null terminator)
//
//go:nosplit
func (h *DTBHeader) ReadStringBuf(offset uint32, buf []byte) int {
	addr := h.dtbAddr + uintptr(offset)
	ptr := (*byte)(unsafe.Pointer(addr))

	// Copy until null or buffer full
	length := 0
	for length < len(buf) {
		b := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + uintptr(length)))
		if b == 0 {
			break
		}
		buf[length] = b
		length++
	}

	return length
}

// StringEquals compares a null-terminated string at offset with provided string
//
//go:nosplit
func (h *DTBHeader) StringEquals(offset uint32, match string) bool {
	addr := h.dtbAddr + uintptr(offset)

	for i := 0; i < len(match); i++ {
		b := *(*byte)(unsafe.Pointer(addr + uintptr(i)))
		if b != match[i] {
			return false
		}
	}

	// Check for null terminator after match
	b := *(*byte)(unsafe.Pointer(addr + uintptr(len(match))))
	return b == 0
}

// GetBaseAddr returns the base address of the DTB
//
//go:nosplit
func (h *DTBHeader) GetBaseAddr() uintptr {
	return h.dtbAddr
}

// Align to 4-byte boundary
//
//go:nosplit
func align4(offset uint32) uint32 {
	return (offset + 3) &^ 3
}

// swapU32 swaps byte order (big-endian to little-endian)
//
//go:nosplit
func swapU32(val uint32) uint32 {
	return ((val & 0xFF000000) >> 24) |
	       ((val & 0x00FF0000) >> 8) |
	       ((val & 0x0000FF00) << 8) |
	       ((val & 0x000000FF) << 24)
}

// swapU64 swaps byte order for 64-bit values
//
//go:nosplit
func swapU64(val uint64) uint64 {
	return ((val & 0xFF00000000000000) >> 56) |
	       ((val & 0x00FF000000000000) >> 40) |
	       ((val & 0x0000FF0000000000) >> 24) |
	       ((val & 0x000000FF00000000) >> 8) |
	       ((val & 0x00000000FF000000) << 8) |
	       ((val & 0x0000000000FF0000) << 24) |
	       ((val & 0x000000000000FF00) << 40) |
	       ((val & 0x00000000000000FF) << 56)
}
