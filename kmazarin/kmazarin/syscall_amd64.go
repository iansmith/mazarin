//go:build amd64

package main

import (
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
//
// MINEFIELD (MAZ-136 IST rotation): the IST1 field (bytes 36-43) is NOT a
// boot-time constant. The exception entry/exit paths in exceptions_amd64.s
// continuously rotate it (entry: -istRotateStride, return: +istRotateStride)
// and every context-restore site rewrites it absolutely from ctx.ISTBase.
// The CPU reads IST fields from this memory at EVERY delivery (LTR caches
// only the TSS base), which is exactly what makes runtime rotation work.
// Do not cache, snapshot, or "fix up" tssBuffer[36:44] anywhere else —
// tssIST1() below is the only sanctioned Go-side reader; all writes go
// through the assembly rotation and restore paths.
var tssBuffer [128]byte

// istRotateStride is the per-nesting-level reservation the IST rotation
// subtracts from TSS.IST1 on every exception entry. It must exceed the worst
// single-level stack use on the exception stack: the 168-byte CPU+GPR frame
// plus one handler's Go call chain. Handlers run effectively nosplit-bounded
// (the 792 B budget; non-nosplit Go in handlers runs against g0's stackguard
// and never legitimately morestacks), so 2 KiB has ~2.4× headroom over the
// theoretical bound. Exported to assembly via go_asm.h as
// $const_istRotateStride — this Go const is the ONLY definition.
const istRotateStride = 0x800

// ist1Floor / ist1Top bound the IST1 half of the exception stack, published
// by copyGDTToOwnedBuffer AFTER the TSS split is final. The asm rotation
// gates on ist1Floor != 0: before publication every exception skips both the
// entry SUB and the return ADD, so the arithmetic can never go unbalanced
// across the publication moment. (Single writer, straight-line init code on
// a single CPU: the floor value one exception sees at entry is necessarily
// the value it sees at return — copyGDTToOwnedBuffer cannot run in between,
// because it IS the interrupted code, and a handler that context-switches
// away instead exits through an absolute ctx.ISTBase restore.)
var ist1Floor uint64
var ist1Top uint64

// tssIST1 reads the live TSS.IST1 value. bytes 36-43 are not 8-aligned; x86
// permits the unaligned qword load on WB memory.
//
//go:nosplit
func tssIST1() uint64 {
	return *(*uint64)(unsafe.Pointer(&tssBuffer[36]))
}

// doubleFaultStack is the dedicated IST2 stack for the #DF handler.
// Separate from IST1 (used by #PF, timer, device IRQs) so that a double
// fault during a page fault handler gets a clean stack for diagnostics
// instead of clobbering IST1 and causing an unrecoverable triple fault.
var doubleFaultStack [4096]byte

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
	// The IST1 half is 16 KiB: the MAZ-136 IST rotation reserves
	// istRotateStride (2 KiB) per exception-nesting level, so 16 KiB gives 8
	// levels before the rotation-overflow tripwire halts. (The whole exception
	// stack is 128 KiB; the RSP0 half keeps the remaining 112 KiB.)
	//
	// RSP0 at offset 4-11: kernel stack for Ring 3 → Ring 0 transitions.
	// Also used by excStackTopForSyscall for SYSCALL instruction entry.
	excStackTopForSyscall -= 8 * istRotateStride // Bottom half of exception stack
	rsp0 := excStackTopForSyscall
	tssBuffer[4] = byte(rsp0)
	tssBuffer[5] = byte(rsp0 >> 8)
	tssBuffer[6] = byte(rsp0 >> 16)
	tssBuffer[7] = byte(rsp0 >> 24)
	tssBuffer[8] = byte(rsp0 >> 32)
	tssBuffer[9] = byte(rsp0 >> 40)
	tssBuffer[10] = byte(rsp0 >> 48)
	tssBuffer[11] = byte(rsp0 >> 56)

	// IST1 at offset 36-43: dedicated stack for #PF + timer/device interrupts.
	// Uses the TOP 16 KiB of the exception stack, separate from SYSCALL.
	//
	// MINEFIELD (MAZ-136): raw IST semantics are a trap — the CPU reloads
	// RSP = IST1 on EVERY delivery, so a second IST-vectored exception while
	// a chain is live on this half (a nested kernel #PF inside the #PF
	// handler, or an IRQ after IF is re-enabled mid-handler) would restart
	// from the SAME fixed top and trample the suspended chain. That was the
	// MAZ-136 corruption. The rotation in exceptions_amd64.s (see the IST
	// ROTATION banner there) lowers this field one istRotateStride per live
	// nesting level so every delivery lands below all live frames. ARM64
	// needs none of this: SP_EL1 nests naturally (EL1h→EL1h pushes at the
	// CURRENT SP_EL1) — the value written here is only the rotation's BASE.
	ist1 := excStackTopForSyscall + 8*istRotateStride // Top half = original excStackTop
	tssBuffer[36] = byte(ist1)
	tssBuffer[37] = byte(ist1 >> 8)
	tssBuffer[38] = byte(ist1 >> 16)
	tssBuffer[39] = byte(ist1 >> 24)
	tssBuffer[40] = byte(ist1 >> 32)
	tssBuffer[41] = byte(ist1 >> 40)
	tssBuffer[42] = byte(ist1 >> 48)
	tssBuffer[43] = byte(ist1 >> 56)

	// IST2 at offset 44-51: dedicated stack for double fault (#DF) handler.
	// MUST be separate from IST1 — if #PF and #DF share the same IST, a
	// nested fault during #PF causes the CPU to reload the same stack pointer,
	// clobbering the #PF frame and producing an unrecoverable triple fault.
	ist2 := uint64(uintptr(unsafe.Pointer(&doubleFaultStack[0])) + uintptr(len(doubleFaultStack)))
	tssBuffer[44] = byte(ist2)
	tssBuffer[45] = byte(ist2 >> 8)
	tssBuffer[46] = byte(ist2 >> 16)
	tssBuffer[47] = byte(ist2 >> 24)
	tssBuffer[48] = byte(ist2 >> 32)
	tssBuffer[49] = byte(ist2 >> 40)
	tssBuffer[50] = byte(ist2 >> 48)
	tssBuffer[51] = byte(ist2 >> 56)

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

	// Publish the IST1 half bounds LAST — this arms the IST rotation in
	// exceptions_amd64.s (it gates on ist1Floor != 0). Everything the
	// rotation depends on (tssBuffer IST1 bytes, LTR) is final above.
	// Order matters: write ist1Top before ist1Floor so any exception
	// delivered between the two stores still sees floor==0 and skips.
	ist1Top = ist1
	ist1Floor = rsp0
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
		efer |= eferSCE
		writeMSR(msrEFER, efer)
	}

	lstar := readMSR(msrLSTAR)
	if lstar == 0 {
		entry := getSyscallEntryAddr()
		writeMSR(msrLSTAR, uint64(entry))
	}

	star := readMSR(msrSTAR)
	if star == 0 {
		writeMSR(msrSTAR, syscallCS<<32)
	}

	fmask := readMSR(msrFMASK)
	if fmask == 0 {
		writeMSR(msrFMASK, 0x200)
	}
}
