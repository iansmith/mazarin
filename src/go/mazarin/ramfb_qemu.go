//go:build qemuvirt && aarch64

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
// For AArch64 virt machine, fw_cfg base is at 0x09020000
// DMA register is at base + 0x10 = 0x09020010
// Note: 0x9020010 and 0x09020010 are the same value (151126032 decimal)
// Control register bits (in big-endian format):
//
//	Bit 0: Error
//	Bit 1: Read
//	Bit 2: Skip
//	Bit 3: Select (upper 16 bits contain the selector index)
//	Bit 4: Write
//	Bits 16-31: Selector index (when Select bit is set)
const (
	// fw_cfg base address for AArch64 virt machine
	FW_CFG_BASE          = 0x09020000
	FW_CFG_DATA_ADDR     = FW_CFG_BASE + 0x00 // Data register (8 bytes)
	FW_CFG_SELECTOR_ADDR = FW_CFG_BASE + 0x08 // Selector register (2 bytes)
	FW_CFG_DMA_ADDR      = FW_CFG_BASE + 0x10 // DMA address register (8 bytes)

	// Feature bitmap keys
	FW_CFG_SIGNATURE = 0x0000 // Signature key
	FW_CFG_ID        = 0x0001 // Feature bitmap key

	// DMA control bits
	FW_CFG_DMA_CTL_ERROR  = 0x01
	FW_CFG_DMA_CTL_READ   = 0x02
	FW_CFG_DMA_CTL_SKIP   = 0x04
	FW_CFG_DMA_CTL_SELECT = 0x08
	FW_CFG_DMA_CTL_WRITE  = 0x10

	// Feature bitmap bits
	FW_CFG_FEATURE_TRADITIONAL = 0x01 // Bit 0: traditional interface (always set)
	FW_CFG_FEATURE_DMA         = 0x02 // Bit 1: DMA interface

	// Selector keys
	FW_CFG_RAMFB_SELECT = 0x19 // etc/ramfb entry selector (fallback)
	FW_CFG_FILE_DIR     = 0x19 // File directory selector
)

// QemuCfgFile represents a file entry in the fw_cfg file directory
// Matching working code: struct QemuCfgFile
type QemuCfgFile struct {
	Size     uint32   // File size (big-endian)
	Select   uint16   // Selector value (big-endian)
	Reserved uint16   // Reserved field
	Name     [56]byte // File name (null-terminated string)
}

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

// Note: We no longer use a global DMA structure
// Local stack-allocated structures work better and match the working RISC-V example

// qemu_cfg_check_dma_support checks if DMA interface is available using traditional interface
// Reads feature bitmap (Key 0x0001) and checks if bit 1 (DMA) is set
// Returns true if DMA is supported, false otherwise
//
//go:nosplit
func qemu_cfg_check_dma_support() bool {
	// Read feature bitmap (selector 0x0001)
	mmio_write16(uintptr(FW_CFG_SELECTOR_ADDR), swap16(uint16(FW_CFG_ID)))
	dsb()
	features := mmio_read(uintptr(FW_CFG_DATA_ADDR))

	// Check if DMA bit (bit 1) is set
	if (features & FW_CFG_FEATURE_DMA) == 0 {
		return false
	}

	// Verify DMA by reading DMA register (should return "QEMU CFG")
	dmaReg1 := swap32(mmio_read(uintptr(FW_CFG_DMA_ADDR)))
	dmaReg2 := swap32(mmio_read(uintptr(FW_CFG_DMA_ADDR + 4)))
	dmaValue := (uint64(dmaReg1) << 32) | uint64(dmaReg2)

	return dmaValue == 0x51454D5520434647
}

// ramfbInit initializes the ramfb device via fw_cfg
// Allocates framebuffer memory and configures ramfb to use it
//
//go:nosplit
func ramfbInit() bool {
	uartPuts("RAMFB: ramfbInit() entry\r\n")
	uartPuts("RAMFB: Initializing...\r\n")
	uartPutc('A') // Breadcrumb: entered ramfbInit

	// First, check if DMA is available
	uartPutc('D') // Breadcrumb: about to check DMA support
	if !qemu_cfg_check_dma_support() {
		uartPuts("RAMFB: ERROR - Cannot proceed without DMA support\r\n")
		uartPutc('X') // Breadcrumb: DMA support missing
		return false
	}
	uartPutc('d') // Breadcrumb: DMA support confirmed

	// Find etc/ramfb selector FIRST (before any other DMA operations)
	uartPutc('S') // Breadcrumb: searching fw_cfg directory
	ramfbSelector := qemu_cfg_find_file()
	if ramfbSelector == 0 {
		uartPuts("RAMFB: ERROR - Could not find etc/ramfb!\r\n")
		uartPutc('F') // Breadcrumb: selector not found
		return false
	}
	uartPutc('s') // Breadcrumb: selector found
	uartPuts("RAMFB: Found etc/ramfb, selector=0x")
	printHex32Helper(ramfbSelector)
	uartPuts("\r\n")

	// Allocate framebuffer memory
	// Must match QEMU_FB_WIDTH and QEMU_FB_HEIGHT from framebuffer_qemu.go
	fbWidth := uint32(1920)
	fbHeight := uint32(1080)
	fbSize := fbWidth * fbHeight * 4

	fbMem := kmalloc(fbSize)
	if fbMem == nil {
		uartPuts("RAMFB: ERROR - kmalloc failed\r\n")
		uartPutc('k') // Breadcrumb: kmalloc failed
		return false
	}
	uartPutc('K') // Breadcrumb: kmalloc succeeded
	fbAddr := pointerToUintptr(fbMem)
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

	// Create config structure with fields in big-endian format (matching working RISC-V example)
	// The working example does: .addr = __builtin_bswap64(fb->fb_addr)
	// Our SetAddr() method stores bytes in big-endian order, so we pass native value
	ramfbCfg.SetAddr(uint64(fbAddr))
	uartPuts("RAMFB: Address set in config (SetAddr handles BE conversion)\r\n")

	// Use 'XR24' (XRGB8888) format - matching working example exactly
	// Working example: .fourcc = __builtin_bswap32(DRM_FORMAT_XRGB8888)
	// DRM_FORMAT_XRGB8888 = fourcc_code('X','R','2','4') = 0x34325258
	// SetFourCC() stores bytes in big-endian order, so we pass the native value
	// (it will be converted to big-endian bytes internally)
	ramfbCfg.SetFourCC(0x34325258) // 'XR24' = XRGB8888 format (32-bit)
	ramfbCfg.SetFlags(0)
	ramfbCfg.SetWidth(fbWidth)   // SetWidth stores in big-endian byte order
	ramfbCfg.SetHeight(fbHeight) // SetHeight stores in big-endian byte order
	// Stride for XRGB8888: width * 4 bytes per pixel (matching working code)
	ramfbCfg.SetStride(fbWidth * 4) // SetStride stores in big-endian byte order

	uartPuts("RAMFB: Config struct created (big-endian)\r\n")
	uartPutc('C') // Breadcrumb: config struct ready

	// Write configuration using DMA - we need to get this working!
	uartPuts("RAMFB: Attempting DMA write of config...\r\n")
	uartPutc('W') // Breadcrumb: about to send DMA write
	fw_cfg_dma_write(ramfbSelector, unsafe.Pointer(&ramfbCfg), 28)
	uartPuts("RAMFB: DMA write returned\r\n")
	uartPutc('w') // Breadcrumb: DMA write call returned
	uartPuts("RAMFB: Config sent (DMA should have processed it)\r\n")

	// Debug: Print the actual config values that were sent (in big-endian)
	// Debug: Print the actual config values that were sent
	// Note: Addr() reads back the value as stored (big-endian bytes interpreted as uint64)
	// The bytes are stored correctly, but when read back as uint64, they represent the address
	uartPuts("RAMFB: Config sent - Addr=0x")
	addrVal := ramfbCfg.Addr()
	// addrVal is the address as stored in BE bytes, which should match fbAddr
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

	// Note: CharsX and CharsY cursor positioning is done in framebufferInit()
	// after calling ramfbInit(), to position cursor at bottom of screen
	// Don't reset them here as it would override that positioning
	uartPuts("RAMFB: Cursor positioning handled in framebufferInit()\r\n")

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
	uartPutc('I') // Breadcrumb: fbinfo stored

	uartPuts("RAMFB: Initialized successfully\r\n")
	uartPutc('i') // Breadcrumb: ramfbInit success
	return true
}

// ramfbReinit re-sends the ramfb configuration to keep it active
// This may be needed if ramfb clears the display after inactivity
//
//go:nosplit
func ramfbReinit() {
	// Re-send the config using the global ramfbCfg variable
	// which should still have the correct values (in big-endian)
	// ramfbReinit doesn't have access to selector, skip for now
	if false && writeRamfbConfig(&ramfbCfg, 0) {
		uartPuts("RAMFB: Config re-sent OK\r\n")
	} else {
		uartPuts("RAMFB: Config re-send failed\r\n")
	}
}

// swap32 swaps bytes in a 32-bit value (little-endian to big-endian)
//
//go:nosplit
func swap16(x uint16) uint16 {
	return ((x & 0xFF00) >> 8) | ((x & 0x00FF) << 8)
}

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

// dumpDmaStructureBytes dumps the raw bytes of the DMA structure (simplified)
//
//go:nosplit
func dumpDmaStructureBytes(dma *FWCfgDmaAccess) {
	dmaPtr := unsafe.Pointer(dma)
	uartPuts("RAMFB: DMA bytes: ")
	// Dump first 4 bytes (control), then length, then address
	for i := 0; i < 16; i++ {
		b := (*byte)(unsafe.Pointer(uintptr(dmaPtr) + uintptr(i)))
		// Print hex digit
		hi := (*b >> 4) & 0xF
		lo := *b & 0xF
		if hi < 10 {
			uartPutc(byte('0' + hi))
		} else {
			uartPutc(byte('A' + hi - 10))
		}
		if lo < 10 {
			uartPutc(byte('0' + lo))
		} else {
			uartPutc(byte('A' + lo - 10))
		}
		if i < 15 {
			uartPuts(" ")
		}
	}
	uartPuts("\r\n")
}

// qemu_cfg_read_entry_traditional reads using traditional interface (no DMA)
//
//go:nosplit
func qemu_cfg_read_entry_traditional(buf unsafe.Pointer, selector uint32, length uint32) {
	// Write selector (big-endian)
	mmio_write16(uintptr(FW_CFG_SELECTOR_ADDR), swap16(uint16(selector)))
	dsb()

	// Read data in 32-bit chunks (data register advances by read width)
	for i := uint32(0); i < length; i += 4 {
		val := mmio_read(uintptr(FW_CFG_DATA_ADDR))
		// Store 4 bytes from this read
		remaining := length - i
		if remaining > 4 {
			remaining = 4
		}
		for j := uint32(0); j < remaining; j++ {
			b := (*byte)(unsafe.Pointer(uintptr(buf) + uintptr(i+j)))
			*b = byte((val >> (j * 8)) & 0xFF)
		}
	}
}

// fw_cfg_dma_read reads data from a fw_cfg entry using DMA
// This is the clean API that hides QEMU's DEVICE_BIG_ENDIAN byte-swapping complexity
//
//go:nosplit
func fw_cfg_dma_read(selector uint32, buf unsafe.Pointer, length uint32) {
	control := (selector << 16) | uint32(FW_CFG_DMA_CTL_SELECT) | uint32(FW_CFG_DMA_CTL_READ)
	qemu_cfg_dma_transfer(buf, length, control)
}

// fw_cfg_dma_write writes data to fw_cfg using DMA
//
//go:nosplit
func fw_cfg_dma_write(selector uint32, buf unsafe.Pointer, length uint32) {
	control := (selector << 16) | uint32(FW_CFG_DMA_CTL_SELECT) | uint32(FW_CFG_DMA_CTL_WRITE)
	qemu_cfg_dma_transfer(buf, length, control)
}

// qemu_cfg_read_entry is now a wrapper for the clean API
//
//go:nosplit
func qemu_cfg_read_entry(buf unsafe.Pointer, selector uint32, length uint32) {
	fw_cfg_dma_read(selector, buf, length)
}

// qemu_cfg_read reads data from current fw_cfg entry (continues from previous read)
// Uses traditional interface for sequential reads after initial DMA select
//
//go:nosplit
func qemu_cfg_read(buf unsafe.Pointer, length uint32) {
	// Read in 32-bit chunks (data register advances by read width)
	for i := uint32(0); i < length; i += 4 {
		val := mmio_read(uintptr(FW_CFG_DATA_ADDR))
		remaining := length - i
		if remaining > 4 {
			remaining = 4
		}
		for j := uint32(0); j < remaining; j++ {
			b := (*byte)(unsafe.Pointer(uintptr(buf) + uintptr(i+j)))
			*b = byte((val >> (j * 8)) & 0xFF)
		}
	}
}

// qemu_cfg_dma_transfer performs a DMA transfer to/from fw_cfg
// Direct translation of working RISC-V example
// CRITICAL: QEMU's fw_cfg DMA region is DEVICE_BIG_ENDIAN
//
//	It byte-swaps values from little-endian guests
//	So we must write byte-swapped values that become correct after QEMU's swap
//
//go:nosplit
func qemu_cfg_dma_transfer(dataAddr unsafe.Pointer, length uint32, control uint32) {
	if length == 0 {
		return
	}

	// Create LOCAL DMA structure on stack
	// Store values in big-endian byte format (how QEMU expects to read them)
	// QEMU will then convert with be32_to_cpu/be64_to_cpu
	var access FWCfgDmaAccess
	access.SetControl(control)
	access.SetLength(length)
	access.SetAddress(uint64(uintptr(dataAddr)))
	dsb()

	// Write DMA structure address to DMA register
	// CRITICAL: Pre-swap the entire 64-bit address so QEMU's DEVICE_BIG_ENDIAN
	// byte-swap produces the correct address in the DMA handler
	accessAddr := uintptr(unsafe.Pointer(&access))
	addr64 := uint64(accessAddr)
	addr64Swapped := swap64(addr64)
	uartPutc('L') // Breadcrumb: DMA descriptor prepared

	uartPuts("RAMFB: DMA addr unswapped=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (addr64 >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts(" swapped=0x")
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (addr64Swapped >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
	uartPuts("\r\n")

	// Write the pre-swapped address as a single 64-bit value
	mmio_write64(uintptr(FW_CFG_DMA_ADDR), addr64Swapped)
	dsb()

	// Wait for DMA transfer to complete
	// Matching working code: while(__builtin_bswap32(access.control) & ~QEMU_CFG_DMA_CTL_ERROR) {}
	// The condition is: continue while (control & ~ERROR) != 0
	// This means: continue while any bits except error bit (bit 0) are set
	maxIterations := 50000 // Timeout for DMA - will help us see if it's timing out
	iterations := 0

	// Memory barrier before reading control field
	// QEMU may have updated the control field, so we need to ensure we read fresh data
	dsb()

	// Check control after register write
	dsb()
	initialControlBE := access.Control()
	initialControl := swap32(initialControlBE)
	if initialControl == 0 {
		uartPuts("RAMFB: WARNING - Control is 0! QEMU may not have processed DMA\r\n")
		uartPutc('0') // Breadcrumb: control reported zero immediately
	} else {
		uartPuts("RAMFB: Control is non-zero (0x")
		printHex32Helper(initialControl)
		uartPuts("), QEMU is processing DMA\r\n")
		uartPutc('1') // Breadcrumb: control non-zero after kick
	}

	for {
		// Memory barrier before reading control field
		// QEMU updates this field, so we need to ensure we read fresh data from memory
		dsb()

		controlBE := access.Control()
		control := swap32(controlBE)
		// Continue while any bits except error bit are set
		// Stop when control is 0 (all bits clear) OR error bit is set
		if (control & 0xFFFFFFFE) == 0 {
			// Check for error bit
			if (control & 0x01) != 0 {
				uartPuts("RAMFB: *** DMA transfer ERROR bit set! ***\r\n")
				uartPuts("RAMFB: Final control=0x")
				printHex32Helper(control)
				uartPuts(" (error bit 0 set)\r\n")
				uartPutc('E') // Breadcrumb: DMA error bit set
			} else {
				uartPuts("RAMFB: *** DMA transfer completed successfully ***\r\n")
				uartPuts("RAMFB: Final control=0x")
				printHex32Helper(control)
				uartPuts(" (all bits clear, no error)\r\n")
				uartPutc('l') // Breadcrumb: DMA done successfully
			}
			uartPuts("RAMFB: Iterations waited: ")
			// Print iterations (simple decimal)
			if iterations < 1000 {
				uartPuts("0x")
				printHex32Helper(uint32(iterations))
			} else {
				uartPuts(">1000")
			}
			uartPuts("\r\n")
			break
		}
		iterations++
		if iterations >= maxIterations {
			uartPuts("RAMFB: *** DMA transfer TIMEOUT! ***\r\n")
			uartPuts("RAMFB: Final control=0x")
			printHex32Helper(control)
			uartPuts(" (initial was 0x")
			printHex32Helper(initialControl)
			uartPuts(")\r\n")
			uartPuts("RAMFB: Control never cleared - DMA may not be working\r\n")
			uartPutc('T') // Breadcrumb: DMA timeout
			break
		}

		// Print progress every 10000 iterations
		if iterations%10000 == 0 {
			uartPuts("RAMFB: Still waiting... control=0x")
			printHex32Helper(control)
			uartPuts(" iterations=")
			uartPuts("0x")
			printHex32Helper(uint32(iterations))
			uartPuts("\r\n")
		}
		// Use proper delay function instead of empty loop
		// This gives QEMU time to process the DMA request
		// The delay() function is implemented in assembly and provides microsecond-scale waits
		delay(1000) // 1000 iterations of assembly delay loop
	}
}

// printHex32Helper prints a uint32 in hex format (helper to reduce stack usage)
//
//go:nosplit
func printHex32Helper(val uint32) {
	for shift := 28; shift >= 0; shift -= 4 {
		digit := (val >> shift) & 0xF
		if digit < 10 {
			uartPutc(byte('0' + digit))
		} else {
			uartPutc(byte('A' + digit - 10))
		}
	}
}

// checkRamfbName checks if the file name matches "etc/ramfb" or "/etc/ramfb"
//
//go:nosplit
func checkRamfbName(name *[56]byte) bool {
	// Try "etc/ramfb" (10 chars)
	ramfbName1 := "etc/ramfb"
	match1 := true
	for i := 0; i < 10 && i < len(ramfbName1); i++ {
		if name[i] != ramfbName1[i] {
			match1 = false
			break
		}
	}
	if match1 && name[9] == 0 {
		return true
	}

	// Try "/etc/ramfb" (11 chars)
	ramfbName2 := "/etc/ramfb"
	match2 := true
	for i := 0; i < 11 && i < len(ramfbName2); i++ {
		if name[i] != ramfbName2[i] {
			match2 = false
			break
		}
	}
	if match2 && (name[10] == 0 || name[10] == ' ') {
		return true
	}
	return false
}

// printFileInfo prints information about a file entry (helper to reduce stack usage)
//
//go:nosplit
func printFileInfo(entryNum uint32, qfile *QemuCfgFile) {
	uartPuts("RAMFB: File[")
	printHex32Helper(entryNum)
	uartPuts("]: name=\"")
	// Print file name up to null terminator or 56 chars
	for i := 0; i < 56; i++ {
		if qfile.Name[i] == 0 {
			break
		}
		uartPutc(qfile.Name[i])
	}
	uartPuts("\" size=0x")
	printHex32Helper(swap32(qfile.Size))
	uartPuts(" selector=0x")
	printHex32Helper(uint32(swap16(qfile.Select)))
	uartPuts("\r\n")
}

// qemu_cfg_find_file searches the fw_cfg file directory for "etc/ramfb" and returns its selector
// Uses traditional interface (not DMA) as recommended in DMA-INVESTIGATION-COMPLETE.md
//
//go:nosplit
func qemu_cfg_find_file() uint32 {
	// Read file directory count (first 4 bytes of file_dir) using traditional interface
	// Traditional interface is 100% reliable, DMA has issues with multiple consecutive reads
	var count uint32
	qemu_cfg_read_entry_traditional(unsafe.Pointer(&count), FW_CFG_FILE_DIR, 4)
	uartPutc('C') // Breadcrumb: file count read completed

	countVal := swap32(count) // Convert from big-endian
	uartPuts("RAMFB: File count=")
	printHex32Helper(countVal)
	uartPuts("\r\n")
	uartPutc('R') // Breadcrumb: about to reselect file_dir for sequential read

	// Re-select file directory so sequential reads start deterministically
	mmio_write16(uintptr(FW_CFG_SELECTOR_ADDR), swap16(uint16(FW_CFG_FILE_DIR)))
	dsb()
	// Skip the 4-byte count field (already read above) using traditional read
	var skipBuf [4]byte
	qemu_cfg_read_entry_traditional(unsafe.Pointer(&skipBuf[0]), FW_CFG_FILE_DIR, 4)
	uartPutc('r') // Breadcrumb: skip completed

	if countVal == 0 {
		uartPuts("RAMFB: Count is zero, returning\r\n")
		return 0
	}

	// Iterate through file entries
	uartPuts("RAMFB: Searching files...\r\n")
	for e := uint32(0); e < countVal; e++ {
		var qfile QemuCfgFile
		qemu_cfg_read(unsafe.Pointer(&qfile), 64) // Use sequential read, not traditional

		if checkRamfbName(&qfile.Name) {
			selector := uint32(swap16(qfile.Select))
			uartPutc('H') // Breadcrumb: ramfb entry matched
			uartPuts("RAMFB: Found etc/ramfb, selector=0x")
			printHex32Helper(selector)
			uartPuts("\r\n")
			uartPutc('h') // Breadcrumb: returning selector
			return selector
		}
		uartPutc('N') // Breadcrumb: entry not ramfb
	}

	uartPuts("RAMFB: etc/ramfb not found\r\n")
	uartPutc('Z') // Breadcrumb: search exhausted
	return 0
}

// writeRamfbConfig writes the ramfb configuration using traditional interface
//
//go:nosplit
func writeRamfbConfig(cfg *RAMFBCfg, selector uint32) bool {
	uartPuts("RAMFB: Writing config via selector 0x")
	printHex32Helper(selector)
	uartPuts("\r\n")

	// Select the etc/ramfb entry (big-endian)
	mmio_write16(uintptr(FW_CFG_SELECTOR_ADDR), swap16(uint16(selector)))
	dsb()

	// Write 28 bytes to data register (7 x 4-byte writes)
	// The config structure has bytes stored in big-endian order
	// When we write a 32-bit value via mmio_write on little-endian machine,
	// the bytes are written in little-endian order (LSB first)
	// So we need to write the value in reverse byte order
	cfgPtr := unsafe.Pointer(cfg)
	for i := uint32(0); i < 28; i += 4 {
		// Read 4 bytes individually (they're stored in big-endian order: MSB first)
		b0 := *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i))) // MSB
		b1 := *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i+1)))
		b2 := *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i+2)))
		b3 := *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i+3))) // LSB

		// Assemble in reverse order so that when written as little-endian,
		// the bytes appear in correct big-endian order: b0, b1, b2, b3
		// On little-endian write: LSB is written first, so we want LSB=b0
		val := uint32(b0) | (uint32(b1) << 8) | (uint32(b2) << 16) | (uint32(b3) << 24)

		// Write to data register
		mmio_write(uintptr(FW_CFG_DATA_ADDR), val)
	}
	dsb()

	uartPuts("RAMFB: Config written\r\n")
	return true
}

// writeRamfbConfigDirect writes the RAMFB configuration directly to fw_cfg
// without using DMA - just pokes the 28 bytes into memory
// This is for debugging - to eliminate DMA as a potential issue
// NOTE: According to QEMU docs, direct writes may be ignored in QEMU 2.4+,
// but this function helps verify the config structure is correct
//
//go:nosplit
func writeRamfbConfigDirect(cfg *RAMFBCfg) bool {
	uartPuts("RAMFB: Direct write mode - poking 28 bytes directly to fw_cfg...\r\n")

	// fw_cfg base address for AArch64 virt machine
	// According to QEMU docs: base is 0x9020000, selector at +8, data at +0
	// Note: 0x9020000 = 0x09020000 (same value, different notation)
	const FW_CFG_BASE = 0x09020000
	const FW_CFG_SELECTOR = FW_CFG_BASE + 0x08 // Selector register (2 bytes, big-endian) = 0x09020008
	const FW_CFG_DATA = FW_CFG_BASE + 0x00     // Data register (8 bytes) = 0x09020000

	// Select the etc/ramfb entry (selector 0x19)
	// Selector is 2 bytes, big-endian
	selectorBE := uint16(FW_CFG_RAMFB_SELECT)
	selectorHigh := byte(selectorBE >> 8)
	selectorLow := byte(selectorBE & 0xFF)

	uartPuts("RAMFB: Writing selector 0x19 to selector register...\r\n")

	// Write selector (2 bytes, big-endian)
	// Selector register is at offset 8, 2 bytes wide
	selectorValue := uint32(selectorHigh)<<24 | uint32(selectorLow)<<16
	uartPuts("RAMFB: About to write selector to 0x09020008...\r\n")

	// Try writing selector - if this hangs, the address might be wrong or access might be restricted
	// Selector register is 2 bytes, but we're writing 32 bits (upper 16 bits should be ignored)
	mmio_write(uintptr(FW_CFG_SELECTOR), selectorValue)

	// If we get here, the write didn't crash
	uartPuts("RAMFB: Selector write complete\r\n")
	dsb()
	uartPuts("RAMFB: Memory barrier complete\r\n")

	// Now write the 28-byte config structure directly to data register
	// Data register is 8 bytes, so we need 4 writes (28 bytes = 3 full writes + 1 partial)
	uartPuts("RAMFB: Writing 28-byte config structure to data register...\r\n")

	cfgPtr := (*[28]byte)(unsafe.Pointer(cfg))

	// Write in 8-byte chunks (4 writes total, last one is partial)
	for i := 0; i < 4; i++ {
		offset := i * 8
		if offset >= 28 {
			break
		}

		// Read 8 bytes (or remaining bytes)
		bytesToWrite := 8
		if offset+8 > 28 {
			bytesToWrite = 28 - offset
		}

		// Build 64-bit value from bytes (big-endian)
		var value uint64
		for j := 0; j < bytesToWrite && j < 8; j++ {
			value |= uint64(cfgPtr[offset+j]) << (56 - j*8)
		}

		uartPuts("RAMFB: Writing chunk ")
		uartPutUint32(uint32(i))
		uartPuts(" (offset ")
		uartPutUint32(uint32(offset))
		uartPuts(") = 0x")
		uartPutHex64(value)
		uartPuts("\r\n")

		// Write to data register (big-endian)
		mmio_write64(uintptr(FW_CFG_DATA), swap64(value))
		dsb()
	}

	uartPuts("RAMFB: Direct write complete (28 bytes written)\r\n")
	uartPuts("RAMFB: NOTE - Direct writes may be ignored by QEMU 2.4+\r\n")
	uartPuts("RAMFB: This function is for debugging/config verification only\r\n")

	return true
}
