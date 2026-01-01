package main

import (
	"unsafe"
)

// Framebuffer constants (shared between RPI and QEMU)
const (
	COLORDEPTH      = 24 // 24 bits per pixel (RGB)
	BYTES_PER_PIXEL = 3  // 3 bytes per pixel (RGB)
	CHAR_WIDTH      = 16 // Character width in pixels (doubled from 8x8)
	CHAR_HEIGHT     = 16 // Character height in pixels (doubled from 8x8)
)

// FramebufferInfo holds information about the framebuffer
// This is shared between both implementations
type FramebufferInfo struct {
	Width       uint32         // Width in pixels
	Height      uint32         // Height in pixels
	Pitch       uint32         // Bytes per row
	Buf         unsafe.Pointer // Pointer to framebuffer memory
	BufSize     uint32         // Size of framebuffer in bytes
	CharsWidth  uint32         // Width in characters
	CharsHeight uint32         // Height in characters
	CharsX      uint32         // Current X cursor position (in characters)
	CharsY      uint32         // Current Y cursor position (in characters)
}

// Global framebuffer info (shared)
var fbinfo FramebufferInfo

// framebufferInit is implemented in:
// - framebuffer_rpi.go (for real hardware, build tag: !qemu)
// - framebuffer_qemu.go (for QEMU, build tag: qemu)
// Both implementations have the same signature:
//   func framebufferInit() int32
// Returns 0 on success, non-zero on error
