package sys

import (
	"errors"
	"mazzy/shared/hid"
	"runtime"
	"unsafe"
)

const (
	sysWaitSoftIRQ       = 0x100A
	sysRegisterSoftIRQ   = 0x100B
	sysQueryInputDevices = 0x100C
)

// WaitSoftIRQ polls until soft IRQ events arrive on the given slot.
// Uses RawSyscall (non-blocking) + runtime.Gosched() to yield to the
// Go scheduler between retries. This avoids kernel-level thread blocking
// which causes Go runtime P-starvation: when a blocked thread is woken
// by the kernel, exitsyscall() can't acquire the P (held by another
// goroutine), so the goroutine parks permanently in the global run queue.
// Returns the number of events written to buf, or an error.
func WaitSoftIRQ(slot int, buf *hid.SoftIRQReturn) (int, error) {
	for {
		r1, _, errno := RawSyscall(sysWaitSoftIRQ,
			uintptr(slot),
			uintptr(unsafe.Pointer(buf)),
			0, 0, 0, 0)

		if errno != 0 {
			if errno == 11 { // EAGAIN — no events yet
				runtime.Gosched()
				continue
			}
			return 0, errors.New("WaitSoftIRQ failed")
		}

		if r1 == 0 {
			// No events, yield and retry
			runtime.Gosched()
			continue
		}

		return int(r1), nil
	}
}

// TryWaitSoftIRQ is a non-blocking variant that returns (0, nil) if no events.
func TryWaitSoftIRQ(slot int, buf *hid.SoftIRQReturn) (int, error) {
	r1, _, errno := RawSyscall(sysWaitSoftIRQ,
		uintptr(slot),
		uintptr(unsafe.Pointer(buf)),
		0, 0, 0, 0)

	if errno != 0 {
		if errno == 11 { // EAGAIN
			return 0, nil
		}
		return 0, errors.New("TryWaitSoftIRQ failed")
	}
	return int(r1), nil
}

// RegisterSoftIRQ registers an IRQ number on a soft IRQ slot.
// The current priest becomes the owner of the slot.
func RegisterSoftIRQ(irqNum uint32, slot int) error {
	r1, _, errno := RawSyscall(sysRegisterSoftIRQ,
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
	r1, _, errno := RawSyscall(sysQueryInputDevices,
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
