//go:build qemuvirt && aarch64

package main

import (
	"mazboot/asm"
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
	asm.MmioWrite16(uintptr(FW_CFG_SELECTOR_ADDR), swap16(uint16(FW_CFG_ID)))
	asm.Dsb()
	features := asm.MmioRead(uintptr(FW_CFG_DATA_ADDR))

	// Check if DMA bit (bit 1) is set
	if (features & FW_CFG_FEATURE_DMA) == 0 {
		return false
	}

	// Verify DMA by reading DMA register (should return "QEMU CFG")
	dmaReg1 := swap32(asm.MmioRead(uintptr(FW_CFG_DMA_ADDR)))
	dmaReg2 := swap32(asm.MmioRead(uintptr(FW_CFG_DMA_ADDR + 4)))
	dmaValue := (uint64(dmaReg1) << 32) | uint64(dmaReg2)

	return dmaValue == 0x51454D5520434647
}

// ramfbInit initializes the ramfb device via fw_cfg
// Allocates framebuffer memory and configures ramfb to use it
func ramfbInit() bool {
	// Check if DMA is available
	if !qemu_cfg_check_dma_support() {
		return false
	}

	// Find etc/ramfb selector
	ramfbSelector := qemu_cfg_find_file()
	if ramfbSelector == 0 {
		return false
	}

	// Allocate framebuffer memory
	fbWidth := uint32(1024)
	fbHeight := uint32(768)
	fbSize := fbWidth * fbHeight * 4

	// CRITICAL: Framebuffer memory allocated here must NEVER be freed
	fbMem := kmallocReserved(fbSize)
	if fbMem == nil {
		return false
	}
	fbAddr := pointerToUintptr(fbMem)
	fbStride := fbWidth * 4

	// Verify framebuffer address is within QEMU's RAM region
	const QEMU_RAM_START = 0x40000000
	const QEMU_RAM_END = 0x80000000
	if fbAddr < QEMU_RAM_START || fbAddr >= QEMU_RAM_END {
		return false
	}

	// Create config structure with fields in big-endian format
	ramfbCfg.SetAddr(uint64(fbAddr))
	ramfbCfg.SetFourCC(0x34325258) // 'XR24' = XRGB8888 format (32-bit)
	ramfbCfg.SetFlags(0)
	ramfbCfg.SetWidth(fbWidth)
	ramfbCfg.SetHeight(fbHeight)
	ramfbCfg.SetStride(fbWidth * 4)

	// Write configuration using DMA
	fw_cfg_dma_write(ramfbSelector, unsafe.Pointer(&ramfbCfg), 28)

	// Store framebuffer info
	fbinfo.Width = fbWidth
	fbinfo.Height = fbHeight
	fbinfo.Pitch = fbStride
	fbinfo.CharsWidth = fbWidth / CHAR_WIDTH
	fbinfo.CharsHeight = fbHeight / CHAR_HEIGHT
	fbinfo.BufSize = uint32(fbStride) * fbHeight
	fbinfo.Buf = fbMem

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
	_ = writeRamfbConfig(&ramfbCfg, 0)
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


// qemu_cfg_read_entry_traditional reads using traditional interface (no DMA)
//
//go:nosplit
func qemu_cfg_read_entry_traditional(buf unsafe.Pointer, selector uint32, length uint32) {
	// Write selector (big-endian)
	asm.MmioWrite16(uintptr(FW_CFG_SELECTOR_ADDR), swap16(uint16(selector)))
	asm.Dsb()

	// Read data in 32-bit chunks (data register advances by read width)
	for i := uint32(0); i < length; i += 4 {
		val := asm.MmioRead(uintptr(FW_CFG_DATA_ADDR))
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
		val := asm.MmioRead(uintptr(FW_CFG_DATA_ADDR))
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
// CRITICAL: QEMU's fw_cfg DMA region is DEVICE_BIG_ENDIAN
//
//go:nosplit
func qemu_cfg_dma_transfer(dataAddr unsafe.Pointer, length uint32, control uint32) {
	if length == 0 {
		return
	}

	// Create LOCAL DMA structure on stack
	var access FWCfgDmaAccess
	access.SetControl(control)
	access.SetLength(length)
	access.SetAddress(uint64(uintptr(dataAddr)))
	asm.Dsb()

	// Write DMA structure address to DMA register
	// Pre-swap for QEMU's DEVICE_BIG_ENDIAN byte-swap
	accessAddr := uintptr(unsafe.Pointer(&access))
	addr64Swapped := swap64(uint64(accessAddr))

	asm.MmioWrite64(uintptr(FW_CFG_DMA_ADDR), addr64Swapped)
	asm.Dsb()

	// Wait for DMA transfer to complete
	maxIterations := 50000
	for iterations := 0; iterations < maxIterations; iterations++ {
		asm.Dsb()
		controlBE := access.Control()
		ctrl := swap32(controlBE)
		// Stop when control is 0 (all bits clear) OR error bit is set
		if (ctrl & 0xFFFFFFFE) == 0 {
			break
		}
		asm.Delay(1000)
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


// qemu_cfg_find_file searches the fw_cfg file directory for "etc/ramfb" and returns its selector
// Uses traditional interface (not DMA)
func qemu_cfg_find_file() uint32 {
	// Read file directory count (first 4 bytes of file_dir)
	var count uint32
	qemu_cfg_read_entry_traditional(unsafe.Pointer(&count), FW_CFG_FILE_DIR, 4)

	countVal := swap32(count)

	// Re-select file directory so sequential reads start deterministically
	asm.MmioWrite16(uintptr(FW_CFG_SELECTOR_ADDR), swap16(uint16(FW_CFG_FILE_DIR)))
	asm.Dsb()
	qemu_cfg_read_entry_traditional(unsafe.Pointer(&count), FW_CFG_FILE_DIR, 4)

	if countVal == 0 {
		return 0
	}

	// Allocate qfile on heap to avoid stack issues
	qfile := (*QemuCfgFile)(kmalloc(64))
	if qfile == nil {
		return 0
	}

	for e := uint32(0); e < countVal; e++ {
		qemu_cfg_read(unsafe.Pointer(qfile), 64)

		if checkRamfbName(&qfile.Name) {
			selector := uint32(swap16(qfile.Select))
			kfree(unsafe.Pointer(qfile))
			return selector
		}
	}

	kfree(unsafe.Pointer(qfile))
	return 0
}

// writeRamfbConfig writes the ramfb configuration using traditional interface
//
//go:nosplit
func writeRamfbConfig(cfg *RAMFBCfg, selector uint32) bool {
	// Select the etc/ramfb entry (big-endian)
	asm.MmioWrite16(uintptr(FW_CFG_SELECTOR_ADDR), swap16(uint16(selector)))
	asm.Dsb()

	// Write 28 bytes to data register (7 x 4-byte writes)
	cfgPtr := unsafe.Pointer(cfg)
	for i := uint32(0); i < 28; i += 4 {
		b0 := *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i)))
		b1 := *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i+1)))
		b2 := *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i+2)))
		b3 := *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i+3)))

		val := uint32(b0) | (uint32(b1) << 8) | (uint32(b2) << 16) | (uint32(b3) << 24)
		asm.MmioWrite(uintptr(FW_CFG_DATA_ADDR), val)
	}
	asm.Dsb()

	return true
}

