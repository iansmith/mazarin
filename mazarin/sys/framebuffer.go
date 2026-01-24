package sys

import (
	"fmt"
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
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

	result, _, _ := syscall.RawSyscall6(
		sysGetFramebuffer,
		uintptr(unsafe.Pointer(&fb)),
		0, 0, 0, 0, 0,
	)

	if merror.IsError(uint64(result)) {
		return nil, fmt.Errorf("GetFramebuffer failed: %s", merror.String(uint64(result)))
	}

	return &fb, nil
}
