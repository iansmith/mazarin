//go:build qemuvirt && aarch64

package virtio

import (
	"kmazarin/console"
	"unsafe"
)

// Helper functions for VirtIO device drivers

// CastToPointer casts a uintptr to a typed pointer
//
//go:nosplit
func CastToPointer[T any](addr uintptr) *T {
	return (*T)(unsafe.Pointer(addr))
}

// PointerToUintptr converts an unsafe.Pointer to uintptr
//
//go:nosplit
func PointerToUintptr(ptr unsafe.Pointer) uintptr {
	return uintptr(ptr)
}

// uartPutsDirect writes a string directly to console
// Used for debug output during device initialization
//
//go:nosplit
func uartPutsDirect(s string) {
	console.KWriteString(s)
}

// uartPutHex8Direct writes an 8-bit hex value
//
//go:nosplit
func uartPutHex8Direct(val uint8) {
	hexChars := "0123456789ABCDEF"
	console.KWriteByte(hexChars[val>>4])
	console.KWriteByte(hexChars[val&0xF])
}

// uartPutHex16Direct writes a 16-bit hex value
//
//go:nosplit
func uartPutHex16Direct(val uint16) {
	uartPutHex8Direct(uint8(val >> 8))
	uartPutHex8Direct(uint8(val))
}

// uartPutHex32Direct writes a 32-bit hex value
//
//go:nosplit
func uartPutHex32Direct(val uint32) {
	uartPutHex16Direct(uint16(val >> 16))
	uartPutHex16Direct(uint16(val))
}

// uartPutHex64Direct writes a 64-bit hex value
//
//go:nosplit
func uartPutHex64Direct(val uint64) {
	uartPutHex32Direct(uint32(val >> 32))
	uartPutHex32Direct(uint32(val))
}

// kmalloc allocates memory using Go's runtime
// For kmazarin, we just use make() to allocate a byte slice
func kmalloc(size uint32) unsafe.Pointer {
	if size == 0 {
		return nil
	}
	// Allocate byte slice and return pointer to first element
	buf := make([]byte, size)
	if len(buf) == 0 {
		return nil
	}
	return unsafe.Pointer(&buf[0])
}

// kfree frees memory (no-op in Go, garbage collector handles this)
//
//go:nosplit
func kfree(ptr unsafe.Pointer) {
	// No-op: Go's garbage collector will handle memory freeing
}

// Bzero4K zeros a memory region
// Note: In kmazarin we can be more flexible with alignment than Cardinal
//
//go:nosplit
func Bzero4K(ptr unsafe.Pointer, size uint32) {
	if size == 0 || ptr == nil {
		return
	}

	// Zero memory byte by byte
	p := (*[1 << 30]byte)(ptr)
	for i := uint32(0); i < size; i++ {
		p[i] = 0
	}
}
