//go:build qemuvirt && aarch64

package main

import (
	"mazboot/asm"
	"sync/atomic"
	"unsafe"
)

// VirtIO RNG Constants
const (
	VIRTIO_RNG_DEVICE_ID_LEGACY = 0x1005 // Legacy/transitional VirtIO RNG
	VIRTIO_RNG_DEVICE_ID_MODERN = 0x1044 // VirtIO 1.0 RNG device ID

	// RNG has just one virtqueue
	VIRTIO_RNG_REQUESTQ = 0 // Request queue index
)

// VirtIO RNG State
var (
	virtioRNGInitialized uint32  // 1 if RNG is initialized
	rngCommonCfgBase     uintptr // Common config BAR address
	rngNotifyBase        uintptr // Notify BAR address
	rngNotifyOffMult     uint32  // Notify offset multiplier
	rngDeviceCfgBase     uintptr // Device-specific config BAR address
	rngBus               uint8   // PCI bus
	rngSlot              uint8   // PCI slot
	rngFunc              uint8   // PCI function

	// Virtqueue for requesting random data
	rngQueue VirtQueue

	// Buffer for random bytes (statically allocated, 256 bytes)
	rngBuffer     [256]byte
	rngBufferFull uint32 // 1 if buffer has data, 0 if empty
	rngBufferPos  uint32 // Current read position in buffer

	// Static buffers for virtqueue (avoid kmalloc)
	// For queue size 8: descriptors=128 bytes, avail=20 bytes, used=36 bytes
	rngDescTable  [8]VirtQDesc      // 8 * 16 bytes = 128 bytes
	rngAvailRing  [20]byte          // flags(2) + idx(2) + ring[8](16) = 20 bytes
	rngUsedRing   [36]byte          // flags(2) + idx(2) + ring[8](16) + avail_event(2) = 36 bytes (actually 34, but align to 4)
)

// initVirtIORNG initializes the VirtIO RNG device
func initVirtIORNG() bool {
	print("VirtIO RNG: Scanning PCI bus...\r\n")

	// Scan PCI bus for VirtIO RNG device (vendor 0x1AF4, device 0x1044)
	for bus := uint8(0); bus < 4; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			// Read vendor and device ID together (they're in one 32-bit register)
			reg := pciConfigRead32(bus, slot, 0, PCI_VENDOR_ID)
			vendorID := uint16(reg & 0xFFFF)      // Bits 0-15
			deviceID := uint16((reg >> 16) & 0xFFFF) // Bits 16-31

			// Debug: print all non-0xFFFF devices found
			if vendorID != 0xFFFF {
				print("PCI ")
				printHex32(uint32(bus))
				print(":")
				printHex32(uint32(slot))
				print(" vendor=0x")
				printHex32(uint32(vendorID))
				print(" device=0x")
				printHex32(uint32(deviceID))
				print("\r\n")
			}

			// Check for both legacy and modern VirtIO RNG device IDs
			if vendorID == VIRTIO_VENDOR_ID &&
			   (deviceID == VIRTIO_RNG_DEVICE_ID_LEGACY || deviceID == VIRTIO_RNG_DEVICE_ID_MODERN) {
				print("VirtIO RNG: Found at PCI ")
				printHex32(uint32(bus))
				print(":")
				printHex32(uint32(slot))
				print(" (device ID 0x")
				printHex32(uint32(deviceID))
				print(")\r\n")

				// Initialize the device
				if !initVirtIORNGDevice(bus, slot) {
					print("VirtIO RNG: Initialization failed\r\n")
					return false
				}

				atomic.StoreUint32(&virtioRNGInitialized, 1)
				print("VirtIO RNG: Ready\r\n")
				return true
			}
		}
	}

	print("VirtIO RNG: Device not found on PCI bus\r\n")
	return false
}

// initVirtIORNGDevice initializes a VirtIO RNG device at the given PCI location
func initVirtIORNGDevice(bus, slot uint8) bool {
	uartPutsDirect("DEBUG: initVirtIORNGDevice() called\r\n")
	funcNum := uint8(0)
	rngBus = bus
	rngSlot = slot
	rngFunc = funcNum

	uartPutsDirect("DEBUG: About to read PCI command register\r\n")
	// Enable PCI bus mastering and memory access
	command := pciConfigRead32(bus, slot, funcNum, PCI_COMMAND)
	uartPutsDirect("DEBUG: PCI command read complete\r\n")
	command |= 0x06 // Enable memory space (bit 1) and bus master (bit 2)
	uartPutsDirect("DEBUG: About to write PCI command\r\n")
	pciConfigWrite32(bus, slot, funcNum, PCI_COMMAND, command)
	// uartPutcDirect('X')  // Simple marker - BREADCRUMB DISABLED
	// uartPutcDirect('\r')
	// uartPutcDirect('\n')
	uartPutsDirect("DEBUG: PCI command write complete\r\n")

	// Find VirtIO capabilities
	var common, notify, isr, device VirtIOCapabilityInfo

	// Debug: Check if device has capabilities and dump them
	capPtr := pciConfigRead8(bus, slot, funcNum, PCI_CAPABILITIES)
	print("VirtIO RNG: Capability pointer = 0x")
	printHex32(uint32(capPtr))
	print("\r\n")

	// Dump all capabilities
	current := capPtr
	for i := 0; i < 10 && current != 0; i++ {
		capType := pciConfigRead8(bus, slot, funcNum, current)
		nextPtr := pciConfigRead8(bus, slot, funcNum, current+1)
		print("  Cap at 0x")
		printHex32(uint32(current))
		print(": type=0x")
		printHex32(uint32(capType))
		if capType == 0x09 {
			// Vendor-specific - read cfg_type
			cfgType := pciConfigRead8(bus, slot, funcNum, current+3)
			print(" cfg_type=0x")
			printHex32(uint32(cfgType))
		}
		print(" next=0x")
		printHex32(uint32(nextPtr))
		print("\r\n")
		current = nextPtr
	}

	if !pciFindVirtIOCapabilities(bus, slot, funcNum, &common, &notify, &isr, &device) {
		print("VirtIO RNG: Failed to find capabilities\r\n")
		return false
	}

	// Read and allocate BAR for common config (handle 64-bit BARs)
	barOffset := 0x10 + common.Bar*4
	barLow := pciConfigRead32(bus, slot, funcNum, uint8(barOffset))

	var barBase uint64
	is64Bit := (barLow & 0x04) != 0

	if is64Bit {
		barHigh := pciConfigRead32(bus, slot, funcNum, uint8(barOffset+4))
		barBase = (uint64(barHigh) << 32) | uint64(barLow&0xFFFFFFF0)
	} else {
		barBase = uint64(barLow & 0xFFFFFFF0)
	}

	// If BAR is not allocated (base is 0), allocate it manually
	// Use fixed MMIO region: 0x10000000 (256MB, well above RAM and below PCI ECAM)
	if barBase == 0 {
		// Allocate unique addresses for each BAR
		// BAR0: 0x10000000, BAR1: 0x10100000, BAR2: 0x10200000, etc.
		barBase = 0x10000000 + (uint64(common.Bar) * 0x100000)

		print("VirtIO RNG: Allocating BAR")
		printHex32(uint32(common.Bar))
		print(" at 0x")
		printHex64(barBase)
		print("\r\n")

		// Write BAR address to PCI config
		if is64Bit {
			pciConfigWrite32(bus, slot, funcNum, uint8(barOffset), uint32(barBase&0xFFFFFFF0)|0x0C)
			pciConfigWrite32(bus, slot, funcNum, uint8(barOffset+4), uint32(barBase>>32))
		} else {
			pciConfigWrite32(bus, slot, funcNum, uint8(barOffset), uint32(barBase&0xFFFFFFF0))
		}
	}

	rngCommonCfgBase = uintptr(barBase) + uintptr(common.OffsetInBar)

	// Calculate notify base (handle 64-bit BARs)
	notifyBarOffset := 0x10 + notify.Bar*4
	notifyBarLow := pciConfigRead32(bus, slot, funcNum, uint8(notifyBarOffset))

	var notifyBarBase uint64
	isNotify64Bit := (notifyBarLow & 0x04) != 0

	if isNotify64Bit {
		notifyBarHigh := pciConfigRead32(bus, slot, funcNum, uint8(notifyBarOffset+4))
		notifyBarBase = (uint64(notifyBarHigh) << 32) | uint64(notifyBarLow&0xFFFFFFF0)
	} else {
		notifyBarBase = uint64(notifyBarLow & 0xFFFFFFF0)
	}

	// If BAR is not allocated, allocate it manually
	if notifyBarBase == 0 {
		notifyBarBase = 0x10000000 + (uint64(notify.Bar) * 0x100000)

		print("VirtIO RNG: Allocating notify BAR")
		printHex32(uint32(notify.Bar))
		print(" at 0x")
		printHex64(notifyBarBase)
		print("\r\n")

		if isNotify64Bit {
			pciConfigWrite32(bus, slot, funcNum, uint8(notifyBarOffset), uint32(notifyBarBase&0xFFFFFFF0)|0x0C)
			pciConfigWrite32(bus, slot, funcNum, uint8(notifyBarOffset+4), uint32(notifyBarBase>>32))
		} else {
			pciConfigWrite32(bus, slot, funcNum, uint8(notifyBarOffset), uint32(notifyBarBase&0xFFFFFFF0))
		}
	}

	rngNotifyBase = uintptr(notifyBarBase) + uintptr(notify.OffsetInBar)

	print("VirtIO RNG: Common config at 0x")
	printHex64(uint64(rngCommonCfgBase))
	print("\r\n")

	// Reset device
	rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS, 0)

	// Set ACKNOWLEDGE status bit
	rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS, VIRTIO_STATUS_ACKNOWLEDGE)

	// Set DRIVER status bit
	rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS,
		VIRTIO_STATUS_ACKNOWLEDGE|VIRTIO_STATUS_DRIVER)

	// Negotiate features (we don't need any special features for basic RNG)
	// Just accept whatever the device offers
	rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE_SELECT, 0)
	rngWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE, 0)
	rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE_SELECT, 1)
	rngWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_DRIVER_FEATURE, 0)

	// Set FEATURES_OK
	rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS,
		VIRTIO_STATUS_ACKNOWLEDGE|VIRTIO_STATUS_DRIVER|VIRTIO_STATUS_FEATURES_OK)

	// Verify FEATURES_OK is still set
	status := rngReadCommonConfig(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS)
	if (status & VIRTIO_STATUS_FEATURES_OK) == 0 {
		print("VirtIO RNG: Feature negotiation failed\r\n")
		return false
	}

	// Set up the request queue (queue 0)
	if !rngSetupQueue(VIRTIO_RNG_REQUESTQ) {
		print("VirtIO RNG: Failed to set up queue\r\n")
		return false
	}

	// Set DRIVER_OK status bit
	rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_DEVICE_STATUS,
		VIRTIO_STATUS_ACKNOWLEDGE|VIRTIO_STATUS_DRIVER|VIRTIO_STATUS_FEATURES_OK|VIRTIO_STATUS_DRIVER_OK)

	print("VirtIO RNG: Initialized successfully\r\n")
	return true
}

// rngRequestBytes requests random bytes from the VirtIO RNG device
// Returns true if request was successful
//
//go:nosplit
func rngRequestBytes(length uint32) bool {
	// Limit request size to buffer size
	if length > uint32(len(rngBuffer)) {
		length = uint32(len(rngBuffer))
	}

	// Get descriptor table
	descTable := (*[8]VirtQDesc)(rngQueue.DescTable)

	// Get available ring - need to access the ring array manually
	availRing := (*[10]uint16)(unsafe.Pointer(rngQueue.Available))
	// availRing layout: [0]=flags, [1]=idx, [2..9]=ring entries

	// Use descriptor 0 for the request
	descIdx := uint16(0)

	// Set up descriptor to point to rngBuffer (device writes random data here)
	bufPhys := pointerToUintptr(unsafe.Pointer(&rngBuffer[0]))
	descTable[descIdx].Addr = uint64(bufPhys)
	descTable[descIdx].Len = length
	descTable[descIdx].Flags = VIRTQ_DESC_F_WRITE // Device writes to this buffer
	descTable[descIdx].Next = 0

	// Add descriptor to available ring
	availIdx := availRing[1] // Current available index
	ringPos := availIdx % 8   // Ring position (queue size is 8)
	availRing[2+ringPos] = descIdx // Add descriptor index to ring

	// Memory barrier - ensure descriptor is written before updating index
	asm.Dsb()

	// Increment available index
	availRing[1] = availIdx + 1

	// Memory barrier - ensure index is updated before notification
	asm.Dsb()

	// Notify device (write queue index to notify address)
	// notify_addr = notify_base + queue_notify_off * notify_off_multiplier
	notifyAddr := rngNotifyBase + uintptr(0)*uintptr(rngNotifyOffMult)
	asm.MmioWrite16(notifyAddr, 0) // Queue 0

	return true
}

// rngPollCompletion polls for completion of the RNG request
// Returns number of bytes read, or 0 if not ready
//
//go:nosplit
func rngPollCompletion() uint32 {
	// Get used ring
	usedRing := (*[10]uint16)(unsafe.Pointer(rngQueue.Used))
	// usedRing layout: [0]=flags, [1]=idx, then VirtQUsedElem array

	// Get last processed index
	lastUsedIdx := rngQueue.LastUsedIdx

	// Get current device index
	currentIdx := usedRing[1]

	// Check if device has processed any buffers
	if lastUsedIdx == currentIdx {
		return 0 // Nothing ready yet
	}

	// Get the used element
	// Each VirtQUsedElem is 8 bytes (uint32 ID + uint32 Len)
	// After flags(2) + idx(2) = 4 bytes offset
	usedElemPtr := unsafe.Pointer(uintptr(unsafe.Pointer(rngQueue.Used)) + 4 + uintptr(lastUsedIdx%8)*8)
	usedElem := (*VirtQUsedElem)(usedElemPtr)

	// Get length written by device
	bytesRead := usedElem.Len

	// Update last used index
	rngQueue.LastUsedIdx = lastUsedIdx + 1

	return bytesRead
}

// getRandomBytes fills the buffer with random bytes from VirtIO RNG
// Returns number of bytes written
//
//go:nosplit
func getRandomBytes(buf unsafe.Pointer, length uint32) uint32 {
	// NOTE: Do NOT use print() here! This function is called during runtime.schedinit()
	// to initialize runtime.globalRand. Calling print() can trigger RNG initialization,
	// creating infinite recursion: SyscallRead → getRandomBytes → print → (needs RNG) → SyscallRead
	if atomic.LoadUint32(&virtioRNGInitialized) == 0 {
		// RNG not initialized, return fake data
		return getFakeRandomBytes(buf, length)
	}

	// Request random bytes from device
	requestLen := length
	if requestLen > uint32(len(rngBuffer)) {
		requestLen = uint32(len(rngBuffer))
	}

	if !rngRequestBytes(requestLen) {
		// Request failed, fall back to fake data
		return getFakeRandomBytes(buf, length)
	}

	// Poll for completion (with timeout)
	var bytesRead uint32
	for i := 0; i < 10000; i++ {
		bytesRead = rngPollCompletion()
		if bytesRead > 0 {
			break
		}
		// Small delay - just burn some cycles
		for j := 0; j < 100; j++ {
			// Empty loop for delay
		}
	}

	if bytesRead == 0 {
		// Timeout, fall back to fake data
		return getFakeRandomBytes(buf, length)
	}

	// Copy data from rngBuffer to caller's buffer
	if bytesRead > length {
		bytesRead = length
	}

	dst := (*[1 << 30]byte)(buf)
	for i := uint32(0); i < bytesRead; i++ {
		dst[i] = rngBuffer[i]
	}

	return bytesRead
}

// getFakeRandomBytes generates fake random bytes using a simple counter
//
//go:nosplit
func getFakeRandomBytes(buf unsafe.Pointer, length uint32) uint32 {
	// Use atomic counter for slightly better "randomness" than pure sequential
	static := (*uint32)(unsafe.Pointer(uintptr(0x41020700))) // Fixed address for counter
	if *static == 0 {
		*static = 0x12345678 // Initialize with seed
	}

	p := (*[1 << 30]byte)(buf) // Cast to byte array
	for i := uint32(0); i < length; i++ {
		// Linear congruential generator (LCG) - simple PRNG
		*static = (*static * 1103515245) + 12345
		p[i] = byte(*static >> 16)
	}

	return length
}

// rngReadCommonConfig reads a 16-bit value from VirtIO common config
func rngReadCommonConfig(offset uintptr) uint16 {
	return asm.MmioRead16(rngCommonCfgBase + offset)
}

// rngWriteCommonConfig writes a 16-bit value to VirtIO common config
func rngWriteCommonConfig(offset uintptr, value uint16) {
	asm.MmioWrite16(rngCommonCfgBase+offset, value)
}

// rngWriteCommonConfig32 writes a 32-bit value to VirtIO common config
func rngWriteCommonConfig32(offset uintptr, value uint32) {
	asm.MmioWrite(rngCommonCfgBase+offset, value)
}

// rngSetupQueue sets up a virtqueue for the RNG device using static buffers
func rngSetupQueue(queueIdx uint16) bool {
	// Select the queue
	rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_SELECT, queueIdx)

	// Check if queue is available
	queueSize := rngReadCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE)
	if queueSize == 0 {
		print("VirtIO RNG: Queue ")
		printHex32(uint32(queueIdx))
		print(" not available\r\n")
		return false
	}

	print("VirtIO RNG: Queue size = ")
	printHex32(uint32(queueSize))
	print("\r\n")

	// Use queue size 8 (matching our static buffer allocation)
	if queueSize > 8 {
		queueSize = 8
		rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_SIZE, queueSize)
	}

	// Initialize virtqueue structure with static buffers (no kmalloc!)
	rngQueue.QueueSize = queueSize
	rngQueue.DescTable = unsafe.Pointer(&rngDescTable[0])
	rngQueue.Available = (*VirtQAvailable)(unsafe.Pointer(&rngAvailRing[0]))
	rngQueue.Used = (*VirtQUsed)(unsafe.Pointer(&rngUsedRing[0]))
	rngQueue.FreeHead = 0
	rngQueue.LastUsedIdx = 0
	rngQueue.NumFree = queueSize

	// Initialize descriptor free list
	for i := uint16(0); i < queueSize-1; i++ {
		rngDescTable[i].Next = i + 1
		rngDescTable[i].Flags = VIRTQ_DESC_F_NEXT
	}
	rngDescTable[queueSize-1].Flags = 0 // Last descriptor has no NEXT flag

	// Zero out available and used rings
	for i := range rngAvailRing {
		rngAvailRing[i] = 0
	}
	for i := range rngUsedRing {
		rngUsedRing[i] = 0
	}

	// Get physical addresses of queue structures
	descPhys := pointerToUintptr(unsafe.Pointer(&rngDescTable[0]))
	availPhys := pointerToUintptr(unsafe.Pointer(&rngAvailRing[0]))
	usedPhys := pointerToUintptr(unsafe.Pointer(&rngUsedRing[0]))

	print("VirtIO RNG: Desc=0x")
	printHex64(uint64(descPhys))
	print(" Avail=0x")
	printHex64(uint64(availPhys))
	print(" Used=0x")
	printHex64(uint64(usedPhys))
	print("\r\n")

	// Write queue addresses to device
	rngWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_LOW, uint32(descPhys&0xFFFFFFFF))
	rngWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_DESC_HIGH, uint32(descPhys>>32))
	rngWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_LOW, uint32(availPhys&0xFFFFFFFF))
	rngWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_AVAIL_HIGH, uint32(availPhys>>32))
	rngWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_USED_LOW, uint32(usedPhys&0xFFFFFFFF))
	rngWriteCommonConfig32(VIRTIO_PCI_COMMON_CFG_QUEUE_USED_HIGH, uint32(usedPhys>>32))

	// Enable the queue
	rngWriteCommonConfig(VIRTIO_PCI_COMMON_CFG_QUEUE_ENABLE, 1)

	print("VirtIO RNG: Queue enabled\r\n")
	return true
}
