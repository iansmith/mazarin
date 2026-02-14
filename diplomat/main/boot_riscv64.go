//go:build riscv64

// diplomat/main/boot_riscv64.go - RISC-V 64-bit boot sequence (non-UEFI)
//
// RISC-V diplomat runs under OpenSBI firmware, not UEFI. This file provides
// alternative implementations of boot services that would normally come from UEFI.

package main

import (
	"unsafe"

	_ "mazzy/shared/blockdev" // Will be used in Task #3
	_ "mazzy/shared/fs/fat32" // Will be used in Task #3
)

// Assembly functions (defined in memory_barrier_riscv64.s)
func memoryBarrier()

// ============================================================================
// QEMU virt Platform Constants
// ============================================================================
// These are hardcoded for QEMU's virt platform to avoid complexity of FDT
// parsing in diplomat. The actual FDT is passed to kmazarin via auxv for
// device discovery.
//
// QEMU virt platform (RISC-V):
//   - RAM: 0x80000000 - 0xFFFFFFFF (2GB default, configurable with -m)
//   - OpenSBI: 0x80000000 - 0x8007FFFF (512KB)
//   - Diplomat: 0x80200000+ (loaded by OpenSBI)
//   - FDT: 0xFFE00000 (passed by OpenSBI in A1 register)
//
const (
	QEMU_VIRT_RAM_BASE  = 0x80000000 // Start of physical RAM
	QEMU_VIRT_RAM_SIZE  = 0x80000000 // 2GB (QEMU default)
	QEMU_VIRT_FDT_ADDR  = 0xFFE00000 // FDT address (OpenSBI convention)
	QEMU_VIRT_CPU_COUNT = 1          // Single CPU for now
)

// Global FDT info (populated by InitializeFDT)
// Use a value instead of pointer to avoid heap allocation during early boot
var fdtInfoStruct FDTInfo
var fdtInfo *FDTInfo = &fdtInfoStruct

// InitializeFDT initializes platform information for RISC-V boot.
//
// For QEMU virt platform, we use hardcoded constants instead of parsing FDT
// in diplomat. The FDT address is passed to kmazarin via auxv (AT_FDT_ADDR)
// so the kernel can parse it for device discovery.
//
// This simplifies diplomat and follows the same pattern as ARM64/x86_64 where
// diplomat provides minimal platform info and the kernel does full discovery.
func InitializeFDT() bool {
	printString("Initializing RISC-V platform (QEMU virt)...\r\n")

	// Hardcoded QEMU virt platform parameters
	bootHartID = 0
	fdtPointer = QEMU_VIRT_FDT_ADDR

	// Populate global FDTInfo structure with hardcoded values
	// NOTE: Use global struct (no new/allocation) to avoid heap allocation during early boot
	// NOTE: ISAString field left empty to avoid string allocation issues
	fdtInfo.RAMBase = QEMU_VIRT_RAM_BASE
	fdtInfo.RAMSize = QEMU_VIRT_RAM_SIZE
	fdtInfo.CPUCount = QEMU_VIRT_CPU_COUNT
	fdtInfo.FDTAddress = fdtPointer

	// Display platform configuration
	printString("Platform: QEMU virt (RISC-V64)\r\n")
	printString("  Hart ID:  ")
	printHex(bootHartID)
	printString("\r\n")
	printString("  FDT addr: ")
	printHex(fdtPointer)
	printString("\r\n")
	printString("  RAM:      ")
	printHex(fdtInfo.RAMBase)
	printString(" - ")
	printHex(fdtInfo.RAMBase + fdtInfo.RAMSize - 1)
	printString(" (")
	printHex(fdtInfo.RAMSize / (1024 * 1024))
	printString(" MB)\r\n")
	printString("  CPUs:     ")
	printHex(uint64(fdtInfo.CPUCount))
	printString("\r\n")
	printString("\r\n")

	return true
}

// InitSpansRISCV wraps InitializeSpans with FDT initialization.
// This is called from the RISC-V boot sequence instead of InitializeSpans directly.
//
//go:nosplit
func InitSpansRISCV() bool {
	// Parse FDT first
	if !InitializeFDT() {
		return false
	}

	// Then initialize memory spans (regular path)
	return InitializeSpans()
}


// ============================================================================
// VirtIO MMIO Constants (RISC-V bare-metal)
// ============================================================================

// VirtIO MMIO device base addresses on QEMU RISC-V virt platform
// Devices are located at 0x10001000, 0x10002000, etc.
const (
	VIRTIO_MMIO_BASE        = 0x10001000
	VIRTIO_MMIO_SIZE        = 0x1000
	VIRTIO_MMIO_MAX_DEVICES = 8
)

// VirtIO block device feature bits (for feature negotiation)
const (
	VIRTIO_BLK_F_SIZE_MAX  = 1  // Maximum size of any single segment
	VIRTIO_BLK_F_SEG_MAX   = 2  // Maximum number of segments
	VIRTIO_BLK_F_GEOMETRY  = 4  // Disk-style geometry
	VIRTIO_BLK_F_RO        = 5  // Device is read-only
	VIRTIO_BLK_F_BLK_SIZE  = 6  // Block size of disk
	VIRTIO_BLK_F_FLUSH     = 9  // Cache flush command support
	VIRTIO_BLK_F_TOPOLOGY  = 10 // Device exports topology information
	VIRTIO_F_VERSION_1     = 32 // VirtIO 1.0 compliance (NOT for MMIO v1!)
)

// VirtIO MMIO register offsets (VirtIO spec 1.0)
const (
	VIRTIO_MMIO_MAGIC_VALUE         = 0x000 // 0x74726976 ('virt')
	VIRTIO_MMIO_VERSION             = 0x004 // Version (1 or 2)
	VIRTIO_MMIO_DEVICE_ID           = 0x008 // Device type (2 = block)
	VIRTIO_MMIO_VENDOR_ID           = 0x00c
	VIRTIO_MMIO_DEVICE_FEATURES     = 0x010
	VIRTIO_MMIO_DEVICE_FEATURES_SEL = 0x014
	VIRTIO_MMIO_DRIVER_FEATURES     = 0x020
	VIRTIO_MMIO_DRIVER_FEATURES_SEL = 0x024
	VIRTIO_MMIO_QUEUE_SEL           = 0x030
	VIRTIO_MMIO_QUEUE_NUM_MAX       = 0x034
	VIRTIO_MMIO_QUEUE_NUM           = 0x038
	VIRTIO_MMIO_QUEUE_ALIGN         = 0x03c // Version 1 only
	VIRTIO_MMIO_QUEUE_PFN           = 0x040 // Version 1 only (Page Frame Number)
	VIRTIO_MMIO_QUEUE_READY         = 0x044 // Version 2 only
	VIRTIO_MMIO_QUEUE_NOTIFY        = 0x050
	VIRTIO_MMIO_INTERRUPT_STATUS    = 0x060
	VIRTIO_MMIO_INTERRUPT_ACK       = 0x064
	VIRTIO_MMIO_STATUS              = 0x070
	VIRTIO_MMIO_QUEUE_DESC_LOW      = 0x080 // Version 2 only
	VIRTIO_MMIO_QUEUE_DESC_HIGH     = 0x084 // Version 2 only
	VIRTIO_MMIO_QUEUE_AVAIL_LOW     = 0x090 // Version 2 only
	VIRTIO_MMIO_QUEUE_AVAIL_HIGH    = 0x094 // Version 2 only
	VIRTIO_MMIO_QUEUE_USED_LOW      = 0x0A0 // Version 2 only
	VIRTIO_MMIO_QUEUE_USED_HIGH     = 0x0A4 // Version 2 only
	VIRTIO_MMIO_CONFIG              = 0x100
)

// VirtIO MMIO queue layout constants
const (
	VIRTIO_MMIO_PAGE_SHIFT = 12 // 4KB pages for QUEUE_PFN
)

// VirtIO device status bits
const (
	VIRTIO_STATUS_ACKNOWLEDGE = 1
	VIRTIO_STATUS_DRIVER      = 2
	VIRTIO_STATUS_DRIVER_OK   = 4
	VIRTIO_STATUS_FEATURES_OK = 8
	VIRTIO_STATUS_FAILED      = 128
)

// VirtIO device types
const (
	VIRTIO_DEVICE_TYPE_BLOCK = 2
)

// Global VirtIO MMIO block device state (avoid heap allocation)
var virtioMMIOBase uintptr
var virtioMMIOVersion uint32  // MMIO transport version (1 or 2)
var virtioBlockCapacity uint64
var virtioBlockSectorSize uint32 = 512

// GetBootDeviceRISCV returns a block device for the boot disk.
// On RISC-V bare-metal boot (no UEFI), we use VirtIO MMIO transport.
//
// NOTE: QEMU provides BOTH transports for the same disk:
//   - VirtIO MMIO (virtio-blk-device) for diplomat bare-metal
//   - VirtIO PCI (virtio-blk-pci) for kmazarin with full PCI init
// Kmazarin will need to detect both and provide a common block device interface.
func GetBootDeviceRISCV() (*UEFIBlockDevice, error) {
	printString("=== Task #2: VirtIO Block Device (MMIO) ===\r\n")

	// Scan for VirtIO MMIO block device
	virtioMMIOBase = scanVirtIOMMIO()
	if virtioMMIOBase == 0 {
		return nil, &errBootServicesNotAvailable
	}

	// Initialize the VirtIO MMIO device
	if !virtioMMIOHandshake(virtioMMIOBase) {
		printString("ERROR: VirtIO MMIO handshake failed\r\n")
		return nil, &errBootServicesNotAvailable
	}

	// TODO: Read device capacity from config space
	// For now, use a placeholder value
	virtioBlockCapacity = 0xFFFFFFFF // Will be read properly later
	printString("VirtIO MMIO block device ready\r\n")

	// Wrap in UEFIBlockDevice for compatibility
	globalBlockDev.protocol = 0 // Not used for VirtIO
	globalBlockDev.media = 0    // Not used for VirtIO
	globalBlockDev.blockSize = uint64(virtioBlockSectorSize)
	globalBlockDev.numBlocks = virtioBlockCapacity
	globalBlockDev.mediaId = 0

	printString("=== Task #2 Complete ===\r\n\r\n")
	return &globalBlockDev, nil
}

// ============================================================================
// VirtIO MMIO Device Scanning
// ============================================================================

// scanVirtIOMMIO scans for VirtIO MMIO block devices.
// Returns the base address of the first block device found, or 0 if none found.
//go:nosplit
func scanVirtIOMMIO() uintptr {
	printString("Scanning VirtIO MMIO devices...\r\n")

	for i := uint32(0); i < VIRTIO_MMIO_MAX_DEVICES; i++ {
		base := uintptr(VIRTIO_MMIO_BASE + (i * VIRTIO_MMIO_SIZE))

		// Check magic value
		magic := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_MAGIC_VALUE))
		if magic != 0x74726976 {
			continue // Not a VirtIO device
		}

		// Check version (1 or 2)
		version := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_VERSION))
		if version != 1 && version != 2 {
			printString("  Device at ")
			printHex(uint64(base))
			printString(" has unsupported version ")
			printHex(uint64(version))
			printString("\r\n")
			continue
		}

		// Check device type
		deviceID := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DEVICE_ID))
		
		printString("  Device at ")
		printHex(uint64(base))
		printString(" type=")
		printHex(uint64(deviceID))
		printString(" version=")
		printHex(uint64(version))
		printString("\r\n")

		if deviceID == VIRTIO_DEVICE_TYPE_BLOCK {
			printString("Found VirtIO MMIO block device at ")
			printHex(uint64(base))
			printString("\r\n")
			virtioMMIOVersion = version  // Save MMIO transport version
			return base
		}
	}

	printString("ERROR: No VirtIO MMIO block device found\r\n")
	return 0
}

// ============================================================================
// VirtIO MMIO Device Initialization
// ============================================================================

// virtioMMIOHandshake performs the VirtIO device initialization handshake for MMIO.
//go:nosplit
func virtioMMIOHandshake(base uintptr) bool {
	printString("VirtIO MMIO handshake...\r\n")

	// Reset device
	printString("  Reset...")
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS)) = 0

	// Set ACKNOWLEDGE
	printString("ACK...")
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS)) = VIRTIO_STATUS_ACKNOWLEDGE

	// Set DRIVER
	printString("DRIVER...")
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS)) =
		VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER

	// Feature negotiation
	printString("Features...\r\n")

	// Read device-offered features
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DEVICE_FEATURES_SEL)) = 0
	deviceFeaturesLow := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DEVICE_FEATURES))
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DEVICE_FEATURES_SEL)) = 1
	deviceFeaturesHigh := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DEVICE_FEATURES))

	printString("  Device offers: low=")
	printHex(uint64(deviceFeaturesLow))
	printString(" high=")
	printHex(uint64(deviceFeaturesHigh))
	printString("\r\n")

	// CRITICAL: Feature negotiation depends on MMIO version
	var driverFeaturesLow uint32
	var driverFeaturesHigh uint32

	if virtioMMIOVersion == 1 {
		// MMIO v1 (legacy): Do NOT negotiate VIRTIO_F_VERSION_1
		// Accept device-offered features to satisfy device requirements
		printString("  MMIO v1 (legacy) - no VERSION_1 feature\r\n")
		driverFeaturesLow = deviceFeaturesLow  // Accept all offered features
		driverFeaturesHigh = 0                  // No high features for legacy
	} else {
		// MMIO v2 (modern): MUST negotiate VIRTIO_F_VERSION_1
		// Modern VirtIO spec requires VERSION_1 for proper operation
		printString("  MMIO v2 (modern) - negotiating VERSION_1\r\n")
		// Accept device features AND add VERSION_1
		driverFeaturesLow = deviceFeaturesLow   // Accept device features
		driverFeaturesHigh = 1                   // VIRTIO_F_VERSION_1 (bit 32)
	}

	printString("  Driver accepts: low=")
	printHex(uint64(driverFeaturesLow))
	printString(" high=")
	printHex(uint64(driverFeaturesHigh))
	printString("\r\n")

	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DRIVER_FEATURES_SEL)) = 0
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DRIVER_FEATURES)) = driverFeaturesLow
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DRIVER_FEATURES_SEL)) = 1
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DRIVER_FEATURES)) = driverFeaturesHigh

	// Set FEATURES_OK
	printString("FEATURES_OK...")
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS)) =
		VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER | VIRTIO_STATUS_FEATURES_OK

	// Verify FEATURES_OK
	printString("Verify...")
	status := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS))
	if (status & VIRTIO_STATUS_FEATURES_OK) == 0 {
		printString("FAILED (features rejected)\r\n")
		return false
	}

	// CRITICAL: Setup queues BEFORE setting DRIVER_OK
	// VirtIO spec requires: FEATURES_OK → queue setup → DRIVER_OK
	printString("Queues...")
	initVirtqueueDuringHandshake()

	// Set DRIVER_OK (only after queues are configured!)
	printString("DRIVER_OK...")
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS)) =
		VIRTIO_STATUS_ACKNOWLEDGE | VIRTIO_STATUS_DRIVER |
		VIRTIO_STATUS_FEATURES_OK | VIRTIO_STATUS_DRIVER_OK

	// Check for FAILED bit
	finalStatus := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS))
	if (finalStatus & VIRTIO_STATUS_FAILED) != 0 {
		printString("ERROR: Device set FAILED bit\r\n")
		return false
	}

	printString("Complete!\r\n")
	return true
}

// ============================================================================
// VirtIO Block Device Operations - Static Virtqueues
// ============================================================================

// VirtIO virtqueue constants
const (
	VIRTQ_DESC_F_NEXT     = 1
	VIRTQ_DESC_F_WRITE    = 2
	VIRTQ_AVAIL_F_NO_INTERRUPT = 1
)

// VirtIO block request header
const (
	VIRTIO_BLK_T_IN  = 0
	VIRTIO_BLK_T_OUT = 1
)

// Virtqueue descriptor
type virtqDesc struct {
	addr  uint64
	len   uint32
	flags uint16
	next  uint16
}

// Virtqueue available ring
type virtqAvail struct {
	flags uint16
	idx   uint16
	ring  [16]uint16 // Queue size = 16
	// used_event would go here but we don't use it
}

// Virtqueue used ring element
type virtqUsedElem struct {
	id  uint32
	len uint32
}

// Virtqueue used ring
type virtqUsed struct {
	flags uint16
	idx   uint16
	ring  [16]virtqUsedElem
	// avail_event would go here but we don't use it
}

// VirtIO block request structure
type virtioBlkReq struct {
	reqType   uint32
	reserved  uint32
	sector    uint64
	data      [512]byte
	status    uint8
	_padding  [7]uint8 // Align to 8 bytes
}

// Statically allocated virtqueue (queue size = 16)
// MUST be 4KB aligned for VirtIO MMIO version 1
// Since Go doesn't reliably align globals, use a larger buffer and manually align
var (
	// Large buffer to guarantee we can find a 4KB-aligned region
	virtqBuffer [8192]byte // 8KB buffer (guarantees 4KB alignment within)

	// Will point to 4KB-aligned regions within virtqBuffer
	virtqDescTable *[16]virtqDesc
	virtqAvailRing *virtqAvail
	virtqUsedRing  *virtqUsed

	virtioReq virtioBlkReq

	virtqInitialized bool
	lastUsedIdx      uint16
)

// ============================================================================
// VirtIO Virtqueue Initialization
// ============================================================================

// initVirtqueueDuringHandshake does the actual queue initialization.
// Called from virtioMMIOHandshake() before DRIVER_OK is set.
func initVirtqueueDuringHandshake() {

	// Find 4KB-aligned address within virtqBuffer
	bufAddr := uintptr(unsafe.Pointer(&virtqBuffer[0]))
	alignedAddr := (bufAddr + 4095) & ^uintptr(4095) // Round up to 4KB

	// VirtIO MMIO v1 requires specific layout:
	// - descriptor_table[Queue Size] (256 bytes for 16 descriptors)
	// - available_ring (6 + 2×16 = 38 bytes)
	// - padding to 4KB boundary
	// - used_ring (6 + 8×16 = 134 bytes)

	descSize := uintptr(16 * 16)  // 16 descriptors × 16 bytes
	availSize := uintptr(6 + 2*16) // Header + ring entries

	descOffset := alignedAddr - bufAddr
	virtqDescTable = (*[16]virtqDesc)(unsafe.Pointer(&virtqBuffer[descOffset]))

	availOffset := descOffset + descSize
	virtqAvailRing = (*virtqAvail)(unsafe.Pointer(&virtqBuffer[availOffset]))

	// Used ring must be 4KB-aligned after avail ring
	usedBase := alignedAddr + descSize + availSize
	usedBaseAligned := (usedBase + 4095) & ^uintptr(4095)  // Round up to 4KB
	usedOffset := usedBaseAligned - bufAddr
	virtqUsedRing = (*virtqUsed)(unsafe.Pointer(&virtqBuffer[usedOffset]))

	// Clear all structures
	for i := range virtqDescTable {
		virtqDescTable[i] = virtqDesc{}
	}
	*virtqAvailRing = virtqAvail{}
	*virtqUsedRing = virtqUsed{}
	lastUsedIdx = 0

	// Set up descriptor chain for block reads
	// Standard 3-descriptor chain per VirtIO block spec:
	// Desc 0: Request header (device reads)
	// Desc 1: Data buffer (device writes)
	// Desc 2: Status byte (device writes)

	// Descriptor 0: Request header (device reads)
	virtqDescTable[0].addr = uint64(uintptr(unsafe.Pointer(&virtioReq)))
	virtqDescTable[0].len = 16 // sizeof(type + reserved + sector)
	virtqDescTable[0].flags = VIRTQ_DESC_F_NEXT
	virtqDescTable[0].next = 1

	// Descriptor 1: Data buffer (device writes)
	virtqDescTable[1].addr = uint64(uintptr(unsafe.Pointer(&virtioReq.data)))
	virtqDescTable[1].len = 512
	virtqDescTable[1].flags = VIRTQ_DESC_F_WRITE | VIRTQ_DESC_F_NEXT
	virtqDescTable[1].next = 2

	// Descriptor 2: Status byte (device writes)
	virtqDescTable[2].addr = uint64(uintptr(unsafe.Pointer(&virtioReq.status)))
	virtqDescTable[2].len = 1
	virtqDescTable[2].flags = VIRTQ_DESC_F_WRITE
	virtqDescTable[2].next = 0

	// Tell device about our virtqueue
	base := virtioMMIOBase
	queueSize := uint32(16)

	// Select queue 0
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_SEL)) = 0

	// Check if queue is already configured
	existingPFN := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_PFN))
	if existingPFN != 0 {
		// Reset queue if already configured (write 0 to QUEUE_PFN)
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_PFN)) = 0
		memoryBarrier()
		printString("  Reset existing queue (PFN was ")
		printHex(uint64(existingPFN))
		printString(")\r\n")
	}

	// Check max queue size
	maxSize := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_NUM_MAX))

	// Determine queue size to use
	if maxSize < queueSize {
		printString("  Queue max: ")
		printHex(uint64(maxSize))
		printString(" (wanted ")
		printHex(uint64(queueSize))
		printString(")\r\n")
		queueSize = maxSize
	} else {
		printString("  Queue max: ")
		printHex(uint64(maxSize))
		printString(" (using ")
		printHex(uint64(queueSize))
		printString(")\r\n")
	}

	// CRITICAL: Write QUEUE_NUM before QUEUE_PFN (triggers virtio_queue_update_rings in QEMU)
	// This was the missing step that prevented device from activating!
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_NUM)) = queueSize
	memoryBarrier()

	// Check device status before queue configuration
	statusBefore := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS))
	printString("  Device status before queue config: ")
	printHex(uint64(statusBefore))
	printString("\r\n")

	// Get descriptor table address (must be 4KB-aligned) and convert to PFN
	descAddr := uint64(uintptr(unsafe.Pointer(virtqDescTable)))
	queuePFN := uint32(descAddr >> VIRTIO_MMIO_PAGE_SHIFT)

	// Verify alignment - must be 4KB aligned
	if (descAddr & 0xFFF) != 0 {
		printString("ERROR: Queue not 4KB aligned: ")
		printHex(descAddr)
		printString("\r\n")
		for {}
	}

	// Queue activation: version 1 vs version 2 use different registers
	// Version 1: QUEUE_ALIGN + QUEUE_PFN (page frame number)
	// Version 2: QUEUE_DESC/AVAIL/USED split addresses + QUEUE_READY

	// For debug output
	availAddr := uint64(uintptr(unsafe.Pointer(virtqAvailRing)))
	usedAddr := uint64(uintptr(unsafe.Pointer(virtqUsedRing)))

	if virtioMMIOVersion == 1 {
		// VirtIO MMIO v1 (legacy) queue activation sequence:
		// 1. QUEUE_SEL (already done above)
		// 2. QUEUE_NUM (already done above)
		// 3. QUEUE_ALIGN (set alignment)
		// 4. QUEUE_PFN (activate queue)

		printString("  Using v1 registers (QUEUE_PFN)\r\n")

		// Step 3: QUEUE_ALIGN (must be AFTER QUEUE_NUM, per Linux driver)
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_ALIGN)) = 4096
		memoryBarrier()

		// Step 4: Set queue PFN - this activates the queue in version 1
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_PFN)) = queuePFN
		memoryBarrier()

		// Verify queue was activated
		queuePFNReadback := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_PFN))
		if queuePFNReadback == 0 {
			printString("ERROR: QUEUE_PFN still zero after write\r\n")
			for {}
		}
	} else {
		// VirtIO MMIO v2 (modern) queue activation sequence:
		// 1. QUEUE_SEL (already done above)
		// 2. QUEUE_NUM (already done above)
		// 3. QUEUE_DESC_LOW/HIGH (descriptor table address)
		// 4. QUEUE_AVAIL_LOW/HIGH (available ring address)
		// 5. QUEUE_USED_LOW/HIGH (used ring address)
		// 6. QUEUE_READY = 1 (activate queue)

		printString("  Using v2 registers (QUEUE_READY)\r\n")

		// Step 3: Set descriptor table address (64-bit split into low/high)
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_DESC_LOW)) = uint32(descAddr & 0xFFFFFFFF)
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_DESC_HIGH)) = uint32(descAddr >> 32)
		memoryBarrier()

		// Step 4: Set available ring address (64-bit split into low/high)
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_AVAIL_LOW)) = uint32(availAddr & 0xFFFFFFFF)
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_AVAIL_HIGH)) = uint32(availAddr >> 32)
		memoryBarrier()

		// Step 5: Set used ring address (64-bit split into low/high)
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_USED_LOW)) = uint32(usedAddr & 0xFFFFFFFF)
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_USED_HIGH)) = uint32(usedAddr >> 32)
		memoryBarrier()

		// Step 6: Activate the queue by setting QUEUE_READY to 1
		*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_READY)) = 1
		memoryBarrier()

		// Verify queue was activated
		queueReady := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_READY))
		if queueReady == 0 {
			printString("ERROR: QUEUE_READY still zero after write\r\n")
			for {}
		}
	}

	// Check device status after queue configuration
	statusAfter := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS))
	printString("  Device status after queue config: ")
	printHex(uint64(statusAfter))
	printString("\r\n")

	// Verify device state after initialization
	queueSel := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_SEL))
	deviceStatus := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS))
	intrStatus := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_INTERRUPT_STATUS))

	// Verify device didn't set FAILED bit
	if (deviceStatus & VIRTIO_STATUS_FAILED) != 0 {
		printString("ERROR: Device set FAILED bit during queue init\r\n")
		for {}
	}

	virtqInitialized = true
	printString("VirtIO virtqueue initialized (MMIO v")
	printHex(uint64(virtioMMIOVersion))
	printString(")\r\n")
	printString("  Desc table: ")
	printHex(descAddr)
	printString("\r\n  Avail ring: ")
	printHex(availAddr)
	printString("\r\n  Used ring:  ")
	printHex(usedAddr)
	printString("\r\n  ")

	if virtioMMIOVersion == 1 {
		// V1: Show QUEUE_PFN
		queuePFNReadback := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_PFN))
		printString("QUEUE_PFN=")
		printHex(uint64(queuePFN))
		printString(" (readback: ")
		printHex(uint64(queuePFNReadback))
		printString(")")
	} else {
		// V2: Show QUEUE_READY
		queueReady := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_READY))
		printString("QUEUE_READY=")
		printHex(uint64(queueReady))
	}

	printString("\r\n  QUEUE_SEL=")
	printHex(uint64(queueSel))
	printString(" STATUS=")
	printHex(uint64(deviceStatus))
	printString(" INTR=")
	printHex(uint64(intrStatus))
	printString("\r\n")
}

// initVirtqueue initializes the VirtIO virtqueue if not already done.
// This is a wrapper that prevents re-initialization.
func initVirtqueue() {
	if virtqInitialized {
		return
	}
	initVirtqueueDuringHandshake()
}

// readBlockVirtIO performs a synchronous block read using VirtIO.
func readBlockVirtIO(lba uint64, buf []byte) {
	if !virtqInitialized {
		initVirtqueue()
	}

	if len(buf) != 512 {
		printString("ERROR: VirtIO only supports 512-byte reads\r\n")
		for {}
	}

	// Set up request
	virtioReq.reqType = VIRTIO_BLK_T_IN
	virtioReq.reserved = 0
	virtioReq.sector = lba
	virtioReq.status = 0xFF // Will be overwritten by device

	// Verbose debug output disabled for speed
	// printString("  Request setup: type=")
	// printHex(uint64(virtioReq.reqType))
	// printString(" sector=")
	// printHex(virtioReq.sector)
	// printString("\r\n")
	//
	// // Debug: Print descriptor chain
	// printString("  Desc[0]: addr=")
	// printHex(virtqDescTable[0].addr)
	// printString(" len=")
	// printHex(uint64(virtqDescTable[0].len))
	// printString(" flags=")
	// printHex(uint64(virtqDescTable[0].flags))
	// printString("\r\n  Desc[1]: addr=")
	// printHex(virtqDescTable[1].addr)
	// printString(" len=")
	// printHex(uint64(virtqDescTable[1].len))
	// printString(" flags=")
	// printHex(uint64(virtqDescTable[1].flags))
	// printString("\r\n  Desc[2]: addr=")
	// printHex(virtqDescTable[2].addr)
	// printString(" len=")
	// printHex(uint64(virtqDescTable[2].len))
	// printString(" flags=")
	// printHex(uint64(virtqDescTable[2].flags))
	// printString("\r\n")

	// Memory barrier: ensure request is fully written before updating avail ring
	memoryBarrier()

	// Add to available ring
	idx := virtqAvailRing.idx
	virtqAvailRing.ring[idx%16] = 0 // Use descriptor 0
	virtqAvailRing.idx = idx + 1

	// Verbose debug output disabled for speed
	// printString("  Avail ring: idx=")
	// printHex(uint64(idx))
	// printString(" -> ")
	// printHex(uint64(virtqAvailRing.idx))
	// printString("\r\n")

	// Memory barrier: ensure avail.idx is visible before notifying device
	memoryBarrier()

	// Notify device
	base := virtioMMIOBase
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_NOTIFY)) = 0 // Queue 0

	// Memory barrier: ensure notification is sent before polling
	memoryBarrier()

	// Verify device state immediately after notification
	// Verbose debug output disabled for speed
	// postNotifyStatus := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS))
	// postNotifyIntr := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_INTERRUPT_STATUS))
	//
	// printString("  Device notified, polling...\r\n")
	// printString("    Before: used.idx=")
	// printHex(uint64(virtqUsedRing.idx))
	// printString(" last=")
	// printHex(uint64(lastUsedIdx))
	// printString("\r\n    Post-notify: STATUS=")
	// printHex(uint64(postNotifyStatus))
	// printString(" INTR=")
	// printHex(uint64(postNotifyIntr))
	// printString("\r\n")

	// Poll for completion
	timeout := 1000000
	for timeout > 0 {
		if virtqUsedRing.idx != lastUsedIdx {
			break
		}
		timeout--
	}

	// Check device state after polling
	// Verbose debug output disabled for speed
	// intrStatus := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_INTERRUPT_STATUS))
	// deviceStatus := *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_STATUS))
	//
	// printString("  After poll: used.idx=")
	// printHex(uint64(virtqUsedRing.idx))
	// printString(" timeout=")
	// printHex(uint64(timeout))
	// printString("\r\n  INTR_STATUS=")
	// printHex(uint64(intrStatus))
	// printString(" STATUS=")
	// printHex(uint64(deviceStatus))
	// printString("\r\n")

	if timeout == 0 {
		printString("ERROR: VirtIO block read timeout\r\n")
		for {}
	}

	// Acknowledge interrupt by writing to INTERRUPT_STATUS (clears it)
	// This is CRITICAL - without this, the device won't signal future interrupts
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_INTERRUPT_STATUS)) = 1
	memoryBarrier()

	// Check status
	if virtioReq.status != 0 {
		printString("ERROR: VirtIO block read failed, status=")
		printHex(uint64(virtioReq.status))
		printString("\r\n")
		for {}
	}

	// Copy data to output buffer
	copy(buf, virtioReq.data[:])

	// Update last used index
	lastUsedIdx = virtqUsedRing.idx
}

// readBlockVirtIOBulk reads multiple consecutive sectors using a single VirtIO request.
// The buf must be a multiple of 512 bytes. The data is read directly into buf,
// which must be at a valid physical address (identity-mapped in diplomat's page tables).
// Maximum read size is limited by the VirtIO spec; typically 128 sectors (64KB) is safe.
func readBlockVirtIOBulk(lba uint64, buf []byte) {
	if !virtqInitialized {
		initVirtqueue()
	}

	numSectors := len(buf) / 512
	if numSectors == 0 || len(buf)%512 != 0 {
		printString("ERROR: VirtIO bulk read requires multiple of 512 bytes\r\n")
		for {}
	}

	// Fall back to single-sector reads if only 1 sector
	if numSectors == 1 {
		readBlockVirtIO(lba, buf)
		return
	}

	// Set up request header
	virtioReq.reqType = VIRTIO_BLK_T_IN
	virtioReq.reserved = 0
	virtioReq.sector = lba
	virtioReq.status = 0xFF

	// Temporarily modify descriptor 1 to point to the caller's buffer
	// Save original values
	origAddr := virtqDescTable[1].addr
	origLen := virtqDescTable[1].len

	virtqDescTable[1].addr = uint64(uintptr(unsafe.Pointer(&buf[0])))
	virtqDescTable[1].len = uint32(len(buf))

	// Memory barrier: ensure descriptors are visible before submitting
	memoryBarrier()

	// Submit request
	idx := virtqAvailRing.idx
	virtqAvailRing.ring[idx%16] = 0
	virtqAvailRing.idx = idx + 1

	memoryBarrier()

	// Notify device
	base := virtioMMIOBase
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_QUEUE_NOTIFY)) = 0

	memoryBarrier()

	// Poll for completion
	timeout := 10000000 // Longer timeout for bulk reads
	for timeout > 0 {
		if virtqUsedRing.idx != lastUsedIdx {
			break
		}
		timeout--
	}

	if timeout == 0 {
		printString("ERROR: VirtIO bulk read timeout, lba=")
		printHex(lba)
		printString(" sectors=")
		printHex(uint64(numSectors))
		printString("\r\n")
		for {}
	}

	// Acknowledge interrupt
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_INTERRUPT_STATUS)) = 1
	memoryBarrier()

	// Check status
	if virtioReq.status != 0 {
		printString("ERROR: VirtIO bulk read failed, status=")
		printHex(uint64(virtioReq.status))
		printString("\r\n")
		for {}
	}

	// Restore descriptor 1 to original (for single-sector reads)
	virtqDescTable[1].addr = origAddr
	virtqDescTable[1].len = origLen

	lastUsedIdx = virtqUsedRing.idx
}
