package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/shared/hid"
	"unsafe"
)

// SyscallWaitSoftIRQ blocks the current thread until soft IRQ events arrive on a slot.
// arg0 = slot number (0-31)
// arg1 = pointer to SoftIRQReturn struct in userspace memory
// Returns: number of events (>0), 0 (woke from block, retry), or negative errno.
//
//go:noinline
func SyscallWaitSoftIRQ(slotNum, bufPtr, _, _, _, _ uint64) int64 {
	if slotNum >= 32 {
		return -22 // EINVAL
	}
	if bufPtr == 0 {
		return -14 // EFAULT
	}

	slot := int32(slotNum)

	// Fast path: drain ring buffer
	var events [hid.MaxHIDEvents]hid.HIDEvent
	n := DrainSoftIRQSlotEvents(slot, events[:], hid.MaxHIDEvents)

	if n > 0 {
		intKind := GetSlotInterruptKind(slot)
		if err := writeSoftIRQReturn(bufPtr, events[:n], n, intKind); err != 0 {
			return err
		}
		return int64(n)
	}

	// No events available — return EAGAIN so userspace can yield
	// to the Go scheduler (runtime.Gosched) and retry.
	// CRITICAL: Do NOT use BlockOnSlot here. Kernel-level thread
	// blocking causes Go runtime P-starvation: the woken thread's
	// exitsyscall() can't acquire the P (held by another goroutine),
	// so the goroutine parks permanently in the global run queue.
	return -11 // EAGAIN
}

// SyscallRegisterSoftIRQ registers an IRQ on a soft IRQ slot for the current priest.
// arg0 = IRQ number
// arg1 = slot number (0-31)
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallRegisterSoftIRQ(irqNum, slotNum, _, _, _, _ uint64) int64 {
	pid := getCurrentThreadPID()
	return RegisterSoftIRQSlotKsyscall(uint32(irqNum), int32(slotNum), int16(pid))
}

// SyscallQueryInputDevices fills a userspace array with available input device info.
// arg0 = pointer to InputDeviceInfo array in userspace (max 8 entries)
// Returns: number of devices found, or negative errno.
//
//go:noinline
func SyscallQueryInputDevices(bufPtr, _, _, _, _, _ uint64) int64 {
	if bufPtr == 0 {
		return -14 // EFAULT
	}

	const maxDevices = 8
	var infos [maxDevices]hid.InputDeviceInfo
	n := QueryInputDevicesKernel(infos[:], maxDevices)

	if n == 0 {
		return 0
	}

	// Ensure user page is mapped (demand-page if needed)
	if kmem.WalkUserPageTable(uintptr(bufPtr)) == 0 {
		if !kmem.HandleUserPageFault(uintptr(bufPtr)) {
			return -14 // EFAULT
		}
	}

	// Write to userspace via scratch mapping
	userPA := kmem.WalkUserPageTable(uintptr(bufPtr))
	if userPA == 0 {
		return -14 // EFAULT
	}

	pageOffset := bufPtr & 0xFFF
	scratchVA := kmem.MapPAToKernelScratch(userPA &^ 0xFFF)
	if scratchVA == 0 {
		return -14
	}

	dst := (*[maxDevices]hid.InputDeviceInfo)(unsafe.Pointer(scratchVA + uintptr(pageOffset)))
	for i := 0; i < n; i++ {
		dst[i] = infos[i]
	}

	return int64(n)
}

// writeSoftIRQReturn writes a SoftIRQReturn struct to userspace memory.
// Returns 0 on success, negative errno on failure.
func writeSoftIRQReturn(bufPtr uint64, events []hid.HIDEvent, count int, intKind hid.InterruptType) int64 {
	// Ensure user page is mapped
	if kmem.WalkUserPageTable(uintptr(bufPtr)) == 0 {
		if !kmem.HandleUserPageFault(uintptr(bufPtr)) {
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

	dst := (*hid.SoftIRQReturn)(unsafe.Pointer(scratchVA + uintptr(pageOffset)))
	dst.Interrupt = intKind
	dst.Length = uint32(count)
	for i := 0; i < count && i < hid.MaxHIDEvents; i++ {
		dst.Events[i] = events[i]
	}

	return 0
}
