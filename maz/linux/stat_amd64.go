//go:build amd64

package main

import "encoding/binary"

// fillStdioStatBuf writes a minimal stat struct for stdin/stdout/stderr.
// x86_64 layout: st_mode at offset 24 (uint32), after st_nlink (uint64 at offset 16).
func fillStdioStatBuf(buf []byte) {
	for i := range buf[:128] {
		buf[i] = 0
	}
	le := binary.LittleEndian
	le.PutUint32(buf[24:], 0020666) // S_IFCHR | 0666
}
