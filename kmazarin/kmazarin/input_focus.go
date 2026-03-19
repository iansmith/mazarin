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

	if wmSID >= 0 && wmSID < int32(proc.MaxShepherds) {
		ringPush(&inputQueues[wmSID][class], ev)
	}

	// Push to focused shepherd only if different from WM
	if focusSID >= 0 && focusSID < int32(proc.MaxShepherds) && focusSID != wmSID {
		ringPush(&inputQueues[focusSID][class], ev)
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

	if wmSID >= 0 && wmSID < int32(proc.MaxShepherds) {
		wakeInputBlockedThread(int(wmSID), class)
	}
	if focusSID >= 0 && focusSID < int32(proc.MaxShepherds) && focusSID != wmSID {
		wakeInputBlockedThread(int(focusSID), class)
	}
}

// wakeInputBlockedThread wakes a thread blocked on inputBlockedTID[sid][class].
// Follows the same pattern as WakeSlotForIRQ: acquires schedulerLock, rewinds PC,
// restores args, enqueues to ready queue.
//
//go:nosplit
//go:noinline
func wakeInputBlockedThread(sid, class int) {
	tid := inputBlockedTID[sid][class]
	if tid < 0 {
		return
	}

	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	// Re-check after lock
	tid = inputBlockedTID[sid][class]
	if tid < 0 {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return
	}

	t := (*Thread)(unsafe.Pointer(inputBlockedPtr[sid][class]))
	if t == nil || t.State != ThreadBlockedInputEvent {
		inputBlockedTID[sid][class] = -1
		inputBlockedPtr[sid][class] = 0
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return
	}

	t.State = ThreadReady
	inputBlockedTID[sid][class] = -1
	inputBlockedPtr[sid][class] = 0

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
func BlockOnInputQueue(sid int32, class int) uintptr {
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
	prev := (*Thread)(unsafe.Pointer(inputBlockedPtr[sid][class]))
	if prev != nil && prev.State == ThreadBlockedInputEvent {
		prev.State = ThreadReady
		enqueueReadySchedLockHeld(prev)
	}

	// Commit: block current thread
	t.State = ThreadBlockedInputEvent
	t.SoftIRQSlotArg = uint64(class)  // Save deviceClass for arg restore
	t.SoftIRQSyscallNum = 0x102E      // SysWaitInputEvent
	inputBlockedTID[sid][class] = t.TID
	inputBlockedPtr[sid][class] = uintptr(unsafe.Pointer(t))

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// DrainInputQueue drains events from inputQueues[sid][class] into buf.
// Returns the number of events drained.
//
//go:noinline
func DrainInputQueue(sid int32, class int, buf []hid.HIDEvent, max int) int {
	if sid < 0 || sid >= int32(proc.MaxShepherds) {
		return 0
	}
	if class < 0 || class >= hid.InputClassCount {
		return 0
	}
	return RingDrain(&inputQueues[sid][class], buf, max)
}

// CleanupInputFocusForShepherd clears focus and WM state for a dying shepherd.
// Called from TerminateShepherd alongside CleanupSoftIRQSlotsForShepherd.
func CleanupInputFocusForShepherd(shepherdID int16) {
	sid := int32(shepherdID)

	// Clear any focus this shepherd held
	for c := 0; c < hid.InputClassCount; c++ {
		atomic.CompareAndSwapInt32(&inputFocusSID[c], sid, -1)
	}

	// If this shepherd was window manager, clear it
	atomic.CompareAndSwapInt32(&windowManagerSID, sid, -1)

	// Wake any threads blocked on this shepherd's input queues
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()
	for c := 0; c < hid.InputClassCount; c++ {
		if inputBlockedTID[sid][c] >= 0 {
			t := (*Thread)(unsafe.Pointer(inputBlockedPtr[sid][c]))
			if t != nil && t.State == ThreadBlockedInputEvent {
				t.State = ThreadReady
				enqueueReadySchedLockHeld(t)
			}
			inputBlockedTID[sid][c] = -1
			inputBlockedPtr[sid][c] = 0
		}
	}
	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	serial.RawUARTPuts("[InputFocus] cleaned for shepherd ")
	serial.RawUARTDecimal(uint64(shepherdID))
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
	if target < 0 || target >= int32(proc.MaxShepherds) {
		return -22 // EINVAL
	}
	atomic.StoreInt32(&inputFocusSID[class], target)
	return 0
}
