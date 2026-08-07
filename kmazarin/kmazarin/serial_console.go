package main

import (
	"fmt"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
	"mazzy/shared/hid"
	"reflect"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

// SoftIRQConsole implements console.Console by pushing bytes into the
// topHalfUartRing. Rachel (or any userspace shepherd registered on the
// UART soft IRQ slot) receives these bytes via WaitSoftIRQ.
type SoftIRQConsole struct {
	// pendingWake is set to 1 by nosplit pushes that cannot call
	// WakeSlotForIRQ. The event poller checks this flag and wakes
	// the slot on their behalf.
	pendingWake uint32
}

// NewSoftIRQConsole creates a SoftIRQConsole.
func NewSoftIRQConsole() *SoftIRQConsole {
	return &SoftIRQConsole{}
}

// pushByte pushes a single byte into the UART soft IRQ ring.
// Kernel console output uses Code=1 (stdout equivalent).
//
//go:nosplit
func (c *SoftIRQConsole) pushByte(b byte) {
	ev := hid.HIDEvent{Type: 0, Code: 1, Value: uint32(b)}
	if !ringPush(&topHalfUartRing, ev) {
		// Ring full — drop silently (or breadcrumb overflow marker)
		return
	}
	atomic.StoreUint32(&c.pendingWake, 1)
}

// KWrite implements console.Console.KWrite
//
//go:nosplit
func (c *SoftIRQConsole) KWrite(p []byte) int {
	for _, b := range p {
		c.pushByte(b)
	}
	return len(p)
}

// KWriteByte implements console.Console.KWriteByte
//
//go:nosplit
func (c *SoftIRQConsole) KWriteByte(b byte) {
	c.pushByte(b)
}

// KWriteString implements console.Console.KWriteString
//
//go:nosplit
func (c *SoftIRQConsole) KWriteString(s string) {
	for i := 0; i < len(s); i++ {
		c.pushByte(s[i])
	}
}

// KPrintf implements console.Console.KPrintf
// Not nosplit — safe to call WakeSlotForIRQ directly.
func (c *SoftIRQConsole) KPrintf(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	c.KWriteString(s)
	// Wake immediately since we're in normal Go context
	c.wake()
}

// KErrPrintf implements console.Console.KErrPrintf
func (c *SoftIRQConsole) KErrPrintf(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	c.KWriteString(s)
	c.wake()
}

// KPrintHex implements console.Console.KPrintHex
func (c *SoftIRQConsole) KPrintHex(value any) {
	v := reflect.ValueOf(value)
	kind := v.Kind()

	var s string
	switch kind {
	case reflect.Int8, reflect.Uint8:
		s = fmt.Sprintf("0x%02X", value)
	case reflect.Int16, reflect.Uint16:
		s = fmt.Sprintf("0x%04X", value)
	case reflect.Int32, reflect.Uint32:
		s = fmt.Sprintf("0x%08X", value)
	case reflect.Int64, reflect.Uint64, reflect.Int, reflect.Uint, reflect.Uintptr:
		s = fmt.Sprintf("0x%016X", value)
	case reflect.Ptr, reflect.UnsafePointer:
		s = fmt.Sprintf("0x%016X", v.Pointer())
	default:
		s = "not hex value"
	}
	c.KWriteString(s)
	c.wake()
}

// wake calls WakeSlotForIRQ and clears the pending flag.
func (c *SoftIRQConsole) wake() {
	if uartIRQNum != 0 {
		atomic.StoreUint32(&c.pendingWake, 0)
		WakeSlotForIRQ(uartIRQNum)
	}
}

// CheckPendingWake is called by the event poller to flush any
// bytes pushed by nosplit code that couldn't call WakeSlotForIRQ.
//
//go:nosplit
func (c *SoftIRQConsole) CheckPendingWake() {
	if atomic.SwapUint32(&c.pendingWake, 0) == 1 {
		WakeSlotForIRQ(uartIRQNum)
	}
}

// PushByteToUartRing pushes a byte into the UART soft IRQ ring with fd info.
// The fd is carried in the HIDEvent.Code field so the consumer (linux shepherd)
// can distinguish stdout (1) from stderr (2). Called by SyscallWrite for
// non-linux shepherd output that needs to appear on the linux shepherd display.
func PushByteToUartRing(fd byte, b byte) {
	ev := hid.HIDEvent{Type: 0, Code: uint16(fd), Value: uint32(b)}
	ringPush(&topHalfUartRing, ev)
}

// FlushUartRingWake wakes the UART slot consumer after a batch of pushes.
func FlushUartRingWake() {
	if uartIRQNum != 0 {
		WakeSlotForIRQ(uartIRQNum)
	}
}

// softIRQConsole is the global instance, set by EnableSoftIRQConsole.
var softIRQConsole *SoftIRQConsole

//go:linkname suppressSerial runtime.suppressSerial
var suppressSerial uint32

// kernelRingPushBlockerTID tracks thread 0 when it parks itself in
// pushStringFull because topHalfUartRing is full. Set non-zero while
// thread 0 is in ThreadBlockedKernelRingPush. The userspace consumer's
// pop path checks this and wakes thread 0 when slots free up. Single
// blocker — only thread 0 ever pushes, so no list is needed.
//
// Layout: 0 = no pusher blocked. Non-zero = TID waiting for ring space.
// Published+cleared under schedulerLock; readers use atomic.Load for the
// fast path so the wake-side fast exit doesn't have to take the lock.
var kernelRingPushBlockerTID int32

// pushBlockerThreadPtr is the *Thread (as uintptr for nosplit safety)
// of the parked pusher; companion to kernelRingPushBlockerTID. Both are
// published under schedulerLock inside SaveThread0AndYield's deadline
// path when pendingYieldBlockState == ThreadBlockedKernelRingPush.
var pushBlockerThreadPtr uintptr

// pushBlockerDeadlineExpired is set to 1 by the deadline handler when
// the pusher's 10ms timer fires before any drain wake. pushStringFull
// checks this after returning from the yield: if set, the remainder
// is dropped and softIRQDroppedBytes is bumped. Cleared on the next
// block attempt.
var pushBlockerDeadlineExpired uint32

// softIRQDroppedBytes counts bytes dropped by pushStringFull when its
// 10ms wait expires (linux consumer genuinely wedged). Cumulative per
// boot, surfaced on the [status] line.
var softIRQDroppedBytes uint64

// EnableSoftIRQConsole switches the kernel console from direct MMIO
// to the soft IRQ ring. Must be called after SetupUartSoftIRQ and
// after a userspace shepherd has registered on the UART slot.
// Suppresses write1 UART output so runtime fmt.Printf goes to the
// ring (and thus to the linux shepherd display) rather than UART.
func EnableSoftIRQConsole() {
	c := NewSoftIRQConsole()
	softIRQConsole = c
	console.Set(c)
	atomic.StoreUint32(&suppressSerial, 1)

	// Wire klog output through the soft IRQ ring so the linux shepherd
	// receives kernel log messages in its console window.
	klog.SetOutputFuncs(
		func(s string) { pushStringFull(c, 1, s) }, // stdout
		func(s string) { pushStringFull(c, 2, s) }, // stderr
	)
	klog.SetLinuxReady()
}

// pushStringToRing pushes bytes from s into the UART soft IRQ ring with the
// given fd (1=stdout, 2=stderr), converting \n to \r\n. Returns the number
// of bytes from s that were consumed. If the ring fills mid-string the caller
// should wake the shepherd, yield, and retry with s[n:].
func pushStringToRing(c *SoftIRQConsole, fd byte, s string) int {
	n := 0
	for n < len(s) {
		b := s[n]
		if b == '\n' {
			if !ringPush(&topHalfUartRing, hid.HIDEvent{Type: 0, Code: uint16(fd), Value: uint32('\r')}) {
				return n
			}
		}
		if !ringPush(&topHalfUartRing, hid.HIDEvent{Type: 0, Code: uint16(fd), Value: uint32(b)}) {
			return n
		}
		n++
	}
	return n
}

// pushStringBlockBudgetMs is the cumulative wall-time ceiling pushStringFull
// will spend waiting for ring space across one call. Past this, the remainder
// is dropped and softIRQDroppedBytes is bumped. 10 ms matches the per-attempt
// deadline on the userspace uring sender side.
const pushStringBlockBudgetMs = 10

// pushStringFull pushes all of s to the ring. If the ring is full mid-push,
// it wakes the consumer and parks the calling thread on
// ThreadBlockedKernelRingPush with a deadline; the consumer's pop path
// (DrainSoftIRQSlotEvents → WakeKernelRingPusher) wakes us early. If
// pushStringBlockBudgetMs elapses without enough drain, the remaining bytes
// are dropped and counted in softIRQDroppedBytes.
//
// Replaces the old runtime.Gosched() retry loop, which was the only Gosched
// left in kernel code and was implicated in IRQ-mask-induced starvation
// (see memory/sync_uart_irq_masked.md).
func pushStringFull(c *SoftIRQConsole, fd byte, s string) {
	if len(s) == 0 {
		return
	}

	// One-shot budget: track wall-time elapsed across all parks in this call.
	freq := uint64(kirq.GetTimerFrequency())
	budgetTicks := freq * pushStringBlockBudgetMs / 1000

	// Single-blocker policy. If another kernel pusher is already parked on
	// this ring, we don't queue behind it — drop and count instead. Same
	// rationale as the uring single-sender policy (Scenario D); avoids
	// unbounded blocker chains and keeps wake-side bookkeeping trivial.
	pushIfBlockerFreeOrDrop := func(remaining string) bool {
		if atomic.LoadInt32(&kernelRingPushBlockerTID) != 0 {
			atomic.AddUint64(&softIRQDroppedBytes, uint64(len(remaining)))
			return false
		}
		return true
	}

	startTick := kirq.ReadCounterValue()

	for len(s) > 0 {
		n := pushStringToRing(c, fd, s)
		s = s[n:]
		if len(s) == 0 {
			break
		}

		// Ring full mid-string. Make sure the consumer is awake, then park.
		c.wake()

		now := kirq.ReadCounterValue()
		elapsed := now - startTick
		if elapsed >= budgetTicks {
			atomic.AddUint64(&softIRQDroppedBytes, uint64(len(s)))
			return
		}
		if !pushIfBlockerFreeOrDrop(s) {
			return
		}

		// Block until the drainer wakes us, or our deadline fires.
		// SaveThread0AndYield publishes (TID, ptr, state) atomically with
		// the deadline-queue insert under schedulerLock, so the wake side
		// (WakeKernelRingPusher) sees a consistent snapshot.
		atomic.StoreUint32(&pushBlockerDeadlineExpired, 0)
		pendingYieldBlockState = ThreadBlockedKernelRingPush
		thread0PendingDeadline = startTick + budgetTicks
		YieldToReadyThread()
		// [MAZ-179] Verify the suspended handler frame is still ours.
		maz179YieldResumeCheck()

		// Resumed. If the deadline expiry handler beat the drain wake,
		// drop the rest — the consumer is genuinely wedged.
		if atomic.LoadUint32(&pushBlockerDeadlineExpired) == 1 {
			atomic.AddUint64(&softIRQDroppedBytes, uint64(len(s)))
			return
		}
	}
	c.wake()
}

// IsSoftIRQConsoleActive returns true if the soft IRQ console is active.
func IsSoftIRQConsoleActive() bool {
	return softIRQConsole != nil
}
