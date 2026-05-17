package main

// iouring.go — kernel-side io_uring state and blocking/waking functions.
//
// The io_uring ring table and blocking state live here (main package) so the
// IRQ top-half and scheduler have direct access to Thread. The syscall handlers
// in ksyscall reach these via go:linkname bridges.

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/proc"
	"mazzy/shared/iouring"
	"sync/atomic"
	"unsafe"
)

// MaxIORings is the maximum number of io_uring instances.
const MaxIORings = 4

// IOUring device types — stored in IOUringSlot.DeviceType.
const (
	IOUringDeviceBlock uint8 = 0 // block device (fs shepherd)
	IOUringDeviceInput uint8 = 1 // HID input devices (window manager)
	IOUringDeviceNet   uint8 = 2 // VirtIO-net (net shepherd, MAZ-28 step 2)
)

// IOUringSlot holds kernel-side state for one io_uring instance.
type IOUringSlot struct {
	KVA           uintptr // kernel VA of the ring page
	PA            uintptr // PA of the ring page (for cleanup)
	OwnerSID      int16   // shepherd that owns this ring
	BlockedTID    int16   // TID of thread blocked on IOUringEnter (-1 = none)
	BlockedPtr    uintptr // *Thread as uintptr (nosplit-safe, no write barrier)
	MinComplete   uint32  // completions needed to wake blocked thread
	BlockDeadline uint64  // timer tick deadline for 10ms timeout (0 = not blocking)
	DeviceType    uint8   // IOUringDeviceBlock or IOUringDeviceInput
}

// IOUringTable holds all io_uring instances.
var IOUringTable [MaxIORings]IOUringSlot

// IOUringBlockedRingID is DEPRECATED — kept only as a compilation target
// for bottom_half.go references during migration. WakeIOUringFromIRQ now
// scans all slots. Will be removed after bottom_half.go is updated.
var IOUringBlockedRingID int32 = -1

// IOUringTimeoutTicks is 10ms worth of hardware timer ticks.
// Set once from SystemTimerFrequency on first IOUringSetup call.
var IOUringTimeoutTicks uint64

// WakeIOUringFromIRQ diagnostic counters (nosplit-safe atomics).
var (
	dbgWakeURCalls     uint32 // total WakeIOUringFromIRQ calls
	dbgWakeURNoWaiter  uint32 // all slots had BlockedTID < 0
	dbgWakeURNotEnough uint32 // had waiter, but completions < minComplete
	dbgWakeURWoke      uint32 // successfully woke a thread
)

// Timeout path diagnostic counters (nosplit-safe atomics).
var (
	dbgTimeoutWakes     uint32 // total timeout wakes
	dbgTimeoutHadEnough uint32 // CQ had enough completions at timeout
	dbgTimeoutNotEnough uint32 // CQ did NOT have enough at timeout
	dbgTimeoutBlkOK     uint32 // block device timeout had enough
	dbgTimeoutBlkNE     uint32 // block device timeout not enough
	dbgTimeoutInpOK     uint32 // input device timeout had enough
	dbgTimeoutInpNE     uint32 // input device timeout not enough
)

// InitIOUringTimeout computes the timeout ticks from the system timer frequency.
// Called from SyscallIOUringSetup.
func InitIOUringTimeout() {
	if IOUringTimeoutTicks == 0 && kirq.SystemTimerFrequency > 0 {
		IOUringTimeoutTicks = kirq.SystemTimerFrequency / 100 // 10ms
	}
}

// BlockForIOUring blocks the current thread until io_uring completions arrive.
// syscallNum is the syscall to re-execute on wake (SysIOUringEnter or
// SysIOUringWaitInput). Returns the context pointer of the next thread to
// run, or 0 if no other thread is available (caller does WFI).
//
//go:noinline
func BlockForIOUring(ringID int, minComplete uint32, syscallNum uint64) uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()

	// Re-check completions with IRQs disabled to close the TOCTOU race:
	// between the fast-path check (IRQs enabled) and here, an IRQ may have
	// delivered CQEs. If so, return ^uintptr(0) sentinel — caller returns
	// the completions directly without blocking.
	ring := (*iouring.IORing)(unsafe.Pointer(IOUringTable[ringID].KVA))
	cqTail := atomic.LoadUint32(&ring.CQTail)
	cqHead := atomic.LoadUint32(&ring.CQHead)
	if cqTail-cqHead >= minComplete {
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return ^uintptr(0) // sentinel: completions already available
	}

	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	next := findNextThreadForBlockSchedLockHeld(t)
	if next == nil {
		// No other thread — return 0, caller does WFI loop.
		if IOUringTimeoutTicks > 0 {
			IOUringTable[ringID].BlockDeadline = kirq.ReadCounterValue() + IOUringTimeoutTicks
		}
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Block the current thread.
	t.State = ThreadBlockedIOUring
	t.SoftIRQSlotArg = uint64(ringID)
	t.SoftIRQSyscallNum = syscallNum

	slot := &IOUringTable[ringID]
	slot.BlockedTID = int16(t.TID)
	slot.BlockedPtr = uintptr(unsafe.Pointer(t))
	slot.MinComplete = minComplete
	if IOUringTimeoutTicks > 0 {
		slot.BlockDeadline = kirq.ReadCounterValue() + IOUringTimeoutTicks
	}

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// WakeIOUringFromIRQ checks all io_uring slots for blocked threads whose
// minComplete is satisfied and wakes them. Called from the IRQ top-half
// after writing CQEs. With MaxIORings=4, the scan is negligible cost.
// MUST be called with IRQs disabled (from IRQ context).
//
//go:nosplit
//go:noinline
func WakeIOUringFromIRQ() {
	atomic.AddUint32(&dbgWakeURCalls, 1)

	// Quick scan: any slots with blocked waiters that have enough CQEs?
	anyToWake := false
	hadWaiter := false
	for i := 0; i < MaxIORings; i++ {
		slot := &IOUringTable[i]
		if slot.BlockedTID < 0 || slot.KVA == 0 {
			continue
		}
		hadWaiter = true
		ring := (*iouring.IORing)(unsafe.Pointer(slot.KVA))
		if atomic.LoadUint32(&ring.CQTail)-atomic.LoadUint32(&ring.CQHead) >= slot.MinComplete {
			anyToWake = true
			break
		}
	}
	if !anyToWake {
		if hadWaiter {
			atomic.AddUint32(&dbgWakeURNotEnough, 1)
		} else {
			atomic.AddUint32(&dbgWakeURNoWaiter, 1)
		}
		return
	}

	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	for i := 0; i < MaxIORings; i++ {
		slot := &IOUringTable[i]
		if slot.BlockedTID < 0 || slot.KVA == 0 {
			continue
		}

		ring := (*iouring.IORing)(unsafe.Pointer(slot.KVA))
		completions := atomic.LoadUint32(&ring.CQTail) - atomic.LoadUint32(&ring.CQHead)
		if completions < slot.MinComplete {
			continue
		}

		t := (*Thread)(unsafe.Pointer(slot.BlockedPtr))
		if t != nil && t.State == ThreadBlockedIOUring {
			atomic.AddUint32(&dbgWakeURWoke, 1)
			t.State = ThreadReady
			t.PriorityWoken = true
			t.Context.RewindToSyscall()
			t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
			t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)

			slot.BlockedTID = -1
			slot.BlockedPtr = 0
			slot.BlockDeadline = 0

			enqueueReadyPrioritySchedLockHeld(t)
			atomic.StoreUint32(&priorityWakePending, 1)
			asm.Dsb()
		}
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// topHalfIOUringTimeoutHook is called via indirect function pointer from
// ProcessDeadlinesTopHalf (timer IRQ) to check the 10ms safety timeout.
// Using an indirect call avoids the nosplit stack checker tracing through
// the full chain — the exception stack is large enough for the actual usage.
var topHalfIOUringTimeoutHook func() = checkIOUringTimeoutFromTimer

// checkIOUringTimeoutFromTimer checks if any blocked io_uring thread has
// timed out. Called from timer tick with schedulerLock held and IRQs disabled.
//
//go:nosplit
func checkIOUringTimeoutFromTimer() {
	now := kirq.ReadCounterValue()
	for i := 0; i < MaxIORings; i++ {
		slot := &IOUringTable[i]
		if slot.BlockedTID < 0 || slot.BlockDeadline == 0 {
			continue
		}
		if now < slot.BlockDeadline {
			continue
		}

		// Timeout expired — wake thread with whatever completions are available.
		t := (*Thread)(unsafe.Pointer(slot.BlockedPtr))
		if t != nil && t.State == ThreadBlockedIOUring {
			// Snapshot CQ state at timeout for diagnostics.
			ring := (*iouring.IORing)(unsafe.Pointer(slot.KVA))
			tmoCQTail := atomic.LoadUint32(&ring.CQTail)
			tmoCQHead := atomic.LoadUint32(&ring.CQHead)
			tmoAvail := tmoCQTail - tmoCQHead
			atomic.AddUint32(&dbgTimeoutWakes, 1)
			if tmoAvail >= slot.MinComplete {
				atomic.AddUint32(&dbgTimeoutHadEnough, 1)
				if slot.DeviceType == IOUringDeviceBlock {
					atomic.AddUint32(&dbgTimeoutBlkOK, 1)
				} else {
					atomic.AddUint32(&dbgTimeoutInpOK, 1)
				}
			} else {
				atomic.AddUint32(&dbgTimeoutNotEnough, 1)
				if slot.DeviceType == IOUringDeviceBlock {
					atomic.AddUint32(&dbgTimeoutBlkNE, 1)
				} else {
					atomic.AddUint32(&dbgTimeoutInpNE, 1)
				}
			}

			t.State = ThreadReady
			t.Context.RewindToSyscall()
			t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
			t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)

			slot.BlockedTID = -1
			slot.BlockedPtr = 0
			slot.BlockDeadline = 0

			enqueueReadySchedLockHeld(t) // Regular priority (timeout, not IRQ)
		}
	}
}

// GetIOUringSlotForBlockIRQ returns the block device ring slot and IORing
// pointer for writing block I/O CQEs from the IRQ top-half.
// Returns nil, nil if no block ring is set up.
//
//go:nosplit
func GetIOUringSlotForBlockIRQ() (*IOUringSlot, *iouring.IORing) {
	for i := 0; i < MaxIORings; i++ {
		slot := &IOUringTable[i]
		if slot.KVA != 0 && slot.DeviceType == IOUringDeviceBlock {
			ring := (*iouring.IORing)(unsafe.Pointer(slot.KVA))
			return slot, ring
		}
	}
	return nil, nil
}

// GetIOUringSlotForInputIRQ returns the input device ring slot and IORing
// pointer for writing HID event CQEs from the IRQ top-half.
// Returns nil, nil if no input ring is set up.
//
//go:nosplit
func GetIOUringSlotForInputIRQ() (*IOUringSlot, *iouring.IORing) {
	for i := 0; i < MaxIORings; i++ {
		slot := &IOUringTable[i]
		if slot.KVA != 0 && slot.DeviceType == IOUringDeviceInput {
			ring := (*iouring.IORing)(unsafe.Pointer(slot.KVA))
			return slot, ring
		}
	}
	return nil, nil
}

// GetIOUringSlotForNetIRQ returns the net device ring slot and IORing
// pointer for writing RX completion CQEs from the IRQ top-half (MAZ-28
// step 2). Returns nil, nil if no net ring is set up (net.elf not yet
// started, or shut down).
//
//go:nosplit
func GetIOUringSlotForNetIRQ() (*IOUringSlot, *iouring.IORing) {
	for i := 0; i < MaxIORings; i++ {
		slot := &IOUringTable[i]
		if slot.KVA != 0 && slot.DeviceType == IOUringDeviceNet {
			ring := (*iouring.IORing)(unsafe.Pointer(slot.KVA))
			return slot, ring
		}
	}
	return nil, nil
}

// --- Accessor functions for ksyscall via go:linkname ---

// MaxIORingsFunc returns the max ring count (constant, but exposed as func for linkname).
func MaxIORingsFunc() int { return MaxIORings }

// IOUringSlotKVA returns the kernel VA for a ring slot (0 if not set up).
func IOUringSlotKVA(ringID int) uintptr {
	if ringID < 0 || ringID >= MaxIORings {
		return 0
	}
	return IOUringTable[ringID].KVA
}

// IOUringSlotOwnerSID returns the owner SID for a ring slot.
func IOUringSlotOwnerSID(ringID int) int16 {
	if ringID < 0 || ringID >= MaxIORings {
		return -1
	}
	return IOUringTable[ringID].OwnerSID
}

// IOUringSlotDeviceType returns the device type for a ring slot.
func IOUringSlotDeviceType(ringID int) uint8 {
	if ringID < 0 || ringID >= MaxIORings {
		return 0
	}
	return IOUringTable[ringID].DeviceType
}

// GetIOUringSlot returns a pointer to the ring slot (as unsafe.Pointer for ksyscall).
func GetIOUringSlot(ringID int) unsafe.Pointer {
	if ringID < 0 || ringID >= MaxIORings {
		return nil
	}
	return unsafe.Pointer(&IOUringTable[ringID])
}

// SetupIOUringSlot initializes a ring slot after the ksyscall handler has
// pinned the page and mapped the KVA.
func SetupIOUringSlot(ringID int, kva, pa uintptr, ownerSID int16, deviceType uint8) {
	if ringID < 0 || ringID >= MaxIORings {
		return
	}
	IOUringTable[ringID] = IOUringSlot{
		KVA:        kva,
		PA:         pa,
		OwnerSID:   ownerSID,
		BlockedTID: -1,
		DeviceType: deviceType,
	}
}

// CheckIOUringWFITimeout checks the timeout in the WFI fallback loop.
// Returns true if the timeout has expired.
func CheckIOUringWFITimeout(ringID int, currentCompletions uint32) bool {
	if ringID < 0 || ringID >= MaxIORings {
		return false
	}
	slot := &IOUringTable[ringID]
	if IOUringTimeoutTicks == 0 || slot.BlockDeadline == 0 {
		return false
	}
	now := kirq.ReadCounterValue()
	if now >= slot.BlockDeadline {
		slot.BlockDeadline = 0
		return true
	}
	return false
}

// Needed by iouring.go but may not be imported yet.
var _ = proc.MaxShepherds // keep proc import alive
