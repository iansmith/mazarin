// sidecar.go — SidecarPool: bitmap-based allocator for Device-nGnRnE DMA slots.
//
// A SidecarPool manages a single 4KB Device-nGnRnE page divided into fixed-size
// slots. Each slot holds protocol headers and status bytes for VirtIO I/O requests
// (e.g., VirtIOBlockReq + status for block, VirtIONetHdr for net).
//
// Device-nGnRnE memory requires no cache management — the device sees writes
// immediately and the CPU sees device writes immediately. This avoids
// CleanDCacheRange/InvalidateDCacheRange for protocol headers.
//
// Allocation/release are nosplit-safe for use in submit/complete paths.

package virtio

import "mazzy/kmazarin/kmem"

// SidecarSlot identifies one allocated slot in a SidecarPool.
type SidecarSlot struct {
	PA    uintptr // Physical address of this slot
	VA    uintptr // Virtual address of this slot
	Index uint8   // Slot index (for Release)
}

// SidecarPool manages a page of fixed-size Device-nGnRnE DMA slots.
type SidecarPool struct {
	PagePA   uintptr // Physical address of the backing page
	PageVA   uintptr // Virtual address of the backing page
	SlotSize uint32  // Bytes per slot
	NumSlots uint32  // Total number of slots (max 64)
	FreeBits uint64  // Bitmap: bit i = 1 means slot i is free
}

// Init allocates a Device-nGnRnE page and divides it into fixed-size slots.
// slotSize should be chosen to fit the protocol header + status for the device.
// Returns true on success.
func (p *SidecarPool) Init(slotSize uint32) bool {
	if slotSize == 0 || slotSize > 4096 {
		return false
	}

	pa, va := kmem.AllocDriverPage()
	if pa == 0 {
		return false
	}

	p.PagePA = pa
	p.PageVA = va
	p.SlotSize = slotSize
	p.NumSlots = 4096 / slotSize
	if p.NumSlots > 64 {
		p.NumSlots = 64
	}

	// Mark all slots as free
	if p.NumSlots == 64 {
		p.FreeBits = ^uint64(0)
	} else {
		p.FreeBits = (uint64(1) << p.NumSlots) - 1
	}

	return true
}

// Alloc returns a free sidecar slot. Returns (zero, false) if exhausted.
//
//go:nosplit
func (p *SidecarPool) Alloc() (SidecarSlot, bool) {
	bits := p.FreeBits
	if bits == 0 {
		return SidecarSlot{}, false
	}

	// Find lowest set bit (linear scan, max 64 iterations)
	var idx uint8
	for idx = 0; idx < uint8(p.NumSlots); idx++ {
		if bits&(uint64(1)<<uint(idx)) != 0 {
			break
		}
	}

	// Clear the bit (mark as allocated)
	p.FreeBits &^= uint64(1) << uint(idx)

	off := uintptr(idx) * uintptr(p.SlotSize)
	return SidecarSlot{
		PA:    p.PagePA + off,
		VA:    p.PageVA + off,
		Index: idx,
	}, true
}

// Release returns a sidecar slot to the pool.
//
//go:nosplit
func (p *SidecarPool) Release(slot SidecarSlot) {
	p.FreeBits |= uint64(1) << uint(slot.Index)
}
