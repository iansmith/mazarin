package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/shared/hid"
	"unsafe"
)

// Pre-allocated per-class event buffers for SyscallWaitInputEvent.
// Each class is serviced by at most one thread at a time per shepherd.
var inputEventBufs [hid.InputClassCount][hid.MaxHIDEvents]hid.HIDEvent

// SyscallRequestWindowManager claims the window manager role for the calling shepherd.
// First-come-first-served; only one WM per system.
// Returns: 0 on success, -1 (EPERM) if already claimed.
//
//go:noinline
func SyscallRequestWindowManager(_, _, _, _, _, _ uint64) int64 {
	sid := getCurrentThreadSID()
	if RequestWindowManagerKernel(int32(sid)) {
		return 0
	}
	return -1 // EPERM — already claimed
}

// SyscallSetInputFocus sets focus for a device class.
// arg0 = target shepherd ID (0 = self)
// arg1 = device class (InputClassKeyboard, InputClassMouseClick, InputClassMouseMove)
// Returns: 0 on success, negative errno on error.
//
// Permission: only self or window manager can set focus.
//
//go:noinline
func SyscallSetInputFocus(targetSID, deviceClass, _, _, _, _ uint64) int64 {
	callerSID := getCurrentThreadSID()
	target := int32(targetSID)
	if target == 0 {
		target = int32(callerSID)
	}
	class := int(deviceClass)

	// Permission: only self or window manager
	if int32(callerSID) != target && int32(callerSID) != GetWindowManagerSID() {
		return -1 // EPERM
	}
	if class < 0 || class >= hid.InputClassCount {
		return -22 // EINVAL
	}

	return SetInputFocusKernel(target, class)
}

// SyscallWaitInputEvent blocks until input events arrive for the caller's
// device class queue. Drains events into a userspace SoftIRQReturn buffer.
// arg0 = device class (0=keyboard, 1=mouse-click, 2=mouse-move)
// arg1 = pointer to SoftIRQReturn struct in userspace
// Returns: number of events (>0), or negative errno.
//
//go:noinline
func SyscallWaitInputEvent(deviceClass, bufPtr, _, _, _, _ uint64) int64 {
	if bufPtr == 0 {
		return -14 // EFAULT
	}
	class := int(deviceClass)
	if class < 0 || class >= hid.InputClassCount {
		return -22 // EINVAL
	}

	slot := proc.ShepherdSlot(getCurrentThreadSlotWrapper())
	events := &inputEventBufs[class]

	// Non-blocking drain attempt
	n := DrainInputQueue(slot, class, events[:], hid.MaxHIDEvents)
	if n > 0 {
		if err := writeInputEventReturn(bufPtr, events[:n], n, class); err != 0 {
			return err
		}
		return int64(n)
	}

	// Blocking path: block this kernel thread on the input queue.
	ctxPtr := BlockOnInputQueue(slot, class)
	if ctxPtr != 0 {
		SetSyscallSwitchTarget(ctxPtr)
		return -11 // Value doesn't matter — overwritten by re-executed SVC
	}

	// No other thread to switch to — WFI loop until events arrive.
	for {
		enableIRQsAndWait()
		n = DrainInputQueue(slot, class, events[:], hid.MaxHIDEvents)
		if n > 0 {
			if err := writeInputEventReturn(bufPtr, events[:n], n, class); err != 0 {
				return err
			}
			return int64(n)
		}
	}
}

// writeInputEventReturn writes a SoftIRQReturn struct to userspace for input events.
func writeInputEventReturn(bufPtr uint64, events []hid.HIDEvent, count int, class int) int64 {
	if kmem.WalkUserPageTable(uintptr(bufPtr)) == 0 {
		if !kmem.HandleUserPageFault(uintptr(bufPtr), 0) {
			return -14
		}
	}

	userPA := kmem.WalkUserPageTable(uintptr(bufPtr))
	if userPA == 0 {
		return -14
	}

	pageOffset := bufPtr & 0xFFF
	scratchVA := kmem.MapPAToKernelScratch(userPA &^ 0xFFF)
	if scratchVA == 0 {
		return -14
	}

	// Map device class to InterruptType for the return struct
	var intKind hid.InterruptType
	switch class {
	case hid.InputClassKeyboard:
		intKind = hid.KeyboardInterrupt
	case hid.InputClassMouseClick, hid.InputClassMouseMove:
		intKind = hid.MouseInterrupt
	}

	dst := (*hid.SoftIRQReturn)(unsafe.Pointer(scratchVA + uintptr(pageOffset)))
	dst.Interrupt = intKind
	dst.Length = uint32(count)
	for i := 0; i < count && i < hid.MaxHIDEvents; i++ {
		dst.Events[i] = events[i]
	}

	return 0
}
