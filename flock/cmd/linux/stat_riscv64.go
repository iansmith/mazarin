//go:build riscv64

package main

import "encoding/binary"

// fillStdioStatBuf writes a minimal stat struct for stdin/stdout/stderr.
// RISC-V layout: same as ARM64, st_mode at offset 16 (uint32).
func fillStdioStatBuf(buf []byte) {
	for i := range buf[:128] {
		buf[i] = 0
	}
	le := binary.LittleEndian
	le.PutUint32(buf[16:], 0020666) // S_IFCHR | 0666
}
