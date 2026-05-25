package transfer

import "encoding/binary"

// Decoder is the matching decoder for Encoder's length-prefix format.
// Each read advances an internal position; short reads return ErrTruncated.
type Decoder struct {
	buf []byte
	pos int
}

// NewDecoder wraps buf for decoding.
func NewDecoder(buf []byte) *Decoder {
	return &Decoder{buf: buf}
}

// remaining returns the number of unread bytes.
func (d *Decoder) remaining() int {
	return len(d.buf) - d.pos
}

// String decodes a length-prefixed string.
func (d *Decoder) String() (string, error) {
	if d.remaining() < 4 {
		return "", ErrTruncated
	}
	n := int(binary.LittleEndian.Uint32(d.buf[d.pos:]))
	d.pos += 4
	if d.remaining() < n {
		return "", ErrTruncated
	}
	s := string(d.buf[d.pos : d.pos+n])
	d.pos += n
	return s, nil
}

// Bytes decodes a length-prefixed byte sequence. The returned slice aliases
// the decoder's buffer; copy if a longer lifetime is needed.
func (d *Decoder) Bytes() ([]byte, error) {
	if d.remaining() < 4 {
		return nil, ErrTruncated
	}
	n := int(binary.LittleEndian.Uint32(d.buf[d.pos:]))
	d.pos += 4
	if d.remaining() < n {
		return nil, ErrTruncated
	}
	b := d.buf[d.pos : d.pos+n]
	d.pos += n
	return b, nil
}

// Uint32 decodes a fixed-width uint32.
func (d *Decoder) Uint32() (uint32, error) {
	if d.remaining() < 4 {
		return 0, ErrTruncated
	}
	v := binary.LittleEndian.Uint32(d.buf[d.pos:])
	d.pos += 4
	return v, nil
}

// Handle decodes a serialized blob reference (VA + Pages, not the bytes).
func (d *Decoder) Handle() (Handle, error) {
	if d.remaining() < 12 {
		return Handle{}, ErrTruncated
	}
	va := binary.LittleEndian.Uint64(d.buf[d.pos:])
	pages := binary.LittleEndian.Uint32(d.buf[d.pos+8:])
	d.pos += 12
	return Handle{VA: uintptr(va), Pages: int(pages)}, nil
}
