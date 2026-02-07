// diplomat/main/dtb_builder.go - Flattened Device Tree binary builder
//
// Writes a valid FDT blob directly into a pre-allocated memory buffer.
// No heap allocation — uses unsafe.Pointer writes. All multi-byte values
// are written in big-endian format per the DTB specification.
//
// Layout in buffer:
//   Offset 0:    FDT Header (40 bytes, big-endian)
//   Offset 40:   Memory reservation map: (0,0) terminator (16 bytes)
//   Offset 56:   Structure block (variable, ~1-3KB)
//   After struct: Strings block (variable, deduplicated)

package main

import "unsafe"

// FDT constants
const (
	fdtMagic     = 0xD00DFEED
	fdtVersion   = 17
	fdtLastComp  = 16
	fdtHeaderLen = 40
	fdtMemRsvLen = 16 // single (0,0) terminator entry

	fdtBeginNode = 0x00000001
	fdtEndNode   = 0x00000002
	fdtProp      = 0x00000003
	fdtEnd       = 0x00000009

	fdtStructStart = fdtHeaderLen + fdtMemRsvLen // 56

	maxStrBufLen  = 512
	maxStrEntries = 32
)

// strEntry maps a property name to its offset in the strings block.
type strEntry struct {
	hash   uint32
	offset uint32
	len    uint16
}

// FDTBuilder writes valid FDT binary into a pre-allocated memory buffer.
type FDTBuilder struct {
	buf       uintptr // Output buffer base address
	bufSize   uint32
	structOff uint32 // Current write position relative to buf

	// String accumulator — property names collected here, deduped
	strBuf   [maxStrBufLen]byte
	strLen   uint32
	strMap   [maxStrEntries]strEntry
	strCount int
}

// Init prepares the builder to write into buf (bufSize bytes).
func (b *FDTBuilder) Init(buf uintptr, bufSize uint32) {
	b.buf = buf
	b.bufSize = bufSize
	b.structOff = fdtStructStart
	b.strLen = 0
	b.strCount = 0

	// Zero the header + memrsv area
	for i := uint32(0); i < fdtStructStart; i++ {
		*(*byte)(unsafe.Pointer(b.buf + uintptr(i))) = 0
	}

	// Write memory reservation map terminator at offset 40
	// Two uint64 zeros — already zeroed above
}

// putBE32 writes a big-endian uint32 at the given buffer offset.
func (b *FDTBuilder) putBE32(off uint32, val uint32) {
	p := b.buf + uintptr(off)
	*(*byte)(unsafe.Pointer(p)) = byte(val >> 24)
	*(*byte)(unsafe.Pointer(p + 1)) = byte(val >> 16)
	*(*byte)(unsafe.Pointer(p + 2)) = byte(val >> 8)
	*(*byte)(unsafe.Pointer(p + 3)) = byte(val)
}

// putBE64 writes a big-endian uint64 at the given buffer offset.
func (b *FDTBuilder) putBE64(off uint32, val uint64) {
	b.putBE32(off, uint32(val>>32))
	b.putBE32(off+4, uint32(val))
}

// writeByte writes a single byte at the current struct offset and advances.
func (b *FDTBuilder) writeByte(v byte) {
	*(*byte)(unsafe.Pointer(b.buf + uintptr(b.structOff))) = v
	b.structOff++
}

// writeU32 writes a big-endian uint32 at the current struct offset and advances.
func (b *FDTBuilder) writeU32(val uint32) {
	b.putBE32(b.structOff, val)
	b.structOff += 4
}

// padTo4 pads the current offset to 4-byte alignment with zero bytes.
func (b *FDTBuilder) padTo4() {
	for b.structOff&3 != 0 {
		b.writeByte(0)
	}
}

// addString returns the offset of name in the strings block, deduplicating.
func (b *FDTBuilder) addString(name string) uint32 {
	h := strHash(name)
	for i := 0; i < b.strCount; i++ {
		if b.strMap[i].hash == h && b.strMap[i].len == uint16(len(name)) {
			// Verify match
			off := b.strMap[i].offset
			match := true
			for j := 0; j < len(name); j++ {
				if b.strBuf[off+uint32(j)] != name[j] {
					match = false
					break
				}
			}
			if match {
				return off
			}
		}
	}

	// New entry
	off := b.strLen
	for i := 0; i < len(name); i++ {
		b.strBuf[off+uint32(i)] = name[i]
	}
	b.strBuf[off+uint32(len(name))] = 0 // null terminator
	b.strLen = off + uint32(len(name)) + 1

	if b.strCount < maxStrEntries {
		b.strMap[b.strCount] = strEntry{hash: h, offset: off, len: uint16(len(name))}
		b.strCount++
	}

	return off
}

// strHash computes a simple hash for deduplication.
func strHash(s string) uint32 {
	var h uint32
	for i := 0; i < len(s); i++ {
		h = h*31 + uint32(s[i])
	}
	return h
}

// BeginNode emits FDT_BEGIN_NODE with the given name.
func (b *FDTBuilder) BeginNode(name string) {
	b.writeU32(fdtBeginNode)
	for i := 0; i < len(name); i++ {
		b.writeByte(name[i])
	}
	b.writeByte(0) // null terminator
	b.padTo4()
}

// BeginNodeAddr emits FDT_BEGIN_NODE with name "prefix@hexaddr".
// Writes the hex address directly — no string concatenation.
func (b *FDTBuilder) BeginNodeAddr(prefix string, addr uint64) {
	b.writeU32(fdtBeginNode)
	for i := 0; i < len(prefix); i++ {
		b.writeByte(prefix[i])
	}
	b.writeByte('@')
	// Write hex digits, skipping leading zeros
	hexChars := "0123456789abcdef"
	started := false
	for shift := 60; shift >= 0; shift -= 4 {
		digit := (addr >> uint(shift)) & 0xF
		if digit != 0 || started || shift == 0 {
			b.writeByte(hexChars[digit])
			started = true
		}
	}
	b.writeByte(0) // null terminator
	b.padTo4()
}

// EndNode emits FDT_END_NODE.
func (b *FDTBuilder) EndNode() {
	b.writeU32(fdtEndNode)
}

// propHeader emits the FDT_PROP token, property length, and name offset.
func (b *FDTBuilder) propHeader(name string, dataLen uint32) {
	b.writeU32(fdtProp)
	b.writeU32(dataLen)
	b.writeU32(b.addString(name))
}

// PropU32 emits a 4-byte property.
func (b *FDTBuilder) PropU32(name string, val uint32) {
	b.propHeader(name, 4)
	b.writeU32(val)
}

// PropU64 emits an 8-byte property.
func (b *FDTBuilder) PropU64(name string, val uint64) {
	b.propHeader(name, 8)
	b.putBE64(b.structOff, val)
	b.structOff += 8
}

// PropString emits a null-terminated string property.
func (b *FDTBuilder) PropString(name string, val string) {
	dataLen := uint32(len(val)) + 1 // include null terminator
	b.propHeader(name, dataLen)
	for i := 0; i < len(val); i++ {
		b.writeByte(val[i])
	}
	b.writeByte(0)
	b.padTo4()
}

// PropStringList emits a property containing null-separated strings.
func (b *FDTBuilder) PropStringList(name string, vals []string) {
	// Calculate total length: each string + null terminator
	var dataLen uint32
	for _, v := range vals {
		dataLen += uint32(len(v)) + 1
	}
	b.propHeader(name, dataLen)
	for _, v := range vals {
		for i := 0; i < len(v); i++ {
			b.writeByte(v[i])
		}
		b.writeByte(0)
	}
	b.padTo4()
}

// PropRegEntry emits a reg property with a single <addrHi addrLo sizeHi sizeLo> entry
// (for #address-cells=2, #size-cells=2).
func (b *FDTBuilder) PropRegEntry(addr, size uint64) {
	b.propHeader("reg", 16) // 4 × uint32
	b.writeU32(uint32(addr >> 32))
	b.writeU32(uint32(addr))
	b.writeU32(uint32(size >> 32))
	b.writeU32(uint32(size))
}

// PropRegMulti emits a reg property with multiple entries.
// Each entry is [addr, size] for #address-cells=2, #size-cells=2.
func (b *FDTBuilder) PropRegMulti(entries [][2]uint64) {
	b.propHeader("reg", uint32(len(entries))*16)
	for _, e := range entries {
		b.writeU32(uint32(e[0] >> 32))
		b.writeU32(uint32(e[0]))
		b.writeU32(uint32(e[1] >> 32))
		b.writeU32(uint32(e[1]))
	}
}

// PropInterrupt emits an interrupts property: <type irqNum flags>.
func (b *FDTBuilder) PropInterrupt(intrType, irqNum, flags uint32) {
	b.propHeader("interrupts", 12)
	b.writeU32(intrType)
	b.writeU32(irqNum)
	b.writeU32(flags)
}

// PropEmpty emits a zero-length property (boolean marker).
func (b *FDTBuilder) PropEmpty(name string) {
	b.propHeader(name, 0)
}

// Finalize writes the FDT_END token, copies the strings block after the
// structure block, fills in the header, and returns the total DTB size.
func (b *FDTBuilder) Finalize() uint32 {
	// Write FDT_END
	b.writeU32(fdtEnd)

	structBlockSize := b.structOff - fdtStructStart

	// Strings block starts right after structure block
	stringsOff := b.structOff

	// Copy strings block
	for i := uint32(0); i < b.strLen; i++ {
		*(*byte)(unsafe.Pointer(b.buf + uintptr(stringsOff+i))) = b.strBuf[i]
	}

	totalSize := stringsOff + b.strLen

	// Fill in the 40-byte header
	b.putBE32(0, fdtMagic)                 // magic
	b.putBE32(4, totalSize)                // totalsize
	b.putBE32(8, fdtStructStart)           // off_dt_struct
	b.putBE32(12, stringsOff)              // off_dt_strings
	b.putBE32(16, fdtHeaderLen)            // off_mem_rsvmap
	b.putBE32(20, fdtVersion)              // version
	b.putBE32(24, fdtLastComp)             // last_comp_version
	b.putBE32(28, 0)                       // boot_cpuid_phys
	b.putBE32(32, b.strLen)                // size_dt_strings
	b.putBE32(36, structBlockSize)         // size_dt_struct

	return totalSize
}
