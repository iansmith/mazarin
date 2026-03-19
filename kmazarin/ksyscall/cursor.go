package ksyscall

import (
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/kmem"
)

// cursorStagingBuf is a static buffer for copying cursor pixel data from userspace.
// Only one cursor registration can be in progress at a time (syscalls are serialized).
const cursorPixelBytes = gpu.CursorWidth * gpu.CursorHeight * 4

var cursorStagingBuf [cursorPixelBytes]byte

// SyscallRegisterCursor registers a new cursor image with the GPU.
// arg0 = pointer to NRGBA pixel data in userspace (64*64*4 = 16384 bytes)
// arg1 = width (must be 64)
// arg2 = height (must be 64)
// arg3 = hotspot X
// arg4 = hotspot Y
// Returns cursor ID (>=0) on success, negative errno on failure.
//
//go:nosplit
func SyscallRegisterCursor(pixelPtr, width, height, hotX, hotY, _ uint64) int64 {
	if pixelPtr == 0 {
		return -14 // EFAULT
	}
	if width != gpu.CursorWidth || height != gpu.CursorHeight {
		return -22 // EINVAL — must be 64x64
	}

	// Copy NRGBA pixel data from userspace into static staging buffer.
	if !kmem.CopyFromUser(cursorStagingBuf[:], uintptr(pixelPtr), cursorPixelBytes) {
		return -14 // EFAULT
	}

	id := gpu.RegisterCursorImage(cursorStagingBuf[:], uint32(hotX), uint32(hotY))
	if id < 0 {
		return -12 // ENOMEM — registry full or GPU error
	}
	return int64(id)
}

// SyscallSetCursor switches the active cursor to a previously registered cursor ID.
// arg0 = cursor ID (from RegisterCursor)
// Returns 0 on success, negative errno on failure.
//
//go:nosplit
func SyscallSetCursor(cursorID, _, _, _, _, _ uint64) int64 {
	if !gpu.SetActiveCursor(int32(cursorID)) {
		return -22 // EINVAL — bad cursor ID
	}
	return 0
}
