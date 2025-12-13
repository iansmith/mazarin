package main

import (
	"unsafe"
)

// Linker symbol access
// These symbols are defined in the linker script (linker.ld)

// Linker symbol: end of kernel (from linker.ld)
// This marks where the kernel ends and we can start using memory
//
//go:linkname __end __end
var __end uintptr

// getLinkerSymbol returns the address of a linker symbol
// Currently only supports __end, but can be extended for other symbols
//
//go:nosplit
func getLinkerSymbol(name string) uintptr {
	// For now, only __end is supported
	// In the future, we could use a map or switch statement
	if name == "__end" {
		return uintptr(unsafe.Pointer(&__end))
	}
	// Unknown symbol - return 0 (caller should check)
	return 0
}

// getLinkerSymbolPointer returns a pointer to a linker symbol's address
// This is useful when you need the address where the symbol is stored
//
//go:nosplit
func getLinkerSymbolPointer(name string) unsafe.Pointer {
	if name == "__end" {
		return unsafe.Pointer(&__end)
	}
	return nil
}

// Memory access abstractions

// readMemory32 reads a 32-bit value from an arbitrary memory address
//
//go:nosplit
func readMemory32(addr uintptr) uint32 {
	ptr := (*uint32)(unsafe.Pointer(addr))
	return *ptr
}

// writeMemory32 writes a 32-bit value to an arbitrary memory address
//
//go:nosplit
func writeMemory32(addr uintptr, value uint32) {
	ptr := (*uint32)(unsafe.Pointer(addr))
	*ptr = value
}

// readMemory8 reads an 8-bit value from an arbitrary memory address
//
//go:nosplit
func readMemory8(addr uintptr) uint8 {
	ptr := (*uint8)(unsafe.Pointer(addr))
	return *ptr
}

// writeMemory8 writes an 8-bit value to an arbitrary memory address
//
//go:nosplit
func writeMemory8(addr uintptr, value uint8) {
	ptr := (*uint8)(unsafe.Pointer(addr))
	*ptr = value
}

// castToPointer converts a uintptr address to a typed pointer
// This hides the unsafe.Pointer conversion
//
//go:nosplit
func castToPointer[T any](addr uintptr) *T {
	return (*T)(unsafe.Pointer(addr))
}

// writeMemory64 writes a 64-bit value to an arbitrary memory address
//
//go:nosplit
func writeMemory64(addr uintptr, value uint64) {
	ptr := (*uint64)(unsafe.Pointer(addr))
	*ptr = value
}

// Disable write barrier for bare-metal
// Go's write barrier interferes with pointer assignments to .bss globals
// This function disables it by setting the write barrier flag to 0
// runtime.zerobase is at 0x358000, write barrier flag is at +704 (0x2C0)
//
//go:nosplit
func disableWriteBarrier() {
	// Write barrier flag is at runtime.zerobase + 704
	// Address: 0x358000 + 0x2C0 = 0x3582C0
	writeBarrierFlagAddr := uintptr(0x3582C0)
	writeMemory32(writeBarrierFlagAddr, 0) // Disable write barrier
}

// pointerToUintptr converts a pointer to uintptr for arithmetic
// This hides the unsafe.Pointer conversion
//
//go:nosplit
func pointerToUintptr(ptr unsafe.Pointer) uintptr {
	return uintptr(ptr)
}

// addToPointer performs pointer arithmetic: returns ptr + offset
//
//go:nosplit
func addToPointer(ptr unsafe.Pointer, offset uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(ptr) + offset)
}

// subtractFromPointer performs pointer arithmetic: returns ptr - offset
//
//go:nosplit
func subtractFromPointer(ptr unsafe.Pointer, offset uintptr) unsafe.Pointer {
	return unsafe.Pointer(uintptr(ptr) - offset)
}
