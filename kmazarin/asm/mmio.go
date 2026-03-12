package asm

// MmioRead8 is implemented in mmio_arm64.s
// Reads an 8-bit value from MMIO using volatile memory access
//
//go:nosplit
func MmioRead8(addr uintptr) byte

// MmioRead16 is implemented in mmio_arm64.s
// Reads a 16-bit value from MMIO using volatile memory access
//
//go:nosplit
func MmioRead16(addr uintptr) uint16

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

// MmioRead is an alias for MmioRead32 (for compatibility)
//
//go:nosplit
func MmioRead(addr uintptr) uint32 {
	return MmioRead32(addr)
}

// MmioWrite is an alias for MmioWrite32 (for compatibility)
//
//go:nosplit
func MmioWrite(addr uintptr, val uint32) {
	MmioWrite32(addr, val)
}

// MmioWrite16 is implemented in mmio_arm64.s
// Writes a 16-bit value to MMIO using volatile memory access
//
//go:nosplit
func MmioWrite16(addr uintptr, val uint16)

// Dsb is implemented in mmio_arm64.s
// Data Synchronization Barrier - ensures all memory accesses complete
//
//go:nosplit
func Dsb()

// DmaWmb is implemented in mmio_arm64.s
// DMA write memory barrier - ensures stores are visible to DMA devices
// This is equivalent to Linux's dma_wmb() - DMB OSHST on ARM64
//
//go:nosplit
func DmaWmb()

// DmaRmb is implemented in mmio_arm64.s
// DMA read memory barrier - ensures device writes are visible to CPU
// This is equivalent to Linux's dma_rmb() - DMB OSH on ARM64
//
//go:nosplit
func DmaRmb()

// CleanDCacheRange is implemented in mmio_arm64.s
// Clean (write back) data cache for address range - required before DMA read
//
//go:nosplit
func CleanDCacheRange(start uintptr, size uintptr)

// InvalidateDCacheRange is implemented in mmio_arm64.s
// Invalidate (discard) data cache for address range - required after DMA write
//
//go:nosplit
func InvalidateDCacheRange(start uintptr, size uintptr)

// Wfe executes Wait For Event (ARM64) or PAUSE (x86_64).
// Under HVF, this causes a VM exit that gives QEMU's event loop a
// scheduling point to process async I/O, then returns immediately.
// Under TCG, this is effectively a NOP hint.
//
//go:nosplit
func Wfe()

// Wfi executes Wait For Interrupt (ARM64/RISC-V) or HLT (x86_64).
// Halts the vCPU until an interrupt fires.
//
//go:nosplit
func Wfi()

// Hlt executes the HLT instruction (x86_64) or a no-op on ARM64/RISC-V.
// Halts the vCPU until an interrupt fires.
//
//go:nosplit
func Hlt()

// StiHlt atomically enables interrupts and halts (x86_64 only).
// STI has a 1-instruction shadow: HLT executes before any pending interrupt
// is delivered. The CPU wakes on the first interrupt. After the interrupt
// handler returns via IRETQ, execution continues at the instruction after HLT
// with IF=1 (restored from the pre-interrupt RFLAGS).
// On ARM64/RISC-V this is a no-op (use Wfi instead).
//
//go:nosplit
func StiHlt()
