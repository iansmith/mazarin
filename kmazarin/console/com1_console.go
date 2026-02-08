//go:build amd64 && !test_stubs

package console

import (
	"fmt"
	"reflect"
	"sync/atomic"
)

// COM1Console implements Console using x86_64 COM1 serial port (I/O port 0x3F8).
// Uses the breadcrumb() function which outputs via OUTB instruction.
// This replaces MMIOUartConsole on x86_64 where there is no PL011 UART.
type COM1Console struct {
	lock uint32 // Spinlock: 0=unlocked, 1=locked
}

// NewCOM1Console creates a new COM1-based console.
func NewCOM1Console() *COM1Console {
	return &COM1Console{}
}

//go:nosplit
func (c *COM1Console) acquireNoIRQ() {
	for !atomic.CompareAndSwapUint32(&c.lock, 0, 1) {
		// Spin
	}
}

//go:nosplit
func (c *COM1Console) releaseNoIRQ() {
	atomic.StoreUint32(&c.lock, 0)
}

// KWrite implements Console.KWrite
//
//go:nosplit
func (c *COM1Console) KWrite(p []byte) int {
	c.acquireNoIRQ()
	for _, b := range p {
		breadcrumb(b)
	}
	c.releaseNoIRQ()
	return len(p)
}

// KWriteByte implements Console.KWriteByte
//
//go:nosplit
func (c *COM1Console) KWriteByte(b byte) {
	if c == nil {
		return
	}
	c.acquireNoIRQ()
	breadcrumb(b)
	c.releaseNoIRQ()
}

// KWriteString implements Console.KWriteString
//
//go:nosplit
func (c *COM1Console) KWriteString(s string) {
	c.acquireNoIRQ()
	for i := 0; i < len(s); i++ {
		breadcrumb(s[i])
	}
	c.releaseNoIRQ()
}

// KPrintf implements Console.KPrintf
func (c *COM1Console) KPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.KWriteString(s)
}

// KErrPrintf implements Console.KErrPrintf
func (c *COM1Console) KErrPrintf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	c.KWriteString(s)
}

// KPrintHex implements Console.KPrintHex
func (c *COM1Console) KPrintHex(value interface{}) {
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
func (c *COM1Console) Breadcrumb(b byte) {
	breadcrumb(b)
}
