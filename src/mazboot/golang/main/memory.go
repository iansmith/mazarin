package main

import (
	"mazboot/asm"
	"unsafe"
)

// Linker symbol access via assembly helpers
// These symbols are defined in the linker script (linker.ld)
//
// We use assembly helper functions (in linker_symbols.s) to access linker symbols.
// This eliminates ALL hardcoded memory addresses - the linker provides the actual values!

// getLinkerSymbol returns the VALUE of a linker symbol
//
//go:nosplit
func getLinkerSymbol(name string) uintptr {
	switch name {
	case "__start":
		return asm.GetStartAddr()
	case "__text_start":
		return asm.GetTextStartAddr()
	case "__text_end":
		return asm.GetTextEndAddr()
	case "__rodata_start":
		return asm.GetRodataStartAddr()
	case "__rodata_end":
		return asm.GetRodataEndAddr()
	case "__data_start":
		return asm.GetDataStartAddr()
	case "__data_end":
		return asm.GetDataEndAddr()
	case "__bss_start":
		return asm.GetBssStartAddr()
	case "__bss_end":
		return asm.GetBssEndAddr()
	case "__end":
		return asm.GetEndAddr()
	case "__stack_top":
		return asm.GetStackTopAddr()
	case "__page_tables_start":
		return asm.GetPageTablesStartAddr()
	case "__page_tables_end":
		return asm.GetPageTablesEndAddr()
	case "__ram_start":
		return asm.GetRamStart()
	case "__dtb_boot_addr":
		return asm.GetDtbBootAddr()
	case "__dtb_size":
		return asm.GetDtbSize()
	case "__g0_stack_bottom":
		return asm.GetG0StackBottom()
	case "__gic_base":
		return asm.GetGicBase()
	case "__gic_size":
		return asm.GetGicSize()
	case "__uart_base":
		return asm.GetUartBase()
	case "__uart_size":
		return asm.GetUartSize()
	case "__rtc_base":
		return asm.GetRtcBase()
	case "__fwcfg_base":
		return asm.GetFwcfgBase()
	case "__fwcfg_size":
		return asm.GetFwcfgSize()
	case "__bochs_display_base":
		return asm.GetBochsDisplayBase()
	case "__bochs_display_size":
		return asm.GetBochsDisplaySize()
	default:
		// Unknown symbol - return 0 (caller should check)
		return 0
	}
}

// getLinkerSymbolPointer returns a pointer to a linker symbol's address
// This is useful when you need the address where the symbol is stored
//
//go:nosplit
func getLinkerSymbolPointer(name string) unsafe.Pointer {
	addr := getLinkerSymbol(name)
	if addr != 0 {
		return unsafe.Pointer(addr)
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

// readMemory16 reads a 16-bit value from an arbitrary memory address
//
//go:nosplit
func readMemory16(addr uintptr) uint16 {
	ptr := (*uint16)(unsafe.Pointer(addr))
	return *ptr
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

// readMemory64 reads a 64-bit value from an arbitrary memory address
//
//go:nosplit
func readMemory64(addr uintptr) uint64 {
	ptr := (*uint64)(unsafe.Pointer(addr))
	return *ptr
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
