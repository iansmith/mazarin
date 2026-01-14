//go:build qemuvirt && aarch64

package asm

// MmioRead8 is implemented in mmio_arm64.s
// Reads an 8-bit value from MMIO using volatile memory access
//
//go:nosplit
func MmioRead8(addr uintptr) byte

// MmioRead32 is implemented in mmio_arm64.s
// Reads a 32-bit value from MMIO using volatile memory access
//
//go:nosplit
func MmioRead32(addr uintptr) uint32

// MmioWrite8 is implemented in mmio_arm64.s
// Writes an 8-bit value to MMIO using volatile memory access
//
//go:nosplit
func MmioWrite8(addr uintptr, val byte)

// MmioWrite32 is implemented in mmio_arm64.s
// Writes a 32-bit value to MMIO using volatile memory access
//
//go:nosplit
func MmioWrite32(addr uintptr, val uint32)
