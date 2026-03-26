//go:build linux

package sys

import (
	"errors"
	"mazzy/shared/mazzy"
	"unsafe"
)

// RegisterCursor registers a 64x64 NRGBA cursor image with the kernel GPU driver.
// pixelData must be exactly 64*64*4 = 16384 bytes of NRGBA pixel data.
// Returns the cursor ID on success.
func RegisterCursor(pixelData []byte, hotX, hotY int) (int, error) {
	if len(pixelData) < 64*64*4 {
		return -1, errors.New("RegisterCursor: pixel data too small")
	}
	r1, _, errno := RawSyscall(mazzy.SysRegisterCursor,
		uintptr(unsafe.Pointer(&pixelData[0])),
		64, // width
		64, // height
		uintptr(hotX),
		uintptr(hotY),
		0)
	if errno != 0 || int64(r1) < 0 {
		return -1, errors.New("RegisterCursor failed")
	}
	return int(r1), nil
}

// SetCursor switches the active hardware cursor to a previously registered cursor ID.
func SetCursor(cursorID int) error {
	r1, _, errno := RawSyscall(mazzy.SysSetCursor,
		uintptr(cursorID),
		0, 0, 0, 0, 0)
	if errno != 0 || int64(r1) < 0 {
		return errors.New("SetCursor failed")
	}
	return nil
}
