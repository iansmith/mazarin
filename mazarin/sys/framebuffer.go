package sys

import (
	"fmt"
	"syscall"
	"unsafe"
)

// FramebufferInfo contains information about the framebuffer.
// This is filled by the kernel during GetFramebuffer syscall.
type FramebufferInfo struct {
	Addr   uintptr // Virtual address of framebuffer in priest space
	Width  uint32  // Width in pixels
	Height uint32  // Height in pixels
	Pitch  uint32  // Bytes per row (typically Width * 4 for BGRA)
}

// GetFramebuffer retrieves framebuffer information from the kernel.
// The framebuffer is pre-mapped into priest's address space at a fixed VA.
// Returns an error if the framebuffer is not available.
func GetFramebuffer() (*FramebufferInfo, error) {
	var fb FramebufferInfo

	_, _, errno := syscall.RawSyscall6(
		sysGetFramebuffer,
		uintptr(unsafe.Pointer(&fb)),
		0, 0, 0, 0, 0,
	)

	if errno != 0 {
		return nil, fmt.Errorf("GetFramebuffer failed: errno %d", errno)
	}

	return &fb, nil
}

// FlushFramebuffer tells the kernel to transfer and flush a region of
// the framebuffer to the display. Without this, writes to the framebuffer
// backing memory are not visible on screen.
func FlushFramebuffer(x, y, width, height uint32) error {
	_, _, errno := syscall.RawSyscall6(
		sysFlushFramebuffer,
		uintptr(x),
		uintptr(y),
		uintptr(width),
		uintptr(height),
		0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("FlushFramebuffer failed: errno %d", errno)
	}
	return nil
}
