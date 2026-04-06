//go:build riscv64

package block

import (
	"unsafe"

	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/device/virtio"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/shared/constants"
)

// RISC-V QEMU virt platform VirtIO MMIO constants
const (
	mmioBasePA    = 0x10001000 // First VirtIO MMIO slot physical address
	mmioStride    = 0x1000    // Stride between VirtIO MMIO slots
	mmioMaxSlots  = 8         // Number of VirtIO MMIO slots
	mmioMagic     = 0x74726976 // "virt" in little-endian

	// VirtIO MMIO register offsets
	mmioMagicValue        = 0x000
	mmioVersion           = 0x004
	mmioDeviceID          = 0x008
	mmioVendorID          = 0x00C
	mmioDeviceFeatures    = 0x010
	mmioDeviceFeaturesSel = 0x014
	mmioDriverFeatures    = 0x020
	mmioDriverFeaturesSel = 0x024
	mmioQueueSel          = 0x030
	mmioQueueNumMax       = 0x034
	mmioQueueNum          = 0x038
	mmioQueueReady        = 0x044
	mmioQueueNotify       = 0x050
	mmioInterruptStatus   = 0x060
	mmioInterruptACK      = 0x064
	mmioStatus            = 0x070
	mmioQueueDescLow      = 0x080
	mmioQueueDescHigh     = 0x084
	mmioQueueAvailLow     = 0x090
	mmioQueueAvailHigh    = 0x094
	mmioQueueUsedLow      = 0x0A0
	mmioQueueUsedHigh     = 0x0A4
	mmioConfig            = 0x100

	// VirtIO device status bits
	mmioStatusAcknowledge = 1
	mmioStatusDriver      = 2
	mmioStatusDriverOK    = 4
	mmioStatusFeaturesOK  = 8
	mmioStatusFailed      = 128

	// VirtIO device type
	mmioDeviceTypeBlock = 2
)

// findVirtIOBlockMMIO scans VirtIO MMIO slots on RISC-V QEMU virt for a block device.
// Returns true if found, sets virtioBlockDevice.MMIOBase to the VA.
func findVirtIOBlockMMIO() bool {

	for i := uintptr(0); i < mmioMaxSlots; i++ {
		pa := uintptr(mmioBasePA) + i*mmioStride
		va := pa + constants.KernelMMIOOffset

		magic := *(*uint32)(unsafe.Pointer(va + mmioMagicValue))
		if magic != mmioMagic {
			continue
		}

		version := *(*uint32)(unsafe.Pointer(va + mmioVersion))
		if version != 2 {
			// Only support modern (v2) MMIO transport in kmazarin
			continue
		}

		deviceID := *(*uint32)(unsafe.Pointer(va + mmioDeviceID))
		if deviceID == mmioDeviceTypeBlock {
			virtioBlockDevice.MMIOBase = va
			return true
		}
	}

	return false
}

// virtioBlockInitMMIO performs the VirtIO MMIO v2 handshake for the block device.
func virtioBlockInitMMIO() bool {
	base := virtioBlockDevice.MMIOBase
	if base == 0 {
		return false
	}


	// Step 1: Reset device
	*(*uint32)(unsafe.Pointer(base + mmioStatus)) = 0
	asm.Dsb()

	// Step 2: Acknowledge
	*(*uint32)(unsafe.Pointer(base + mmioStatus)) = mmioStatusAcknowledge
	asm.Dsb()

	// Step 3: Driver
	*(*uint32)(unsafe.Pointer(base + mmioStatus)) = mmioStatusAcknowledge | mmioStatusDriver
	asm.Dsb()

	// Step 4: Feature negotiation
	// Read device features (word 0)
	*(*uint32)(unsafe.Pointer(base + mmioDeviceFeaturesSel)) = 0
	asm.Dsb()
	_ = *(*uint32)(unsafe.Pointer(base + mmioDeviceFeatures))

	// Read device features (word 1)
	*(*uint32)(unsafe.Pointer(base + mmioDeviceFeaturesSel)) = 1
	asm.Dsb()
	_ = *(*uint32)(unsafe.Pointer(base + mmioDeviceFeatures))

	// Accept VIRTIO_F_VERSION_1 (bit 32 = word 1 bit 0)
	*(*uint32)(unsafe.Pointer(base + mmioDriverFeaturesSel)) = 0
	*(*uint32)(unsafe.Pointer(base + mmioDriverFeatures)) = 0
	asm.Dsb()
	*(*uint32)(unsafe.Pointer(base + mmioDriverFeaturesSel)) = 1
	*(*uint32)(unsafe.Pointer(base + mmioDriverFeatures)) = 1 // VIRTIO_F_VERSION_1
	asm.Dsb()

	// Step 5: Features OK
	*(*uint32)(unsafe.Pointer(base + mmioStatus)) = mmioStatusAcknowledge | mmioStatusDriver | mmioStatusFeaturesOK
	asm.Dsb()

	status := *(*uint32)(unsafe.Pointer(base + mmioStatus))
	if (status & mmioStatusFeaturesOK) == 0 {
		klog.Errf("[VirtIO Block] MMIO: device rejected features\n")
		return false
	}

	// Step 6: Allocate DMA page for virtqueue
	queuePagePA := kmem.AllocKernelFrame()
	if queuePagePA == 0 {
		klog.Errf("[VirtIO Block] MMIO: failed to allocate queue DMA page\n")
		return false
	}
	queuePageVA := queuePagePA + constants.KernelMMIOOffset

	// Use queue size 128 (same as PCI path)
	queueSize := uint16(128)

	// Select queue 0
	*(*uint32)(unsafe.Pointer(base + mmioQueueSel)) = 0
	asm.Dsb()

	// Check max queue size
	maxSize := *(*uint32)(unsafe.Pointer(base + mmioQueueNumMax))
	if maxSize == 0 {
		klog.Errf("[VirtIO Block] MMIO: queue 0 not available\n")
		return false
	}
	if uint32(queueSize) > maxSize {
		queueSize = uint16(maxSize)
	}

	// Initialize virtqueue on DMA page
	endOff := virtio.VirtqueueInitOnDMAPage(&virtioBlockDevice.Queue, queueSize, queuePagePA, queuePageVA, 0)
	if endOff == 0 {
		klog.Errf("[VirtIO Block] MMIO: failed to init queue on DMA page\n")
		return false
	}

	vq := &virtioBlockDevice.Queue

	// Write queue size
	*(*uint32)(unsafe.Pointer(base + mmioQueueNum)) = uint32(queueSize)
	asm.Dsb()

	// Write descriptor table address
	*(*uint32)(unsafe.Pointer(base + mmioQueueDescLow)) = uint32(vq.DescPA & 0xFFFFFFFF)
	*(*uint32)(unsafe.Pointer(base + mmioQueueDescHigh)) = uint32(vq.DescPA >> 32)
	asm.Dsb()

	// Write available ring address
	*(*uint32)(unsafe.Pointer(base + mmioQueueAvailLow)) = uint32(vq.AvailPA & 0xFFFFFFFF)
	*(*uint32)(unsafe.Pointer(base + mmioQueueAvailHigh)) = uint32(vq.AvailPA >> 32)
	asm.Dsb()

	// Write used ring address
	*(*uint32)(unsafe.Pointer(base + mmioQueueUsedLow)) = uint32(vq.UsedPA & 0xFFFFFFFF)
	*(*uint32)(unsafe.Pointer(base + mmioQueueUsedHigh)) = uint32(vq.UsedPA >> 32)
	asm.Dsb()

	// Activate queue
	*(*uint32)(unsafe.Pointer(base + mmioQueueReady)) = 1
	asm.Dsb()

	// Verify queue activation
	ready := *(*uint32)(unsafe.Pointer(base + mmioQueueReady))
	if ready == 0 {
		klog.Errf("[VirtIO Block] MMIO: queue ready failed\n")
		return false
	}

	// Step 7: Driver OK
	*(*uint32)(unsafe.Pointer(base + mmioStatus)) = mmioStatusAcknowledge | mmioStatusDriver | mmioStatusFeaturesOK | mmioStatusDriverOK
	asm.Dsb()

	// Check for FAILED bit
	finalStatus := *(*uint32)(unsafe.Pointer(base + mmioStatus))
	if (finalStatus & mmioStatusFailed) != 0 {
		klog.Errf("[VirtIO Block] MMIO: device set FAILED bit\n")
		return false
	}

	return true
}

// virtioBlockReadConfigMMIO reads the block device configuration from MMIO config space.
func virtioBlockReadConfigMMIO() bool {
	base := virtioBlockDevice.MMIOBase
	if base == 0 {
		return false
	}

	// Device-specific config starts at MMIO base + 0x100
	cfgBase := base + mmioConfig

	// Read capacity (uint64 at offset 0)
	capacityLow := *(*uint32)(unsafe.Pointer(cfgBase + 0))
	capacityHigh := *(*uint32)(unsafe.Pointer(cfgBase + 4))
	virtioBlockDevice.Capacity = uint64(capacityLow) | (uint64(capacityHigh) << 32)

	// Read block size (uint32 at offset 20, BlkSize field)
	blkSize := *(*uint32)(unsafe.Pointer(cfgBase + 20))
	if blkSize == 0 {
		blkSize = 512
	}
	virtioBlockDevice.BlockSizeBytes = blkSize

	return true
}
