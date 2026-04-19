package sys

import (
	"errors"
	"mazzy/shared/mazzy"
	"syscall"
	"unsafe"
)

// OwnExport is one entry in the caller's own symbol-export table.
type OwnExport struct {
	Name string
	Addr uintptr
}

// GetOwnExports returns the caller shepherd's exported symbol table,
// serialized by kernel SysGetOwnExports (see kmazarin/ksyscall/getownexports.go
// for the wire format). Used by mazdl.RegisterHost on Mazarin to
// populate the global symbol table for plugin UNDEF resolution.
//
// Two-call pattern: first call with a nil buf to learn the size, then
// again with a sized buf. Any kernel error is surfaced as a non-nil
// error; an empty table returns an empty slice and nil error.
func GetOwnExports() ([]OwnExport, error) {
	r1, _, errno := syscall.RawSyscall6(
		mazzy.SysGetOwnExports,
		0, 0, 0, 0, 0, 0,
	)
	if errno != 0 || int64(r1) < 0 {
		return nil, errors.New("GetOwnExports: sizing call failed")
	}
	needed := uint64(r1)
	if needed < 4 {
		return nil, errors.New("GetOwnExports: impossibly small size")
	}

	buf := make([]byte, needed)
	// Touch every page so demand faults fire before CopyToUser writes.
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 0
	}
	if len(buf) > 0 {
		buf[len(buf)-1] = 0
	}

	r1, _, errno = syscall.RawSyscall6(
		mazzy.SysGetOwnExports,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, 0, 0, 0,
	)
	if errno != 0 || int64(r1) < 0 {
		return nil, errors.New("GetOwnExports: fill call failed")
	}
	if uint64(r1) > needed {
		// Symbol table grew between sizing and fill — bail rather than
		// return a truncated view. Caller can retry.
		return nil, errors.New("GetOwnExports: table size raced")
	}

	n := readU32(buf[0:4])
	out := make([]OwnExport, 0, n)
	off := 4
	for i := uint32(0); i < n; i++ {
		if off+10 > len(buf) {
			return nil, errors.New("GetOwnExports: truncated entry header")
		}
		addr := readU64(buf[off : off+8])
		off += 8
		nameLen := int(readU16(buf[off : off+2]))
		off += 2
		if off+nameLen > len(buf) {
			return nil, errors.New("GetOwnExports: truncated entry name")
		}
		out = append(out, OwnExport{
			Name: string(buf[off : off+nameLen]),
			Addr: uintptr(addr),
		})
		off += nameLen
	}
	return out, nil
}

func readU16(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

func readU32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func readU64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}
