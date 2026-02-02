package main

import (
	"fmt"
	"mazzy/kmazarin/console"
	"mazzy/shared/hid"
	"reflect"
	"sync/atomic"
)

// SoftIRQConsole implements console.Console by pushing bytes into the
// topHalfUartRing. Dapope (or any userspace priest registered on the
// UART soft IRQ slot) receives these bytes via WaitSoftIRQ.
//
// Breadcrumb remains direct MMIO for IRQ-safe debug output.
// write(2) from userspace also remains Breadcrumb, so dapope's own
// fmt.Printf does not loop back through the ring.
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
//
//go:nosplit
func (c *SoftIRQConsole) pushByte(b byte) {
	ev := hid.HIDEvent{Type: 0, Code: 0, Value: uint32(b)}
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
func (c *SoftIRQConsole) KPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.KWriteString(s)
	// Wake immediately since we're in normal Go context
	c.wake()
}

// KErrPrintf implements console.Console.KErrPrintf
func (c *SoftIRQConsole) KErrPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.KWriteString(s)
	c.wake()
}

// KPrintHex implements console.Console.KPrintHex
func (c *SoftIRQConsole) KPrintHex(value interface{}) {
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

// Breadcrumb implements console.Console.Breadcrumb
// Always goes direct to MMIO — never through the ring.
//
//go:nosplit
func (c *SoftIRQConsole) Breadcrumb(b byte) {
	console.BreadcrumbNoSplit(b)
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

// PushByteToUartRing pushes a single byte directly into the UART soft IRQ ring
// and immediately wakes any blocked consumer. This is used by SyscallWrite for
// non-stdio priest output that needs to appear on the stdio display.
// Unlike KWriteByte (which defers wake to the event poller), this wakes immediately.
func PushByteToUartRing(b byte) {
	ev := hid.HIDEvent{Type: 0, Code: 0, Value: uint32(b)}
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

// EnableSoftIRQConsole switches the kernel console from direct MMIO
// to the soft IRQ ring. Must be called after SetupUartSoftIRQ and
// after a userspace priest has registered on the UART slot.
func EnableSoftIRQConsole() {
	c := NewSoftIRQConsole()
	softIRQConsole = c
	console.Set(c)
}

// IsSoftIRQConsoleActive returns true if the soft IRQ console is active.
func IsSoftIRQConsoleActive() bool {
	return softIRQConsole != nil
}
