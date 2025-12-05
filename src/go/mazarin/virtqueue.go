//go:build qemu

package main

import (
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
func virtqueueSize(queueSize uint16) uintptr {
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
func virtqueueInit(vq *VirtQueue, queueSize uint16) bool {
	if queueSize == 0 || (queueSize&(queueSize-1)) != 0 {
		// Queue size must be a power of 2
		uartPuts("VirtQueue: ERROR - queue size must be power of 2\r\n")
		return false
	}

	vq.QueueSize = queueSize

	// Calculate sizes
	descSize := uintptr(queueSize) * unsafe.Sizeof(VirtQDesc{})
	availSize := 2 + 2 + uintptr(queueSize)*2 + 2                             // flags + idx + ring[] + used_event
	usedSize := 2 + 2 + uintptr(queueSize)*unsafe.Sizeof(VirtQUsedElem{}) + 2 // flags + idx + ring[] + avail_event

	// Allocate descriptor table (must be 16-byte aligned)
	descAlloc := kmalloc(uint32(descSize + 16)) // Allocate extra for alignment
	if descAlloc == nil {
		uartPuts("VirtQueue: ERROR - failed to allocate descriptor table\r\n")
		return false
	}

	// Align to 16 bytes
	descAddr := pointerToUintptr(descAlloc)
	if descAddr%16 != 0 {
		descAddr = ((descAddr / 16) + 1) * 16
	}
	vq.DescTable = unsafe.Pointer(descAddr)
	vq.DescAlloc = descAlloc

	// Zero out descriptor table
	bzero(vq.DescTable, uint32(descSize))

	// Allocate available ring (must be 2-byte aligned)
	availAlloc := kmalloc(uint32(availSize + 2)) // Allocate extra for alignment
	if availAlloc == nil {
		uartPuts("VirtQueue: ERROR - failed to allocate available ring\r\n")
		kfree(descAlloc)
		return false
	}

	// Align to 2 bytes
	availAddr := pointerToUintptr(availAlloc)
	if availAddr%2 != 0 {
		availAddr = ((availAddr / 2) + 1) * 2
	}
	vq.Available = castToPointer[VirtQAvailable](availAddr)
	vq.AvailableAlloc = availAlloc

	// Zero out available ring
	bzero(unsafe.Pointer(vq.Available), uint32(availSize))

	// Allocate used ring (must be 4-byte aligned)
	usedAlloc := kmalloc(uint32(usedSize + 4)) // Allocate extra for alignment
	if usedAlloc == nil {
		uartPuts("VirtQueue: ERROR - failed to allocate used ring\r\n")
		kfree(descAlloc)
		kfree(availAlloc)
		return false
	}

	// Align to 4 bytes
	usedAddr := pointerToUintptr(usedAlloc)
	if usedAddr%4 != 0 {
		usedAddr = ((usedAddr / 4) + 1) * 4
	}
	vq.Used = castToPointer[VirtQUsed](usedAddr)
	vq.UsedAlloc = usedAlloc

	// Zero out used ring
	bzero(unsafe.Pointer(vq.Used), uint32(usedSize))

	// Initialize free descriptor list
	// All descriptors are initially free, linked in a chain
	vq.FreeHead = 0
	vq.NumFree = queueSize
	for i := uint16(0); i < queueSize-1; i++ {
		descPtr := castToPointer[VirtQDesc](pointerToUintptr(vq.DescTable) + uintptr(i)*descSize)
		descPtr.Next = i + 1
	}
	lastDescPtr := castToPointer[VirtQDesc](pointerToUintptr(vq.DescTable) + uintptr(queueSize-1)*descSize)
	lastDescPtr.Next = 0xFFFF // End of chain marker

	// Initialize ring indices
	vq.Available.Idx = 0
	vq.Used.Idx = 0
	vq.LastUsedIdx = 0

	uartPuts("VirtQueue: Initialized queue size=")
	uartPutUint32(uint32(queueSize))
	uartPuts("\r\n")

	return true
}

// virtqueueCleanup frees memory allocated for a virtqueue
//
//go:nosplit
func virtqueueCleanup(vq *VirtQueue) {
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

// virtqueueGetPhysicalAddr returns the physical address of a virtqueue structure
// Since we're identity-mapped, virtual address = physical address
//
//go:nosplit
func virtqueueGetPhysicalAddr(ptr unsafe.Pointer) uint64 {
	return uint64(pointerToUintptr(ptr))
}

// virtqueueAddDesc adds a descriptor to the queue
// Returns descriptor index, or 0xFFFF on failure
//
//go:nosplit
func virtqueueAddDesc(vq *VirtQueue, addr uint64, len uint32, flags uint16, next uint16) uint16 {
	if vq.NumFree == 0 {
		return 0xFFFF // No free descriptors
	}

	// Get free descriptor
	descIdx := vq.FreeHead
	var descSize uintptr = unsafe.Sizeof(VirtQDesc{})
	descPtr := castToPointer[VirtQDesc](pointerToUintptr(vq.DescTable) + uintptr(descIdx)*descSize)
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
func virtqueueGetRingElement(vq *VirtQueue, index uint16) *uint16 {
	// Ring starts after flags (2 bytes) and idx (2 bytes)
	ringBase := pointerToUintptr(unsafe.Pointer(vq.Available)) + 4
	ringPtr := castToPointer[uint16](ringBase + uintptr(index)*2)
	return ringPtr
}

// virtqueueGetUsedElement returns a pointer to an element in the used ring
//
//go:nosplit
func virtqueueGetUsedElement(vq *VirtQueue, index uint16) *VirtQUsedElem {
	// Ring starts after flags (2 bytes) and idx (2 bytes)
	ringBase := pointerToUintptr(unsafe.Pointer(vq.Used)) + 4
	ringPtr := castToPointer[VirtQUsedElem](ringBase + uintptr(index)*unsafe.Sizeof(VirtQUsedElem{}))
	return ringPtr
}

// virtqueueAddToAvailable adds a descriptor chain to the available ring
// Returns true on success
//
//go:nosplit
func virtqueueAddToAvailable(vq *VirtQueue, descIdx uint16) bool {
	// Get current available index
	availIdx := vq.Available.Idx

	// Add descriptor index to available ring
	ringElem := virtqueueGetRingElement(vq, availIdx%vq.QueueSize)
	*ringElem = descIdx

	// Memory barrier to ensure descriptor is written before index
	dsb()

	// Increment available index
	vq.Available.Idx = availIdx + 1

	return true
}

// virtqueueHasUsed checks if there are used descriptors to process
//
//go:nosplit
func virtqueueHasUsed(vq *VirtQueue) bool {
	// Memory barrier to ensure we read the latest used index
	dsb()

	// Check if device has written new used entries
	return vq.Used.Idx != vq.LastUsedIdx
}

// virtqueueGetUsed retrieves the next used descriptor
// Returns descriptor index and length, or 0xFFFF if none available
//
//go:nosplit
func virtqueueGetUsed(vq *VirtQueue) (descIdx uint32, len uint32) {
	if !virtqueueHasUsed(vq) {
		return 0xFFFF, 0
	}

	// Get used element
	usedIdx := vq.LastUsedIdx % vq.QueueSize
	usedElem := virtqueueGetUsedElement(vq, usedIdx)

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
func virtqueueFreeDescChain(vq *VirtQueue, descIdx uint16) {
	current := descIdx
	var descSize uintptr = unsafe.Sizeof(VirtQDesc{})
	for {
		descPtr := castToPointer[VirtQDesc](pointerToUintptr(vq.DescTable) + uintptr(current)*descSize)
		desc := descPtr

		// Add to free list
		desc.Next = vq.FreeHead
		vq.FreeHead = current
		vq.NumFree++

		// Check if this is the last descriptor in the chain
		if (desc.Flags & VIRTQ_DESC_F_NEXT) == 0 {
			break
		}

		current = desc.Next
		if current == 0xFFFF {
			break
		}
	}
}

// virtqueueNotify notifies the device that new descriptors are available
// This is done by writing to the notify register (device-specific)
// For now, this is a placeholder - actual implementation depends on VirtIO PCI transport
//
//go:nosplit
func virtqueueNotify(vq *VirtQueue, notifyOffset uintptr) {
	// Write queue index to notify register
	// Format: queue_index (16-bit)
	mmio_write16(notifyOffset, vq.Available.Idx-1) // Notify about last added descriptor
	dsb()                                          // Memory barrier
}

// virtqueueReset resets a virtqueue to initial state
//
//go:nosplit
func virtqueueReset(vq *VirtQueue) {
	// Zero out all structures
	if vq.DescTable != nil {
		descSize := uintptr(vq.QueueSize) * unsafe.Sizeof(VirtQDesc{})
		bzero(vq.DescTable, uint32(descSize))
	}
	if vq.Available != nil {
		availSize := 2 + 2 + uintptr(vq.QueueSize)*2 + 2
		bzero(unsafe.Pointer(vq.Available), uint32(availSize))
	}
	if vq.Used != nil {
		usedSize := 2 + 2 + uintptr(vq.QueueSize)*unsafe.Sizeof(VirtQUsedElem{}) + 2
		bzero(unsafe.Pointer(vq.Used), uint32(usedSize))
	}

	// Reinitialize free descriptor list
	vq.FreeHead = 0
	vq.NumFree = vq.QueueSize
	var descSize uintptr = unsafe.Sizeof(VirtQDesc{})
	for i := uint16(0); i < vq.QueueSize-1; i++ {
		descPtr := castToPointer[VirtQDesc](pointerToUintptr(vq.DescTable) + uintptr(i)*descSize)
		descPtr.Next = i + 1
	}
	lastDescPtr := castToPointer[VirtQDesc](pointerToUintptr(vq.DescTable) + uintptr(vq.QueueSize-1)*descSize)
	lastDescPtr.Next = 0xFFFF

	// Reset indices
	vq.Available.Idx = 0
	vq.Used.Idx = 0
	vq.LastUsedIdx = 0
}
