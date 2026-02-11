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

	// Calculate offset from bootstrap (0x80000000) to entry point
	// JAL offset is in bytes, stored as signed 21-bit immediate (20-bit after dropping LSB)
	bootstrapAddr := uint64(0x80000000)
	offset := int64(entryAddr) - int64(bootstrapAddr)

	// JAL encoding: offset is split across instruction bits
	// offset[20|10:1|11|19:12] rd opcode
	// opcode = 0x6f (JAL)
	// rd = 0 (zero register, discard return address)

	if offset < -1048576 || offset > 1048575 {
		return fmt.Errorf("entry point too far for JAL: offset=%d bytes (max ±1MB)", offset)
	}

	// Encode JAL immediate field
	immBits := uint64(offset & 0x1FFFFF) // 21 bits (but bit 0 must be 0)
	jal := uint64(0x6f) | // opcode
		((immBits & 0x100000) << 11) |  // bit[20] -> bit[31]
		((immBits & 0x7FE) << 20) |     // bits[10:1] -> bits[30:21]
		((immBits & 0x800) << 9) |      // bit[11] -> bit[20]
		((immBits & 0xFF000))            // bits[19:12] -> bits[19:12]

	stubOffset := 0x1000
	stubSize := 4
	if stubOffset+stubSize > len(data) {
		return fmt.Errorf("file too small to inject bootstrap stub")
	}

	fmt.Printf("Injecting RISC-V bootstrap stub at file offset 0x%x:\n", stubOffset)
	fmt.Printf("  Bootstrap address: 0x%08x\n", bootstrapAddr)
	fmt.Printf("  Entry point:       0x%08x\n", entryAddr)
	fmt.Printf("  JAL offset:        %d bytes (0x%x)\n", offset, offset)

	// Single JAL instruction to jump to entry point
	order.PutUint32(data[stubOffset:], uint32(jal))

	return nil
}
