package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// SyscallGetOwnExports serializes the calling shepherd's SymbolTable
// (built during launch from its own ELF .symtab — see
// kmazarin/ksyscall/launch.go:buildSymbolTable) into a user-supplied
// buffer. This is what mazdl.RegisterHost uses on Mazarin to populate
// globalSyms, replacing the Linux /proc/self/maps + debug/elf path.
//
// arg0: bufPtr — user buffer start, or 0 to just query size
// arg1: bufLen — buffer size in bytes
//
// Returns: bytes of serialized data needed for the full table.
//   - If bufLen >= needed: copies everything, returns needed.
//   - If bufLen < needed:  writes nothing, returns needed so the caller
//                          can resize and retry.
//   - On hard error (no shepherd, table empty, copy fault): negative errno.
//
// Wire format (packed, little-endian, no padding):
//
//	[u32 numSyms]
//	for each sym:
//	  [u64 addr]
//	  [u16 nameLen]
//	  [nameLen bytes name]
//
// Addresses are absolute VAs (shepherds are ET_EXEC at fixed addresses,
// no load-base math needed).
func SyscallGetOwnExports(bufPtr, bufLen, _, _, _, _ uint64) int64 {
	p := proc.CurrentShepherd()
	if p == nil {
		return -22 // EINVAL — no shepherd context (kernel thread?)
	}
	if p.SymbolTable == nil {
		// Empty table is valid — return 4 bytes (just the count=0 header).
		if bufLen >= 4 && bufPtr != 0 {
			var hdr [4]byte
			if !kmem.CopyToUser(uintptr(bufPtr), hdr[:]) {
				return -14 // EFAULT
			}
		}
		return 4
	}

	// First pass: compute exact size.
	needed := uint64(4) // numSyms header
	for name := range p.SymbolTable {
		needed += 8 + 2 + uint64(len(name))
	}

	if bufPtr == 0 || bufLen < needed {
		return int64(needed)
	}

	// Second pass: serialize into a kernel-local buffer, then CopyToUser
	// in one go. Using a single copy keeps the partial-fault story simple.
	buf := make([]byte, needed)
	putU32(buf[0:4], uint32(len(p.SymbolTable)))
	off := 4
	for name, addr := range p.SymbolTable {
		putU64(buf[off:off+8], addr)
		off += 8
		putU16(buf[off:off+2], uint16(len(name)))
		off += 2
		copy(buf[off:off+len(name)], name)
		off += len(name)
	}

	if !kmem.CopyToUser(uintptr(bufPtr), buf) {
		// Touch first and last bytes to demand-fault the range, then retry.
		if kmem.HandleUserPageFault(uintptr(bufPtr), 0) &&
			kmem.HandleUserPageFault(uintptr(bufPtr)+uintptr(needed-1), 0) {
			if !kmem.CopyToUser(uintptr(bufPtr), buf) {
				return -14 // EFAULT
			}
		} else {
			return -14 // EFAULT
		}
	}
	return int64(needed)
}

func putU16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func putU32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putU64(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
	b[5] = byte(v >> 40)
	b[6] = byte(v >> 48)
	b[7] = byte(v >> 56)
}

