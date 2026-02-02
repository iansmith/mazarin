//go:build arm64

package main

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio/input"
	"mazzy/shared/hid"
	"sync/atomic"
	"unsafe"
)

// ============================================================================
// Soft IRQ Slot System
// ============================================================================
//
// Maps InterruptType → slot → ring buffer → blocked thread.
// Userspace registers slots via RegisterSoftIRQ. The top-half pushes events
// into per-device ring buffers. The WaitSoftIRQ syscall drains from the ring,
// blocking if empty.

const maxSoftIRQSlots = 32

// softIRQSlot represents one registered soft IRQ slot.
type softIRQSlot struct {
	active      uint32         // atomic: 1 = active
	irqNum      uint32
	priestID    int16
	devIdx      int            // index into input.AllDevices()
	intKind     hid.InterruptType // KeyboardInterrupt or MouseInterrupt
	blockedTID  ThreadId       // TID of thread blocked on this slot (-1 = none)
	ring        *softIRQRing   // pointer to per-device ring buffer
}

// Ider implementation for StaticList compatibility.
// The slot's ID is its index, stored externally.
func (s *softIRQSlot) Id() int32 { return int32(s.irqNum) }

var softIRQSlotData [maxSoftIRQSlots]softIRQSlot
var softIRQSlotInUse [maxSoftIRQSlots]bool

// irqToSlot maps IRQ numbers to slot indices for fast lookup from IRQ context.
var irqToSlot [256]int32 // -1 = no mapping

// SoftIRQSlotFire is called from the input device IRQ handler (via softIRQFireFunc).
// With the ring buffer architecture, this is a no-op — events are pushed directly
// by NonTimerIRQTopHalf and wake happens via WakeSlotForIRQ.
//
//go:nosplit
func SoftIRQSlotFire(irqNum uint32) {}

func init() {
	for i := range irqToSlot {
		irqToSlot[i] = -1
	}
	for i := range softIRQSlotData {
		softIRQSlotData[i].blockedTID = -1
	}
}

// RegisterSoftIRQSlotKsyscall registers an IRQ on a soft IRQ slot for a priest.
// Called from ksyscall via linkname.
func RegisterSoftIRQSlotKsyscall(irqNum uint32, slotNum int32, priestID int16) int64 {
	if slotNum < 0 || slotNum >= maxSoftIRQSlots {
		return -22 // EINVAL
	}

	// Find which device has this IRQ
	devIdx := -1
	var intKind hid.InterruptType
	devices := input.AllDevices()
	for i, dev := range devices {
		if dev != nil && dev.IRQNum == irqNum {
			devIdx = i
			if dev.DevType == hid.DeviceTypeMouse {
				intKind = hid.MouseInterrupt
			} else {
				intKind = hid.KeyboardInterrupt
			}
			break
		}
	}
	var ring *softIRQRing
	if devIdx < 0 {
		// Not an input device — check if it's the UART or timer
		if irqNum == uartIRQNum && uartIRQNum != 0 {
			intKind = hid.SerialInterrupt
			ring = &topHalfUartRing
			devIdx = 0 // dummy
		} else if irqNum == hid.TimerVirtualIRQ {
			intKind = hid.TimerInterrupt
			ring = &topHalfTimerRing
			devIdx = 0 // dummy
		} else {
			console.KPrintf("[SoftIRQSlot] No device found for IRQ %d\n", irqNum)
			return -19 // ENODEV
		}
	} else {
		// Determine which ring buffer to use for input devices
		if intKind == hid.MouseInterrupt {
			ring = &topHalfMouseRing
		} else {
			ring = &topHalfKbdRing
		}
	}

	slot := &softIRQSlotData[slotNum]
	slot.irqNum = irqNum
	slot.priestID = priestID
	slot.devIdx = devIdx
	slot.intKind = intKind
	slot.ring = ring
	slot.blockedTID = -1
	atomic.StoreUint32(&slot.active, 1)
	softIRQSlotInUse[slotNum] = true

	if irqNum < 256 {
		irqToSlot[irqNum] = slotNum
	}

	console.KPrintf("[SoftIRQSlot] Registered slot %d: IRQ=%d priest=%d dev=%d kind=%d\n",
		slotNum, irqNum, priestID, devIdx, intKind)

	// When a userspace priest registers on the UART serial slot,
	// switch the kernel console to push through the soft IRQ ring
	// so the priest receives kernel output.
	if intKind == hid.SerialInterrupt && !IsSoftIRQConsoleActive() {
		console.KPrintf("[SoftIRQSlot] Switching kernel console to soft IRQ ring\n")
		EnableSoftIRQConsole()
	}

	return 0
}

// WakeSlotForIRQ is called from the nosplit top-half after pushing events.
// If a thread is blocked on a slot for this IRQ, wake it with priority.
//
//go:nosplit
func WakeSlotForIRQ(irqNum uint32) {
	if irqNum >= 256 {
		return
	}
	slotIdx := atomic.LoadInt32(&irqToSlot[irqNum])
	if slotIdx < 0 || slotIdx >= maxSoftIRQSlots {
		return
	}
	slot := &softIRQSlotData[slotIdx]
	tid := slot.blockedTID
	if tid < 0 {
		return // no blocked thread, events stay in ring
	}

	// CRITICAL: Disable IRQs before acquiring schedulerLock.
	// Without this, a timer interrupt can fire while we hold the lock,
	// and the timer handler's checkThreadPreemptionImpl will try to
	// acquire the same lock — deadlock.
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	// Re-check after acquiring lock
	tid = slot.blockedTID
	if tid < 0 {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return
	}

	t := threadList.FindByIdAll(int32(tid))
	if t == nil || t.State != ThreadBlockedSoftIRQ {
		slot.blockedTID = -1
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return
	}

	t.State = ThreadReady
	slot.blockedTID = -1
	enqueueReadySchedLockHeld(t)

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// BlockOnSlot blocks the current thread waiting for events on the given slot.
// Returns the context pointer of the next thread to switch to, or 0.
// Called from the syscall handler (normal Go context).
//
//go:nosplit
func BlockOnSlot(slotNum int32) uintptr {
	if CurrentThreadIdx < 0 {
		return 0
	}

	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := threadList.Get(int(CurrentThreadIdx))
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	next := findReadyThreadSchedLockHeld()
	if next == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// If another thread was previously blocked on this slot (orphaned by
	// Go runtime M migration), unblock it so its thread slot is reclaimed.
	// The Go runtime will exit the old M once it sees the goroutine moved.
	prevTID := softIRQSlotData[slotNum].blockedTID
	if prevTID >= 0 {
		prev := threadList.FindByIdAll(int32(prevTID))
		if prev != nil && prev.State == ThreadBlockedSoftIRQ {
			prev.State = ThreadReady
			enqueueReadySchedLockHeld(prev)
		}
	}

	// Commit: block current thread, record in slot
	t.State = ThreadBlockedSoftIRQ
	softIRQSlotData[slotNum].blockedTID = t.TID

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// DrainSoftIRQSlotEvents drains events from the ring buffer associated with a slot.
// Called from ksyscall via linkname.
func DrainSoftIRQSlotEvents(slotNum int32, buf []hid.HIDEvent, max int) int {
	if slotNum < 0 || slotNum >= maxSoftIRQSlots {
		return 0
	}
	slot := &softIRQSlotData[slotNum]
	if atomic.LoadUint32(&slot.active) == 0 {
		return 0
	}
	if slot.ring == nil {
		return 0
	}

	return RingDrain(slot.ring, buf, max)
}


// PushTimerEventAndWake pushes time events into the timer ring and wakes the slot.
// Called from processStaticDeadlinesSchedLockHeld when a timer deadline expires.
// REQUIRES: schedulerLock held, IRQs masked.
//
//go:nosplit
func PushTimerEventAndWake(sec, nsec uint64) {
	// Push 3 events: seconds low, nanoseconds, seconds high
	ev0 := hid.HIDEvent{Type: 0, Code: 0, Value: uint32(sec)}
	ev1 := hid.HIDEvent{Type: 0, Code: 1, Value: uint32(nsec)}
	ev2 := hid.HIDEvent{Type: 0, Code: 2, Value: uint32(sec >> 32)}
	ringPush(&topHalfTimerRing, ev0)
	ringPush(&topHalfTimerRing, ev1)
	ringPush(&topHalfTimerRing, ev2)

	// Wake the slot — but we already hold schedulerLock, so inline the wake logic
	irqNum := hid.TimerVirtualIRQ
	slotIdx := irqToSlot[irqNum]
	if slotIdx < 0 || slotIdx >= maxSoftIRQSlots {
		return
	}
	slot := &softIRQSlotData[slotIdx]
	tid := slot.blockedTID
	if tid < 0 {
		return
	}
	t := threadList.FindByIdAll(int32(tid))
	if t == nil || t.State != ThreadBlockedSoftIRQ {
		slot.blockedTID = -1
		return
	}
	t.State = ThreadReady
	slot.blockedTID = -1
	enqueueReadySchedLockHeld(t)
}

// GetUartSlotPriestID returns the priest ID that owns the UART serial slot.
// Returns -1 if no priest has registered on the UART slot.
//
//go:nosplit
func GetUartSlotPriestID() int16 {
	if uartIRQNum == 0 || uartIRQNum >= 256 {
		return -1
	}
	slotIdx := irqToSlot[uartIRQNum]
	if slotIdx < 0 || slotIdx >= maxSoftIRQSlots {
		return -1
	}
	if atomic.LoadUint32(&softIRQSlotData[slotIdx].active) == 0 {
		return -1
	}
	return softIRQSlotData[slotIdx].priestID
}

// GetSlotInterruptKind returns the InterruptType for a given slot.
func GetSlotInterruptKind(slotNum int32) hid.InterruptType {
	if slotNum < 0 || slotNum >= maxSoftIRQSlots {
		return 0
	}
	return softIRQSlotData[slotNum].intKind
}

// QueryInputDevicesKernel fills device info from discovered input devices.
// Called from ksyscall via linkname.
func QueryInputDevicesKernel(infos []hid.InputDeviceInfo, max int) int {
	devices := input.AllDevices()
	n := 0
	for _, dev := range devices {
		if n >= max || n >= len(infos) {
			break
		}
		if dev == nil {
			continue
		}
		kind := hid.KeyboardInterrupt
		if dev.DevType == hid.DeviceTypeMouse {
			kind = hid.MouseInterrupt
		}
		infos[n] = hid.InputDeviceInfo{
			IRQNum:        dev.IRQNum,
			DeviceType:    dev.DevType,
			InterruptKind: kind,
		}
		n++
	}
	// Also report the UART as a serial device
	if uartIRQNum != 0 && n < max && n < len(infos) {
		infos[n] = hid.InputDeviceInfo{
			IRQNum:        uartIRQNum,
			DeviceType:    hid.DeviceTypeSerial,
			InterruptKind: hid.SerialInterrupt,
		}
		n++
	}
	// Report the timer as a virtual device
	if n < max && n < len(infos) {
		infos[n] = hid.InputDeviceInfo{
			IRQNum:        hid.TimerVirtualIRQ,
			DeviceType:    hid.DeviceTypeTimer,
			InterruptKind: hid.TimerInterrupt,
		}
		n++
	}
	return n
}
