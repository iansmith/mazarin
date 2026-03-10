package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"unsafe"
)

// patchJ_RISCV patches a function body to trampoline to targetAddr using
// AUIPC+JALR(x0). Same encoding as patchJAL_RISCV but the funcVA IS the
// function to patch (no call-site decoding needed). Used for morestack.
func patchJ_RISCV(funcVA, targetAddr uint64, l0PA uintptr) {
	offset := int64(targetAddr) - int64(funcVA)
	if offset < -(1<<31) || offset >= (1<<31) {
		return
	}

	off32 := uint32(int32(offset))
	hi20 := ((off32 + 0x800) >> 12) & 0xFFFFF
	lo12 := off32 & 0xFFF

	// AUIPC t1(x6), hi20
	auipc := (hi20 << 12) | (6 << 7) | 0x17
	// JALR x0, lo12(t1) — tail call (preserves ra)
	jalr := (lo12 << 20) | (6 << 15) | 0x67

	writeU32ToUser(uintptr(funcVA), auipc, l0PA)
	writeU32ToUser(uintptr(funcVA+4), jalr, l0PA)
}

// patchJAL_RISCV patches the thin stub targeted by a JAL call site to
// trampoline to the host's function using AUIPC+JALR (±2GB range).
//
// RISC-V JAL has only ±1MB range, which is insufficient when the .mzr
// module (at 0x30000000) needs to call the priest's runtime functions
// at distant addresses. Instead of patching the 4-byte call-site JAL
// (which can't encode large offsets), we decode the JAL to find the
// stub address and replace the 8-byte stub body (NOP + J .-4) with
// AUIPC t1,hi20 + JALR x0,lo12(t1) — a tail-call trampoline that
// preserves ra so the host function returns directly to the .mzr caller.
func patchJAL_RISCV(instrVA, targetAddr uint64, l0PA uintptr) {
	// Read the JAL instruction at the call site to decode the stub address.
	pa := kmem.WalkUserPageTableWithL0(uintptr(instrVA), l0PA)
	if pa == 0 {
		return
	}
	kva := kmem.MapPAToKernelScratch(pa)
	if kva == 0 {
		return
	}
	insn := *(*uint32)(unsafe.Pointer(kva))

	// Verify this is a JAL instruction (opcode bits [6:0] = 1101111).
	if insn&0x7F != 0x6F {
		return
	}

	// Decode J-immediate: imm[20|10:1|11|19:12]
	imm20 := (insn >> 31) & 1
	imm10_1 := (insn >> 21) & 0x3FF
	imm11 := (insn >> 20) & 1
	imm19_12 := (insn >> 12) & 0xFF
	rawImm := (imm20 << 20) | (imm19_12 << 12) | (imm11 << 11) | (imm10_1 << 1)
	if imm20 != 0 {
		rawImm |= 0xFFE00000 // sign extend from bit 20
	}

	stubVA := uint64(int64(instrVA) + int64(int32(rawImm)))

	// Calculate offset from stub PC to host function.
	offset := int64(targetAddr) - int64(stubVA)

	// AUIPC+JALR range check: ±2GB.
	if offset < -(1<<31) || offset >= (1<<31) {
		return
	}

	// Split offset into hi20 + sign_ext(lo12).
	// The +0x800 compensates for JALR's sign extension of the low 12 bits.
	off32 := uint32(int32(offset))
	hi20 := ((off32 + 0x800) >> 12) & 0xFFFFF
	lo12 := off32 & 0xFFF

	// AUIPC t1(x6), hi20: t1 = stub_PC + (hi20 << 12)
	auipc := (hi20 << 12) | (6 << 7) | 0x17
	// JALR x0, lo12(t1): jump to t1 + sign_ext(lo12), rd=x0 preserves ra
	jalr := (lo12 << 20) | (6 << 15) | 0x67

	// Replace the stub body (was NOP + J .-4) with the trampoline.
	writeU32ToUser(uintptr(stubVA), auipc, l0PA)
	writeU32ToUser(uintptr(stubVA+4), jalr, l0PA)
}
