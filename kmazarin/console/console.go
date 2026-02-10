// Package console provides a console abstraction for kernel output.
// This allows early boot to use direct MMIO writes, then swap to
// interrupt-driven UART after device discovery is complete.
package console

import (
	"fmt"
	"mazzy/kmazarin/asm"
	"reflect"
	"sync/atomic"
)

// Console is the interface for kernel console output.
// Implementations must be safe to call from any context, including
// interrupt handlers (nosplit).
type Console interface {
	// KWrite writes bytes to the console.
	// Returns number of bytes written.
	KWrite(p []byte) int

	// KWriteByte writes a single byte to the console.
	KWriteByte(c byte)

	// KWriteString writes a string to the console.
	KWriteString(s string)

	// KPrintf formats and writes to the console (like fmt.Printf).
	KPrintf(format string, args ...interface{})

	// KErrPrintf formats and writes error output to the console.
	KErrPrintf(format string, args ...interface{})

	// KPrintHex formats and writes a value in hex with appropriate width.
	// Uses reflection to determine the type and format integers/pointers accordingly.
	KPrintHex(value interface{})

	// Breadcrumb writes a single byte directly to UART hardware.
	// This is safe to call from any context, including IRQ handlers.
	// Bypasses all abstractions and ring buffers.
	Breadcrumb(b byte)
}

// consoleWrapper wraps a Console interface so atomic.Value always stores the same concrete type.
// This is necessary because atomic.Value panics if you try to store different concrete types.
type consoleWrapper struct {
	impl Console
}

// current holds the active console implementation wrapped in consoleWrapper.
// Uses atomic.Value for safe swapping from any context.
var current atomic.Value

// Set sets the active console implementation.
// Safe to call from any context.
func Set(c Console) {
	current.Store(&consoleWrapper{impl: c})
}

// Get returns the current console, or nil if not set.
//
//go:nosplit
//go:noinline
func Get() Console {
	v := current.Load()
	if v == nil {
		return nil
	}

	wrapper, ok := v.(*consoleWrapper)
	if !ok {
		return nil
	}
	return wrapper.impl
}

// KWrite writes bytes to the current console.
// No-op if no console is set.
//
//go:nosplit
func KWrite(p []byte) int {
	c := Get()
	if c == nil {
		return 0
	}
	return c.KWrite(p)
}

// KWriteByte writes a single byte to the current console.
// No-op if no console is set.
//
//go:nosplit
func KWriteByte(b byte) {
	c := Get()
	if c == nil {
		return
	}
	c.KWriteByte(b)
}

// KWriteString writes a string to the current console.
// No-op if no console is set.
//
//go:nosplit
func KWriteString(s string) {
	c := Get()
	if c == nil {
		return
	}
	c.KWriteString(s)
}

// KPrint writes a string to the console (no newline).
//
//go:nosplit
func KPrint(s string) {
	KWriteString(s)
}

// KPrintln writes a string followed by CRLF.
//
//go:nosplit
func KPrintln(s string) {
	KWriteString(s)
	KWriteByte('\r')
	KWriteByte('\n')
}

// KPrintHex64 writes a 64-bit value in hex.
//
//go:nosplit
func KPrintHex64(val uint64) {
	hexChars := "0123456789ABCDEF"
	KWriteString("0x")
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		KWriteByte(hexChars[nibble])
	}
}

// KPrintf formats and writes to the current console.
// No-op if no console is set.
func KPrintf(format string, args ...interface{}) {
	c := Get()
	if c == nil {
		return
	}
	c.KPrintf(format, args...)
}

// KErrPrintf formats and writes error output to the current console.
// No-op if no console is set.
func KErrPrintf(format string, args ...interface{}) {
	c := Get()
	if c == nil {
		return
	}
	c.KErrPrintf(format, args...)
}

// KPrintHex formats and writes a value in hex with appropriate width.
// Uses reflection to determine the type and format integers/pointers accordingly.
// No-op if no console is set.
func KPrintHex(value interface{}) {
	c := Get()
	if c == nil {
		return
	}
	c.KPrintHex(value)
}

// Breadcrumb writes a single byte directly to UART hardware.
// Safe to call from any context, including IRQ handlers.
// If console is set, uses console's Breadcrumb method; otherwise uses direct MMIO.
//
//go:nosplit
func Breadcrumb(b byte) {
	c := Get()
	if c == nil {
		// No console set - use direct MMIO
		breadcrumb(b)
		return
	}
	c.Breadcrumb(b)
}

// BreadcrumbNoSplit writes a single byte directly to UART hardware via MMIO.
// This function bypasses the console abstraction entirely, calling the internal
// breadcrumb() function directly.
//
// USE THIS FUNCTION WHEN:
// - You're in scheduler/timer/preemption code (nosplit context)
// - Adding debug output causes "nosplit stack overflow" errors
// - You need the smallest possible stack footprint
//
// The regular Breadcrumb() function calls Get() which has a type assertion,
// and the Go compiler's nosplit analysis includes the panic path from that
// type assertion, causing stack overflow in tight nosplit call chains.
//
// This function has zero function calls with panic paths, making it safe
// for the deepest nosplit contexts.
//
//go:nosplit
func BreadcrumbNoSplit(b byte) {
	breadcrumb(b)
}

// BreadcrumbHex8NoSplit writes a single hex digit (0-15) directly to UART.
// For use in nosplit contexts. Does NOT print "0x" prefix.
//
//go:nosplit
func BreadcrumbHex8NoSplit(val uint8) {
	const hexChars = "0123456789ABCDEF"
	breadcrumb(hexChars[val>>4])
	breadcrumb(hexChars[val&0xF])
}

// BreadcrumbHex16NoSplit writes a 16-bit value as 4 hex digits.
// For use in nosplit contexts. Does NOT print "0x" prefix.
//
//go:nosplit
func BreadcrumbHex16NoSplit(val uint16) {
	const hexChars = "0123456789ABCDEF"
	breadcrumb(hexChars[(val>>12)&0xF])
	breadcrumb(hexChars[(val>>8)&0xF])
	breadcrumb(hexChars[(val>>4)&0xF])
	breadcrumb(hexChars[val&0xF])
}

// BreadcrumbHex32NoSplit writes a 32-bit value as 8 hex digits.
// For use in nosplit contexts. Does NOT print "0x" prefix.
//
//go:nosplit
func BreadcrumbHex32NoSplit(val uint32) {
	const hexChars = "0123456789ABCDEF"
	for i := 28; i >= 0; i -= 4 {
		breadcrumb(hexChars[(val>>i)&0xF])
	}
}

// BreadcrumbHex64NoSplit writes a 64-bit value as 16 hex digits.
// For use in nosplit contexts. Does NOT print "0x" prefix.
//
//go:nosplit
func BreadcrumbHex64NoSplit(val uint64) {
	const hexChars = "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		breadcrumb(hexChars[(val>>i)&0xF])
	}
}

// BreadcrumbDecimalNoSplit prints a uint64 as decimal digits directly to UART.
// Uses a fixed small buffer to minimize stack usage in nosplit contexts.
//
//go:nosplit
func BreadcrumbDecimalNoSplit(val uint64) {
	// For values we expect (0-15 seconds), this is plenty.
	// Max uint64 is 20 digits; we use fixed iteration to avoid variable-size stack.
	if val == 0 {
		breadcrumb('0')
		return
	}
	// Extract digits in reverse, then print forward.
	// Use 5 fixed slots (handles up to 99999 — more than enough for seconds).
	var d0, d1, d2, d3, d4 byte
	n := 0
	d0 = byte(val%10) + '0'
	val /= 10
	n = 1
	if val > 0 {
		d1 = byte(val%10) + '0'
		val /= 10
		n = 2
	}
	if val > 0 {
		d2 = byte(val%10) + '0'
		val /= 10
		n = 3
	}
	if val > 0 {
		d3 = byte(val%10) + '0'
		val /= 10
		n = 4
	}
	if val > 0 {
		d4 = byte(val%10) + '0'
		n = 5
	}
	if n >= 5 {
		breadcrumb(d4)
	}
	if n >= 4 {
		breadcrumb(d3)
	}
	if n >= 3 {
		breadcrumb(d2)
	}
	if n >= 2 {
		breadcrumb(d1)
	}
	breadcrumb(d0)
}

// BreadcrumbStringNoSplit writes a string directly to UART via MMIO.
// For use in nosplit contexts where even BreadcrumbNoSplit per-char is fine.
//
//go:nosplit
func BreadcrumbStringNoSplit(s string) {
	for i := 0; i < len(s); i++ {
		breadcrumb(s[i])
	}
}

// breadcrumb writes a byte directly to UART MMIO.
// Implementation is in breadcrumb_asm.go (real hardware) or breadcrumb_stubs.go (test builds).

// MMIOUartConsole implements Console using direct MMIO writes.
// This is used during early boot before interrupt-driven UART is available.
// It has no dependencies on GIC or interrupts.
//
// CRITICAL IRQ SAFETY WARNING:
// The spinlock-protected methods (KWrite, KWriteByte, KWriteString, KPrintf, etc.)
// MUST NOT be called from IRQ context. If an IRQ preempts a thread holding the lock
// and then tries to acquire it, deadlock will occur (the interrupted thread can never
// release the lock because the IRQ handler spins forever).
//
// For IRQ-safe output, use Breadcrumb() which bypasses the spinlock and writes
// directly to UART hardware.
type MMIOUartConsole struct {
	baseAddr uintptr
	lock     uint32 // Spinlock: 0=unlocked, 1=locked
}

// NewMMIOUartConsole creates a new MMIO-based console.
// baseAddr should be the high-memory UART address (e.g., 0xFFFFFFFF09000000).
func NewMMIOUartConsole(baseAddr uintptr) *MMIOUartConsole {
	return &MMIOUartConsole{baseAddr: baseAddr}
}

// acquireNoIRQ acquires the spinlock.
//
// WARNING: MUST NOT be called from IRQ context - use Breadcrumb() instead.
// If an IRQ preempts a lock holder and tries to acquire, deadlock occurs.
//
//go:nosplit
func (c *MMIOUartConsole) acquireNoIRQ() {
	for !atomic.CompareAndSwapUint32(&c.lock, 0, 1) {
		// Spin
	}
}

// releaseNoIRQ releases the spinlock.
//
//go:nosplit
func (c *MMIOUartConsole) releaseNoIRQ() {
	atomic.StoreUint32(&c.lock, 0)
}

// KWrite implements Console.KWrite
//
//go:nosplit
func (c *MMIOUartConsole) KWrite(p []byte) int {
	c.acquireNoIRQ()
	for _, b := range p {
		c.writeByteLocked(b)
	}
	c.releaseNoIRQ()
	return len(p)
}

// KWriteByte implements Console.KWriteByte
//
//go:nosplit
func (c *MMIOUartConsole) KWriteByte(b byte) {
	// Safety check: if c is nil, just return
	if c == nil {
		return
	}
	c.acquireNoIRQ()
	c.writeByteLocked(b)
	c.releaseNoIRQ()
}

// writeByteLocked writes a byte with lock already held
//
//go:nosplit
func (c *MMIOUartConsole) writeByteLocked(b byte) {
	asm.MmioWrite32(c.baseAddr, uint32(b))
}

// KWriteString implements Console.KWriteString
//
//go:nosplit
func (c *MMIOUartConsole) KWriteString(s string) {
	c.acquireNoIRQ()
	for i := 0; i < len(s); i++ {
		c.writeByteLocked(s[i])
	}
	c.releaseNoIRQ()
}

// KPrintf implements Console.KPrintf
func (c *MMIOUartConsole) KPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.KWriteString(s)
}

// KErrPrintf implements Console.KErrPrintf
func (c *MMIOUartConsole) KErrPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.KWriteString(s)
}

// KPrintHex implements Console.KPrintHex
func (c *MMIOUartConsole) KPrintHex(value interface{}) {
	v := reflect.ValueOf(value)
	kind := v.Kind()

	switch kind {
	case reflect.Int8, reflect.Uint8:
		c.KPrintf("0x%02X", value)
	case reflect.Int16, reflect.Uint16:
		c.KPrintf("0x%04X", value)
	case reflect.Int32, reflect.Uint32:
		c.KPrintf("0x%08X", value)
	case reflect.Int64, reflect.Uint64, reflect.Int, reflect.Uint, reflect.Uintptr:
		c.KPrintf("0x%016X", value)
	case reflect.Ptr, reflect.UnsafePointer:
		c.KPrintf("0x%016X", v.Pointer())
	default:
		c.KWriteString("not hex value")
	}
}

// Breadcrumb implements Console.Breadcrumb
//
//go:nosplit
func (c *MMIOUartConsole) Breadcrumb(b byte) {
	breadcrumb(b)
}
