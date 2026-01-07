//go:build qemuvirt && aarch64

package dtb

import "unsafe"

// DTBProperty represents a single property in a node
type DTBProperty struct {
	nameOff uint32 // Offset in strings block
	value   [256]byte
	valueLen uint32
}

// DTBNode represents a node during DTB traversal
type DTBNode struct {
	header      *DTBHeader
	name        [64]byte
	nameLen     int
	properties  [16]DTBProperty
	propCount   int
	structOff   uint32
	stringsOff  uint32
}

// NodeCallback is called for each node discovered during DTB walk
type NodeCallback func(node *DTBNode)

// Walk walks the DTB structure block and calls the callback for each node
//
//go:nosplit
func Walk(dtbAddr uintptr, callback NodeCallback) bool {
	header := NewDTBHeader(dtbAddr)

	// Validate DTB magic
	if !header.Validate() {
		return false
	}

	structOff := header.GetStructOffset()
	stringsOff := header.GetStringsOffset()

	// Walk the structure block
	walkStructBlock(header, structOff, stringsOff, callback)

	return true
}

// walkStructBlock walks the structure block tokens
//
//go:nosplit
func walkStructBlock(header *DTBHeader, structOff, stringsOff uint32, callback NodeCallback) {
	offset := structOff
	baseAddr := header.GetBaseAddr()

	for {
		token := header.readU32BE(offset)
		offset += 4

		switch token {
		case FDT_BEGIN_NODE:
			// Create node on stack (no heap allocation)
			var node DTBNode
			node.header = header
			node.structOff = structOff
			node.stringsOff = stringsOff
			node.propCount = 0

			// Read node name into fixed buffer
			node.nameLen = header.ReadStringBuf(offset, node.name[:])

			// Calculate offset after name (include null terminator)
			nameLen := uint32(node.nameLen) + 1
			offset = align4(offset + nameLen)

			// Collect properties for this node
			offset = collectProperties(header, offset, stringsOff, &node)

			// Call callback with complete node
			callback(&node)

		case FDT_END_NODE:
			// Just move to next node
			continue

		case FDT_PROP:
			// Properties are handled by collectProperties
			// This shouldn't be reached in normal flow
			propLen := header.readU32BE(offset)
			offset += 8 // Skip len and nameoff
			offset = align4(offset + propLen)

		case FDT_NOP:
			// Skip padding
			continue

		case FDT_END:
			// End of structure block
			return

		default:
			// Unknown token, skip
			_ = baseAddr // Suppress unused warning
			continue
		}
	}
}

// collectProperties collects all properties for a node
// Returns offset after the last property (pointing to END_NODE or child BEGIN_NODE)
//
//go:nosplit
func collectProperties(header *DTBHeader, offset, stringsOff uint32, node *DTBNode) uint32 {
	for node.propCount < len(node.properties) {
		token := header.readU32BE(offset)

		if token != FDT_PROP {
			// Not a property, return to caller
			return offset
		}

		offset += 4

		// Read property header
		propLen := header.readU32BE(offset)
		nameOff := header.readU32BE(offset + 4)
		offset += 8

		// Store property in fixed array
		prop := &node.properties[node.propCount]
		prop.nameOff = nameOff
		prop.valueLen = propLen

		// Copy property value (truncate if too large)
		baseAddr := header.GetBaseAddr()
		valueAddr := baseAddr + uintptr(offset)
		copyLen := propLen
		if copyLen > uint32(len(prop.value)) {
			copyLen = uint32(len(prop.value))
		}
		for i := uint32(0); i < copyLen; i++ {
			prop.value[i] = *(*byte)(unsafe.Pointer(valueAddr + uintptr(i)))
		}

		node.propCount++

		// Move to next property
		offset = align4(offset + propLen)
	}

	return offset
}

// findProperty finds a property by name
//
//go:nosplit
func (n *DTBNode) findProperty(name string) *DTBProperty {
	for i := 0; i < n.propCount; i++ {
		prop := &n.properties[i]
		if n.header.StringEquals(n.stringsOff+prop.nameOff, name) {
			return prop
		}
	}
	return nil
}

// HasCompatible checks if the node has a matching compatible string
//
//go:nosplit
func (n *DTBNode) HasCompatible(match string) bool {
	compat := n.findProperty("compatible")
	if compat == nil {
		return false
	}

	// Compatible property is a list of null-terminated strings
	i := uint32(0)
	for i < compat.valueLen {
		// Find next null terminator
		j := i
		for j < compat.valueLen && compat.value[j] != 0 {
			j++
		}

		// Compare this string
		matchLen := uint32(len(match))
		if j-i == matchLen {
			matches := true
			for k := uint32(0); k < matchLen; k++ {
				if compat.value[i+k] != match[k] {
					matches = false
					break
				}
			}
			if matches {
				return true
			}
		}

		// Move to next string
		i = j + 1
	}

	return false
}

// GetReg returns the base address and size from the reg property
// Returns (addr, size, ok)
//
//go:nosplit
func (n *DTBNode) GetReg() (uintptr, uint64, bool) {
	reg := n.findProperty("reg")
	if reg == nil || reg.valueLen < 16 {
		return 0, 0, false
	}

	// Parse reg: <addr-hi(4) addr-lo(4) size-hi(4) size-lo(4)>
	// Convert from big-endian
	addrHi := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[0])))
	addrLo := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[4])))
	sizeHi := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[8])))
	sizeLo := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[12])))

	addr := (uint64(addrHi) << 32) | uint64(addrLo)
	size := (uint64(sizeHi) << 32) | uint64(sizeLo)

	return uintptr(addr), size, true
}

// GetRegMulti returns multiple reg entries (for devices like GIC with GICD and GICC)
// Fills provided result array, returns number of entries
//
//go:nosplit
func (n *DTBNode) GetRegMulti(result *[4][2]uint64) int {
	reg := n.findProperty("reg")
	if reg == nil {
		return 0
	}

	// Each entry is 16 bytes (2x 64-bit values in big-endian)
	numEntries := int(reg.valueLen) / 16
	if numEntries == 0 {
		return 0
	}
	if numEntries > len(result) {
		numEntries = len(result)
	}

	for i := 0; i < numEntries; i++ {
		offset := i * 16
		addrHi := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[offset+0])))
		addrLo := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[offset+4])))
		sizeHi := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[offset+8])))
		sizeLo := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[offset+12])))

		addr := (uint64(addrHi) << 32) | uint64(addrLo)
		size := (uint64(sizeHi) << 32) | uint64(sizeLo)

		result[i] = [2]uint64{addr, size}
	}

	return numEntries
}

// GetInterrupt returns the IRQ number from the interrupts property
// Returns (irq, ok)
// Format: <type(4) irq(4) flags(4)>
//
//go:nosplit
func (n *DTBNode) GetInterrupt() (int, bool) {
	intr := n.findProperty("interrupts")
	if intr == nil || intr.valueLen < 12 {
		return 0, false
	}

	// Parse: <type irq-num flags>
	// type := swapU32(*(*uint32)(unsafe.Pointer(&intr.value[0])))
	irqNum := swapU32(*(*uint32)(unsafe.Pointer(&intr.value[4])))
	// flags := swapU32(*(*uint32)(unsafe.Pointer(&intr.value[8])))

	// For GIC SPI (type 0), add 32 to get interrupt ID
	return int(irqNum) + 32, true
}

// GetNameBuf copies the node name into provided buffer
// Returns the length
//
//go:nosplit
func (n *DTBNode) GetNameBuf(buf []byte) int {
	copyLen := n.nameLen
	if copyLen > len(buf) {
		copyLen = len(buf)
	}
	for i := 0; i < copyLen; i++ {
		buf[i] = n.name[i]
	}
	return copyLen
}
