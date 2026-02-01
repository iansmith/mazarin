package ksyscall

import (
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/kmem"
	"unsafe"
)

// SyscallFlushFramebuffer transfers and flushes a region of the framebuffer to the display.
// arg0 = x, arg1 = y, arg2 = width, arg3 = height (in framebuffer pixel coordinates)
//
//go:nosplit
func SyscallFlushFramebuffer(x, y, width, height, _, _ uint64) int64 {
	gpu.UpdateDisplay(uint32(x), uint32(y), uint32(width), uint32(height))
	return 0
}

// FramebufferInfo matches the layout expected by userspace (mazarin/sys.FramebufferInfo)
type FramebufferInfo struct {
	Addr   uint64 // Virtual address of framebuffer in priest space
	Width  uint32 // Width in pixels
	Height uint32 // Height in pixels
	Pitch  uint32 // Bytes per row
}

// SyscallGetFramebuffer fills in framebuffer info for userspace.
// arg0: pointer to FramebufferInfo struct in userspace
//
//go:nosplit
func SyscallGetFramebuffer(fbInfoPtr, _, _, _, _, _ uint64) int64 {
	if fbInfoPtr == 0 {
		return -14 // EFAULT - bad address
	}

	// Get GPU dimensions
	width := gpu.GetWidth()
	height := gpu.GetHeight()

	// If GPU not initialized, return error
	if width == 0 || height == 0 {
		return -19 // ENODEV - no such device
	}

	// Create the info struct
	info := FramebufferInfo{
		Addr:   UserFramebufferVA,
		Width:  width,
		Height: height,
		Pitch:  width * 4, // 4 bytes per pixel (BGRA)
	}

	// Ensure user page is mapped (demand-page if needed)
	if kmem.WalkUserPageTable(uintptr(fbInfoPtr)) == 0 {
		if !kmem.HandleUserPageFault(uintptr(fbInfoPtr)) {
			return -14 // EFAULT - can't map
		}
	}

	// Copy to userspace via scratch mapping
	userPA := kmem.WalkUserPageTable(uintptr(fbInfoPtr))
	if userPA == 0 {
		return -14 // EFAULT - page not mapped
	}

	// Map the user page to kernel scratch and copy
	pageOffset := fbInfoPtr & 0xFFF
	scratchVA := kmem.MapPAToKernelScratch(userPA &^ 0xFFF)
	if scratchVA == 0 {
		return -14 // EFAULT - can't map
	}

	// Copy struct to userspace
	dst := (*FramebufferInfo)(unsafe.Pointer(scratchVA + uintptr(pageOffset)))
	*dst = info

	return 0
}
