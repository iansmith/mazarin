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
	VIRTIO_MMIO_QUEUE_READY         = 0x044
	VIRTIO_MMIO_QUEUE_NOTIFY        = 0x050
	VIRTIO_MMIO_INTERRUPT_STATUS    = 0x060
	VIRTIO_MMIO_INTERRUPT_ACK       = 0x064
	VIRTIO_MMIO_STATUS              = 0x070
	VIRTIO_MMIO_CONFIG              = 0x100
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

	// Feature negotiation - accept VIRTIO_F_VERSION_1 (bit 32)
	printString("Features...")
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DEVICE_FEATURES_SEL)) = 0
	_ = *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DEVICE_FEATURES)) // Read low features
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DEVICE_FEATURES_SEL)) = 1
	_ = *(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DEVICE_FEATURES)) // Read high features

	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DRIVER_FEATURES_SEL)) = 0
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DRIVER_FEATURES)) = 0
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DRIVER_FEATURES_SEL)) = 1
	*(*uint32)(unsafe.Pointer(base + VIRTIO_MMIO_DRIVER_FEATURES)) = 1 // VIRTIO_F_VERSION_1

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

	// Set DRIVER_OK
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
// VirtIO Block Device Operations (Stubs for now)
// ============================================================================

// TODO: Implement VirtIO virtqueue setup and block I/O operations
// This will be needed for Tasks #3-4 (FAT32 mount and kmazarin loading)
