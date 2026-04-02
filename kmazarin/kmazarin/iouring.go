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
	"mazzy/shared/mazzy"
	"sync/atomic"
	"unsafe"
)

// MaxIORings is the maximum number of io_uring instances.
const MaxIORings = 4

// IOUringSlot holds kernel-side state for one io_uring instance.
type IOUringSlot struct {
	KVA           uintptr // kernel VA of the ring page
	PA            uintptr // PA of the ring page (for cleanup)
	OwnerSID      int16   // shepherd that owns this ring
	BlockedTID    int16   // TID of thread blocked on IOUringEnter (-1 = none)
	BlockedPtr    uintptr // *Thread as uintptr (nosplit-safe, no write barrier)
	MinComplete   uint32  // completions needed to wake blocked thread
	BlockDeadline uint64  // timer tick deadline for 10ms timeout (0 = not blocking)
}

// IOUringTable holds all io_uring instances.
var IOUringTable [MaxIORings]IOUringSlot

// IOUringBlockedRingID is the ring index that has a blocked waiter, or -1.
// Checked by the IRQ top-half after writing CQEs.
var IOUringBlockedRingID int32 = -1

// IOUringTimeoutTicks is 10ms worth of hardware timer ticks.
// Set once from SystemTimerFrequency on first IOUringSetup call.
var IOUringTimeoutTicks uint64

// InitIOUringTimeout computes the timeout ticks from the system timer frequency.
// Called from SyscallIOUringSetup.
func InitIOUringTimeout() {
	if IOUringTimeoutTicks == 0 && kirq.SystemTimerFrequency > 0 {
		IOUringTimeoutTicks = kirq.SystemTimerFrequency / 100 // 10ms
	}
}

// BlockForIOUring blocks the current thread until io_uring completions arrive.
// Modeled on BlockForMailboxRecv. Returns the context pointer of the next
// thread to run, or 0 if no other thread is available (caller does WFI).
//
//go:noinline
func BlockForIOUring(ringID int, minComplete uint32) uintptr {
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
	t.SoftIRQSyscallNum = uint64(mazzy.SysIOUringEnter)

	slot := &IOUringTable[ringID]
	slot.BlockedTID = int16(t.TID)
	slot.BlockedPtr = uintptr(unsafe.Pointer(t))
	slot.MinComplete = minComplete
	if IOUringTimeoutTicks > 0 {
		slot.BlockDeadline = kirq.ReadCounterValue() + IOUringTimeoutTicks
	}
	IOUringBlockedRingID = int32(ringID)

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// WakeIOUringFromIRQ checks if a blocked io_uring thread's minComplete is
// satisfied and wakes it. Called from the IRQ top-half after writing CQEs.
// MUST be called with IRQs disabled (from IRQ context).
//
//go:nosplit
//go:noinline
func WakeIOUringFromIRQ() {
	ringID := atomic.LoadInt32(&IOUringBlockedRingID)
	if ringID < 0 || ringID >= MaxIORings {
		return
	}
	slot := &IOUringTable[ringID]
	if slot.BlockedTID < 0 || slot.KVA == 0 {
		return
	}

	ring := (*iouring.IORing)(unsafe.Pointer(slot.KVA))
	cqTail := atomic.LoadUint32(&ring.CQTail)
	cqHead := atomic.LoadUint32(&ring.CQHead)
	completions := cqTail - cqHead
	if completions < slot.MinComplete {
		return
	}

	// Wake the blocked thread.
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := (*Thread)(unsafe.Pointer(slot.BlockedPtr))
	if t != nil && t.State == ThreadBlockedIOUring {
		t.State = ThreadReady
		t.MailboxWoken = true // Reuse flag for priority scheduling
		t.Context.RewindToSyscall()
		t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
		t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)

		slot.BlockedTID = -1
		slot.BlockedPtr = 0
		slot.BlockDeadline = 0
		atomic.StoreInt32(&IOUringBlockedRingID, -1)

		enqueueReadyPrioritySchedLockHeld(t)
		atomic.StoreUint32(&priorityWakePending, 1)
		asm.Dsb()
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// topHalfIOUringTimeoutHook is called via indirect function pointer from
// ProcessDeadlinesTopHalf (timer IRQ) to check the 10ms safety timeout.
// Using an indirect call avoids the nosplit stack checker tracing through
// the full chain — the exception stack is large enough for the actual usage.
var topHalfIOUringTimeoutHook func() = checkIOUringTimeoutFromTimer

// checkIOUringTimeoutFromTimer checks if a blocked io_uring thread has
// timed out. Called from timer tick with schedulerLock held and IRQs disabled.
//
//go:nosplit
func checkIOUringTimeoutFromTimer() {
	ringID := atomic.LoadInt32(&IOUringBlockedRingID)
	if ringID < 0 || ringID >= MaxIORings {
		return
	}
	slot := &IOUringTable[ringID]
	if slot.BlockedTID < 0 || slot.BlockDeadline == 0 {
		return
	}

	now := kirq.ReadCounterValue()
	if now < slot.BlockDeadline {
		return
	}

	// Timeout expired — wake thread with whatever completions are available.
	t := (*Thread)(unsafe.Pointer(slot.BlockedPtr))
	if t != nil && t.State == ThreadBlockedIOUring {
		t.State = ThreadReady
		t.Context.RewindToSyscall()
		t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
		t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)

		slot.BlockedTID = -1
		slot.BlockedPtr = 0
		slot.BlockDeadline = 0
		atomic.StoreInt32(&IOUringBlockedRingID, -1)

		enqueueReadySchedLockHeld(t) // Regular priority (timeout, not IRQ)
	}
}

// GetIOUringSlotForIRQ returns the ring slot and IORing pointer for the
// blocked ring, for use by the IRQ top-half when writing CQEs.
// Returns nil, nil if no ring is blocked.
//
//go:nosplit
func GetIOUringSlotForIRQ() (*IOUringSlot, *iouring.IORing) {
	ringID := atomic.LoadInt32(&IOUringBlockedRingID)
	if ringID < 0 || ringID >= MaxIORings {
		return nil, nil
	}
	slot := &IOUringTable[ringID]
	if slot.KVA == 0 {
		return nil, nil
	}
	ring := (*iouring.IORing)(unsafe.Pointer(slot.KVA))
	return slot, ring
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

// GetIOUringSlot returns a pointer to the ring slot (as unsafe.Pointer for ksyscall).
func GetIOUringSlot(ringID int) unsafe.Pointer {
	if ringID < 0 || ringID >= MaxIORings {
		return nil
	}
	return unsafe.Pointer(&IOUringTable[ringID])
}

// SetupIOUringSlot initializes a ring slot after the ksyscall handler has
// pinned the page and mapped the KVA.
func SetupIOUringSlot(ringID int, kva, pa uintptr, ownerSID int16) {
	if ringID < 0 || ringID >= MaxIORings {
		return
	}
	IOUringTable[ringID] = IOUringSlot{
		KVA:        kva,
		PA:         pa,
		OwnerSID:   ownerSID,
		BlockedTID: -1,
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
