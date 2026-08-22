package sys

import (
	"errors"
	"mazzy/shared/hid"
	"mazzy/shared/mazzy"
	"unsafe"
)

// WaitSoftIRQ blocks until events arrive on the given slot.
// Uses the kernel's BlockOnSlot mechanism — the thread sleeps until
// WakeSlotForIRQ wakes it when events are pushed to the ring.
// Uses BlockingSyscall (entersyscallblock) because this is a known-blocking
// call — the kernel holds the thread until IRQ events arrive.
func WaitSoftIRQ(slot int, buf *hid.SoftIRQReturn) (int, error) {
	r1, _, errno := BlockingSyscall(mazzy.SysWaitSoftIRQ,
		uintptr(slot),
		uintptr(unsafe.Pointer(buf)),
		0, 0, 0, 0) // flag=0 → blocking mode

	if errno != 0 {
		return 0, errors.New("WaitSoftIRQ failed")
	}
	if r1 > 0 {
		return int(r1), nil
	}
	return 0, nil
}

// RegisterSoftIRQ registers an IRQ number on a soft IRQ slot.
// The current shepherd becomes the owner of the slot.
func RegisterSoftIRQ(irqNum uint32, slot int) error {
	r1, _, errno := RawSyscall(mazzy.SysRegisterSoftIRQ,
		uintptr(irqNum),
		uintptr(slot),
		0, 0, 0, 0)

	if errno != 0 || int64(r1) < 0 {
		return errors.New("RegisterSoftIRQ failed")
	}
	return nil
}

// QueryInputDevices returns information about available input devices.
func QueryInputDevices() ([]hid.InputDeviceInfo, error) {
	var infos [8]hid.InputDeviceInfo
	r1, _, errno := RawSyscall(mazzy.SysQueryInputDevices,
		uintptr(unsafe.Pointer(&infos[0])),
		0, 0, 0, 0, 0)

	if errno != 0 || int64(r1) < 0 {
		return nil, errors.New("QueryInputDevices failed")
	}

	n := int(r1)
	if n > 8 {
		n = 8
	}
	return infos[:n], nil
}
