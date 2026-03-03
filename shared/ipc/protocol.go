// Package ipc defines the IPC message protocol used between priests.
// All IPC messages start with an IPCHeader, followed by opcode-specific payload.
package ipc

// IPCHeader is the header for all IPC messages.
// Total size: 24 bytes, followed by payload.
type IPCHeader struct {
	Opcode     uint32 // Operation code (see filesystem opcodes below)
	Flags      uint32 // Opcode-specific flags
	PayloadLen uint64 // Bytes of payload after this header
	ErrorCode  int64  // Reply only: 0 = success, negative = errno
}

// HeaderSize is the byte size of IPCHeader.
const HeaderSize = 24

// Filesystem opcodes
const (
	OpFSRead uint32 = 1 // Read a file: payload = null-terminated path
	OpFSStat uint32 = 2 // Stat a file: payload = null-terminated path
	OpFSList uint32 = 3 // List directory: payload = null-terminated path
)

// MarshalHeader writes an IPCHeader to a byte slice (little-endian).
func MarshalHeader(buf []byte, h *IPCHeader) {
	if len(buf) < HeaderSize {
		return
	}
	putU32LE(buf[0:4], h.Opcode)
	putU32LE(buf[4:8], h.Flags)
	putU64LE(buf[8:16], h.PayloadLen)
	putI64LE(buf[16:24], h.ErrorCode)
}

// UnmarshalHeader reads an IPCHeader from a byte slice (little-endian).
func UnmarshalHeader(buf []byte) IPCHeader {
	if len(buf) < HeaderSize {
		return IPCHeader{}
	}
	return IPCHeader{
		Opcode:     getU32LE(buf[0:4]),
		Flags:      getU32LE(buf[4:8]),
		PayloadLen: getU64LE(buf[8:16]),
		ErrorCode:  getI64LE(buf[16:24]),
	}
}

func putU32LE(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putU64LE(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
	b[5] = byte(v >> 40)
	b[6] = byte(v >> 48)
	b[7] = byte(v >> 56)
}

func putI64LE(b []byte, v int64) {
	putU64LE(b, uint64(v))
}

func getU32LE(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func getU64LE(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

func getI64LE(b []byte) int64 {
	return int64(getU64LE(b))
}
