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

	// waitSoftIRQNonBlock is the flags value for non-blocking mode.
	// Passed as arg2 to SyscallWaitSoftIRQ.
	waitSoftIRQNonBlock = 1
)

// WaitSoftIRQ waits for soft IRQ events on the given slot.
//
// Uses non-blocking poll + Gosched loop with RawSyscall (no entersyscall).
// This avoids creating extra M's and futex contention that occurs with
// Syscall6/entersyscall under GOMAXPROCS=1. The goroutine cooperatively
// yields the P via Gosched between polls, allowing other goroutines to run.
func WaitSoftIRQ(slot int, buf *hid.SoftIRQReturn) (int, error) {
	for {
		r1, _, errno := RawSyscall(sysWaitSoftIRQ,
			uintptr(slot),
			uintptr(unsafe.Pointer(buf)),
			waitSoftIRQNonBlock, 0, 0, 0)

		if errno != 0 {
			if errno == 11 { // EAGAIN — no events ready
				runtime.Gosched()
				continue
			}
			return 0, errors.New("WaitSoftIRQ failed")
		}

		if r1 > 0 {
			return int(r1), nil
		}
		runtime.Gosched()
	}
}

// WaitSoftIRQBlocking blocks until events arrive on the given slot.
// Uses the kernel's BlockOnSlot mechanism — the thread sleeps until
// WakeSlotForIRQ wakes it when events are pushed to the ring.
//
// Calls runtime.entersyscall/exitsyscall to release the P before blocking,
// so other goroutines can run on a different M while this one sleeps.
func WaitSoftIRQBlocking(slot int, buf *hid.SoftIRQReturn) (int, error) {
	runtime_entersyscall()
	r1, _, errno := RawSyscall(sysWaitSoftIRQ,
		uintptr(slot),
		uintptr(unsafe.Pointer(buf)),
		0, 0, 0, 0) // flag=0 → blocking mode
	runtime_exitsyscall()

	if errno != 0 {
		return 0, errors.New("WaitSoftIRQBlocking failed")
	}
	if r1 > 0 {
		return int(r1), nil
	}
	return 0, nil
}

// TryWaitSoftIRQ is a non-blocking variant that returns (0, nil) if no events.
func TryWaitSoftIRQ(slot int, buf *hid.SoftIRQReturn) (int, error) {
	r1, _, errno := RawSyscall(sysWaitSoftIRQ,
		uintptr(slot),
		uintptr(unsafe.Pointer(buf)),
		waitSoftIRQNonBlock, 0, 0, 0)

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
