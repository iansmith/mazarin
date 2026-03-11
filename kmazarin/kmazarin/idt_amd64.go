//go:build amd64

package main

import "unsafe"

// IDT Entry (Interrupt Gate Descriptor) layout - 16 bytes:
//   Bytes 0-1:   offset[15:0]   (handler addr low)
//   Bytes 2-3:   segment selector (0x08 = kernel CS)
//   Byte  4:     IST (0 = no stack switch)
//   Byte  5:     type/attributes (0x8E = present, DPL=0, 64-bit interrupt gate)
//   Bytes 6-7:   offset[31:16]  (handler addr mid)
//   Bytes 8-11:  offset[63:32]  (handler addr high)
//   Bytes 12-15: reserved (0)

const (
	idtEntries = 256
	idtSize    = idtEntries * 16 // 4096 bytes
)

// idtTable is the Interrupt Descriptor Table (256 entries, 4096 bytes).
var idtTable [idtSize]byte

// idtrDescriptor is the 10-byte IDTR structure: [limit:16][base:64].
// Passed to LIDT instruction via SetVBAR.
var idtrDescriptor [10]byte

// ISR address getters defined in exceptions_amd64.s
// These return the raw code address of each ISR entry point via LEAQ.
func getISR0Addr() uintptr
func getISR1Addr() uintptr
func getISR6Addr() uintptr
func getISR8Addr() uintptr
func getISR13Addr() uintptr
func getISRIgnoreAddr() uintptr
func getISR14Addr() uintptr
func getISR48Addr() uintptr
func getISR128Addr() uintptr
func getISR255Addr() uintptr

// deviceISRAddrs holds addresses of ISR stubs for vectors 32-47 (IOAPIC device IRQs).
// Filled by initDeviceISRTable (assembly) before BuildIDT registers them.
var deviceISRAddrs [16]uintptr

// initDeviceISRTable fills deviceISRAddrs with the addresses of isrDev32..isrDev47.
// Defined in exceptions_amd64.s.
func initDeviceISRTable()

// ReadCS returns the current CS (Code Segment) selector value.
// Defined in exceptions_amd64.s.
func ReadCS() uint16

// setIDTEntry populates a single IDT entry with IST=0.
//
//go:nosplit
func setIDTEntry(vector int, handler uintptr, cs uint16) {
	setIDTEntryIST(vector, handler, cs, 0)
}

// setIDTEntryIST populates a single IDT entry with a specific IST index.
// IST=0 means use normal stack switching; IST=1-7 selects a TSS IST stack.
//
//go:nosplit
func setIDTEntryIST(vector int, handler uintptr, cs uint16, ist byte) {
	offset := vector * 16
	addr := uint64(handler)

	// offset[15:0]
	idtTable[offset+0] = byte(addr)
	idtTable[offset+1] = byte(addr >> 8)

	// segment selector (from current CS register)
	idtTable[offset+2] = byte(cs)
	idtTable[offset+3] = byte(cs >> 8)

	// IST index (0-7)
	idtTable[offset+4] = ist & 0x7

	// type/attributes = 0x8E (present, DPL=0, 64-bit interrupt gate)
	idtTable[offset+5] = 0x8E

	// offset[31:16]
	idtTable[offset+6] = byte(addr >> 16)
	idtTable[offset+7] = byte(addr >> 24)

	// offset[63:32]
	idtTable[offset+8] = byte(addr >> 32)
	idtTable[offset+9] = byte(addr >> 40)
	idtTable[offset+10] = byte(addr >> 48)
	idtTable[offset+11] = byte(addr >> 56)

	// reserved
	idtTable[offset+12] = 0
	idtTable[offset+13] = 0
	idtTable[offset+14] = 0
	idtTable[offset+15] = 0
}

// BuildIDT populates the IDT with entries for the exception/interrupt vectors
// we handle, and builds the IDTR descriptor.
//
//go:nosplit
func BuildIDT() {
	// Read the current CS register value - UEFI sets CS=0x38, not 0x08
	cs := ReadCS()

	// Set all 256 entries to a default ignore handler first (prevents triple fault)
	defaultHandler := getISRIgnoreAddr()
	for i := 0; i < 256; i++ {
		setIDTEntry(i, defaultHandler, cs)
	}

	// Then set up entries for the vectors we specifically handle
	setIDTEntry(0, getISR0Addr(), cs)     // Divide error (#DE)
	setIDTEntry(1, getISR1Addr(), cs)     // Debug exception (#DB) — DR0 watchpoint
	setIDTEntry(6, getISR6Addr(), cs)     // Invalid opcode (#UD)
	setIDTEntryIST(8, getISR8Addr(), cs, 1) // Double fault (#DF) — IST=1 (must use dedicated stack to catch nested faults)
	setIDTEntry(13, getISR13Addr(), cs)   // General protection (#GP)
	setIDTEntryIST(14, getISR14Addr(), cs, 1) // Page fault (#PF) — IST=1 (must use dedicated stack; faulting stack may be unmapped)

	// IOAPIC device interrupt vectors 32-47 — use IST=1 so interrupts
	// always use the exception stack, even when preempting kernel code
	// running on small goroutine stacks.
	initDeviceISRTable()
	for i := 0; i < 16; i++ {
		setIDTEntryIST(32+i, deviceISRAddrs[i], cs, 1)
	}

	setIDTEntryIST(48, getISR48Addr(), cs, 1) // LAPIC timer — IST=1
	setIDTEntry(128, getISR128Addr(), cs) // Syscall via INT $0x80
	// Set DPL=3 for vector 128 so userspace (Ring 3) can use INT $0x80.
	// type/attributes byte: 0xEE = Present(1), DPL=3(11), 0, type=1110 (64-bit interrupt gate)
	idtTable[128*16+5] = 0xEE
	setIDTEntry(255, getISR255Addr(), cs) // Spurious interrupt

	// Build IDTR descriptor: [limit:16][base:64]
	limit := uint16(idtSize - 1) // 4095
	base := uint64(uintptr(unsafe.Pointer(&idtTable[0])))

	idtrDescriptor[0] = byte(limit)
	idtrDescriptor[1] = byte(limit >> 8)
	idtrDescriptor[2] = byte(base)
	idtrDescriptor[3] = byte(base >> 8)
	idtrDescriptor[4] = byte(base >> 16)
	idtrDescriptor[5] = byte(base >> 24)
	idtrDescriptor[6] = byte(base >> 32)
	idtrDescriptor[7] = byte(base >> 40)
	idtrDescriptor[8] = byte(base >> 48)
	idtrDescriptor[9] = byte(base >> 56)
}

// GetIDTRAddr returns the address of the IDTR descriptor.
//
//go:nosplit
func GetIDTRAddr() uintptr {
	return uintptr(unsafe.Pointer(&idtrDescriptor[0]))
}

// getExceptionVectorBaseInternal builds the IDT and returns the IDTR address.
// Called via tail-call from GetExceptionVectorBase assembly stub.
//
//go:nosplit
func getExceptionVectorBaseInternal() uintptr {
	BuildIDT()
	return GetIDTRAddr()
}

