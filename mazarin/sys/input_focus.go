package sys

import (
	"errors"
	"mazzy/shared/hid"
	"mazzy/shared/mazzy"
	"runtime"
	"unsafe"
)

// RequestWindowManager claims the window manager role for the calling shepherd.
// First-come-first-served; only one WM per system.
func RequestWindowManager() error {
	r1, _, errno := RawSyscall(mazzy.SysRequestWindowManager, 0, 0, 0, 0, 0, 0)
	if errno != 0 || int64(r1) < 0 {
		return errors.New("RequestWindowManager: already claimed")
	}
	return nil
}

// SetInputFocus sets input focus for a device class.
// targetSID=0 means "self". Only self or the WM can call this.
func SetInputFocus(targetSID int, class int) error {
	r1, _, errno := RawSyscall(mazzy.SysSetInputFocus,
		uintptr(targetSID),
		uintptr(class),
		0, 0, 0, 0)
	if errno != 0 || int64(r1) < 0 {
		return errors.New("SetInputFocus failed")
	}
	return nil
}

// WaitInputEvent blocks until input events arrive for the caller's device class queue.
// Returns the number of events and fills buf with HID event data.
// Uses entersyscall/exitsyscall to release the P while blocking.
func WaitInputEvent(class int, buf *hid.SoftIRQReturn) (int, error) {
	runtime_entersyscall()
	r1, _, errno := RawSyscall(mazzy.SysWaitInputEvent,
		uintptr(class),
		uintptr(unsafe.Pointer(buf)),
		0, 0, 0, 0)
	runtime_exitsyscall()

	if errno != 0 {
		return 0, errors.New("WaitInputEvent failed")
	}
	if r1 > 0 {
		return int(r1), nil
	}
	return 0, nil
}

// PollInputEvent is a single non-blocking attempt that returns (0, nil) if no events.
func PollInputEvent(class int, buf *hid.SoftIRQReturn) (int, error) {
	r1, _, errno := RawSyscall(mazzy.SysWaitInputEvent,
		uintptr(class),
		uintptr(unsafe.Pointer(buf)),
		0, 0, 0, 0)

	if errno != 0 {
		return 0, nil // no events
	}
	if r1 > 0 {
		return int(r1), nil
	}
	runtime.Gosched()
	return 0, nil
}
