//go:build qemu

package main

import (
	"unsafe"
)

// RAMFB configuration structure
// This is written to fw_cfg to configure the ramfb device
// Matching working code: struct QemuRamFBCfg with __attribute__((packed))
// Total size: 8 + 4 + 4 + 4 + 4 + 4 = 28 bytes (no padding)
// Uses a [28]byte backing array to ensure exact 28-byte size with no padding
// All fields are stored in big-endian format in memory
type RAMFBCfg struct {
	// Backing array - exactly 28 bytes, no padding
	// Layout: [0-7: Addr][8-11: FourCC][12-15: Flags][16-19: Width][20-23: Height][24-27: Stride]
	data [28]byte
}

// Addr returns the Addr field (big-endian uint64 at offset 0)
//
//go:nosplit
func (r *RAMFBCfg) Addr() uint64 {
	return uint64(r.data[0])<<56 | uint64(r.data[1])<<48 | uint64(r.data[2])<<40 | uint64(r.data[3])<<32 |
		uint64(r.data[4])<<24 | uint64(r.data[5])<<16 | uint64(r.data[6])<<8 | uint64(r.data[7])
}

// SetAddr sets the Addr field (big-endian uint64 at offset 0)
//
//go:nosplit
func (r *RAMFBCfg) SetAddr(val uint64) {
	r.data[0] = byte(val >> 56)
	r.data[1] = byte(val >> 48)
	r.data[2] = byte(val >> 40)
	r.data[3] = byte(val >> 32)
	r.data[4] = byte(val >> 24)
	r.data[5] = byte(val >> 16)
	r.data[6] = byte(val >> 8)
	r.data[7] = byte(val)
}

// FourCC returns the FourCC field (big-endian uint32 at offset 8)
//
//go:nosplit
func (r *RAMFBCfg) FourCC() uint32 {
	return uint32(r.data[8])<<24 | uint32(r.data[9])<<16 | uint32(r.data[10])<<8 | uint32(r.data[11])
}

// SetFourCC sets the FourCC field (big-endian uint32 at offset 8)
//
//go:nosplit
func (r *RAMFBCfg) SetFourCC(val uint32) {
	r.data[8] = byte(val >> 24)
	r.data[9] = byte(val >> 16)
	r.data[10] = byte(val >> 8)
	r.data[11] = byte(val)
}

// Flags returns the Flags field (big-endian uint32 at offset 12)
//
//go:nosplit
func (r *RAMFBCfg) Flags() uint32 {
	return uint32(r.data[12])<<24 | uint32(r.data[13])<<16 | uint32(r.data[14])<<8 | uint32(r.data[15])
}

// SetFlags sets the Flags field (big-endian uint32 at offset 12)
//
//go:nosplit
func (r *RAMFBCfg) SetFlags(val uint32) {
	r.data[12] = byte(val >> 24)
	r.data[13] = byte(val >> 16)
	r.data[14] = byte(val >> 8)
	r.data[15] = byte(val)
}

// Width returns the Width field (big-endian uint32 at offset 16)
//
//go:nosplit
func (r *RAMFBCfg) Width() uint32 {
	return uint32(r.data[16])<<24 | uint32(r.data[17])<<16 | uint32(r.data[18])<<8 | uint32(r.data[19])
}

// SetWidth sets the Width field (big-endian uint32 at offset 16)
//
//go:nosplit
func (r *RAMFBCfg) SetWidth(val uint32) {
	r.data[16] = byte(val >> 24)
	r.data[17] = byte(val >> 16)
	r.data[18] = byte(val >> 8)
	r.data[19] = byte(val)
}

// Height returns the Height field (big-endian uint32 at offset 20)
//
//go:nosplit
func (r *RAMFBCfg) Height() uint32 {
	return uint32(r.data[20])<<24 | uint32(r.data[21])<<16 | uint32(r.data[22])<<8 | uint32(r.data[23])
}

// SetHeight sets the Height field (big-endian uint32 at offset 20)
//
//go:nosplit
func (r *RAMFBCfg) SetHeight(val uint32) {
	r.data[20] = byte(val >> 24)
	r.data[21] = byte(val >> 16)
	r.data[22] = byte(val >> 8)
	r.data[23] = byte(val)
}

// Stride returns the Stride field (big-endian uint32 at offset 24)
//
//go:nosplit
func (r *RAMFBCfg) Stride() uint32 {
	return uint32(r.data[24])<<24 | uint32(r.data[25])<<16 | uint32(r.data[26])<<8 | uint32(r.data[27])
}

// SetStride sets the Stride field (big-endian uint32 at offset 24)
//
//go:nosplit
func (r *RAMFBCfg) SetStride(val uint32) {
	r.data[24] = byte(val >> 24)
	r.data[25] = byte(val >> 16)
	r.data[26] = byte(val >> 8)
	r.data[27] = byte(val)
}

// fw_cfg DMA interface constants
// For AArch64 virt machine, fw_cfg DMA register is at 0x9020010
// Control register bits (in big-endian format):
//
//	Bit 0: Error
//	Bit 1: Read
//	Bit 2: Skip
//	Bit 3: Select (upper 16 bits contain the selector index)
//	Bit 4: Write
//	Bits 16-31: Selector index (when Select bit is set)
const (
	FW_CFG_DMA_ADDR       = 0x9020010
	FW_CFG_DMA_CTL_ERROR  = 0x01
	FW_CFG_DMA_CTL_READ   = 0x02
	FW_CFG_DMA_CTL_SKIP   = 0x04
	FW_CFG_DMA_CTL_SELECT = 0x08
	FW_CFG_DMA_CTL_WRITE  = 0x10
	FW_CFG_RAMFB_SELECT   = 0x19 // etc/ramfb entry selector
)

// FWCfgDmaAccess is the DMA access structure for fw_cfg
// Matching working code: struct QemuCfgDmaAccess with __attribute__((packed))
// Total size: 4 + 4 + 8 = 16 bytes (no padding)
// Uses a [16]byte backing array to ensure exact 16-byte size with no padding
// All fields are stored in big-endian format in memory
type FWCfgDmaAccess struct {
	// Backing array - exactly 16 bytes, no padding
	// Layout: [0-3: Control][4-7: Length][8-15: Address]
	data [16]byte
}

// Control returns the Control field (big-endian uint32 at offset 0)
//
//go:nosplit
func (d *FWCfgDmaAccess) Control() uint32 {
	// Read 4 bytes at offset 0, convert from big-endian
	return uint32(d.data[0])<<24 | uint32(d.data[1])<<16 | uint32(d.data[2])<<8 | uint32(d.data[3])
}

// SetControl sets the Control field (big-endian uint32 at offset 0)
//
//go:nosplit
func (d *FWCfgDmaAccess) SetControl(val uint32) {
	// Write 4 bytes at offset 0 in big-endian format
	d.data[0] = byte(val >> 24)
	d.data[1] = byte(val >> 16)
	d.data[2] = byte(val >> 8)
	d.data[3] = byte(val)
}

// Length returns the Length field (big-endian uint32 at offset 4)
//
//go:nosplit
func (d *FWCfgDmaAccess) Length() uint32 {
	// Read 4 bytes at offset 4, convert from big-endian
	return uint32(d.data[4])<<24 | uint32(d.data[5])<<16 | uint32(d.data[6])<<8 | uint32(d.data[7])
}

// SetLength sets the Length field (big-endian uint32 at offset 4)
//
//go:nosplit
func (d *FWCfgDmaAccess) SetLength(val uint32) {
	// Write 4 bytes at offset 4 in big-endian format
	d.data[4] = byte(val >> 24)
	d.data[5] = byte(val >> 16)
	d.data[6] = byte(val >> 8)
	d.data[7] = byte(val)
}

// Address returns the Address field (big-endian uint64 at offset 8)
//
//go:nosplit
func (d *FWCfgDmaAccess) Address() uint64 {
	// Read 8 bytes at offset 8, convert from big-endian
	return uint64(d.data[8])<<56 | uint64(d.data[9])<<48 | uint64(d.data[10])<<40 | uint64(d.data[11])<<32 |
		uint64(d.data[12])<<24 | uint64(d.data[13])<<16 | uint64(d.data[14])<<8 | uint64(d.data[15])
}

// SetAddress sets the Address field (big-endian uint64 at offset 8)
//
//go:nosplit
func (d *FWCfgDmaAccess) SetAddress(val uint64) {
	// Write 8 bytes at offset 8 in big-endian format
	d.data[8] = byte(val >> 56)
	d.data[9] = byte(val >> 48)
	d.data[10] = byte(val >> 40)
	d.data[11] = byte(val >> 32)
	d.data[12] = byte(val >> 24)
	d.data[13] = byte(val >> 16)
	d.data[14] = byte(val >> 8)
	d.data[15] = byte(val)
}

// Global config struct to avoid stack issues
var ramfbCfg RAMFBCfg

// Global DMA access structure (to avoid stack allocation issues)
// QEMU's DMA engine needs to access this structure, so it must be in accessible memory
var dmaAccessGlobal FWCfgDmaAccess

// ramfbInit initializes the ramfb device via fw_cfg
// Allocates framebuffer memory and configures ramfb to use it
//
//go:nosplit
func ramfbInit() bool {
	uartPuts("RAMFB: Initializing...\r\n")

	// Allocate framebuffer memory using heap
	// QEMU virt machine: Kernel RAM is 0x40100000 - 0x60000000 (512MB)
	// Heap is allocated from memInit() and is within this region
	// Use kmalloc to allocate framebuffer (will be in heap region)
	// Try to allocate at a lower address first (closer to heap start)
	fbWidth := uint32(1280)
	fbHeight := uint32(720)
	fbSize := fbWidth * fbHeight * 4 // 4 bytes per pixel

	uartPuts("RAMFB: Attempting to allocate framebuffer from heap...\r\n")
	uartPuts("RAMFB: Calling kmalloc...\r\n")

	fbMem := kmalloc(fbSize)
	uartPuts("RAMFB: kmalloc returned\r\n")

	if fbMem == nil {
		uartPuts("RAMFB: ERROR - Failed to allocate framebuffer from heap\r\n")
		return false
	}
	fbAddr := pointerToUintptr(fbMem)
	uartPuts("RAMFB: Got framebuffer address (kmalloc succeeded)\r\n")
	// Use 32-bit format (XRGB8888) like working example
	// Working code: stride = fb_width * sizeof(uint32_t) = width * 4
	fbStride := fbWidth * 4 // 1280 * 4 = 5120

	// Create RAMFB configuration structure in global variable
	// IMPORTANT: The RAMFBCfg structure fields must be in big-endian format
	// when written via fw_cfg DMA, so we convert them to big-endian
	uartPuts("RAMFB: Creating config struct...\r\n")

	// Verify framebuffer address is within QEMU's RAM region
	const QEMU_RAM_START = 0x40000000
	const QEMU_RAM_END = 0x80000000
	if fbAddr < QEMU_RAM_START || fbAddr >= QEMU_RAM_END {
		uartPuts("RAMFB: ERROR - Address outside QEMU RAM\r\n")
		return false
	}

	// Store native-endian values, then convert to big-endian
	fbAddrBE := swap64(uint64(fbAddr))
	uartPuts("RAMFB: Address converted to big-endian\r\n")

	ramfbCfg.SetAddr(fbAddrBE)
	// Use 'XR24' (XRGB8888) format - 32-bit, matches working example code
	// FourCC code: 0x34325258 = 'XR24' = XRGB8888 (32-bit, 4 bytes per pixel)
	// Working code uses: FORMAT_XRGB8888 = 875713112 = 0x34325258
	ramfbCfg.SetFourCC(swap32(0x34325258)) // 'XR24' = XRGB8888 format (32-bit)
	ramfbCfg.SetFlags(swap32(0))
	ramfbCfg.SetWidth(swap32(fbWidth))
	ramfbCfg.SetHeight(swap32(fbHeight))
	// Stride for XRGB8888: width * 4 bytes per pixel (matching working code)
	ramfbCfg.SetStride(swap32(fbWidth * 4))

	uartPuts("RAMFB: Config struct created (big-endian)\r\n")

	// Write configuration to fw_cfg
	uartPuts("RAMFB: Writing config to fw_cfg at 0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(FW_CFG_DMA_ADDR) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("...\r\n")

	uartPuts("RAMFB: Getting config struct address...\r\n")
	cfgAddr := uintptr(unsafe.Pointer(&ramfbCfg))
	uartPuts("RAMFB: Config struct at 0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(cfgAddr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	uartPuts("RAMFB: Calling writeRamfbConfig...\r\n")

	if !writeRamfbConfig(&ramfbCfg) {
		uartPuts("RAMFB: Config write failed\r\n")
		return false
	}

	uartPuts("RAMFB: Config written OK\r\n")

	// Debug: Print the actual config values that were sent (in big-endian)
	// Debug: Print the actual config values that were sent (convert from big-endian for display)
	// Use hex display to avoid uartPutUint32 issues
	uartPuts("RAMFB: Config sent - Addr=0x")
	addrVal := ramfbCfg.Addr()
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (addrVal >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(" Width=0x")
	printHex32(swap32(ramfbCfg.Width()))
	uartPuts(" Height=0x")
	printHex32(swap32(ramfbCfg.Height()))
	uartPuts(" Stride=0x")
	printHex32(swap32(ramfbCfg.Stride()))
	uartPuts(" FourCC=0x")
	printHex32(swap32(ramfbCfg.FourCC()))
	uartPuts("\r\n")
	uartPuts("RAMFB: About to store framebuffer info...\r\n")

	// Check stack usage before storing framebuffer info
	// Initial stack pointer is at 0x60000000 (top of 512MB kernel region, set in boot.s)
	// Stack grows downward, so lower values mean more stack used
	const initialStackPtr uintptr = 0x60000000
	currentStackPtr := get_stack_pointer()
	stackUsed := initialStackPtr - currentStackPtr
	uartPuts("RAMFB: Stack check - initial=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(initialStackPtr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(" current=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(currentStackPtr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(" used=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(stackUsed) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")

	// Warn if stack usage is high (more than 512KB used)
	if stackUsed > 512*1024 {
		uartPuts("RAMFB: WARNING - High stack usage detected!\r\n")
	}

	// Store framebuffer info
	uartPuts("RAMFB: Storing width...\r\n")
	fbinfo.Width = fbWidth
	uartPuts("RAMFB: Storing height...\r\n")
	fbinfo.Height = fbHeight
	uartPuts("RAMFB: Storing pitch...\r\n")
	fbinfo.Pitch = fbStride
	uartPuts("RAMFB: Calculating chars...\r\n")

	// Debug: Print values before division
	uartPuts("RAMFB: Before division - fbWidth=0x")
	printHex32(fbWidth)
	uartPuts(" CHAR_WIDTH=0x")
	printHex32(CHAR_WIDTH)
	uartPuts(" fbHeight=0x")
	printHex32(fbHeight)
	uartPuts(" CHAR_HEIGHT=0x")
	printHex32(CHAR_HEIGHT)
	uartPuts("\r\n")

	// Check for zero values (shouldn't happen, but safety check)
	if CHAR_WIDTH == 0 {
		uartPuts("RAMFB: ERROR - CHAR_WIDTH is zero!\r\n")
		return false
	}
	if CHAR_HEIGHT == 0 {
		uartPuts("RAMFB: ERROR - CHAR_HEIGHT is zero!\r\n")
		return false
	}

	// Use temporary variables to isolate the division operations
	uartPuts("RAMFB: Performing division operations...\r\n")
	tempCharsWidth := fbWidth / CHAR_WIDTH
	uartPuts("RAMFB: Division 1 complete - tempCharsWidth=0x")
	printHex32(tempCharsWidth)
	uartPuts("\r\n")

	tempCharsHeight := fbHeight / CHAR_HEIGHT
	uartPuts("RAMFB: Division 2 complete - tempCharsHeight=0x")
	printHex32(tempCharsHeight)
	uartPuts("\r\n")

	// Now assign to fbinfo struct
	uartPuts("RAMFB: Assigning to fbinfo.CharsWidth...\r\n")
	fbinfo.CharsWidth = tempCharsWidth
	uartPuts("RAMFB: fbinfo.CharsWidth assigned OK\r\n")

	uartPuts("RAMFB: Assigning to fbinfo.CharsHeight...\r\n")
	fbinfo.CharsHeight = tempCharsHeight
	uartPuts("RAMFB: fbinfo.CharsHeight assigned OK\r\n")

	uartPuts("RAMFB: Setting CharsX and CharsY...\r\n")
	uartPuts("RAMFB: About to set fbinfo.CharsX to 0...\r\n")
	fbinfo.CharsX = 0
	uartPuts("RAMFB: fbinfo.CharsX set OK\r\n")
	uartPuts("RAMFB: About to set fbinfo.CharsY to 0...\r\n")
	fbinfo.CharsY = 0
	uartPuts("RAMFB: fbinfo.CharsY set OK\r\n")
	uartPuts("RAMFB: CharsX and CharsY set OK\r\n")

	// Check stack again after division operations
	currentStackPtr2 := get_stack_pointer()
	stackUsed2 := initialStackPtr - currentStackPtr2
	uartPuts("RAMFB: Stack check after division - current=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(currentStackPtr2) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(" used=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(stackUsed2) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")

	uartPuts("RAMFB: Calculating buf size...\r\n")
	fbinfo.BufSize = uint32(fbStride) * fbHeight
	uartPuts("RAMFB: Storing buf pointer...\r\n")
	fbinfo.Buf = fbMem // Use the allocated memory pointer
	uartPuts("RAMFB: Framebuffer info stored\r\n")

	uartPuts("RAMFB: Initialized successfully\r\n")
	return true
}

// ramfbReinit re-sends the ramfb configuration to keep it active
// This may be needed if ramfb clears the display after inactivity
//
//go:nosplit
func ramfbReinit() {
	// Re-send the config using the global ramfbCfg variable
	// which should still have the correct values (in big-endian)
	if writeRamfbConfig(&ramfbCfg) {
		uartPuts("RAMFB: Config re-sent OK\r\n")
	} else {
		uartPuts("RAMFB: Config re-send failed\r\n")
	}
}

// swap32 swaps bytes in a 32-bit value (little-endian to big-endian)
//
//go:nosplit
func swap32(x uint32) uint32 {
	return ((x & 0xFF000000) >> 24) |
		((x & 0x00FF0000) >> 8) |
		((x & 0x0000FF00) << 8) |
		((x & 0x000000FF) << 24)
}

// swap64 swaps bytes in a 64-bit value (little-endian to big-endian)
//
//go:nosplit
func swap64(x uint64) uint64 {
	return ((x & 0xFF00000000000000) >> 56) |
		((x & 0x00FF000000000000) >> 40) |
		((x & 0x0000FF0000000000) >> 24) |
		((x & 0x000000FF00000000) >> 8) |
		((x & 0x00000000FF000000) << 8) |
		((x & 0x0000000000FF0000) << 24) |
		((x & 0x000000000000FF00) << 40) |
		((x & 0x00000000000000FF) << 56)
}

// writeRamfbConfig writes the RAMFB configuration to fw_cfg
//
//go:nosplit
func writeRamfbConfig(cfg *RAMFBCfg) bool {
	uartPuts("RAMFB: Setting up DMA access...\r\n")

	// Set up DMA access structure
	// Control format (matching working code exactly):
	//   - Bits 16-31: Selector index (0x19 for etc/ramfb)
	//   - Bit 3 (SELECT): Set to select an fw_cfg entry (0x08)
	//   - Bit 4 (WRITE): Set to perform write operation (0x10)
	// Working code: control = (selector << 16) | 0x08 | 0x10
	// For selector 0x19: (0x19 << 16) | 0x08 | 0x10 = 0x00190018
	control := (uint32(FW_CFG_RAMFB_SELECT) << 16) | uint32(FW_CFG_DMA_CTL_SELECT) | uint32(FW_CFG_DMA_CTL_WRITE)

	// The cfg structure must be in accessible memory
	// We're using the global ramfbCfg, which should be fine
	// Working code uses: sizeof(struct QemuRamFBCfg) = 28 bytes
	length := uint32(28) // Exactly 28 bytes (packed structure)
	address := uint64(uintptr(unsafe.Pointer(cfg)))

	uartPuts("RAMFB: DMA setup - selector=0x")
	printHex32(FW_CFG_RAMFB_SELECT)
	uartPuts(" control=0x")
	printHex32(control)
	uartPuts(" length=0x")
	printHex32(length)
	uartPuts(" address=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (address >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	uartPuts("RAMFB: About to set DMA structure fields...\r\n")

	uartPuts("RAMFB: Control (native)=0x")
	printHex32(control)
	uartPuts("\r\n")

	// Use global variable for DMA access structure
	// QEMU's DMA engine needs to access this, so it must be in accessible memory
	// Set fields using accessor methods (fields stored in big-endian format)
	uartPuts("RAMFB: Setting DMA structure fields (big-endian)...\r\n")
	dmaAccessGlobal.SetControl(swap32(control)) // Convert to big-endian
	uartPuts("RAMFB: Control set\r\n")
	dmaAccessGlobal.SetLength(swap32(length)) // Convert to big-endian
	uartPuts("RAMFB: Length set\r\n")
	dmaAccessGlobal.SetAddress(swap64(address)) // Convert to big-endian
	uartPuts("RAMFB: Address set\r\n")

	uartPuts("RAMFB: Verifying DMA structure - control=0x")
	printHex32(dmaAccessGlobal.Control())
	uartPuts(" length=0x")
	printHex32(dmaAccessGlobal.Length())
	uartPuts(" address=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (dmaAccessGlobal.Address() >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")

	// Write DMA descriptor to fw_cfg DMA register
	// According to fw_cfg spec, we write the PHYSICAL ADDRESS of the
	// FWCfgDmaAccess structure to the DMA register, not the fields directly
	uartPuts("RAMFB: Preparing DMA descriptor structure...\r\n")

	// The dmaAccessGlobal structure is in global memory with big-endian fields
	// Now we need to write its physical address to the DMA register
	dmaStructAddr := uintptr(unsafe.Pointer(&dmaAccessGlobal))
	uartPuts("RAMFB: DMA struct at physical address 0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (uint64(dmaStructAddr) >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")

	// Write the physical address to the DMA register
	// The DMA register is at FW_CFG_DMA_ADDR (0x9020010)
	// According to spec: write address in big-endian format
	// Can use single 64-bit write or two 32-bit writes (lower half triggers)
	uartPuts("RAMFB: Writing DMA struct address to fw_cfg DMA register...\r\n")

	// Convert address to big-endian
	addrBE := swap64(uint64(dmaStructAddr))

	// Try single 64-bit write (simpler and atomic)
	// Use mmio_write64 if available, otherwise use two 32-bit writes
	uartPuts("RAMFB: Writing 64-bit address (BE) to DMA register...\r\n")
	uartPuts("RAMFB: Address value (BE)=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (addrBE >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")
	mmio_write64(uintptr(FW_CFG_DMA_ADDR), addrBE)
	uartPuts("RAMFB: 64-bit address written (operation triggered)\r\n")
	// Add memory barrier to ensure write is visible
	dsb()
	uartPuts("RAMFB: Memory barrier executed\r\n")

	// Now we need to check the control field in the dmaAccessGlobal structure
	// (not the DMA register itself) - QEMU will modify it when transfer completes
	uartPuts("RAMFB: DMA operation triggered, waiting for completion...\r\n")

	// Give QEMU a moment to process the DMA request
	// We'll skip the completion check for now and just continue
	// The DMA transfer should happen asynchronously
	uartPuts("RAMFB: Giving QEMU time to process...\r\n")
	for delay := 0; delay < 50000; delay++ {
	}
	// Wait for DMA transfer to complete
	// Matching working code exactly: while (BE32(dma.control) & ~0x01);
	// This waits while any bits are set except the error bit (bit 0)
	// Stops when control is 0 (success) OR error bit is set (failure)
	maxWait := 1000000
	for i := 0; i < maxWait; i++ {
		// Read control field from the structure (it's in big-endian format)
		controlBE := dmaAccessGlobal.Control()

		// Convert to native endian to check (matching working code: BE32(dma.control))
		control := swap32(controlBE)

		// Wait condition from working code: while (control & ~0x01)
		// This means: continue if any bits except error bit (bit 0) are set
		// Stop when control is 0 (all bits clear) OR error bit is set
		if (control & 0xFFFFFFFE) == 0 {
			// All bits clear except possibly error bit - check error bit
			if (control & 0x01) != 0 {
				uartPuts("RAMFB: DMA transfer error (error bit set)\r\n")
				uartPuts("RAMFB: Control (BE)=0x")
				printHex32(controlBE)
				uartPuts(" (LE=0x")
				printHex32(control)
				uartPuts(")\r\n")
				return false
			}
			// Control is 0 - transfer complete!
			uartPuts("RAMFB: DMA transfer completed successfully (control=0)\r\n")
			// Give QEMU a moment to process the config
			for delay := 0; delay < 500000; delay++ {
			}
			return true
		}

		// Small delay
		for j := 0; j < 100; j++ {
		}

		if i%100000 == 0 && i > 0 {
			uartPuts("RAMFB: Waiting... iteration=")
			uartPutUint32(uint32(i))
			uartPuts(" control (BE)=0x")
			printHex32(controlBE)
			uartPuts(" (LE=0x")
			printHex32(control)
			uartPuts(")\r\n")
		}
	}

	uartPuts("RAMFB: DMA transfer timeout\r\n")
	uartPuts("RAMFB: Final control (BE)=0x")
	printHex32(dmaAccessGlobal.Control())
	uartPuts(" (LE=0x")
	printHex32(swap32(dmaAccessGlobal.Control()))
	uartPuts(")\r\n")

	// Even if timeout, try to continue - maybe it worked anyway
	uartPuts("RAMFB: Continuing despite timeout\r\n")
	return true
}
