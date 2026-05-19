// engine.go — VirtIO I/O Engine: manages submit/complete on a single PCI virtqueue.
//
// Engine wraps a VirtQueue and provides a descriptor-chain-oriented API.
// Each VirtIO device that wants structured I/O creates one Engine per queue.
// The Engine owns the virtqueue structures, handles descriptor allocation,
// and provides nosplit-safe methods for use in boot and IRQ contexts.
//
// Designed for composition: the caller builds a DescChain, calls Submit,
// then Notify. Completion is via HasUsed + PopUsed (polling) or IOComplete
// atomic flag (interrupt-driven). The Engine does NOT own the I/O wait loop
// — that stays in device-specific code (e.g., WFI in ksyscall/blockio.go).

package virtio

import (
	"mazzy/kmazarin/asm"
	"unsafe"
)

// IOTag identifies an in-flight I/O request by its head descriptor index.
type IOTag uint16

// InvalidIOTag indicates no valid I/O tag (e.g., submit failure or empty used ring).
const InvalidIOTag IOTag = 0xFFFF

// MaxDescChainLen is the maximum descriptors in a single chain.
const MaxDescChainLen = 8

// DescSpec describes one buffer in a descriptor chain.
type DescSpec struct {
	PA    uint64 // Physical address of buffer
	Len   uint32 // Buffer length in bytes
	Flags uint16 // VIRTQ_DESC_F_WRITE for device-writable; NEXT is managed by Engine
}

// DescChain is a fixed-capacity descriptor chain specification.
// Fill Descs[0..Count-1] before passing to Engine.Submit.
type DescChain struct {
	Descs [MaxDescChainLen]DescSpec
	Count int
}

// CompletionInfo holds the result of a completed I/O request.
type CompletionInfo struct {
	Tag     IOTag  // Head descriptor index of the completed chain
	UsedLen uint32 // Bytes written by device (from used ring element)
}

// Engine manages submit/complete on a single VirtIO PCI virtqueue.
type Engine struct {
	VQ         VirtQueue  // Virtqueue structures (on DMA page)
	Dev        *PCIDevice // Back-pointer to PCI transport
	QueueIndex uint16     // Queue index on the device
	NotifyOff  uint16     // Per-queue notify offset
	IOComplete uint32     // Atomic: set to 1 by IRQ top-half
}

// Init initializes the Engine: sets up the virtqueue on a DMA page, writes
// queue addresses to the device, and enables the queue.
//
// dev: PCI device (must have completed Handshake + SetMSIXConfig)
// qIdx: queue index (0 for most devices)
// qSize: number of queue entries (power of 2)
// queuePagePA/VA: DMA page for queue structures
// pageOffset: starting offset within the page
// msixVec: MSI-X vector (MSIXNoVector for INTx platforms)
func (e *Engine) Init(dev *PCIDevice, qIdx, qSize uint16,
	queuePagePA, queuePageVA uintptr, pageOffset uintptr, msixVec uint16) bool {

	e.Dev = dev
	e.QueueIndex = qIdx

	notifyOff, ok := dev.SetupQueue(qIdx, &e.VQ, qSize, queuePagePA, queuePageVA, pageOffset, msixVec)
	if !ok {
		return false
	}
	e.NotifyOff = notifyOff
	return true
}

// Submit builds a descriptor chain from the DescChain specification, adds it
// to the available ring, and returns the IOTag (head descriptor index).
// Does NOT notify the device — call Notify() separately.
// Returns InvalidIOTag if not enough free descriptors.
//
//go:nosplit
func (e *Engine) Submit(chain *DescChain) IOTag {
	vq := &e.VQ
	if chain.Count == 0 || int(vq.NumFree) < chain.Count {
		return InvalidIOTag
	}

	descStride := unsafe.Sizeof(VirtQDesc{})

	// Allocate all descriptors from free list
	var indices [MaxDescChainLen]uint16
	for i := 0; i < chain.Count; i++ {
		idx := vq.FreeHead
		desc := (*VirtQDesc)(unsafe.Pointer(uintptr(vq.DescTable) + uintptr(idx)*descStride))
		vq.FreeHead = desc.Next
		vq.NumFree--
		indices[i] = idx
	}

	// Fill descriptors and link with NEXT flags
	for i := 0; i < chain.Count; i++ {
		spec := &chain.Descs[i]
		desc := (*VirtQDesc)(unsafe.Pointer(uintptr(vq.DescTable) + uintptr(indices[i])*descStride))
		desc.Addr = spec.PA
		desc.Len = spec.Len
		desc.Flags = spec.Flags
		if i < chain.Count-1 {
			desc.Flags |= VIRTQ_DESC_F_NEXT
			desc.Next = indices[i+1]
		} else {
			desc.Next = 0xFFFF
		}
		// Clean each descriptor's cache line so the device sees the PA/len/flags
		// we just wrote. Without this, on non-cache-coherent DMA the device may
		// read a stale descriptor containing a PA that was freed and reused as
		// Go heap, causing the " failed " heap corruption pattern (Bug-B/MAZ-5).
		asm.CleanDCacheRange(uintptr(unsafe.Pointer(desc)), descStride)
	}

	// Barrier: ensure descriptor clean and writes are visible before avail ring update
	asm.Dsb()

	// Add head descriptor to available ring
	VirtqueueAddToAvailable(vq, indices[0])

	return IOTag(indices[0])
}

// Notify sends a virtqueue notification to the device.
//
//go:nosplit
func (e *Engine) Notify() {
	e.Dev.Notify(e.NotifyOff, e.QueueIndex)
}

// HasUsed returns true if the device has posted completion(s) to the used ring.
//
//go:nosplit
func (e *Engine) HasUsed() bool {
	return VirtqueueHasUsed(&e.VQ)
}

// PopUsed pops one entry from the used ring and frees the descriptor chain.
// Returns {InvalidIOTag, 0} if the used ring is empty.
//
//go:nosplit
func (e *Engine) PopUsed() CompletionInfo {
	descIdx, usedLen := VirtqueueGetUsed(&e.VQ)
	if uint16(descIdx) == 0xFFFF {
		return CompletionInfo{Tag: InvalidIOTag}
	}
	VirtqueueFreeDescChain(&e.VQ, uint16(descIdx))
	return CompletionInfo{
		Tag:     IOTag(descIdx),
		UsedLen: usedLen,
	}
}

// PopUsedNoFree pops one entry from the used ring WITHOUT freeing the
// descriptor chain. The slot stays "owned" by the caller — used by the
// slot-pinned net RX path where net.elf re-arms the same descIdx with a
// fresh page PA via IOUringOpNetRearmDesc. The freelist is bypassed
// entirely; the slot lives across the round trip.
//
// Returns {InvalidIOTag, 0} if the used ring is empty.
//
//go:nosplit
func (e *Engine) PopUsedNoFree() CompletionInfo {
	descIdx, usedLen := VirtqueueGetUsed(&e.VQ)
	if uint16(descIdx) == 0xFFFF {
		return CompletionInfo{Tag: InvalidIOTag}
	}
	return CompletionInfo{
		Tag:     IOTag(descIdx),
		UsedLen: usedLen,
	}
}

// InFlight returns the number of in-flight descriptor slots.
//
//go:nosplit
func (e *Engine) InFlight() int {
	return int(e.VQ.QueueSize - e.VQ.NumFree)
}
