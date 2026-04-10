package flat

import "fmt"

// bitmapAlloc scans a bitmap for the first free bit, sets it, and returns
// the index. Returns an error if all slots are allocated.
func bitmapAlloc(bitmap []byte, capacity int) (int, error) {
	for i := 0; i < capacity; i++ {
		byteIdx := i / 8
		bitIdx := uint(i % 8)
		if bitmap[byteIdx]&(1<<bitIdx) == 0 {
			bitmap[byteIdx] |= 1 << bitIdx
			return i, nil
		}
	}
	return -1, fmt.Errorf("flat: bitmap full")
}

// bitmapFree clears the bit at the given index.
func bitmapFree(bitmap []byte, idx int) {
	byteIdx := idx / 8
	bitIdx := uint(idx % 8)
	bitmap[byteIdx] &^= 1 << bitIdx
}

// bitmapIsSet returns true if the bit at idx is set.
func bitmapIsSet(bitmap []byte, idx int) bool {
	byteIdx := idx / 8
	bitIdx := uint(idx % 8)
	return bitmap[byteIdx]&(1<<bitIdx) != 0
}

// AllocNode allocates a node slot and returns its index.
func (pr *PageRegion) AllocNode() (int16, error) {
	idx, err := bitmapAlloc(pr.nodeBitmap, pr.NodeCapacity)
	if err != nil {
		return -1, fmt.Errorf("flat: node slots full")
	}
	// Zero the node.
	off := idx * AttrNodeSize
	for i := off; i < off+AttrNodeSize; i++ {
		pr.Nodes[i] = 0
	}
	return int16(idx), nil
}

// FreeNode frees a node slot.
func (pr *PageRegion) FreeNode(idx int16) {
	bitmapFree(pr.nodeBitmap, int(idx))
}

// IsNodeAllocated returns true if the given node slot is allocated.
func (pr *PageRegion) IsNodeAllocated(idx int16) bool {
	return bitmapIsSet(pr.nodeBitmap, int(idx))
}

// FreeString frees a string slot given its region offset.
func (pr *PageRegion) FreeString(ref StrRef) {
	slotIdx := int(ref.RegionOffset) / StringSlotSize
	bitmapFree(pr.stringBitmap, slotIdx)
}
