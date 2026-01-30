package dtb

import "unsafe"

// GetMemoryInfo parses the DTB /memory node to get RAM base address and size.
// This is a minimal, nosplit-safe function for use during early boot before
// heap allocation is available. It walks the DTB directly without using
// callbacks or closures to avoid heap allocation.
//
// Returns (base, size, ok). If the memory node is not found or invalid,
// returns (0, 0, false).
//
//go:nosplit
func GetMemoryInfo(dtbAddr uintptr) (uintptr, uint64, bool) {
	// Use stack-local DTBHeader to avoid heap allocation.
	// NewDTBHeader returns &DTBHeader{} which escapes to heap.
	var hdr DTBHeader
	hdr.dtbAddr = dtbAddr
	header := &hdr
	if !header.Validate() {
		return 0, 0, false
	}

	structOff := header.GetStructOffset()
	stringsOff := header.GetStringsOffset()
	baseAddr := header.GetBaseAddr()
	_ = stringsOff

	offset := structOff

	for {
		token := header.readU32BE(offset)
		offset += 4

		switch token {
		case FDT_BEGIN_NODE:
			// Read node name
			var nameBuf [64]byte
			nameLen := header.ReadStringBuf(offset, nameBuf[:])

			// Skip past name (include null terminator, align to 4)
			skipLen := uint32(nameLen) + 1
			offset = align4(offset + skipLen)

			// Check if this is a memory node (name starts with "memory")
			isMemory := nameLen >= 6 &&
				nameBuf[0] == 'm' && nameBuf[1] == 'e' &&
				nameBuf[2] == 'm' && nameBuf[3] == 'o' &&
				nameBuf[4] == 'r' && nameBuf[5] == 'y'

			// Scan properties for this node
			for {
				propToken := header.readU32BE(offset)
				if propToken != FDT_PROP {
					break
				}
				offset += 4

				propLen := header.readU32BE(offset)
				propNameOff := header.readU32BE(offset + 4)
				offset += 8

				if isMemory && header.StringEquals(stringsOff+propNameOff, "reg") && propLen >= 16 {
					// Parse reg: <addr-hi(4) addr-lo(4) size-hi(4) size-lo(4)>
					valueAddr := baseAddr + uintptr(offset)
					addrHi := swapU32(*(*uint32)(unsafe.Pointer(valueAddr)))
					addrLo := swapU32(*(*uint32)(unsafe.Pointer(valueAddr + 4)))
					sizeHi := swapU32(*(*uint32)(unsafe.Pointer(valueAddr + 8)))
					sizeLo := swapU32(*(*uint32)(unsafe.Pointer(valueAddr + 12)))

					addr := (uint64(addrHi) << 32) | uint64(addrLo)
					size := (uint64(sizeHi) << 32) | uint64(sizeLo)

					if size > 0 {
						return uintptr(addr), size, true
					}
				}

				offset = align4(offset + propLen)
			}

		case FDT_END_NODE:
			continue

		case FDT_PROP:
			// Shouldn't reach here in normal flow, but handle it
			propLen := header.readU32BE(offset)
			offset += 8
			offset = align4(offset + propLen)

		case FDT_NOP:
			continue

		case FDT_END:
			return 0, 0, false

		default:
			continue
		}
	}
}
