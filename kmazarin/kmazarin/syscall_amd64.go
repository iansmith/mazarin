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
// MINEFIELD (MAZ-136 rotations): NEITHER stack field in here is a boot-time
// constant. BOTH are GLOBAL nesting cursors rotated by exceptions_amd64.s —
// the IST1 field (bytes 36-43, stride istRotateStride) AND the RSP0 field
// (bytes 4-11, stride rsp0RotateStride). Each is rotated on every exception
// entry (-stride), every normal return (+stride), and every context switch
// made from a handler (+stride, retiring the abandoning handler's level).
// The CPU reads the IST and RSP0 fields from this memory at EVERY delivery
// (LTR caches only the TSS base), which is exactly what makes runtime
// rotation work. Do not cache, snapshot, "fix up", or per-thread-restore
// tssBuffer[4:12] or tssBuffer[36:44] anywhere — a per-thread restore reused
// the shared IST half over another thread's suspended live chain (the
// KVM-run-4 trample); the RSP0 half is shared the same way (parked
// SyscallWaitSoftIRQ chains). All writes go through the assembly rotation
// paths; there is no sanctioned Go-side writer.
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

// rsp0RotateStride is the per-nesting-level reservation the RSP0 rotation
// (the SECOND cursor — see the RSP0 ROTATION banner in exceptions_amd64.s)
// subtracts from TSS.RSP0 on every exception entry. RSP0-half chains run
// DEEPER than IST-half ones: a ring-3 syscall's full Go dispatch (syscall
// table, klog, delegate machinery) executes on this half via GO_CALL — the
// parked SyscallWaitSoftIRQ chain was observed at ~0x410 bytes and live
// dispatch chains grow past that. 8 KiB is ~8× that observation.
//
// ‼ ONE-LEVEL-USE BOUND: a single nesting level on the RSP0 half MUST stay
// under this stride, or its frames can reach below the lowered cursor and
// the next delivery lands inside them anyway. The RSPOVF floor tripwire and
// the IRETQ guard convert violations into diagnosable halts, not silent
// corruption. 14 levels fit the 112 KiB RSP0 half. Exported to assembly via
// go_asm.h as $const_rsp0RotateStride — this Go const is the ONLY definition.
const rsp0RotateStride = 0x2000

// ist1Floor / ist1Ceil bound the IST1 half of the exception stack, published
// by copyGDTToOwnedBuffer AFTER the TSS split is final. The asm rotation
// gates on ist1Floor != 0: before publication every exception skips the
// entry SUB, the return ADD, and the switch-retire ADD, so the arithmetic
// can never go unbalanced across the publication moment. (Single writer,
// straight-line init code on a single CPU: the floor value one exception
// sees at entry is necessarily the value it sees at exit —
// copyGDTToOwnedBuffer cannot run in between, because it IS the interrupted
// code.) ist1Ceil is the cursor's resting-state value (the IST1-half top):
// the ADD sites tripwire-halt if an ADD would raise the cursor ABOVE it —
// an over-retire, i.e. some exit path released a still-live reservation
// (the precursor of an IST-half trample).
var ist1Floor uint64
var ist1Ceil uint64

// IST-rotation accounting counters (diagnostic, incremented from the three
// asm rotation sites). Invariant when healthy:
// istSubCount − istEretAddCount − istLcAddCount == number of LIVE exception
// chains. The kernel-mode unhandled-fault dump (pf_neither_handled) prints
// all three plus the live cursor as "ISTCT ..." so a single over-retire —
// which lands the cursor exactly AT ist1Ceil and is therefore invisible to
// the ISTOVR value tripwire — is attributable to its site after the fact.
var istSubCount uint64
var istEretAddCount uint64
var istLcAddCount uint64

// rsp0Floor / rsp0Ceil bound the RSP0 half of the exception stack — the twin
// of ist1Floor/ist1Ceil for the SECOND rotation cursor, TSS.RSP0 (tssBuffer
// bytes 4-11). Same publication discipline (each ceil before its floor; the
// floor write ARMS the rotation), same single-writer/straight-line-init
// consistency argument as ist1Floor above, same tripwire pair (RSPOVF on a
// floor-crossing entry SUB, RSPOVR on a ceiling-crossing exit ADD).
// rsp0Floor is the bottom of the WHOLE exception stack (the RSP0 half is the
// stack's lower portion); rsp0Ceil is the post-split RSP0-half top — the
// cursor's resting-state value, and numerically the same address as
// ist1Floor (the split boundary between the two halves).
var rsp0Floor uint64
var rsp0Ceil uint64

// RSP0-rotation accounting counters — twins of the IST set above, printed as
// a "RSPCT ..." line next to ISTCT in the kernel-mode unhandled-fault dump.
// Same healthy invariant: rsp0SubCount − rsp0EretAddCount − rsp0LcAddCount
// == number of LIVE exception chains, and the same purpose: a single
// over-retire parks the cursor exactly AT rsp0Ceil, invisible to the RSPOVR
// value tripwire — only this accounting can name the guilty site after the
// fact.
var rsp0SubCount uint64
var rsp0EretAddCount uint64
var rsp0LcAddCount uint64

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
	// stack is 128 KiB; the RSP0 half keeps the remaining 112 KiB, rotated at
	// rsp0RotateStride (8 KiB) per level = 14 levels.)
	//
	// RSP0 at offset 4-11: kernel stack for Ring 3 → Ring 0 transitions.
	// SYSCALL instruction entry lands on the same half via syscallEntry's
	// software switch (which reads these same TSS bytes — the live cursor).
	//
	// MINEFIELD (MAZ-136): like IST1 below, raw RSP0 semantics are a trap —
	// the CPU reloads RSP = TSS.RSP0 on EVERY non-IST ring3→ring0 transition,
	// so a fixed top tramples any chain PARKED live on this half
	// (SyscallWaitSoftIRQ's STI+HLT parks exactly such a chain; run-45 caught
	// the trample as execute-the-stack at RSP0top−0x410). The RSP0 ROTATION
	// in exceptions_amd64.s keeps every delivery below all live frames — the
	// value written here is only the rotation's BASE (= rsp0Ceil).
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

	// Publish the rotation bounds, each FLOOR last — a floor write is what
	// arms its cursor's rotation in exceptions_amd64.s (the SUB/ADD sites
	// gate on floor != 0). Everything the rotations depend on (tssBuffer
	// RSP0/IST1 bytes = the cursors' initial top values, LTR) is final
	// above. An exception arriving between the two floor writes sees a
	// consistent world for its whole lifetime — the writer is THIS
	// straight-line code, suspended until that exception returns (the GATE
	// paragraph of the IST ROTATION banner). excStackBottom (the RSP0
	// floor) was published by InitThreads long before any exception.
	ist1Ceil = ist1
	rsp0Ceil = rsp0
	ist1Floor = rsp0
	rsp0Floor = excStackBottom
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
