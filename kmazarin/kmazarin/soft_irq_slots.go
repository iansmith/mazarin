//go:build arm64 || amd64 || riscv64

package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/device/virtio/input"
	"mazzy/kmazarin/serial"
	"mazzy/shared/hid"
	"sync/atomic"
	"unsafe"
)

// Debug counters for preemption tracking
var dbgPreemptSwitchCount uint64
var dbgPreemptNoNextCount uint64

// dbgLastTimerWakeTID records the TID of the last thread woken by PushTimerEventAndWake.
// Read from the EVT handler for diagnostics.
var dbgLastTimerWakeTID int32 = -1

// blockDeviceOwnerPID tracks which priest owns the block device.
// Set when a priest registers for BlockVirtualIRQ via RegisterSoftIRQ.
// -1 means no owner.
var blockDeviceOwnerPID int16 = -1

// GetBlockDeviceOwnerPID returns the PID of the priest that owns the block device.
// Returns -1 if no priest has registered for block device ownership.
//
//go:nosplit
func GetBlockDeviceOwnerPID() int16 {
	return blockDeviceOwnerPID
}

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
	active        uint32         // atomic: 1 = active
	irqNum        uint32
	priestID      int16
	devIdx        int            // index into input.AllDevices()
	intKind       hid.InterruptType // KeyboardInterrupt or MouseInterrupt
	blockedTID       ThreadId // TID of thread blocked on this slot (-1 = none)
	blockedThreadPtr uintptr  // cached *Thread as uintptr (avoids GC write barrier in nosplit ISR)
	ring          *softIRQRing   // pointer to per-device ring buffer
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
		} else if irqNum == hid.BlockVirtualIRQ {
			intKind = hid.DiskInterrupt
			ring = nil // Block device doesn't use ring buffers — uses SysBlockRead
			devIdx = 0 // dummy
			blockDeviceOwnerPID = priestID
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

	// When a userspace priest registers on the UART serial slot,
	// switch the kernel console to push through the soft IRQ ring
	// so the priest receives kernel output.
	if intKind == hid.SerialInterrupt && !IsSoftIRQConsoleActive() {
		EnableSoftIRQConsole()
	}

	return 0
}

// WakeSlotForIRQ is called from the nosplit top-half after pushing events.
// If a thread is blocked on a slot for this IRQ, wake it with priority.
//
//go:nosplit
//go:noinline
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

	t := (*Thread)(unsafe.Pointer(slot.blockedThreadPtr))
	if t == nil || t.State != ThreadBlockedSoftIRQ {
		slot.blockedTID = -1
		slot.blockedThreadPtr = 0
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return
	}

	t.State = ThreadReady
	slot.blockedTID = -1
	slot.blockedThreadPtr = 0
	// Rewind the thread's saved PC so it re-executes the SVC instruction
	// when scheduled. SyscallWaitSoftIRQ will run fresh and drain the events
	// from the ring, returning success instead of EAGAIN.
	// Also restore X0/a0 (first arg = slotNum) which was overwritten with
	// the return value by the SVC handler.
	t.Context.RewindToSyscall()
	t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
	enqueueReadySchedLockHeld(t)
	asm.Dsb() // Memory barrier to ensure enqueue is visible to other CPUs

	serial.RawUARTPuts("W")
	serial.RawUARTDecimal(uint64(t.TID))
	serial.RawUARTPuts(" ")

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// BlockOnSlot blocks the current thread waiting for events on the given slot.
// Returns the context pointer of the next thread to switch to, or 0.
// Called from the syscall handler (normal Go context).
//
//go:nosplit
//go:noinline
func BlockOnSlot(slotNum int32) uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Pluck current thread from ready queue if it's there
	// (It might not be if it was already running and not re-queued)
	pluckFromAllQueues(t.TID)

	// Use filtered search for userspace threads to avoid returning kernel
	// thread 0 (EL1t SPSR) when a userspace thread is blocking — same fix
	// as the other 4 EL0 context-switch paths.
	var next *Thread
	if t.PageTableL0PA != 0 {
		next = findReadyUserspaceThreadSchedLockHeld(-1)
	} else {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		// No ready userspace thread — process nanosleep/futex deadlines
		// while we hold the scheduler lock. This can wake sleeping threads
		// (e.g. sysmon) immediately, avoiding a round-trip through WFI.
		processStaticDeadlinesSchedLockHeld()
		if t.PageTableL0PA != 0 {
			next = findReadyUserspaceThreadSchedLockHeld(-1)
		} else {
			next = findReadyThreadSchedLockHeld()
		}
	}
	if next == nil && t.PageTableL0PA != 0 {
		// Last resort: accept ANY ready thread including kernel thread 0.
		// Without this fallback, a userspace thread returns 0 and loops at
		// EL1 (SVC handler doing WFI) where timer preemption can't fire
		// (SPSR shows EL1). On HVF (native speed) the EL0 window between
		// SVCs is too brief for timer preemption to catch, causing deadlock.
		// Switching to thread 0 lets KernelIdleLoop run and reschedule.
		// The SVC handler's DoContextSwitch handles EL0→EL1 transitions
		// correctly via SPSR in the target ThreadContext.
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// If another thread was previously blocked on this slot (orphaned by
	// Go runtime M migration), unblock it so its thread slot is reclaimed.
	// The Go runtime will exit the old M once it sees the goroutine moved.
	prev := (*Thread)(unsafe.Pointer(softIRQSlotData[slotNum].blockedThreadPtr))
	if prev != nil && prev.State == ThreadBlockedSoftIRQ {
		prev.State = ThreadReady
		enqueueReadySchedLockHeld(prev)
		asm.Dsb() // Memory barrier to ensure enqueue is visible
	}

	// Commit: block current thread, record in slot
	t.State = ThreadBlockedSoftIRQ
	t.SoftIRQSlotArg = uint64(slotNum) // Save for RewindToSyscall arg restore
	softIRQSlotData[slotNum].blockedTID = t.TID
	softIRQSlotData[slotNum].blockedThreadPtr = uintptr(unsafe.Pointer(t))

	serial.RawUARTPuts("B")
	serial.RawUARTDecimal(uint64(t.TID))
	serial.RawUARTPuts(">")
	serial.RawUARTDecimal(uint64(next.TID))
	serial.RawUARTPuts(" ")

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

	n := RingDrain(slot.ring, buf, max)
	if n > 0 {
		atomic.AddUint32(&dbgDrainTotal, uint32(n))
		atomic.AddUint32(&dbgDrainCalls, 1)
		if slotNum < maxSoftIRQSlots {
			atomic.AddUint32(&dbgDrainPerSlot[slotNum], uint32(n))
		}
	}
	return n
}


// PushTimerEventAndWake pushes time events into the timer ring and wakes the slot.
// Called from processStaticDeadlinesSchedLockHeld when a timer deadline expires.
// REQUIRES: schedulerLock held, IRQs masked.
//
//go:nosplit
//go:noinline
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
	t := (*Thread)(unsafe.Pointer(slot.blockedThreadPtr))
	if t == nil || t.State != ThreadBlockedSoftIRQ {
		slot.blockedTID = -1
		slot.blockedThreadPtr = 0
		return
	}
	t.State = ThreadReady
	slot.blockedTID = -1
	slot.blockedThreadPtr = 0
	// Rewind the thread's saved PC so it re-executes the SVC instruction
	// when scheduled. SyscallWaitSoftIRQ will run fresh and drain the events
	// we just pushed, returning success instead of EAGAIN.
	// Also restore X0/a0 (first arg = slotNum) which was overwritten with
	// the return value by the SVC handler.
	t.Context.RewindToSyscall()
	t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
	// Push to HEAD of queue so the timer goroutine is scheduled promptly.
	// processStaticDeadlinesSchedLockHeld processes futex deadlines before
	// the timer deadline, filling the queue with futex-cycling runtime Ms.
	// Without head-push, the timer thread is at the back and gets starved.
	targetCPU := t.HomeCPU
	if targetCPU < 0 || targetCPU >= int8(GetCPUCount()) {
		targetCPU = int8(GetCPUID())
	}
	GetPerCPUByID(uint64(targetCPU)).LocalReadyQueue.PushHeadNoDuplicate(t.TID)
	asm.Dsb() // Memory barrier to ensure enqueue is visible
	dbgLastTimerWakeTID = int32(tid)
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
	// Report the block device as a virtual device (for disk priest ownership)
	if n < max && n < len(infos) {
		infos[n] = hid.InputDeviceInfo{
			IRQNum:        hid.BlockVirtualIRQ,
			DeviceType:    hid.DeviceTypeBlock,
			InterruptKind: hid.DiskInterrupt,
		}
		n++
	}
	return n
}
