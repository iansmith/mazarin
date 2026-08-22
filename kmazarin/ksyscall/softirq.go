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

// SyscallQueryInputDevices fills a userspace array with available input device info.
// arg0 = pointer to InputDeviceInfo array in userspace (max 8 entries)
// Returns: number of devices found, or negative errno.
//
// ┌──────────────────────────────────────────────────────────────────────────┐
// │ ALLOCATION-FREE CONTRACT (MAZ-136) — this handler and its whole call tree  │
// │ must never heap-allocate.                                                  │
// └──────────────────────────────────────────────────────────────────────────┘
// A user syscall runs INLINE on the exception stack under the kernel's
// borrowed g0/m0 identity (the g0 switch in exceptions_{amd64,arm64}.s). Heap
// allocation on that path reads the mcache via getMCache(g0.m) — g0.m.p.mcache,
// or the global mcache0 when m0 is P-less — and nil-derefs deep inside mallocgc
// whenever m0 has no P and mcache0 is cleared (the MAZ-136 crash:
// `!F:0 @mallocgcSmallNoscan`). The MPNIL tripwire at the borrow site halts on
// that exact condition; this contract keeps handlers from ever reaching an
// allocator on that path in the first place. The transitive must-stay-
// allocation-free set is:
//
//	SyscallQueryInputDevices
//	  ├─ kmem.WalkUserPageTable / WalkUserPageTableWithL0  (page-table walk)
//	  ├─ kmem.HandleUserPageFault                          (demand-page fill)
//	  ├─ kmem.MapPAToKernelScratch                         (scratch remap)
//	  └─ QueryInputDevicesKernel (soft_irq_slots.go)       (see its own contract)
//
// The user destination is filled DIRECTLY through the scratch mapping (non-heap
// memory) instead of via a local [maxDevices]hid.InputDeviceInfo array: that
// array escaped to the heap (newobject, 128-byte class) across the //go:noinline
// cross-package QueryInputDevicesKernel call and was the convicted allocation.
// Do NOT reintroduce escaping locals, slices, maps, closures, interface boxing,
// or fmt here. VERIFY after any change: the handler's frame must show no call to
// runtime.newobject / runtime.mallocgc in the FINAL ELF (disassemble the symbol
// with gobjdump for amd64 / llvm-objdump for arm64). The residual GC-STW window
// where p != 0 but the mcache is mid-flush is tracked separately as MAZ-140.
//
//go:noinline
func SyscallQueryInputDevices(bufPtr, _, _, _, _, _ uint64) int64 {
	if bufPtr == 0 {
		return -14 // EFAULT
	}

	const maxDevices = 8

	// Map the user page FIRST, then have QueryInputDevicesKernel fill it
	// DIRECTLY through the scratch mapping — no intermediate
	// [maxDevices]hid.InputDeviceInfo array. That local array used to escape
	// to the heap (newobject, 128-byte size class) across the //go:noinline
	// cross-package QueryInputDevicesKernel call, and a user syscall runs on
	// the borrowed g0/m0 identity: if m0 is P-less, the escape nil-derefs
	// deep inside mallocgc on the exception stack (MAZ-136). The scratch
	// mapping is non-heap memory, so the slice over it does not allocate —
	// this handler is now allocation-free (MAZ-136 Option D). The paging /
	// single-page-scratch behavior is unchanged from the prior version.
	if kmem.WalkUserPageTable(uintptr(bufPtr)) == 0 {
		if !kmem.HandleUserPageFault(uintptr(bufPtr), 0) {
			return -14 // EFAULT
		}
	}

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
	n := QueryInputDevicesKernel(dst[:], maxDevices)

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
