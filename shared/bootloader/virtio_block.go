// VirtIO MMIO block device driver for bootloaders.
//
// Minimal, allocation-free, polling-based driver for reading/writing blocks
// via VirtIO MMIO transport. Suitable for ARM64 bootloaders where memory
// is identity-mapped and no Go runtime heap is available.
//
// Only built for arm64 targets.

//go:build arm64

package bootloader

import (
	"unsafe"
)

// VirtIO MMIO register offsets
const (
	virtioMagicValue       = 0x000
	virtioVersion          = 0x004
	virtioDeviceID         = 0x008
	virtioDeviceFeatures   = 0x010
	virtioDeviceFeaturesSel = 0x014
	virtioDriverFeatures   = 0x020
	virtioDriverFeaturesSel = 0x024
	virtioGuestPageSize    = 0x028 // legacy (v1) only
	virtioQueueSel         = 0x030
	virtioQueueNumMax      = 0x034
	virtioQueueNum         = 0x038
	virtioQueueAlign       = 0x03C // legacy (v1) only
	virtioQueuePFN         = 0x040 // legacy (v1) only
	virtioQueueReady       = 0x044 // modern (v2) only
	virtioQueueNotify      = 0x050
	virtioInterruptStatus  = 0x060
	virtioInterruptACK     = 0x064
	virtioStatus           = 0x070
	virtioQueueDescLow     = 0x080
	virtioQueueDescHigh    = 0x084
	virtioQueueDriverLow   = 0x090
	virtioQueueDriverHigh  = 0x094
	virtioQueueDeviceLow   = 0x0A0
	virtioQueueDeviceHigh  = 0x0A4
	virtioConfig           = 0x100
)

// VirtIO device status bits
const (
	virtioStatusAcknowledge = 1 << 0
	virtioStatusDriver      = 1 << 1
	virtioStatusDriverOK    = 1 << 2
	virtioStatusFeaturesOK  = 1 << 3
	virtioStatusFailed      = 1 << 7
)

// VirtIO block device constants
const (
	VirtIOBlockDeviceID = 2
	virtioBlkTIn        = 0 // Read
	virtioBlkTOut       = 1 // Write
	virtioBlkSOK        = 0

	// VirtIO descriptor flags
	virtqDescFNext  = 1
	virtqDescFWrite = 2
)

// VirtIO MMIO magic value
const virtioMagicExpected = 0x74726976 // "virt"

// virtioBlkReqHeader is the request header for block operations
type virtioBlkReqHeader struct {
	Type     uint32
	Reserved uint32
	Sector   uint64
}

// virtqDesc is a virtqueue descriptor (16 bytes)
type virtqDesc struct {
	Addr  uint64
	Len   uint32
	Flags uint16
	Next  uint16
}

// virtqUsedElem is a used ring element
type virtqUsedElem struct {
	ID  uint32
	Len uint32
}

// Pre-allocated queue size
const bootVQSize = 4 // Minimal queue for bootloader use

// Contiguous available ring: flags(2) + idx(2) + ring[N](2*N) + used_event(2)
type virtqAvailFull struct {
	Flags     uint16
	Idx       uint16
	Ring      [bootVQSize]uint16
	UsedEvent uint16
}

// Contiguous used ring: flags(2) + idx(2) + ring[N](8*N) + avail_event(2)
type virtqUsedFull struct {
	Flags      uint16
	Idx        uint16
	Ring       [bootVQSize]virtqUsedElem
	AvailEvent uint16
}

// Pre-allocated virtqueue structures (no heap allocation).
// These are global variables so their addresses are stable for DMA.
var (
	vqDescs [bootVQSize]virtqDesc
	vqAvail virtqAvailFull
	vqUsed  virtqUsedFull

	// Pre-allocated request buffers
	blkReqHdr virtioBlkReqHeader
	blkStatus [1]byte
)

// legacyVirtQueue is a page-aligned contiguous buffer for legacy (v1) VirtIO.
// Legacy VirtIO requires descriptors, available ring, and used ring in a specific
// contiguous layout at a single PFN.
//
// Layout (for bootVQSize=4):
//   Offset 0x000: Descriptors (4 * 16 = 64 bytes)
//   Offset 0x040: Available ring (2 + 2 + 4*2 + 2 = 14 bytes)
//   Offset 0x04E: Padding to 4096-byte alignment
//   Offset 0x1000: Used ring (2 + 2 + 4*8 + 2 = 38 bytes)
//
// Total: 2 pages
var legacyVQ [8192]byte // 2 pages, page-aligned via linker

// VirtIOBlockDevice implements blockdev.BlockDevice for VirtIO MMIO.
type VirtIOBlockDevice struct {
	mmioBase    uintptr
	capacity    uint64
	version     uint32
	lastUsedIdx uint16
	initialized bool
	legacy      bool // true if using legacy (v1) contiguous queue layout
}

// Pointers to legacy contiguous queue structures (set during init if legacy)
var (
	legacyDescPtr  *[bootVQSize]virtqDesc
	legacyAvailPtr *virtqAvailFull
	legacyUsedPtr  *virtqUsedFull
)

// Global block device instance (avoid heap allocation)
var globalVirtIOBlock VirtIOBlockDevice

// Pre-allocated error values
var (
	ErrVirtIOBadMagic  = &ELFError{"virtio: bad magic"}
	ErrVirtIONotBlock  = &ELFError{"virtio: not a block device"}
	ErrVirtIONoQueue   = &ELFError{"virtio: no queue available"}
	ErrVirtIOFeatures  = &ELFError{"virtio: feature negotiation failed"}
	ErrVirtIOTimeout   = &ELFError{"virtio: timeout"}
	ErrVirtIOReadFail  = &ELFError{"virtio: read failed"}
	ErrVirtIOWriteFail = &ELFError{"virtio: write failed"}
	ErrVirtIOBufSmall  = &ELFError{"virtio: buffer too small"}
	ErrVirtIOLBARange  = &ELFError{"virtio: lba out of range"}
)

// MMIO register access (identity-mapped physical addresses)

func mmioRead32(base uintptr, offset uintptr) uint32 {
	return *(*uint32)(unsafe.Pointer(base + offset))
}

// MmioRead32Pub is a public accessor for mmioRead32 (for debug use).
func MmioRead32Pub(base uintptr, offset uintptr) uint32 {
	return mmioRead32(base, offset)
}

func mmioWrite32(base uintptr, offset uintptr, val uint32) {
	*(*uint32)(unsafe.Pointer(base + offset)) = val
}

// InitVirtIOBlock initializes a VirtIO block device at the given MMIO base.
// The MMIO region must already be mapped. Returns the global block device.
func InitVirtIOBlock(mmioBase uintptr) (*VirtIOBlockDevice, error) {
	dev := &globalVirtIOBlock
	dev.mmioBase = mmioBase

	// Check magic
	magic := mmioRead32(mmioBase, virtioMagicValue)
	if magic != virtioMagicExpected {
		return nil, ErrVirtIOBadMagic
	}

	version := mmioRead32(mmioBase, virtioVersion)
	dev.version = version

	// Check device type
	deviceID := mmioRead32(mmioBase, virtioDeviceID)
	if deviceID != VirtIOBlockDeviceID {
		return nil, ErrVirtIONotBlock
	}

	// 1. Reset
	mmioWrite32(mmioBase, virtioStatus, 0)

	// Legacy: set guest page size
	if version == 1 {
		mmioWrite32(mmioBase, virtioGuestPageSize, 4096)
	}

	// 2. Acknowledge
	mmioWrite32(mmioBase, virtioStatus, virtioStatusAcknowledge)

	// 3. Driver
	status := mmioRead32(mmioBase, virtioStatus)
	mmioWrite32(mmioBase, virtioStatus, status|virtioStatusDriver)

	// 4. Feature negotiation
	if version == 1 {
		mmioWrite32(mmioBase, virtioDriverFeatures, 0)
	} else {
		// Read features
		mmioWrite32(mmioBase, virtioDeviceFeaturesSel, 0)
		mmioWrite32(mmioBase, virtioDeviceFeaturesSel, 1)
		devFeatHi := mmioRead32(mmioBase, virtioDeviceFeatures)

		// Accept VIRTIO_F_VERSION_1 if offered
		drvFeatHi := uint32(0)
		if devFeatHi&1 != 0 {
			drvFeatHi = 1
		}

		mmioWrite32(mmioBase, virtioDriverFeaturesSel, 0)
		mmioWrite32(mmioBase, virtioDriverFeatures, 0)
		mmioWrite32(mmioBase, virtioDriverFeaturesSel, 1)
		mmioWrite32(mmioBase, virtioDriverFeatures, drvFeatHi)

		// 5. Features OK
		status = mmioRead32(mmioBase, virtioStatus)
		mmioWrite32(mmioBase, virtioStatus, status|virtioStatusFeaturesOK)

		// 6. Verify
		if mmioRead32(mmioBase, virtioStatus)&virtioStatusFeaturesOK == 0 {
			status = mmioRead32(mmioBase, virtioStatus)
			mmioWrite32(mmioBase, virtioStatus, status|virtioStatusFailed)
			return nil, ErrVirtIOFeatures
		}
	}

	// 7. Setup virtqueue 0
	mmioWrite32(mmioBase, virtioQueueSel, 0)
	maxQueueSize := mmioRead32(mmioBase, virtioQueueNumMax)
	if maxQueueSize == 0 {
		return nil, ErrVirtIONoQueue
	}

	qSize := uint32(bootVQSize)
	if maxQueueSize < qSize {
		qSize = maxQueueSize
	}
	mmioWrite32(mmioBase, virtioQueueNum, qSize)

	// Initialize descriptor free list
	for i := 0; i < bootVQSize-1; i++ {
		vqDescs[i].Next = uint16(i + 1)
		vqDescs[i].Flags = 0
	}
	vqDescs[bootVQSize-1].Next = 0xFFFF
	vqDescs[bootVQSize-1].Flags = 0

	// Initialize rings
	vqAvail = virtqAvailFull{}
	vqUsed = virtqUsedFull{}
	dev.lastUsedIdx = 0

	if version == 1 {
		// Legacy: requires contiguous queue memory at a page-aligned PFN
		// The layout is: descriptors | avail ring | <padding to page> | used ring
		//
		// Align legacyVQ buffer to page boundary
		base := uintptr(unsafe.Pointer(&legacyVQ[0]))
		aligned := (base + 4095) &^ 4095

		// Descriptors at aligned base
		descBase := aligned
		// Available ring immediately after descriptors
		availBase := descBase + uintptr(bootVQSize)*16 // 4*16 = 64
		// Used ring at next page boundary after avail ring
		// Avail ring size: 2(flags) + 2(idx) + bootVQSize*2 + 2(used_event) = 14
		usedBase := (availBase + 14 + 4095) &^ 4095

		// Copy descriptor pointers to the contiguous buffer
		vqDescsLegacy := (*[bootVQSize]virtqDesc)(unsafe.Pointer(descBase))
		vqAvailLegacy := (*virtqAvailFull)(unsafe.Pointer(availBase))
		vqUsedLegacy := (*virtqUsedFull)(unsafe.Pointer(usedBase))

		// Initialize in-place
		for i := 0; i < bootVQSize-1; i++ {
			vqDescsLegacy[i].Next = uint16(i + 1)
			vqDescsLegacy[i].Flags = 0
		}
		vqDescsLegacy[bootVQSize-1].Next = 0xFFFF
		vqDescsLegacy[bootVQSize-1].Flags = 0
		*vqAvailLegacy = virtqAvailFull{}
		*vqUsedLegacy = virtqUsedFull{}

		// Point the globals at the contiguous layout so doBlockIO uses them
		legacyDescPtr = vqDescsLegacy
		legacyAvailPtr = vqAvailLegacy
		legacyUsedPtr = vqUsedLegacy
		dev.legacy = true

		pfn := uint32(descBase >> 12)
		mmioWrite32(mmioBase, virtioQueueAlign, 4096)
		mmioWrite32(mmioBase, virtioQueuePFN, pfn)
	} else {
		// Modern: provide separate addresses
		descPhys := uint64(uintptr(unsafe.Pointer(&vqDescs[0])))
		availPhys := uint64(uintptr(unsafe.Pointer(&vqAvail)))
		usedPhys := uint64(uintptr(unsafe.Pointer(&vqUsed)))

		mmioWrite32(mmioBase, virtioQueueDescLow, uint32(descPhys))
		mmioWrite32(mmioBase, virtioQueueDescHigh, uint32(descPhys>>32))
		mmioWrite32(mmioBase, virtioQueueDriverLow, uint32(availPhys))
		mmioWrite32(mmioBase, virtioQueueDriverHigh, uint32(availPhys>>32))
		mmioWrite32(mmioBase, virtioQueueDeviceLow, uint32(usedPhys))
		mmioWrite32(mmioBase, virtioQueueDeviceHigh, uint32(usedPhys>>32))
		mmioWrite32(mmioBase, virtioQueueReady, 1)
	}

	// 8. Driver OK
	status = mmioRead32(mmioBase, virtioStatus)
	mmioWrite32(mmioBase, virtioStatus, status|virtioStatusDriverOK)

	// 9. Read capacity
	capLow := mmioRead32(mmioBase, virtioConfig+0)
	capHigh := mmioRead32(mmioBase, virtioConfig+4)
	dev.capacity = uint64(capLow) | (uint64(capHigh) << 32)
	dev.initialized = true

	return dev, nil
}

func (d *VirtIOBlockDevice) Name() string {
	return "virtio-blk"
}

func (d *VirtIOBlockDevice) Close() error {
	mmioWrite32(d.mmioBase, virtioStatus, 0)
	d.initialized = false
	return nil
}

func (d *VirtIOBlockDevice) BlockSize() uint64 {
	return 512
}

func (d *VirtIOBlockDevice) NumBlocks() uint64 {
	return d.capacity
}

// ReadBlock reads a single 512-byte block at the given LBA.
func (d *VirtIOBlockDevice) ReadBlock(lba uint64, buf []byte) error {
	if len(buf) < 512 {
		return ErrVirtIOBufSmall
	}
	if lba >= d.capacity {
		return ErrVirtIOLBARange
	}

	return d.doBlockIO(virtioBlkTIn, lba, buf)
}

// WriteBlock writes a single 512-byte block at the given LBA.
func (d *VirtIOBlockDevice) WriteBlock(lba uint64, buf []byte) error {
	if len(buf) < 512 {
		return ErrVirtIOBufSmall
	}
	if lba >= d.capacity {
		return ErrVirtIOLBARange
	}

	return d.doBlockIO(virtioBlkTOut, lba, buf)
}

// doBlockIO performs a block read or write using a 3-descriptor chain.
func (d *VirtIOBlockDevice) doBlockIO(reqType uint32, lba uint64, buf []byte) error {
	// Setup request header
	blkReqHdr.Type = reqType
	blkReqHdr.Reserved = 0
	blkReqHdr.Sector = lba
	blkStatus[0] = 0xFF

	// Physical addresses (identity-mapped in bootloader)
	hdrPhys := uint64(uintptr(unsafe.Pointer(&blkReqHdr)))
	dataPhys := uint64(uintptr(unsafe.Pointer(&buf[0])))
	statusPhys := uint64(uintptr(unsafe.Pointer(&blkStatus[0])))

	// Get pointers to the queue structures (legacy vs modern)
	descs := &vqDescs
	avail := &vqAvail
	used := &vqUsed
	if d.legacy {
		descs = legacyDescPtr
		avail = legacyAvailPtr
		used = legacyUsedPtr
	}

	// Setup 3-descriptor chain: header → data → status
	descs[0].Addr = hdrPhys
	descs[0].Len = uint32(unsafe.Sizeof(blkReqHdr))
	descs[0].Flags = virtqDescFNext
	descs[0].Next = 1

	dataFlags := uint16(virtqDescFNext)
	if reqType == virtioBlkTIn {
		dataFlags |= virtqDescFWrite
	}
	descs[1].Addr = dataPhys
	descs[1].Len = 512
	descs[1].Flags = dataFlags
	descs[1].Next = 2

	descs[2].Addr = statusPhys
	descs[2].Len = 1
	descs[2].Flags = virtqDescFWrite
	descs[2].Next = 0xFFFF

	// Add to available ring
	ringIdx := avail.Idx % bootVQSize
	avail.Ring[ringIdx] = 0

	dsb()
	avail.Idx++
	dsb()

	// Notify device
	mmioWrite32(d.mmioBase, virtioQueueNotify, 0)

	// Poll for completion
	for i := 0; i < 10_000_000; i++ {
		dsb()
		if used.Idx != d.lastUsedIdx {
			break
		}
	}

	if used.Idx == d.lastUsedIdx {
		return ErrVirtIOTimeout
	}

	d.lastUsedIdx = used.Idx

	// Check status
	if blkStatus[0] != virtioBlkSOK {
		if reqType == virtioBlkTIn {
			return ErrVirtIOReadFail
		}
		return ErrVirtIOWriteFail
	}

	return nil
}

// dsb executes a Data Synchronization Barrier (ARM64).
//
//go:nosplit
func dsb()

// isb executes an Instruction Synchronization Barrier (ARM64).
//
//go:nosplit
func isb()
