// Package console provides a console abstraction for kernel output.
// This allows early boot to use direct MMIO writes, then swap to
// interrupt-driven UART after device discovery is complete.
package console

import (
	"sync/atomic"
	"unsafe"
)

// Console is the interface for kernel console output.
// Implementations must be safe to call from any context, including
// interrupt handlers (nosplit).
type Console interface {
	// Write writes bytes to the console.
	// Returns number of bytes written.
	Write(p []byte) int

	// WriteByte writes a single byte to the console.
	WriteByte(c byte)

	// WriteString writes a string to the console.
	WriteString(s string)
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

	wrapper := v.(*consoleWrapper)
	return wrapper.impl
}

// Write writes bytes to the current console.
// No-op if no console is set.
//
//go:nosplit
func Write(p []byte) int {
	c := Get()
	if c == nil {
		return 0
	}
	return c.Write(p)
}

// WriteByte writes a single byte to the current console.
// No-op if no console is set.
//
//go:nosplit
func WriteByte(b byte) {
	c := Get()
	if c == nil {
		return
	}
	c.WriteByte(b)
}

// WriteString writes a string to the current console.
// No-op if no console is set.
//
//go:nosplit
func WriteString(s string) {
	c := Get()
	if c == nil {
		return
	}
	c.WriteString(s)
}

// Print writes a string to the console (no newline).
//
//go:nosplit
func Print(s string) {
	WriteString(s)
}

// Println writes a string followed by CRLF.
//
//go:nosplit
func Println(s string) {
	WriteString(s)
	WriteByte('\r')
	WriteByte('\n')
}

// PrintHex64 writes a 64-bit value in hex.
//
//go:nosplit
func PrintHex64(val uint64) {
	hexChars := "0123456789ABCDEF"
	WriteString("0x")
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		WriteByte(hexChars[nibble])
	}
}

// MMIOUartConsole implements Console using direct MMIO writes.
// This is used during early boot before interrupt-driven UART is available.
// It has no dependencies on GIC or interrupts.
// Uses a spinlock to prevent interleaved output from IRQ handlers.
type MMIOUartConsole struct {
	baseAddr uintptr
	lock     uint32 // Spinlock: 0=unlocked, 1=locked
}

// NewMMIOUartConsole creates a new MMIO-based console.
// baseAddr should be the high-memory UART address (e.g., 0xFFFFFFFF09000000).
func NewMMIOUartConsole(baseAddr uintptr) *MMIOUartConsole {
	return &MMIOUartConsole{baseAddr: baseAddr}
}

// acquire acquires the spinlock
//
//go:nosplit
func (c *MMIOUartConsole) acquire() {
	for !atomic.CompareAndSwapUint32(&c.lock, 0, 1) {
		// Spin
	}
}

// release releases the spinlock
//
//go:nosplit
func (c *MMIOUartConsole) release() {
	atomic.StoreUint32(&c.lock, 0)
}

// Write implements Console.Write
//
//go:nosplit
func (c *MMIOUartConsole) Write(p []byte) int {
	c.acquire()
	for _, b := range p {
		c.writeByteLocked(b)
	}
	c.release()
	return len(p)
}

// WriteByte implements Console.WriteByte
//
//go:nosplit
func (c *MMIOUartConsole) WriteByte(b byte) {
	// Safety check: if c is nil, just return
	if c == nil {
		return
	}
	c.acquire()
	c.writeByteLocked(b)
	c.release()
}

// writeByteLocked writes a byte with lock already held
//
//go:nosplit
func (c *MMIOUartConsole) writeByteLocked(b byte) {
	*(*byte)(unsafe.Pointer(c.baseAddr)) = b
}

// WriteString implements Console.WriteString
//
//go:nosplit
func (c *MMIOUartConsole) WriteString(s string) {
	c.acquire()
	for i := 0; i < len(s); i++ {
		c.writeByteLocked(s[i])
	}
	c.release()
}
