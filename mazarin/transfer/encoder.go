package transfer

import "encoding/binary"

// Encoder is the length-prefix encoder for IPC small fields — the part of
// an IPC message that fits in a single uring SQE. Blob bodies use the
// transfer modes (Reserve/Commit here, HandOff and GrantWrite in MAZ-53/54).
//
// Encoding format (little-endian throughout):
//
//	String:      [u32 length][raw bytes]
//	AppendBytes: [u32 length][raw bytes]
//	Uint32:      [u32 value]
//	Handle:      [u64 VA][u32 Pages]
//
// The encoder appends to an internal buffer. Pass nil to NewEncoder for a
// fresh buffer; pass an existing slice to extend it in place.
type Encoder struct {
	buf []byte
}

// NewEncoder wraps an externally-provided buf for encoding.
// Pass nil for a fresh buffer.
func NewEncoder(buf []byte) *Encoder {
	return &Encoder{buf: buf}
}

// Bytes returns the encoded bytes accumulated so far. The slice aliases the
// encoder's internal buffer; do not mutate it while the encoder is still
// being used.
func (e *Encoder) Bytes() []byte {
	return e.buf
}

// String appends a length-prefixed string.
func (e *Encoder) String(s string) {
	e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(len(s)))
	e.buf = append(e.buf, s...)
}

// AppendBytes appends a length-prefixed byte sequence. (Named AppendBytes
// because the bare Bytes name is the accessor returning the encoded buffer.)
func (e *Encoder) AppendBytes(b []byte) {
	e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(len(b)))
	e.buf = append(e.buf, b...)
}

// Uint32 appends a fixed-width uint32.
func (e *Encoder) Uint32(v uint32) {
	e.buf = binary.LittleEndian.AppendUint32(e.buf, v)
}

// Handle appends a serialized blob reference (the Handle's VA + Pages,
// not the bytes the handle points at).
func (e *Encoder) Handle(h Handle) {
	e.buf = binary.LittleEndian.AppendUint64(e.buf, uint64(h.VA))
	e.buf = binary.LittleEndian.AppendUint32(e.buf, uint32(h.Pages))
}
