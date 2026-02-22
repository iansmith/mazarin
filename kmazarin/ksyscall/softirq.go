package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/shared/hid"
	"unsafe"
)

// SyscallWaitSoftIRQ drains soft IRQ events from a slot's ring buffer.
// arg0 = slot number (0-31)
// arg1 = pointer to SoftIRQReturn struct in userspace memory
// Returns: number of events (>0), or -EAGAIN if no events available.
//
// NOTE: Kernel-level thread blocking (BlockOnSlot) causes Go runtime
// P-starvation on all architectures. When a blocked thread is woken,
// exitsyscall() can't reliably re-acquire the P — all Ms eventually
// park in stopm() waiting for a P nobody is handing off. Userspace
// must poll with RawSyscall + Gosched instead.
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

	var events [hid.MaxHIDEvents]hid.HIDEvent

	// First check: non-blocking drain
	n := DrainSoftIRQSlotEvents(slot, events[:], hid.MaxHIDEvents)
	if n > 0 {
		intKind := GetSlotInterruptKind(slot)
		if err := writeSoftIRQReturn(bufPtr, events[:n], n, intKind); err != 0 {
			return err
		}
		return int64(n)
	}

	// No events available. Return EAGAIN immediately so userspace can
	// Gosched (yielding CPU to other goroutines) and the kernel can
	// preempt to other threads via timer interrupts.
	//
	// NOTE: We previously used enableIRQsAndWait() (STI+HLT/WFI) here
	// to reduce syscall rate. But on single-core systems, HLT inside a
	// syscall starves other processes: the timer ISR fires during kernel
	// mode (CS=0x08), preemption is skipped for safety, and the calling
	// process monopolizes the CPU. Returning EAGAIN ensures other
	// processes get CPU time through normal timer-driven preemption.
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
