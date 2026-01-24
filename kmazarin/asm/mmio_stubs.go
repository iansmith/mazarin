//go:build test_stubs

package asm

// Test stubs for MMIO functions.
// These no-op implementations allow unit tests to run without hardware access.

//go:nosplit
func MmioRead8(addr uintptr) byte {
	return 0
}

//go:nosplit
func MmioRead16(addr uintptr) uint16 {
	return 0
}

//go:nosplit
func MmioRead32(addr uintptr) uint32 {
	return 0
}

//go:nosplit
func MmioWrite8(addr uintptr, val byte) {
}

//go:nosplit
func MmioWrite32(addr uintptr, val uint32) {
}

//go:nosplit
func MmioRead(addr uintptr) uint32 {
	return 0
}

//go:nosplit
func MmioWrite(addr uintptr, val uint32) {
}

//go:nosplit
func MmioWrite16(addr uintptr, val uint16) {
}

//go:nosplit
func Dsb() {
}

//go:nosplit
func DmaWmb() {
}

//go:nosplit
func DmaRmb() {
}

//go:nosplit
func CleanDCacheRange(start uintptr, size uintptr) {
}

//go:nosplit
func InvalidateDCacheRange(start uintptr, size uintptr) {
}
