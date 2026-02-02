
package ksyscall

import (
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/ktime"
)

// SyscallSetTimerDeadline implements Mazzy syscall 0x100E.
// Sets a timer deadline on a soft IRQ slot. When the deadline expires,
// the kernel pushes time events to the slot's ring buffer and wakes
// the thread via WakeSlotForIRQ. The thread should block via WaitSoftIRQ separately.
//
// arg0: slotNum        — soft IRQ slot number
// arg1: deadlineSec    — target seconds (Unix epoch)
// arg2: deadlineNsec   — target nanoseconds
//
//go:nosplit
func SyscallSetTimerDeadline(slotNum, deadlineSec, deadlineNsec, _, _, _ uint64) int64 {
	// Get current time and timer state
	nowSec, nowNsec := ktime.GetTime()
	currentTick := kirq.ReadCounterValue()
	frequency := uint64(kirq.GetTimerFrequency())

	// Check if deadline is already past
	if deadlineSec < nowSec || (deadlineSec == nowSec && deadlineNsec <= nowNsec) {
		return 0 // Already past, return immediately
	}

	// Convert wall-clock delta to ticks
	deltaSec := deadlineSec - nowSec
	var deltaTicks uint64
	deltaTicks = deltaSec * frequency
	if deadlineNsec > nowNsec {
		deltaTicks += ((deadlineNsec - nowNsec) * frequency) / 1_000_000_000
	} else if deadlineNsec < nowNsec {
		// Borrow a second
		deltaTicks -= ((nowNsec - deadlineNsec) * frequency) / 1_000_000_000
	}

	tickDeadline := currentTick + deltaTicks

	// Record which slot this timer belongs to, and add deadline
	currentTID := int32(GetCurrentThreadTID())
	RecordTimerSlot(currentTID, int32(slotNum))
	AddDeadlineStatic(tickDeadline, currentTID)

	return 0
}
