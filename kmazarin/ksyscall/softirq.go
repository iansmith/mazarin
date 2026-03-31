package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/shared/hid"
	"sync/atomic"
	"unsafe"
)

// dbgWaitSoftIRQEntries counts total SyscallWaitSoftIRQ calls (for EVT diagnostic)
var DbgWaitSoftIRQEntries uint64

// slotEventBufs holds pre-allocated per-slot event buffers to avoid heap
// allocation in SyscallWaitSoftIRQ. Each slot is serviced by at most one
// kernel thread at a time, so no synchronization is needed.
var slotEventBufs [32][hid.MaxHIDEvents]hid.HIDEvent

// DbgSlot3DrainHit counts how many times slot 3 (timer) had events to drain.
var DbgSlot3DrainHit uint64

// DbgSlot3DrainMiss counts how many times slot 3 (timer) was polled but empty.
var DbgSlot3DrainMiss uint64

// SyscallWaitSoftIRQ drains soft IRQ events from a slot's ring buffer.
// arg0 = slot number (0-31)
// arg1 = pointer to SoftIRQReturn struct in userspace memory
// arg2 = flags: if bit 0 set, non-blocking (return -EAGAIN if no events)
// Returns: number of events (>0), or -EAGAIN if no events available.
//
// When called in non-blocking mode (arg2 & 1), the syscall returns
// immediately with -EAGAIN if no events are in the ring. This allows
// userspace to poll with runtime.Gosched() between calls, avoiding
// the entersyscall/exitsyscall P-acquisition deadlock on single-P
// configurations (GOMAXPROCS=1).
//
// When called in blocking mode (arg2 == 0), the calling kernel thread
// blocks via BlockOnSlot until events arrive.
//
//go:noinline
func SyscallWaitSoftIRQ(slotNum, bufPtr, flags, _, _, _ uint64) int64 {
	atomic.AddUint64(&DbgWaitSoftIRQEntries, 1)
	if slotNum >= 32 {
		return -22 // EINVAL
	}
	if bufPtr == 0 {
		return -14 // EFAULT
	}

	slot := int32(slotNum)
	nonblock := (flags & 1) != 0

	events := &slotEventBufs[slotNum]

	// Non-blocking drain
	//
	// CLAIM(hvf-vm-exit): The UART writes on slot 0 below allegedly serve a
	// functional purpose under HVF. The theory is that they cause VM exits
	// that give QEMU an opportunity to inject pending block device interrupts.
	// Without them, the vCPU supposedly runs the entire SVC handler at native
	// speed and block IRQ injection is delayed past the drain check. This
	// claim has NOT been rigorously verified. If block I/O works reliably
	// without these writes, they should be removed. An ISB or explicit yield
	// point would be a cleaner alternative if a VM exit is truly needed.
	// if slot == 0 {
	// 	serial.PollWrite('S') // CLAIM(hvf-vm-exit): forces VM exit for IRQ injection
	// }
	n := DrainSoftIRQSlotEvents(slot, events[:], hid.MaxHIDEvents)
	if n > 0 {
		// if slot == 0 {
		// 	serial.PollWrite('D') // CLAIM(hvf-vm-exit): forces VM exit for IRQ injection
		// }
		if slot == 3 {
			atomic.AddUint64(&DbgSlot3DrainHit, 1)
		}
		intKind := GetSlotInterruptKind(slot)
		if err := writeSoftIRQReturn(bufPtr, events[:n], n, intKind); err != 0 {
			return err
		}
		return int64(n)
	}

	// No events available
	if nonblock {
		if slot == 3 {
			atomic.AddUint64(&DbgSlot3DrainMiss, 1)
		}
		return -11 // EAGAIN — caller should Gosched and retry
	}

	// Blocking path: block this kernel thread on the slot.
	// if slot == 0 {
	// 	serial.PollWrite('b') // CLAIM(hvf-vm-exit): forces VM exit for IRQ injection
	// }
	ctxPtr := BlockOnSlot(slot)
	if ctxPtr != 0 {
		// Context switch to another thread. The wake path (PushTimerEventAndWake
		// or WakeSlotForIRQ) will rewind our ELR to re-execute the SVC, so
		// SyscallWaitSoftIRQ runs fresh and drains events on resume.
		SetSyscallSwitchTarget(ctxPtr)
		return -11 // Value doesn't matter — overwritten by re-executed SVC
	}

	// No other thread to switch to — drain-then-WFI loop until events arrive.
	// Drain BEFORE WFI: when the block IRQ fires during DAIFClr (before WFI
	// executes), events land in the ring but WFI still halts waiting for the
	// NEXT interrupt. Draining first catches events that arrived during
	// BlockOnSlot or in the DAIFClr→WFI window.
	for {
		n = DrainSoftIRQSlotEvents(slot, events[:], hid.MaxHIDEvents)
		if n > 0 {
			intKind := GetSlotInterruptKind(slot)
			if err := writeSoftIRQReturn(bufPtr, events[:n], n, intKind); err != 0 {
				return err
			}
			return int64(n)
		}
		enableIRQsAndWait()
	}
}

// SyscallRegisterSoftIRQ registers an IRQ on a soft IRQ slot for the current shepherd.
// arg0 = IRQ number
// arg1 = slot number (0-31)
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallRegisterSoftIRQ(irqNum, slotNum, _, _, _, _ uint64) int64 {
	pid := getCurrentThreadSID()
	return RegisterSoftIRQSlotKsyscall(uint32(irqNum), int32(slotNum), int16(pid))
}

// SyscallRegisterCompletionRing registers a userspace-owned shared completion
// ring page with the kernel. The kernel pins the page and the IRQ top-half
// writes completions directly to it.
// arg0 = completion ring page VA (userspace-allocated, page-aligned)
// arg1 = device type (0 = block device, 1 = HID input)
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallRegisterCompletionRing(ringVA, deviceType, _, _, _, _ uint64) int64 {
	if ringVA == 0 {
		return -14 // EFAULT
	}
	if ringVA&0xFFF != 0 {
		return -22 // EINVAL — must be page-aligned
	}
	pid := getCurrentThreadSID()
	switch deviceType {
	case 0:
		return RegisterBlockCompletionRing(uintptr(ringVA), int16(pid))
	case 1:
		return RegisterInputCompletionRing(uintptr(ringVA), int16(pid))
	default:
		return -22 // EINVAL
	}
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
		if !kmem.HandleUserPageFault(uintptr(bufPtr), 0) {
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

	dst := (*hid.SoftIRQReturn)(unsafe.Pointer(scratchVA + uintptr(pageOffset)))
	dst.Interrupt = intKind
	dst.Length = uint32(count)
	for i := 0; i < count && i < hid.MaxHIDEvents; i++ {
		dst.Events[i] = events[i]
	}

	return 0
}
