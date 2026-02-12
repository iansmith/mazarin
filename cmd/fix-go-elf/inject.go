package main

import (
	"encoding/binary"
	"fmt"
)

// injectBootstrapStub injects a tiny bootstrap trampoline at the beginning of
// the .text section that jumps to the actual entry point.
//
// For RISC-V, OpenSBI jumps to the load address (first PT_LOAD segment), not
// the ELF entry point. Go's linker places other code before our entry point,
// so we inject a stub that loads the entry address and jumps to it.
//
// The stub overwrites whatever code is at offset 0x1000 (typically internal/abi.Kind.String),
// but that's fine because the runtime linked into diplomat is never used.
func injectBootstrapStub(data []byte, order binary.ByteOrder, entryAddr uint64) error {
	// RISC-V bootstrap stub (24 bytes):
	//   lui  t1, 0x10000           ; UART base address upper
	//   addi t2, zero, '!'         ; Character '!'
	//   sb   t2, 0(t1)             ; Write '!' to UART (proof of bootstrap)
	//   lui  t0, %hi(entryAddr)    ; Load entry address upper
	//   addi t0, t0, %lo(entryAddr); Add entry address lower
	//   jalr zero, t0, 0           ; Jump to entry point

	// Calculate upper and lower parts of address
	// Note: ADDI sign-extends the 12-bit immediate, so if lower[11] is set,
	// we need to add 1 to upper to compensate.
	lower := entryAddr & 0xFFF
	upper := (entryAddr >> 12) & 0xFFFFF
	if lower >= 0x800 {
		upper += 1
	}

	// Encode RISC-V instructions (little-endian)
	// SOLUTION: Use JAL to jump directly to trampoline (_rt0_riscv64_linux)
	// JALR doesn't work in this environment (S-mode restriction or PMP issue)
	// but JAL works and has ±1MB range, which is sufficient

	// For entry points beyond JAL range (±1MB), use AUIPC + JALR
	// This loads the full 32-bit PC-relative address and jumps to it
	//
	// AUIPC t0, %pcrel_hi(entry)   ; Load upper 20 bits of PC-relative offset
	// JALR zero, t0, %pcrel_lo(entry) ; Add lower 12 bits and jump
	//
	// Note: We calculate PC-relative from file offset 0x1000, which maps to
	// the first PT_LOAD segment vaddr after fixing

	stubOffset := 0x1000
	stubSize := 16 // 4 instructions: 2 for preserving A0/A1, 2 for jump
	if stubOffset+stubSize > len(data) {
		return fmt.Errorf("file too small to inject bootstrap stub")
	}

	// Calculate PC-relative offset from stub to entry point
	// After ELF fixing, file offset 0x1000 will map to the first PT_LOAD vaddr
	// We need to find that vaddr to calculate the offset
	var stubVaddr uint64
	e_phoff := order.Uint64(data[32:40])
	e_phentsize := order.Uint16(data[54:56])
	e_phnum := order.Uint16(data[56:58])

	for i := uint16(0); i < e_phnum; i++ {
		phOffset := e_phoff + uint64(i)*uint64(e_phentsize)
		if phOffset+56 > uint64(len(data)) {
			continue
		}
		p_type := order.Uint32(data[phOffset : phOffset+4])
		p_vaddr := order.Uint64(data[phOffset+16 : phOffset+24])
		if p_type == 1 { // PT_LOAD
			stubVaddr = p_vaddr
			break
		}
	}

	if stubVaddr == 0 {
		return fmt.Errorf("could not find PT_LOAD segment for bootstrap")
	}

	offset := int64(entryAddr) - int64(stubVaddr)

	fmt.Printf("Injecting RISC-V bootstrap stub at file offset 0x%x:\n", stubOffset)
	fmt.Printf("  Bootstrap vaddr:   0x%08x\n", stubVaddr)
	fmt.Printf("  Entry point:       0x%08x\n", entryAddr)
	fmt.Printf("  PC-relative offset: %d bytes (0x%x)\n", offset, offset)

	// Split offset into upper 20 bits and lower 12 bits
	// Note: ADDI sign-extends the 12-bit immediate, so if bit 11 is set, we add 1 to upper
	lower = uint64(offset & 0xFFF)
	upper = uint64((offset >> 12) & 0xFFFFF)
	if lower >= 0x800 {
		upper += 1
	}

	// Preserve A0/A1 (OpenSBI firmware parameters) to S0/S1 (callee-saved)
	// This is CRITICAL - the trampoline expects these values!
	//
	// MOV S0, A0  is encoded as: ADDI S0, A0, 0
	// Encoding: imm[11:0]=0 | rs1[19:15]=10 (A0) | 000 | rd[11:7]=8 (S0) | 0010011
	movS0A0 := uint32((0 << 20) | (10 << 15) | (8 << 7) | 0x13)

	// MOV S1, A1  is encoded as: ADDI S1, A1, 0
	// Encoding: imm[11:0]=0 | rs1[19:15]=11 (A1) | 000 | rd[11:7]=9 (S1) | 0010011
	movS1A1 := uint32((0 << 20) | (11 << 15) | (9 << 7) | 0x13)

	// AUIPC t0, upper   (0x17 = AUIPC, rd=5 for t0)
	// Encoding: imm[31:12] | rd[11:7] | 0010111
	auipc := uint32((upper << 12) | (5 << 7) | 0x17)

	// JALR zero, t0, lower  (0x67 = JALR, rd=0, rs1=5, funct3=000)
	// Encoding: imm[11:0] | rs1[19:15] | 000 | rd[11:7] | 1100111
	jalr := uint32(((lower & 0xFFF) << 20) | (5 << 15) | 0x67)

	order.PutUint32(data[stubOffset:], movS0A0)
	order.PutUint32(data[stubOffset+4:], movS1A1)
	order.PutUint32(data[stubOffset+8:], auipc)
	order.PutUint32(data[stubOffset+12:], jalr)

	return nil
}
