//go:build qemuvirt && aarch64

package virtio

import (
	"kmazarin/asm"
	"kmazarin/kmem"
	"unsafe"
)

// VirtQueue data structures and operations
// Based on VirtIO 1.2 specification

// VirtQueue descriptor flags
const (
	VIRTQ_DESC_F_NEXT     = 1 << 0 // Next descriptor in chain
	VIRTQ_DESC_F_WRITE    = 1 << 1 // Write-only descriptor (device writes)
	VIRTQ_DESC_F_INDIRECT = 1 << 2 // Descriptor refers to indirect table
)

// VirtQueue available ring flags
const (
	VIRTQ_AVAIL_F_NO_INTERRUPT = 1 << 0 // Don't notify device
)

// VirtQueue used ring flags
const (
	VIRTQ_USED_F_NO_NOTIFY = 1 << 0 // Don't notify guest
)

// VirtQDesc is a VirtQueue descriptor
// Each descriptor describes a buffer in guest memory
type VirtQDesc struct {
	Addr  uint64 // Physical address of buffer
	Len   uint32 // Length of buffer
	Flags uint16 // Flags (VIRTQ_DESC_F_*)
	Next  uint16 // Next descriptor index (if VIRTQ_DESC_F_NEXT is set)
}

// VirtQUsedElem is an element in the used ring
// Returned by the device after processing a descriptor chain
type VirtQUsedElem struct {
	ID  uint32 // Descriptor index (from available ring)
	Len uint32 // Length of data written by device
}

// VirtQAvailable is the available ring structure
// Guest writes here to notify device of new descriptors
type VirtQAvailable struct {
	Flags uint16    // Flags (VIRTQ_AVAIL_F_*)
	Idx   uint16    // Available ring index (guest increments)
	Ring  [0]uint16 // Array of descriptor indices (variable size)
	// Note: In Go, we'll use unsafe pointer arithmetic to access Ring[]
	// The actual size is queue_size
}

// VirtQUsed is the used ring structure
// Device writes here after processing descriptors
type VirtQUsed struct {
	Flags uint16           // Flags (VIRTQ_USED_F_*)
	Idx   uint16           // Used ring index (device increments)
	Ring  [0]VirtQUsedElem // Array of used elements (variable size)
	// Note: In Go, we'll use unsafe pointer arithmetic to access Ring[]
	// The actual size is queue_size
}

// VirtQueue represents a VirtIO virtqueue
type VirtQueue struct {
	QueueSize      uint16          // Size of the queue (power of 2, typically 256 or 512)
	DescTable      unsafe.Pointer  // Descriptor table (array of descriptors) - use unsafe pointer for array access
	Available      *VirtQAvailable // Available ring
	Used           *VirtQUsed      // Used ring
	FreeHead       uint16          // Index of first free descriptor
	LastUsedIdx    uint16          // Last used index we've processed
	NumFree        uint16          // Number of free descriptors
	DescAlloc      unsafe.Pointer  // Allocated memory for descriptor table
	AvailableAlloc unsafe.Pointer  // Allocated memory for available ring
	UsedAlloc      unsafe.Pointer  // Allocated memory for used ring
}

// virtqueueSize calculates the total size needed for a virtqueue
// Returns size in bytes
//
//go:nosplit
func VirtqueueSize(queueSize uint16) uintptr {
	// Descriptor table: queue_size * sizeof(VirtQDesc)
	descSize := uintptr(queueSize) * unsafe.Sizeof(VirtQDesc{})

	// Available ring: sizeof(VirtQAvailable) + queue_size * sizeof(uint16) + sizeof(uint16) (used_event)
	// Structure: flags (2) + idx (2) + ring[queue_size] (2*queue_size) + used_event (2)
	availSize := 2 + 2 + uintptr(queueSize)*2 + 2

	// Used ring: sizeof(VirtQUsed) + queue_size * sizeof(VirtQUsedElem) + sizeof(uint16) (avail_event)
	// Structure: flags (2) + idx (2) + ring[queue_size] (sizeof(VirtQUsedElem)*queue_size) + avail_event (2)
	usedSize := 2 + 2 + uintptr(queueSize)*unsafe.Sizeof(VirtQUsedElem{}) + 2

	// Total size (with alignment)
	// Descriptors must be 16-byte aligned
	// Available ring must be 2-byte aligned
	// Used ring must be 4-byte aligned
	totalSize := descSize + availSize + usedSize

	// Align to 4096 bytes (page boundary) for better performance
	pageSize := uintptr(4096)
	if totalSize%pageSize != 0 {
		totalSize = ((totalSize / pageSize) + 1) * pageSize
	}

	return totalSize
}

// virtqueueInit initializes a virtqueue
// Allocates memory and sets up descriptor table, available ring, and used ring
// Returns true on success, false on failure
//
//go:nosplit
func VirtqueueInit(vq *VirtQueue, queueSize uint16) bool {
	if queueSize == 0 || (queueSize&(queueSize-1)) != 0 {
		return false
	}

	vq.QueueSize = queueSize

	// Calculate sizes
	descStride := unsafe.Sizeof(VirtQDesc{})                                  // Size of one descriptor
	descTableSize := uintptr(queueSize) * descStride                          // Total descriptor table size
	availSize := 2 + 2 + uintptr(queueSize)*2 + 2                             // flags + idx + ring[] + used_event
	usedSize := 2 + 2 + uintptr(queueSize)*unsafe.Sizeof(VirtQUsedElem{}) + 2 // flags + idx + ring[] + avail_event

	// Allocate descriptor table (must be 16-byte aligned)
	descAlloc := kmalloc(uint32(descTableSize + 16))
	if descAlloc == nil {
		return false
	}

	// Align to 16 bytes
	descAddr := PointerToUintptr(descAlloc)
	if descAddr%16 != 0 {
		descAddr = ((descAddr / 16) + 1) * 16
	}
	vq.DescTable = unsafe.Pointer(descAddr)
	vq.DescAlloc = descAlloc

	// Zero out descriptor table
	Bzero4K(vq.DescTable, uint32(descTableSize))

	// Allocate available ring (must be 2-byte aligned)
	availAlloc := kmalloc(uint32(availSize + 2))
	if availAlloc == nil {
		kfree(descAlloc)
		return false
	}

	// Align to 2 bytes
	availAddr := PointerToUintptr(availAlloc)
	if availAddr%2 != 0 {
		availAddr = ((availAddr / 2) + 1) * 2
	}
	vq.Available = CastToPointer[VirtQAvailable](availAddr)
	vq.AvailableAlloc = availAlloc

	// Zero out available ring
	Bzero4K(unsafe.Pointer(vq.Available), uint32(availSize))

	// Allocate used ring (must be 4-byte aligned)
	usedAlloc := kmalloc(uint32(usedSize + 4))
	if usedAlloc == nil {
		kfree(descAlloc)
		kfree(availAlloc)
		return false
	}

	// Align to 4 bytes
	usedAddr := PointerToUintptr(usedAlloc)
	if usedAddr%4 != 0 {
		usedAddr = ((usedAddr / 4) + 1) * 4
	}
	vq.Used = CastToPointer[VirtQUsed](usedAddr)
	vq.UsedAlloc = usedAlloc

	// Zero out used ring
	Bzero4K(unsafe.Pointer(vq.Used), uint32(usedSize))

	// Initialize free descriptor list
	// All descriptors are initially free, linked in a chain
	vq.FreeHead = 0
	vq.NumFree = queueSize
	for i := uint16(0); i < queueSize-1; i++ {
		descPtr := CastToPointer[VirtQDesc](PointerToUintptr(vq.DescTable) + uintptr(i)*descStride)
		descPtr.Next = i + 1
	}
	lastDescPtr := CastToPointer[VirtQDesc](PointerToUintptr(vq.DescTable) + uintptr(queueSize-1)*descStride)
	lastDescPtr.Next = 0xFFFF // End of chain marker

	// Initialize ring indices
	vq.Available.Idx = 0
	vq.Used.Idx = 0
	vq.LastUsedIdx = 0

	return true
}

// virtqueueCleanup frees memory allocated for a virtqueue
//
//go:nosplit
func VirtqueueCleanup(vq *VirtQueue) {
	if vq.DescAlloc != nil {
		kfree(vq.DescAlloc)
		vq.DescAlloc = nil
	}
	if vq.AvailableAlloc != nil {
		kfree(vq.AvailableAlloc)
		vq.AvailableAlloc = nil
	}
	if vq.UsedAlloc != nil {
		kfree(vq.UsedAlloc)
		vq.UsedAlloc = nil
	}
	vq.DescTable = nil
	vq.Available = nil
	vq.Used = nil
	vq.DescAlloc = nil
	vq.AvailableAlloc = nil
	vq.UsedAlloc = nil
}

// VirtqueueGetPhysicalAddr returns the physical address of a virtqueue structure
// Uses page table walk to get the actual physical address mapping.
//
//go:nosplit
func VirtqueueGetPhysicalAddr(ptr unsafe.Pointer) uint64 {
	vaddr := PointerToUintptr(ptr)

	// Walk page tables to get actual physical address
	// This is required because Go heap memory is demand-paged and mapped
	// to physical frames that may not be at the "obvious" physical address.
	paddr := kmem.WalkPageTable(vaddr)

	return uint64(paddr)
}

// virtqueueAddDesc adds a descriptor to the queue
// Returns descriptor index, or 0xFFFF on failure
//
//go:nosplit
func VirtqueueAddDesc(vq *VirtQueue, addr uint64, len uint32, flags uint16, next uint16) uint16 {
	if vq.NumFree == 0 {
		return 0xFFFF // No free descriptors
	}

	// Get free descriptor
	descIdx := vq.FreeHead
	var descSize uintptr = unsafe.Sizeof(VirtQDesc{})
	descPtr := CastToPointer[VirtQDesc](PointerToUintptr(vq.DescTable) + uintptr(descIdx)*descSize)
	desc := descPtr

	// Update free list
	vq.FreeHead = desc.Next
	vq.NumFree--

	// Fill in descriptor
	desc.Addr = addr
	desc.Len = len
	desc.Flags = flags
	desc.Next = next

	return descIdx
}

// virtqueueGetRingElement returns a pointer to an element in the available ring
//
//go:nosplit
func VirtqueueGetRingElement(vq *VirtQueue, index uint16) *uint16 {
	// Ring starts after flags (2 bytes) and idx (2 bytes)
	ringBase := PointerToUintptr(unsafe.Pointer(vq.Available)) + 4
	ringPtr := CastToPointer[uint16](ringBase + uintptr(index)*2)
	return ringPtr
}

// virtqueueGetUsedElement returns a pointer to an element in the used ring
//
//go:nosplit
func VirtqueueGetUsedElement(vq *VirtQueue, index uint16) *VirtQUsedElem {
	// Ring starts after flags (2 bytes) and idx (2 bytes)
	ringBase := PointerToUintptr(unsafe.Pointer(vq.Used)) + 4
	ringPtr := CastToPointer[VirtQUsedElem](ringBase + uintptr(index)*unsafe.Sizeof(VirtQUsedElem{}))
	return ringPtr
}

// VirtqueueAddToAvailable adds a descriptor chain to the available ring
// Returns true on success
//
//go:nosplit
func VirtqueueAddToAvailable(vq *VirtQueue, descIdx uint16) bool {
	// Get current available index
	availIdx := vq.Available.Idx

	// Add descriptor index to available ring
	ringElem := VirtqueueGetRingElement(vq, availIdx%vq.QueueSize)
	*ringElem = descIdx

	// Memory barrier to ensure descriptor is written before index
	asm.Dsb()

	// Calculate the address of the Idx field (offset 2 in struct: flags=2 bytes, then idx)
	availVirtAddr := PointerToUintptr(unsafe.Pointer(vq.Available))
	idxAddr := availVirtAddr + 2
	newIdx := availIdx + 1

	// Use volatile MMIO write to ensure the write is visible to hardware
	asm.MmioWrite16(idxAddr, newIdx)

	// Also update Go's view of the struct for consistency
	vq.Available.Idx = newIdx

	return true
}

// virtqueueHasUsed checks if there are used descriptors to process
//
//go:nosplit
func VirtqueueHasUsed(vq *VirtQueue) bool {
	// Memory barrier to ensure we read the latest used index
	asm.Dsb()

	// Check if device has written new used entries
	return vq.Used.Idx != vq.LastUsedIdx
}

// virtqueueGetUsed retrieves the next used descriptor
// Returns descriptor index and length, or 0xFFFF if none available
//
//go:nosplit
func VirtqueueGetUsed(vq *VirtQueue) (descIdx uint32, len uint32) {
	if !VirtqueueHasUsed(vq) {
		return 0xFFFF, 0
	}

	// Get used element
	usedIdx := vq.LastUsedIdx % vq.QueueSize
	usedElem := VirtqueueGetUsedElement(vq, usedIdx)

	descIdx = usedElem.ID
	len = usedElem.Len

	// Update last used index
	vq.LastUsedIdx++

	// Free the descriptor chain
	// For now, we'll just mark descriptors as free (simplified)
	// In a full implementation, we'd traverse the chain and free each descriptor

	return descIdx, len
}

// virtqueueFreeDescChain frees a descriptor chain
// Traverses the chain starting at descIdx and frees all descriptors
//
//go:nosplit
func VirtqueueFreeDescChain(vq *VirtQueue, descIdx uint16) {
	current := descIdx
	var descSize uintptr = unsafe.Sizeof(VirtQDesc{})
	for {
		descPtr := CastToPointer[VirtQDesc](PointerToUintptr(vq.DescTable) + uintptr(current)*descSize)
		desc := descPtr

		// Save the original next pointer BEFORE we overwrite it
		hasNext := (desc.Flags & VIRTQ_DESC_F_NEXT) != 0
		nextIdx := desc.Next

		// Add to free list (this overwrites desc.Next)
		desc.Next = vq.FreeHead
		vq.FreeHead = current
		vq.NumFree++

		// Check if this is the last descriptor in the chain
		if !hasNext {
			break
		}

		current = nextIdx
		if current == 0xFFFF {
			break
		}
	}
}

// virtqueueNotify notifies the device that new descriptors are available
// This is done by writing to the notify register (device-specific)
// queueIndex is the index of the queue to notify (e.g., 0 for controlq, 1 for cursorq)
//
//go:nosplit
func VirtqueueNotify(vq *VirtQueue, notifyOffset uintptr, queueIndex uint16) {
	// Write queue index to notify register
	// Format: queue_index (16-bit)
	asm.MmioWrite16(notifyOffset, queueIndex) // Notify device about this queue
	asm.Dsb()                                  // Memory barrier

	// Read back to verify write completed
	readback := asm.MmioRead16(notifyOffset)
	_ = readback // Suppress unused variable warning
}

// virtqueueReset resets a virtqueue to initial state
//
//go:nosplit
func VirtqueueReset(vq *VirtQueue) {
	// Zero out all structures
	if vq.DescTable != nil {
		descSize := uintptr(vq.QueueSize) * unsafe.Sizeof(VirtQDesc{})
		Bzero4K(vq.DescTable, uint32(descSize))
	}
	if vq.Available != nil {
		availSize := 2 + 2 + uintptr(vq.QueueSize)*2 + 2
		Bzero4K(unsafe.Pointer(vq.Available), uint32(availSize))
	}
	if vq.Used != nil {
		usedSize := 2 + 2 + uintptr(vq.QueueSize)*unsafe.Sizeof(VirtQUsedElem{}) + 2
		Bzero4K(unsafe.Pointer(vq.Used), uint32(usedSize))
	}

	// Reinitialize free descriptor list
	vq.FreeHead = 0
	vq.NumFree = vq.QueueSize
	var descSize uintptr = unsafe.Sizeof(VirtQDesc{})
	for i := uint16(0); i < vq.QueueSize-1; i++ {
		descPtr := CastToPointer[VirtQDesc](PointerToUintptr(vq.DescTable) + uintptr(i)*descSize)
		descPtr.Next = i + 1
	}
	lastDescPtr := CastToPointer[VirtQDesc](PointerToUintptr(vq.DescTable) + uintptr(vq.QueueSize-1)*descSize)
	lastDescPtr.Next = 0xFFFF

	// Reset indices
	vq.Available.Idx = 0
	vq.Used.Idx = 0
	vq.LastUsedIdx = 0
}
