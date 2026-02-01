package ksyscall

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/kmem"
	"mazzy/shared/hid"
	"sync/atomic"
	"unsafe"
)

var waitSoftIRQCallCount uint32

// SyscallWaitSoftIRQ blocks the current thread until soft IRQ events arrive on a slot.
// arg0 = slot number (0-31)
// arg1 = pointer to SoftIRQReturn struct in userspace memory
// Returns: number of events (>0), or negative errno on error.
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

	// Try draining events first (non-blocking fast path)
	var events [hid.MaxHIDEvents]hid.HIDEvent
	n := DrainSoftIRQSlotEvents(slot, events[:], hid.MaxHIDEvents)

	cnt := atomic.AddUint32(&waitSoftIRQCallCount, 1)
	if cnt <= 10 {
		console.KPrintf("[WaitSoftIRQ] slot=%d call#%d drained=%d\n", slotNum, cnt, n)
	}
	if n > 0 {
		// Write results to userspace
		if err := writeSoftIRQReturn(bufPtr, events[:n], n); err != 0 {
			return err
		}
		return int64(n)
	}

	// No events available — return EAGAIN so userspace can retry
	// (The eventPoller goroutine may not be running, so blocking would deadlock)
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
func writeSoftIRQReturn(bufPtr uint64, events []hid.HIDEvent, count int) int64 {
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
	dst.Length = uint32(count)
	for i := 0; i < count && i < hid.MaxHIDEvents; i++ {
		dst.Events[i] = events[i]
	}

	return 0
}
