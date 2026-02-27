//go:build amd64 && !test_stubs

package main

import (
	"mazzy/kmazarin/console"
	"unsafe"
)

// Assembly helpers in syscall_amd64.s
func readMSR(msr uint32) uint64
func writeMSR(msr uint32, val uint64)
func readGDTBase() uintptr
func readGDTLimit() uint16
func writeGDTR(desc uintptr)
func loadTR(selector uint16)
func getSyscallEntryAddr() uintptr

// MSR constants
const (
	msrEFER  = 0xC0000080
	msrSTAR  = 0xC0000081
	msrLSTAR = 0xC0000082
	msrFMASK = 0xC0000084

	eferSCE = 0x1 // SYSCALL Enable bit

	syscallCS = uint64(0x0008) // Code segment selector for SYSCALL entry (Ring 0 code at GDT 0x08)
)

// newGDT holds the standard GDT copied from diplomat's buffer to kmazarin-owned memory.
// Standard layout: 0x00 (null), 0x08 (Ring 0 code), 0x10 (Ring 0 data),
// 0x18 (Ring 3 code), 0x20 (Ring 3 data), 0x28 (TSS).
var newGDT [512]byte

// gdtrDesc is the 10-byte GDTR descriptor for LGDT.
var gdtrDesc [10]byte

// tssBuffer is the 104-byte Task State Segment for x86_64.
// Used for RSP0 (kernel stack on Ring 3 → Ring 0 transitions).
var tssBuffer [128]byte

// copyGDTToOwnedBuffer copies the current GDT (set up by diplomat) to kmazarin's
// own newGDT buffer and reloads LGDT. This ensures the GDT survives after
// diplomat's memory is reclaimed.
//
// After copying, it rebuilds the TSS descriptor to point to kmazarin's tssBuffer,
// and updates TSS.RSP0 to the current exception stack top.
//
//go:nosplit
func copyGDTToOwnedBuffer() {
	base := readGDTBase()
	limit := readGDTLimit()
	oldSize := int(limit) + 1

	// Copy existing GDT entries (including diplomat's Ring 3 + TSS entries)
	src := (*[512]byte)(unsafe.Pointer(base))
	for i := 0; i < oldSize && i < 512; i++ {
		newGDT[i] = src[i]
	}

	// Set up kmazarin's own TSS buffer
	for i := range tssBuffer {
		tssBuffer[i] = 0
	}
	// Split exception stack into two halves to prevent IST/SYSCALL collision.
	// When SyscallWaitSoftIRQ enables interrupts (STI+HLT) during syscall
	// processing, a timer interrupt via IST would overwrite the SYSCALL frame
	// if both used the same stack region. Bottom half → SYSCALL/RSP0 entry,
	// top half → IST1 for timer/device interrupts.
	//
	// RSP0 at offset 4-11: kernel stack for Ring 3 → Ring 0 transitions.
	// Also used by excStackTopForSyscall for SYSCALL instruction entry.
	excStackTopForSyscall -= 8192 // Bottom half of exception stack
	rsp0 := excStackTopForSyscall
	tssBuffer[4] = byte(rsp0)
	tssBuffer[5] = byte(rsp0 >> 8)
	tssBuffer[6] = byte(rsp0 >> 16)
	tssBuffer[7] = byte(rsp0 >> 24)
	tssBuffer[8] = byte(rsp0 >> 32)
	tssBuffer[9] = byte(rsp0 >> 40)
	tssBuffer[10] = byte(rsp0 >> 48)
	tssBuffer[11] = byte(rsp0 >> 56)

	// IST1 at offset 36-43: dedicated stack for timer/device interrupts.
	// Uses the TOP half of the exception stack (8KB), separate from SYSCALL.
	// On ARM64/RISC-V, exceptions always switch to SP_EL1/sscratch. On x86_64,
	// ring 0→ring 0 interrupts push to the current RSP by default. IST forces
	// the CPU to load a dedicated stack for the interrupt.
	ist1 := excStackTopForSyscall + 8192 // Top half = original excStackTop
	tssBuffer[36] = byte(ist1)
	tssBuffer[37] = byte(ist1 >> 8)
	tssBuffer[38] = byte(ist1 >> 16)
	tssBuffer[39] = byte(ist1 >> 24)
	tssBuffer[40] = byte(ist1 >> 32)
	tssBuffer[41] = byte(ist1 >> 40)
	tssBuffer[42] = byte(ist1 >> 48)
	tssBuffer[43] = byte(ist1 >> 56)

	// IOPB offset at 102-103: set to TSS size (104) to disable I/O bitmap
	tssBuffer[102] = 104
	tssBuffer[103] = 0

	// Rebuild TSS descriptor at GDT offset 0x28 pointing to kmazarin's tssBuffer
	tssBase := uint64(uintptr(unsafe.Pointer(&tssBuffer[0])))
	tssLimit := uint64(103) // 104 bytes - 1

	// TSS descriptor is 16 bytes (system descriptor in 64-bit mode)
	// Bytes 0-1: limit[15:0]
	newGDT[0x28] = byte(tssLimit)
	newGDT[0x29] = byte(tssLimit >> 8)
	// Bytes 2-4: base[23:0]
	newGDT[0x2A] = byte(tssBase)
	newGDT[0x2B] = byte(tssBase >> 8)
	newGDT[0x2C] = byte(tssBase >> 16)
	// Byte 5: access (0x89 = present, DPL=0, type=9 = available 64-bit TSS)
	newGDT[0x2D] = 0x89
	// Byte 6: flags[3:0] | limit[19:16] — granularity=0, limit high nibble
	newGDT[0x2E] = byte(tssLimit >> 16)
	// Byte 7: base[31:24]
	newGDT[0x2F] = byte(tssBase >> 24)
	// Bytes 8-11: base[63:32]
	newGDT[0x30] = byte(tssBase >> 32)
	newGDT[0x31] = byte(tssBase >> 40)
	newGDT[0x32] = byte(tssBase >> 48)
	newGDT[0x33] = byte(tssBase >> 56)
	// Bytes 12-15: reserved
	newGDT[0x34] = 0
	newGDT[0x35] = 0
	newGDT[0x36] = 0
	newGDT[0x37] = 0

	// Set new GDT limit to cover through TSS descriptor (0x37)
	newLimit := uint16(0x37)
	if limit > newLimit {
		newLimit = limit
	}
	newBase := uint64(uintptr(unsafe.Pointer(&newGDT[0])))

	// Build GDTR descriptor: [limit:16][base:64]
	gdtrDesc[0] = byte(newLimit)
	gdtrDesc[1] = byte(newLimit >> 8)
	gdtrDesc[2] = byte(newBase)
	gdtrDesc[3] = byte(newBase >> 8)
	gdtrDesc[4] = byte(newBase >> 16)
	gdtrDesc[5] = byte(newBase >> 24)
	gdtrDesc[6] = byte(newBase >> 32)
	gdtrDesc[7] = byte(newBase >> 40)
	gdtrDesc[8] = byte(newBase >> 48)
	gdtrDesc[9] = byte(newBase >> 56)

	writeGDTR(uintptr(unsafe.Pointer(&gdtrDesc[0])))
	loadTR(0x28)
}

// SetupSyscallMSRs takes ownership of the GDT (copying from diplomat's buffer
// to kmazarin-owned memory) and validates that SYSCALL MSRs are configured.
//
// Diplomat already set up:
//   - Standard GDT (Ring 0: 0x08/0x10, Ring 3: 0x18/0x20, TSS: 0x28)
//   - TSS with RSP0 = exception stack top
//   - EFER.SCE, STAR, LSTAR, FMASK MSRs
//
// This function copies the GDT to kmazarin-owned memory (so it survives diplomat
// memory reclaim), rebuilds the TSS descriptor to point to kmazarin's tssBuffer,
// and reloads LTR.
//
// Must be called after SetVBAR (IDT loaded).
func SetupSyscallMSRs() {
	// Copy diplomat's GDT to kmazarin-owned buffer, rebuild TSS, reload LGDT+LTR
	copyGDTToOwnedBuffer()

	// Validate SYSCALL MSRs are configured by diplomat
	efer := readMSR(msrEFER)
	if efer&eferSCE == 0 {
		console.KPrintln("[SYSCALL] WARNING: EFER.SCE not set, enabling")
		efer |= eferSCE
		writeMSR(msrEFER, efer)
	}

	lstar := readMSR(msrLSTAR)
	if lstar == 0 {
		console.KPrintln("[SYSCALL] WARNING: LSTAR not set, configuring")
		entry := getSyscallEntryAddr()
		writeMSR(msrLSTAR, uint64(entry))
	}

	star := readMSR(msrSTAR)
	if star == 0 {
		console.KPrintln("[SYSCALL] WARNING: STAR not set, configuring")
		writeMSR(msrSTAR, syscallCS<<32)
	}

	fmask := readMSR(msrFMASK)
	if fmask == 0 {
		console.KPrintln("[SYSCALL] WARNING: FMASK not set, configuring")
		writeMSR(msrFMASK, 0x200)
	}
}
