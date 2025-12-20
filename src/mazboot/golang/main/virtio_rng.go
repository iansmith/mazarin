//go:build qemuvirt && aarch64

package main

import (
	"sync/atomic"
	"unsafe"
)

// VirtIO RNG Constants
const (
	VIRTIO_RNG_DEVICE_ID = 0x1044 // VirtIO 1.0 RNG device ID

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

	// Simple single-buffer approach (no complex virtqueue ring)
	rngBuffer     [64]byte // Buffer for random bytes
	rngBufferFull uint32   // 1 if buffer has data, 0 if empty
	rngBufferPos  uint32   // Current read position in buffer
)

// initVirtIORNG initializes the VirtIO RNG device
//
//go:nosplit
func initVirtIORNG() bool {
	print("VirtIO RNG: Scanning PCI bus...\r\n")

	// Scan PCI bus for VirtIO RNG device (vendor 0x1AF4, device 0x1044)
	for bus := uint8(0); bus < 4; bus++ {
		for slot := uint8(0); slot < 32; slot++ {
			vendorID := uint16(pciConfigRead32(bus, slot, 0, PCI_VENDOR_ID) & 0xFFFF)
			deviceID := uint16(pciConfigRead32(bus, slot, 0, PCI_DEVICE_ID) & 0xFFFF)

			if vendorID == VIRTIO_VENDOR_ID && deviceID == VIRTIO_RNG_DEVICE_ID {
				print("VirtIO RNG: Found at PCI ")
				printHex32(uint32(bus))
				print(":")
				printHex32(uint32(slot))
				print("\r\n")

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
//
//go:nosplit
func initVirtIORNGDevice(bus, slot uint8) bool {
	// Enable PCI bus mastering and memory access
	command := pciConfigRead32(bus, slot, 0, PCI_COMMAND)
	command |= 0x06 // Enable memory space (bit 1) and bus master (bit 2)
	pciConfigWrite32(bus, slot, 0, PCI_COMMAND, command)

	// For simplicity with modern VirtIO 1.0 devices on QEMU:
	// We'll use a minimal approach - just read entropy directly without full virtqueue setup
	// This is a STUB implementation that returns fake random data for now

	// TODO: Implement full VirtIO initialization:
	// 1. Parse capabilities to find common cfg, notify cfg, device cfg BARs
	// 2. Set up virtqueue with descriptors
	// 3. Negotiate features
	// 4. Enable device

	print("VirtIO RNG: Using stub implementation (fake random data)\r\n")
	return true
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

	// TODO: Implement real VirtIO RNG read
	// For now, use fake random data
	return getFakeRandomBytes(buf, length)
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
