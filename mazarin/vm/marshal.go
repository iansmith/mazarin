// marshal.go — Program serialization for storage in shared page bytecode region.
//
// Format:
//   Header (12 bytes):
//     [4]byte magic "MZBC"
//     uint16  numInstructions
//     uint16  numStrings
//     uint16  numFuncs
//     uint16  entry
//   Instructions: numInstructions × 16 bytes
//   String table: numStrings × (uint16 len + bytes)
//   Func table: numFuncs × FuncInfo (serialized)

package vm

import (
	"encoding/binary"
	"errors"
)

// Bytecode magic bytes.
var bytecodeMagic = [4]byte{'M', 'Z', 'B', 'C'}

const (
	marshalHeaderSize = 12 // 4 magic + 2 numInst + 2 numStr + 2 numFunc + 2 entry
	marshalInstSize   = 16
)

// Marshal serializes a Program to a byte slice for storage in the bytecode region.
func (p *Program) Marshal() []byte {
	// Calculate total size.
	size := marshalHeaderSize
	size += len(p.Code) * marshalInstSize

	// String table: for each string, uint16 len + bytes.
	for _, s := range p.Strings {
		size += 2 + len(s)
	}

	// Func table: for each func, 2+2+2+2 = 8 bytes (PC, NumArgs, NumLocals, LocalBase)
	// plus uint16 nameLen + name bytes.
	for _, f := range p.Funcs {
		size += 8 + 2 + len(f.Name)
	}

	buf := make([]byte, size)
	off := 0

	// Header.
	copy(buf[off:off+4], bytecodeMagic[:])
	off += 4
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(p.Code)))
	off += 2
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(p.Strings)))
	off += 2
	binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(p.Funcs)))
	off += 2
	binary.LittleEndian.PutUint16(buf[off:off+2], p.Entry)
	off += 2

	// Instructions.
	for _, inst := range p.Code {
		buf[off] = inst.Opcode
		buf[off+1] = inst.Typ
		binary.LittleEndian.PutUint16(buf[off+2:off+4], inst.Op1)
		binary.LittleEndian.PutUint16(buf[off+4:off+6], inst.Op2)
		binary.LittleEndian.PutUint16(buf[off+6:off+8], inst.Flags)
		binary.LittleEndian.PutUint64(buf[off+8:off+16], inst.Imm)
		off += marshalInstSize
	}

	// String table.
	for _, s := range p.Strings {
		binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(s)))
		off += 2
		copy(buf[off:off+len(s)], s)
		off += len(s)
	}

	// Func table.
	for _, f := range p.Funcs {
		binary.LittleEndian.PutUint16(buf[off:off+2], f.PC)
		off += 2
		binary.LittleEndian.PutUint16(buf[off:off+2], f.NumArgs)
		off += 2
		binary.LittleEndian.PutUint16(buf[off:off+2], f.NumLocals)
		off += 2
		binary.LittleEndian.PutUint16(buf[off:off+2], f.LocalBase)
		off += 2
		binary.LittleEndian.PutUint16(buf[off:off+2], uint16(len(f.Name)))
		off += 2
		copy(buf[off:off+len(f.Name)], f.Name)
		off += len(f.Name)
	}

	return buf
}

// UnmarshalProgram deserializes a Program from a byte slice.
func UnmarshalProgram(data []byte) (*Program, error) {
	if len(data) < marshalHeaderSize {
		return nil, errors.New("vm: bytecode too short for header")
	}

	// Check magic.
	if data[0] != bytecodeMagic[0] || data[1] != bytecodeMagic[1] ||
		data[2] != bytecodeMagic[2] || data[3] != bytecodeMagic[3] {
		return nil, errors.New("vm: invalid bytecode magic")
	}

	off := 4
	numInst := int(binary.LittleEndian.Uint16(data[off : off+2]))
	off += 2
	numStr := int(binary.LittleEndian.Uint16(data[off : off+2]))
	off += 2
	numFunc := int(binary.LittleEndian.Uint16(data[off : off+2]))
	off += 2
	entry := binary.LittleEndian.Uint16(data[off : off+2])
	off += 2

	// Instructions.
	instBytes := numInst * marshalInstSize
	if off+instBytes > len(data) {
		return nil, errors.New("vm: bytecode truncated in instruction section")
	}
	code := make([]Inst, numInst)
	for i := 0; i < numInst; i++ {
		base := off + i*marshalInstSize
		code[i] = Inst{
			Opcode: data[base],
			Typ:    data[base+1],
			Op1:    binary.LittleEndian.Uint16(data[base+2 : base+4]),
			Op2:    binary.LittleEndian.Uint16(data[base+4 : base+6]),
			Flags:  binary.LittleEndian.Uint16(data[base+6 : base+8]),
			Imm:    binary.LittleEndian.Uint64(data[base+8 : base+16]),
		}
	}
	off += instBytes

	// String table.
	strings := make([]string, numStr)
	for i := 0; i < numStr; i++ {
		if off+2 > len(data) {
			return nil, errors.New("vm: bytecode truncated in string table")
		}
		slen := int(binary.LittleEndian.Uint16(data[off : off+2]))
		off += 2
		if off+slen > len(data) {
			return nil, errors.New("vm: bytecode truncated in string data")
		}
		strings[i] = string(data[off : off+slen])
		off += slen
	}

	// Func table.
	var funcs []FuncInfo
	if numFunc > 0 {
		funcs = make([]FuncInfo, numFunc)
		for i := 0; i < numFunc; i++ {
			if off+10 > len(data) {
				return nil, errors.New("vm: bytecode truncated in func table")
			}
			funcs[i].PC = binary.LittleEndian.Uint16(data[off : off+2])
			off += 2
			funcs[i].NumArgs = binary.LittleEndian.Uint16(data[off : off+2])
			off += 2
			funcs[i].NumLocals = binary.LittleEndian.Uint16(data[off : off+2])
			off += 2
			funcs[i].LocalBase = binary.LittleEndian.Uint16(data[off : off+2])
			off += 2
			nlen := int(binary.LittleEndian.Uint16(data[off : off+2]))
			off += 2
			if off+nlen > len(data) {
				return nil, errors.New("vm: bytecode truncated in func name")
			}
			funcs[i].Name = string(data[off : off+nlen])
			off += nlen
		}
	}

	return &Program{
		Code:    code,
		Strings: strings,
		Funcs:   funcs,
		Entry:   entry,
	}, nil
}


