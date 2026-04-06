package main

import (
	"fmt"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/klog"
	"mazzy/shared/hid"
	"reflect"
	"sync/atomic"
	_ "unsafe" // for go:linkname
)

// SoftIRQConsole implements console.Console by pushing bytes into the
// topHalfUartRing. Rachel (or any userspace shepherd registered on the
// UART soft IRQ slot) receives these bytes via WaitSoftIRQ.
//
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
		func(s string) { pushStringToRing(c, 1, s) }, // stdout
		func(s string) { pushStringToRing(c, 2, s) }, // stderr
	)
	klog.SetLinuxReady()
}

// pushStringToRing pushes every byte of s into the UART soft IRQ ring
// with the given fd (1=stdout, 2=stderr), converting \n to \r\n, then
// wakes the linux shepherd.
func pushStringToRing(c *SoftIRQConsole, fd byte, s string) {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '\n' {
			ev := hid.HIDEvent{Type: 0, Code: uint16(fd), Value: uint32('\r')}
			ringPush(&topHalfUartRing, ev)
		}
		ev := hid.HIDEvent{Type: 0, Code: uint16(fd), Value: uint32(b)}
		ringPush(&topHalfUartRing, ev)
	}
	c.wake()
}

// IsSoftIRQConsoleActive returns true if the soft IRQ console is active.
func IsSoftIRQConsoleActive() bool {
	return softIRQConsole != nil
}
