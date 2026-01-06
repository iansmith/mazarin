// Package dev contains device driver assembly routines for Cardinal.
// This includes UART, timer, MMIO, and semihosting support.
package dev

import _ "unsafe" // for go:linkname

// UART PL011 driver (global symbols)

//go:linkname UartInitPl011 uart_init_pl011
//go:nosplit
func UartInitPl011()

//go:linkname UartPutcPl011 uart_putc_pl011
//go:nosplit
func UartPutcPl011(c byte)

// Generic Timer - Virtual timer (global symbols)

//go:linkname ReadCntvCtlEl0 read_cntv_ctl_el0
//go:nosplit
func ReadCntvCtlEl0() uint32

//go:linkname WriteCntvCtlEl0 write_cntv_ctl_el0
//go:nosplit
func WriteCntvCtlEl0(val uint32)

//go:linkname ReadCntvTvalEl0 read_cntv_tval_el0
//go:nosplit
func ReadCntvTvalEl0() uint32

//go:linkname WriteCntvTvalEl0 write_cntv_tval_el0
//go:nosplit
func WriteCntvTvalEl0(val uint32)

//go:linkname ReadCntvCvalEl0 read_cntv_cval_el0
//go:nosplit
func ReadCntvCvalEl0() uint64

//go:linkname WriteCntvCvalEl0 write_cntv_cval_el0
//go:nosplit
func WriteCntvCvalEl0(val uint64)

//go:linkname ReadCntvctEl0 read_cntvct_el0
//go:nosplit
func ReadCntvctEl0() uint64

//go:linkname ReadCntfrqEl0 read_cntfrq_el0
//go:nosplit
func ReadCntfrqEl0() uint32

// Generic Timer - Physical timer (global symbols)

//go:linkname ReadCntpCtlEl0 read_cntp_ctl_el0
//go:nosplit
func ReadCntpCtlEl0() uint32

//go:linkname WriteCntpCtlEl0 write_cntp_ctl_el0
//go:nosplit
func WriteCntpCtlEl0(val uint32)

//go:linkname ReadCntpTvalEl0 read_cntp_tval_el0
//go:nosplit
func ReadCntpTvalEl0() uint32

//go:linkname WriteCntpTvalEl0 write_cntp_tval_el0
//go:nosplit
func WriteCntpTvalEl0(val uint32)

//go:linkname ReadCntpCvalEl0 read_cntp_cval_el0
//go:nosplit
func ReadCntpCvalEl0() uint64

//go:linkname WriteCntpCvalEl0 write_cntp_cval_el0
//go:nosplit
func WriteCntpCvalEl0(val uint64)

//go:linkname ReadCntpctEl0 read_cntpct_el0
//go:nosplit
func ReadCntpctEl0() uint64

// MMIO operations (global symbols)

//go:linkname MmioRead mmio_read
//go:nosplit
func MmioRead(addr uintptr) uint32

//go:linkname MmioWrite mmio_write
//go:nosplit
func MmioWrite(addr uintptr, val uint32)

//go:linkname MmioRead16 mmio_read16
//go:nosplit
func MmioRead16(addr uintptr) uint16

//go:linkname MmioWrite16 mmio_write16
//go:nosplit
func MmioWrite16(addr uintptr, val uint16)

//go:linkname MmioWrite64 mmio_write64
//go:nosplit
func MmioWrite64(addr uintptr, val uint64)

//go:linkname StorePointerNobarrier store_pointer_nobarrier
//go:nosplit
func StorePointerNobarrier(dst, val uintptr)

// Semihosting (global symbols)

//go:linkname QemuExit qemu_exit
//go:nosplit
func QemuExit()

//go:linkname SemihostingExit SemihostingExit
//go:nosplit
func SemihostingExit()

//go:linkname JumpToNull jump_to_null
//go:nosplit
func JumpToNull()

// BreadcrumbExit prints a single character and immediately exits QEMU.
// Use this for debugging hangs - place calls at suspected locations and use
// binary search to find where the kernel hangs.
//
// The character is printed to UART, then semihosting exit flushes all buffers,
// guaranteeing the output appears even if QEMU times out or hangs.
//
// Example:
//   dev.BreadcrumbExit('A')  // Will print "A\r\n" then exit QEMU
//
// Debugging pattern (binary search):
//   1. Place BreadcrumbExit('A') at the start of suspicious function
//   2. If 'A' appears: hang is later - move marker down
//   3. If 'A' doesn't appear: hang is earlier - move marker up
//   4. Repeat until you find the exact hanging instruction
//
//go:linkname BreadcrumbExit breadcrumb_exit
//go:nosplit
func BreadcrumbExit(c byte)

// Image data accessors (global symbols)

//go:linkname ImageDataStart imageDataStart
//go:nosplit
func ImageDataStart() uintptr

//go:linkname ImageDataEnd imageDataEnd
//go:nosplit
func ImageDataEnd() uintptr

// Kmazarin binary accessors (package-local assembly functions)

//go:nosplit
func kmazarinBinaryStart() uintptr

//go:nosplit
func kmazarinBinaryEnd() uintptr

// Exported wrappers
func KmazarinBinaryStart() uintptr { return kmazarinBinaryStart() }
func KmazarinBinaryEnd() uintptr   { return kmazarinBinaryEnd() }
