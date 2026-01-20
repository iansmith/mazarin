package virtio

import (
	"kmazarin/asm"
	"kmazarin/console"
	device_virtio "kmazarin/device/virtio"
	"kmazarin/deviceapi"
	"kmazarin/dtb"
	"kmazarin/kmem"
	"unsafe"
)

// VirtIO Block Device ID
const DeviceIDBlock = 2

// VirtIO block request types
const (
	VIRTIO_BLK_T_IN  = 0 // Read
	VIRTIO_BLK_T_OUT = 1 // Write
)

// VirtIO block status values
const (
	VIRTIO_BLK_S_OK     = 0
	VIRTIO_BLK_S_IOERR  = 1
	VIRTIO_BLK_S_UNSUPP = 2
)

// VirtIO block config space offsets (from Config base at 0x100)
const (
	BlockConfigCapacity = 0x00 // 64-bit: number of 512-byte sectors
)

// virtioBlkReqHeader is the request header sent to the device
type virtioBlkReqHeader struct {
	Type     uint32 // VIRTIO_BLK_T_IN or VIRTIO_BLK_T_OUT
	Reserved uint32 // Reserved, must be 0
	Sector   uint64 // Sector number
}

// BlockDriver implements deviceapi.Discoverable for VirtIO Block
type BlockDriver struct{}

func (d *BlockDriver) Compatible() []string {
	return []string{"virtio,mmio"}
}

func (d *BlockDriver) Probe(node *dtb.Node) bool {
	if node.Reg == nil || len(node.Reg) == 0 {
		return false
	}
	return true
}

func (d *BlockDriver) Init(node *dtb.Node) (deviceapi.Closable, error) {
	var irq uint32
	if node.Interrupts != nil && len(node.Interrupts) > 0 {
		irq = node.Interrupts[0]
	}

	reg := node.Reg[0]

	// Map MMIO region before accessing hardware
	if err := kmem.MapDeviceMMIO(reg.Address, reg.Size); err != nil {
		return nil, err
	}

	// Convert physical address to high-memory kernel address
	const KernelMMIOOffset = 0xFFFFFFFF00000000

	transport := &MMIOTransport{
		baseAddr: reg.Address + KernelMMIOOffset,
		irq:      irq,
	}

	// Check if this is a block device
	deviceID := transport.ReadDeviceType()
	console.KWriteString("[VirtIO Block] Checking device at ")
	console.KPrintHex64(uint64(reg.Address))
	console.KWriteString(" type=")
	console.KPrintHex64(uint64(deviceID))
	console.KWriteString("\r\n")
	if deviceID != DeviceIDBlock {
		return nil, deviceapi.ErrNotMyDevice
	}

	blk := &BlockDevice{
		transport: transport,
	}

	// Initialize the device
	if err := blk.init(); err != nil {
		return nil, err
	}

	return blk, nil
}

// BlockDevice implements deviceapi.BlockDevice
type BlockDevice struct {
	transport *MMIOTransport
	vq        device_virtio.VirtQueue
	capacity  uint64
	version   uint32 // MMIO version (1=legacy, 2=modern)
	// Pre-allocated buffers for requests
	reqHeader virtioBlkReqHeader
	status    [1]byte
}

// Closable implementation
func (b *BlockDevice) Name() string {
	return "virtio-blk"
}

func (b *BlockDevice) Close() error {
	device_virtio.VirtqueueCleanup(&b.vq)
	return b.transport.Reset()
}

// BlockDevice implementation
func (b *BlockDevice) BlockSize() uint64 {
	return 512
}

func (b *BlockDevice) NumBlocks() uint64 {
	return b.capacity
}

// init performs VirtIO initialization sequence
func (b *BlockDevice) init() error {
	console.KWriteString("[VirtIO Block] Initializing device...\r\n")

	// Check MMIO magic and version
	magic := b.transport.ReadReg(MagicValue)
	version := b.transport.ReadReg(Version)
	console.KWriteString("[VirtIO Block] Magic=")
	console.KPrintHex64(uint64(magic))
	console.KWriteString(" Version=")
	console.KPrintHex64(uint64(version))
	console.KWriteString("\r\n")

	if magic != 0x74726976 {
		return &VirtIOError{"bad magic value"}
	}

	// Store version for queue setup
	b.version = version

	// 1. Reset device
	b.transport.Reset()

	// For legacy VirtIO MMIO, set guest page size before any queue operations
	if version == 1 {
		b.transport.WriteReg(GuestPageSize, 4096)
	}

	// 2. Set ACKNOWLEDGE status
	b.transport.AddStatus(StatusAcknowledge)

	// 3. Set DRIVER status
	b.transport.AddStatus(StatusDriver)

	// 4. Read and negotiate features
	if version == 1 {
		// Legacy VirtIO MMIO - no feature selectors, just bits 0-31
		devFeatures := b.transport.ReadReg(DeviceFeatures)
		console.KWriteString("[VirtIO Block] Device features (legacy): ")
		console.KPrintHex64(uint64(devFeatures))
		console.KWriteString("\r\n")

		// Accept no special features for basic block
		b.transport.WriteReg(DriverFeatures, 0)
	} else {
		// Modern VirtIO MMIO (version 2)
		b.transport.WriteReg(DeviceFeaturesSel, 0)
		devFeatures0 := b.transport.ReadReg(DeviceFeatures)
		b.transport.WriteReg(DeviceFeaturesSel, 1)
		devFeatures1 := b.transport.ReadReg(DeviceFeatures)

		console.KWriteString("[VirtIO Block] Device features: lo=")
		console.KPrintHex64(uint64(devFeatures0))
		console.KWriteString(" hi=")
		console.KPrintHex64(uint64(devFeatures1))
		console.KWriteString("\r\n")

		// Accept VIRTIO_F_VERSION_1 (bit 32 = bit 0 in high word) if offered
		drvFeatures1 := uint32(0)
		if devFeatures1&1 != 0 {
			drvFeatures1 = 1
		}

		b.transport.WriteReg(DriverFeaturesSel, 0)
		b.transport.WriteReg(DriverFeatures, 0)
		b.transport.WriteReg(DriverFeaturesSel, 1)
		b.transport.WriteReg(DriverFeatures, drvFeatures1)

		// 5. Set FEATURES_OK status (modern only)
		b.transport.AddStatus(StatusFeaturesOK)

		// 6. Verify FEATURES_OK is still set
		if b.transport.GetStatus()&StatusFeaturesOK == 0 {
			b.transport.AddStatus(StatusFailed)
			return ErrFeatureNegotiation
		}
	}

	// 7. Setup virtqueue 0 (requestq)
	b.transport.WriteReg(QueueSel, 0)

	maxQueueSize := uint16(b.transport.ReadReg(QueueNumMax))
	if maxQueueSize == 0 {
		b.transport.AddStatus(StatusFailed)
		return &VirtIOError{"no queue available"}
	}

	queueSize := uint16(64)
	if maxQueueSize < queueSize {
		queueSize = maxQueueSize
	}

	// Set queue size in device
	b.transport.WriteReg(QueueNum, uint32(queueSize))

	if version == 1 {
		// Legacy VirtIO MMIO - use contiguous queue with QueuePFN
		if !device_virtio.VirtqueueInitLegacy(&b.vq, queueSize) {
			b.transport.AddStatus(StatusFailed)
			return &VirtIOError{"failed to init virtqueue"}
		}

		// Get physical address and set QueuePFN (page frame number = addr >> 12)
		queuePhys := device_virtio.VirtqueueGetPhysicalAddr(b.vq.DescTable)
		pfn := uint32(queuePhys >> 12)

		console.KWriteString("[VirtIO Block] Legacy queue PFN=")
		console.KPrintHex64(uint64(pfn))
		console.KWriteString(" (phys=")
		console.KPrintHex64(queuePhys)
		console.KWriteString(")\r\n")

		b.transport.WriteReg(QueueAlign, 4096)
		b.transport.WriteReg(QueuePFN, pfn)
	} else {
		// Modern VirtIO MMIO - use separate queue addresses
		if !device_virtio.VirtqueueInit(&b.vq, queueSize) {
			b.transport.AddStatus(StatusFailed)
			return &VirtIOError{"failed to init virtqueue"}
		}

		descPhys := device_virtio.VirtqueueGetPhysicalAddr(b.vq.DescTable)
		availPhys := device_virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(b.vq.Available))
		usedPhys := device_virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(b.vq.Used))

		console.KWriteString("[VirtIO Block] Queue desc=")
		console.KPrintHex64(uint64(descPhys))
		console.KWriteString(" avail=")
		console.KPrintHex64(uint64(availPhys))
		console.KWriteString(" used=")
		console.KPrintHex64(uint64(usedPhys))
		console.KWriteString("\r\n")

		b.transport.WriteReg(QueueDescLow, uint32(descPhys))
		b.transport.WriteReg(QueueDescHigh, uint32(descPhys>>32))
		b.transport.WriteReg(QueueDriverLow, uint32(availPhys))
		b.transport.WriteReg(QueueDriverHigh, uint32(availPhys>>32))
		b.transport.WriteReg(QueueDeviceLow, uint32(usedPhys))
		b.transport.WriteReg(QueueDeviceHigh, uint32(usedPhys>>32))

		// Enable queue (modern only)
		b.transport.WriteReg(QueueReady, 1)
	}

	// 8. Set DRIVER_OK status
	b.transport.AddStatus(StatusDriverOK)

	console.KWriteString("[VirtIO Block] Device status after init: ")
	console.KPrintHex64(uint64(b.transport.GetStatus()))
	console.KWriteString("\r\n")

	// 9. Read capacity from config space
	capLow := b.transport.ReadReg(Config + BlockConfigCapacity)
	capHigh := b.transport.ReadReg(Config + BlockConfigCapacity + 4)
	b.capacity = uint64(capLow) | (uint64(capHigh) << 32)

	return nil
}

// ReadBlock reads a single block at the given LBA
func (b *BlockDevice) ReadBlock(lba uint64, buf []byte) error {
	// Debug output disabled to reduce noise
	// console.KWriteString("[VirtIO Block] ReadBlock LBA=")
	// console.KPrintHex64(lba)
	// console.KWriteString(" cap=")
	// console.KPrintHex64(b.capacity)
	// console.KWriteString("\r\n")

	if len(buf) < 512 {
		console.KWriteString("[VirtIO Block] ERROR: buffer too small\r\n")
		return &VirtIOError{"buffer too small"}
	}
	if lba >= b.capacity {
		console.KWriteString("[VirtIO Block] ERROR: lba out of range\r\n")
		return &VirtIOError{"lba out of range"}
	}

	// Touch the buffer to ensure it's mapped (triggers page fault if needed)
	// This is required for stack-allocated buffers that haven't been touched yet
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 0
	}
	buf[len(buf)-1] = 0

	// Setup request header
	b.reqHeader.Type = VIRTIO_BLK_T_IN
	b.reqHeader.Reserved = 0
	b.reqHeader.Sector = lba

	// Setup status byte
	b.status[0] = 0xFF // Will be overwritten by device

	// Build 3-descriptor chain: header -> data -> status
	headerPhys := device_virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&b.reqHeader))
	dataPhys := device_virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&buf[0]))
	statusPhys := device_virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&b.status[0]))

	// Debug disabled
	// console.KWriteString("[VirtIO Block] Req hdr=")
	// console.KPrintHex64(uint64(headerPhys))
	// console.KWriteString(" data=")
	// console.KPrintHex64(uint64(dataPhys))
	// console.KWriteString(" status=")
	// console.KPrintHex64(uint64(statusPhys))
	// console.KWriteString("\r\n")

	// Allocate descriptors
	headerDesc := device_virtio.VirtqueueAddDesc(&b.vq, headerPhys,
		uint32(unsafe.Sizeof(b.reqHeader)), device_virtio.VIRTQ_DESC_F_NEXT, 0xFFFF)
	if headerDesc == 0xFFFF {
		return &VirtIOError{"no descriptors available"}
	}

	dataDesc := device_virtio.VirtqueueAddDesc(&b.vq, dataPhys,
		512, device_virtio.VIRTQ_DESC_F_NEXT|device_virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if dataDesc == 0xFFFF {
		device_virtio.VirtqueueFreeDescChain(&b.vq, headerDesc)
		return &VirtIOError{"no descriptors available"}
	}

	statusDesc := device_virtio.VirtqueueAddDesc(&b.vq, statusPhys,
		1, device_virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if statusDesc == 0xFFFF {
		device_virtio.VirtqueueFreeDescChain(&b.vq, headerDesc)
		device_virtio.VirtqueueFreeDescChain(&b.vq, dataDesc)
		return &VirtIOError{"no descriptors available"}
	}

	// Link descriptors
	descSize := unsafe.Sizeof(device_virtio.VirtQDesc{})
	headerDescPtr := device_virtio.CastToPointer[device_virtio.VirtQDesc](
		device_virtio.PointerToUintptr(b.vq.DescTable) + uintptr(headerDesc)*descSize)
	headerDescPtr.Next = dataDesc

	dataDescPtr := device_virtio.CastToPointer[device_virtio.VirtQDesc](
		device_virtio.PointerToUintptr(b.vq.DescTable) + uintptr(dataDesc)*descSize)
	dataDescPtr.Next = statusDesc

	// Add to available ring
	device_virtio.VirtqueueAddToAvailable(&b.vq, headerDesc)

	// Cache maintenance
	descTableSize := uintptr(b.vq.QueueSize) * descSize
	asm.CleanDCacheRange(device_virtio.PointerToUintptr(b.vq.DescTable), descTableSize)
	availSize := uintptr(4 + b.vq.QueueSize*2 + 2)
	asm.CleanDCacheRange(device_virtio.PointerToUintptr(unsafe.Pointer(b.vq.Available)), availSize)
	asm.DmaWmb()

	// Notify device
	b.transport.WriteReg(QueueNotify, 0)

	// Debug disabled
	// console.KWriteString("[VirtIO Block] Avail idx=")
	// console.KPrintHex64(uint64(b.vq.Available.Idx))
	// console.KWriteString(" Last used=")
	// console.KPrintHex64(uint64(b.vq.LastUsedIdx))
	// console.KWriteString("\r\n")

	// Poll for completion
	maxWait := 1000000
	for i := 0; i < maxWait; i++ {
		asm.Dsb()
		if device_virtio.VirtqueueHasUsed(&b.vq) {
			break
		}
	}

	if !device_virtio.VirtqueueHasUsed(&b.vq) {
		console.KWriteString("[VirtIO Block] Used idx=")
		console.KPrintHex64(uint64(b.vq.Used.Idx))
		console.KWriteString("\r\n")
		console.KWriteString("[VirtIO Block] ERROR: timeout waiting for block read\r\n")
		return &VirtIOError{"timeout waiting for block read"}
	}

	// DMA read barrier
	asm.DmaRmb()

	// Get used descriptor
	usedDesc, _ := device_virtio.VirtqueueGetUsed(&b.vq)
	if usedDesc == 0xFFFF {
		return &VirtIOError{"failed to get used descriptor"}
	}

	// Free descriptor chain
	device_virtio.VirtqueueFreeDescChain(&b.vq, uint16(usedDesc))

	// Check status
	if b.status[0] != VIRTIO_BLK_S_OK {
		return &VirtIOError{"block read failed"}
	}

	return nil
}

// WriteBlock writes a single block at the given LBA
func (b *BlockDevice) WriteBlock(lba uint64, buf []byte) error {
	if len(buf) < 512 {
		return &VirtIOError{"buffer too small"}
	}
	if lba >= b.capacity {
		return &VirtIOError{"lba out of range"}
	}

	// Setup request header
	b.reqHeader.Type = VIRTIO_BLK_T_OUT
	b.reqHeader.Reserved = 0
	b.reqHeader.Sector = lba

	// Setup status byte
	b.status[0] = 0xFF

	// Build 3-descriptor chain: header -> data -> status
	headerPhys := device_virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&b.reqHeader))
	dataPhys := device_virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&buf[0]))
	statusPhys := device_virtio.VirtqueueGetPhysicalAddr(unsafe.Pointer(&b.status[0]))

	// Allocate descriptors (data is read-only for writes)
	headerDesc := device_virtio.VirtqueueAddDesc(&b.vq, headerPhys,
		uint32(unsafe.Sizeof(b.reqHeader)), device_virtio.VIRTQ_DESC_F_NEXT, 0xFFFF)
	if headerDesc == 0xFFFF {
		return &VirtIOError{"no descriptors available"}
	}

	dataDesc := device_virtio.VirtqueueAddDesc(&b.vq, dataPhys,
		512, device_virtio.VIRTQ_DESC_F_NEXT, 0xFFFF) // No WRITE flag for write operation
	if dataDesc == 0xFFFF {
		device_virtio.VirtqueueFreeDescChain(&b.vq, headerDesc)
		return &VirtIOError{"no descriptors available"}
	}

	statusDesc := device_virtio.VirtqueueAddDesc(&b.vq, statusPhys,
		1, device_virtio.VIRTQ_DESC_F_WRITE, 0xFFFF)
	if statusDesc == 0xFFFF {
		device_virtio.VirtqueueFreeDescChain(&b.vq, headerDesc)
		device_virtio.VirtqueueFreeDescChain(&b.vq, dataDesc)
		return &VirtIOError{"no descriptors available"}
	}

	// Link descriptors
	descSize := unsafe.Sizeof(device_virtio.VirtQDesc{})
	headerDescPtr := device_virtio.CastToPointer[device_virtio.VirtQDesc](
		device_virtio.PointerToUintptr(b.vq.DescTable) + uintptr(headerDesc)*descSize)
	headerDescPtr.Next = dataDesc

	dataDescPtr := device_virtio.CastToPointer[device_virtio.VirtQDesc](
		device_virtio.PointerToUintptr(b.vq.DescTable) + uintptr(dataDesc)*descSize)
	dataDescPtr.Next = statusDesc

	// Add to available ring
	device_virtio.VirtqueueAddToAvailable(&b.vq, headerDesc)

	// Cache maintenance
	descTableSize := uintptr(b.vq.QueueSize) * descSize
	asm.CleanDCacheRange(device_virtio.PointerToUintptr(b.vq.DescTable), descTableSize)
	availSize := uintptr(4 + b.vq.QueueSize*2 + 2)
	asm.CleanDCacheRange(device_virtio.PointerToUintptr(unsafe.Pointer(b.vq.Available)), availSize)
	asm.DmaWmb()

	// Notify device
	b.transport.WriteReg(QueueNotify, 0)

	// Poll for completion
	maxWait := 1000000
	for i := 0; i < maxWait; i++ {
		asm.Dsb()
		if device_virtio.VirtqueueHasUsed(&b.vq) {
			break
		}
	}

	if !device_virtio.VirtqueueHasUsed(&b.vq) {
		return &VirtIOError{"timeout waiting for block write"}
	}

	// DMA read barrier
	asm.DmaRmb()

	// Get used descriptor
	usedDesc, _ := device_virtio.VirtqueueGetUsed(&b.vq)
	if usedDesc == 0xFFFF {
		return &VirtIOError{"failed to get used descriptor"}
	}

	// Free descriptor chain
	device_virtio.VirtqueueFreeDescChain(&b.vq, uint16(usedDesc))

	// Check status
	if b.status[0] != VIRTIO_BLK_S_OK {
		return &VirtIOError{"block write failed"}
	}

	return nil
}
