package dtb

import (
	"fmt"
	"unsafe"
)

// Node represents a device tree node for the new device system
type Node struct {
	Name       string
	Compatible []string
	Reg        []Reg
	Interrupts []uint32
}

// Reg represents a register region (address and size)
type Reg struct {
	Address uintptr
	Size    uint64
}

// Tree represents a parsed device tree
type Tree struct {
	dtbAddr uintptr
	nodes   []*Node
}

// Parse parses a Device Tree Blob and returns a Tree
func Parse(dtbAddr uintptr) (*Tree, error) {
	header := NewDTBHeader(dtbAddr)

	// Validate DTB magic
	if !header.Validate() {
		return nil, fmt.Errorf("invalid DTB magic at 0x%X", dtbAddr)
	}

	totalSize := header.GetTotalSize()

	tree := &Tree{
		dtbAddr: dtbAddr,
		nodes:   make([]*Node, 0, 32),
	}

	fmt.Printf("[DTB] Parsing at 0x%X, size %d bytes\n", dtbAddr, totalSize)

	// Walk the DTB and collect nodes
	Walk(dtbAddr, func(dtbNode *DTBNode) {
		node := convertNode(dtbNode)
		tree.nodes = append(tree.nodes, node)
	})

	fmt.Printf("[DTB] Found %d nodes\n", len(tree.nodes))

	return tree, nil
}

// Walk calls the function for each node in the tree
func (t *Tree) Walk(fn func(*Node) error) error {
	for _, node := range t.nodes {
		if err := fn(node); err != nil {
			return err
		}
	}
	return nil
}

// convertNode converts a DTBNode to a Node
func convertNode(dtbNode *DTBNode) *Node {
	node := &Node{
		Compatible: make([]string, 0, 4),
		Reg:        make([]Reg, 0, 4),
		Interrupts: make([]uint32, 0, 2),
	}

	// Copy name
	var nameBuf [64]byte
	nameLen := dtbNode.GetNameBuf(nameBuf[:])
	node.Name = string(nameBuf[:nameLen])

	// Extract compatible strings
	if compat := dtbNode.findProperty("compatible"); compat != nil {
		i := uint32(0)
		for i < compat.valueLen {
			// Find next null terminator
			j := i
			for j < compat.valueLen && compat.value[j] != 0 {
				j++
			}

			// Add this string
			if j > i {
				compatStr := string(compat.value[i:j])
				node.Compatible = append(node.Compatible, compatStr)
			}

			// Move to next string
			i = j + 1
		}
	}

	// Extract reg entries
	if reg := dtbNode.findProperty("reg"); reg != nil {
		// Each entry is 16 bytes (2x 64-bit values)
		numEntries := int(reg.valueLen) / 16
		for i := 0; i < numEntries; i++ {
			offset := i * 16
			addrHi := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[offset+0])))
			addrLo := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[offset+4])))
			sizeHi := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[offset+8])))
			sizeLo := swapU32(*(*uint32)(unsafe.Pointer(&reg.value[offset+12])))

			addr := (uint64(addrHi) << 32) | uint64(addrLo)
			size := (uint64(sizeHi) << 32) | uint64(sizeLo)

			node.Reg = append(node.Reg, Reg{
				Address: uintptr(addr),
				Size:    size,
			})
		}
	}

	// Extract interrupts
	if intr := dtbNode.findProperty("interrupts"); intr != nil {
		// Format: <type(4) irq-num(4) flags(4)>
		numEntries := int(intr.valueLen) / 12
		for i := 0; i < numEntries; i++ {
			offset := i * 12
			intrType := swapU32(*(*uint32)(unsafe.Pointer(&intr.value[offset+0])))
			irqNum := swapU32(*(*uint32)(unsafe.Pointer(&intr.value[offset+4])))

			// For GIC SPI (type 0), add 32 to get interrupt ID
			if intrType == 0 {
				node.Interrupts = append(node.Interrupts, irqNum+32)
			}
		}
	}

	return node
}
