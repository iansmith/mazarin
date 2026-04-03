//go:build arm64 || amd64 || riscv64

package main

import (
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/hid"
	"sync/atomic"
	"unsafe"
)

// ============================================================================
// Input Focus System — Focus-Based Input Routing
// ============================================================================
//
// Replaces per-IRQ soft IRQ registration for keyboard/mouse with a focus model.
// Three device classes (keyboard, mouse-click, mouse-move) each have an independent
// focus target (shepherd ID). The window manager shepherd receives ALL input events.
//
// Top-half routing (nosplit):
//   - Pushes to WM's queue (always, if WM is claimed)
//   - Pushes to focused shepherd's queue (if different from WM)
//
// Syscall interface:
//   - SysRequestWindowManager: claim WM role (first-come-first-served)
//   - SysSetInputFocus: set focus for a device class
//   - SysWaitInputEvent: block until events arrive for caller's queue

// inputFocusSID tracks the focused shepherd for each device class.
// -1 = no focus set. Read atomically from nosplit top-half.
var inputFocusSID [hid.InputClassCount]int32

// windowManagerSID is the shepherd that claimed WM role. -1 = none.
var windowManagerSID int32 = -1

// Per-shepherd per-device-class ring buffers.
// Indexed as inputQueues[shepherdIdx][deviceClass].
var inputQueues [proc.MaxShepherds][hid.InputClassCount]softIRQRing

// Per-shepherd per-device-class blocked thread tracking.
// blockedTID = -1 means no thread is blocked.
var inputBlockedTID [proc.MaxShepherds][hid.InputClassCount]ThreadId
var inputBlockedPtr [proc.MaxShepherds][hid.InputClassCount]uintptr

func init() {
	for i := range inputFocusSID {
		inputFocusSID[i] = -1
	}
	for s := range inputBlockedTID {
		for c := range inputBlockedTID[s] {
			inputBlockedTID[s][c] = -1
		}
	}
}

// routeInputEvent pushes an HID event to the WM's queue and the focused
// shepherd's queue (if different). Called from the nosplit top-half.
//
//go:nosplit
//go:noinline
func routeInputEvent(ev hid.HIDEvent, class int) {
	wmSID := atomic.LoadInt32(&windowManagerSID)
	focusSID := atomic.LoadInt32(&inputFocusSID[class])

	if wmSID >= 0 {
		wmSlot := proc.ShepherdIdToSlot(proc.ShepherdId(wmSID))
		if wmSlot != proc.ShepherdSlotInvalid {
			ringPush(&inputQueues[wmSlot][class], ev)
		}
	}

	// Push to focused shepherd only if different from WM
	if focusSID >= 0 && focusSID != wmSID {
		focusSlot := proc.ShepherdIdToSlot(proc.ShepherdId(focusSID))
		if focusSlot != proc.ShepherdSlotInvalid {
			ringPush(&inputQueues[focusSlot][class], ev)
		}
	}
}

// wakeInputConsumers wakes threads blocked on WaitInputEvent for both WM
// and focused shepherd. Called from the nosplit top-half after pushing events.
//
//go:nosplit
//go:noinline
func wakeInputConsumers(class int) {
	wmSID := atomic.LoadInt32(&windowManagerSID)
	focusSID := atomic.LoadInt32(&inputFocusSID[class])

	if wmSID >= 0 {
		wmSlot := proc.ShepherdIdToSlot(proc.ShepherdId(wmSID))
		if wmSlot != proc.ShepherdSlotInvalid {
			wakeInputBlockedThread(wmSlot, class)
		}
	}
	if focusSID >= 0 && focusSID != wmSID {
		focusSlot := proc.ShepherdIdToSlot(proc.ShepherdId(focusSID))
		if focusSlot != proc.ShepherdSlotInvalid {
			wakeInputBlockedThread(focusSlot, class)
		}
	}
}

// wakeInputBlockedThread wakes a thread blocked on inputBlockedTID[slot][class].
// Follows the same pattern as WakeSlotForIRQ: acquires schedulerLock, rewinds PC,
// restores args, enqueues to ready queue.
//
//go:nosplit
//go:noinline
func wakeInputBlockedThread(slot proc.ShepherdSlot, class int) {
	tid := inputBlockedTID[slot][class]
	if tid < 0 {
		return
	}

	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	// Re-check after lock
	tid = inputBlockedTID[slot][class]
	if tid < 0 {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return
	}

	t := (*Thread)(unsafe.Pointer(inputBlockedPtr[slot][class]))
	if t == nil || t.State != ThreadBlockedInputEvent {
		inputBlockedTID[slot][class] = -1
		inputBlockedPtr[slot][class] = 0
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return
	}

	t.State = ThreadReady
	inputBlockedTID[slot][class] = -1
	inputBlockedPtr[slot][class] = 0

	// Rewind so the SVC re-executes SyscallWaitInputEvent on resume
	t.Context.RewindToSyscall()
	t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
	t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
	enqueueReadySchedLockHeld(t)

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// BlockOnInputQueue blocks the current thread waiting for input events on
// the given shepherd's device class queue. Returns the context pointer of
// the next thread to switch to, or 0.
//
//go:nosplit
//go:noinline
func BlockOnInputQueue(slot proc.ShepherdSlot, class int) uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	pluckFromAllQueues(t.TID)

	// Find next thread — same logic as BlockOnSlot
	var next *Thread
	if t.PageTableL0PA != 0 {
		next = findReadyUserspaceThreadSchedLockHeld(-1)
	} else {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		processStaticDeadlinesSchedLockHeld()
		if t.PageTableL0PA != 0 {
			next = findReadyUserspaceThreadSchedLockHeld(-1)
		} else {
			next = findReadyThreadSchedLockHeld()
		}
	}
	if next == nil && t.PageTableL0PA != 0 {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Clear any previous blocked thread on this queue (M migration)
	prev := (*Thread)(unsafe.Pointer(inputBlockedPtr[slot][class]))
	if prev != nil && prev.State == ThreadBlockedInputEvent {
		prev.State = ThreadReady
		enqueueReadySchedLockHeld(prev)
	}

	// Commit: block current thread
	t.State = ThreadBlockedInputEvent
	t.SoftIRQSlotArg = uint64(class) // Save deviceClass for arg restore
	t.SoftIRQSyscallNum = 0x102E     // SysWaitInputEvent
	inputBlockedTID[slot][class] = t.TID
	inputBlockedPtr[slot][class] = uintptr(unsafe.Pointer(t))

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// DrainInputQueue drains events from inputQueues[slot][class] into buf.
// Returns the number of events drained.
//
//go:noinline
func DrainInputQueue(slot proc.ShepherdSlot, class int, buf []hid.HIDEvent, max int) int {
	if slot < 0 || int(slot) >= proc.MaxShepherds {
		return 0
	}
	if class < 0 || class >= hid.InputClassCount {
		return 0
	}
	return RingDrain(&inputQueues[slot][class], buf, max)
}

// CleanupInputFocusForShepherd clears focus and WM state for a dying shepherd.
// Called from TerminateShepherd alongside CleanupSoftIRQSlotsForShepherd.
// slot is the shepherd list index (for array access); sid is the numeric SID
// (for comparing against inputFocusSID and windowManagerSID stored values).
func CleanupInputFocusForShepherd(slot proc.ShepherdSlot, sid proc.ShepherdId) {
	sidInt32 := int32(sid)

	// Clear any focus this shepherd held (SID comparison against stored values)
	for c := 0; c < hid.InputClassCount; c++ {
		atomic.CompareAndSwapInt32(&inputFocusSID[c], sidInt32, -1)
	}

	// If this shepherd was window manager, clear it
	atomic.CompareAndSwapInt32(&windowManagerSID, sidInt32, -1)

	if slot < 0 || int(slot) >= proc.MaxShepherds {
		return
	}

	// Wake any threads blocked on this shepherd's input queues (slot-indexed)
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()
	for c := 0; c < hid.InputClassCount; c++ {
		if inputBlockedTID[slot][c] >= 0 {
			t := (*Thread)(unsafe.Pointer(inputBlockedPtr[slot][c]))
			if t != nil && t.State == ThreadBlockedInputEvent {
				t.State = ThreadReady
				enqueueReadySchedLockHeld(t)
			}
			inputBlockedTID[slot][c] = -1
			inputBlockedPtr[slot][c] = 0
		}
	}
	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	serial.RawUARTPuts("[InputFocus] cleaned for shepherd ")
	serial.RawUARTDecimal(uint64(sid))
	serial.RawUARTPuts("\r\n")
}

// GetWindowManagerSID returns the window manager shepherd ID (-1 if none).
//
//go:nosplit
func GetWindowManagerSID() int32 {
	return atomic.LoadInt32(&windowManagerSID)
}

// requestWindowManagerKernel atomically claims the WM role for a shepherd.
// Returns true if successful (first-come-first-served).
func requestWindowManagerKernel(sid int32) bool {
	return atomic.CompareAndSwapInt32(&windowManagerSID, -1, sid)
}

// setInputFocusKernel sets focus for a device class.
// Returns 0 on success, negative errno on error.
func setInputFocusKernel(target int32, class int) int64 {
	if class < 0 || class >= hid.InputClassCount {
		return -22 // EINVAL
	}
	if target < 0 || proc.FindShepherdBySID(proc.ShepherdId(target)) == nil {
		return -22 // EINVAL
	}
	atomic.StoreInt32(&inputFocusSID[class], target)
	return 0
}
