package mem

import (
	"mazzy/shared/constants"
	"syscall"
	"unsafe"
)

const (
	// mapAnonymous is MAP_ANONYMOUS (0x20) — defined here because
	// syscall.MAP_ANONYMOUS only exists on Linux (macOS uses MAP_ANON).
	mapAnonymous = 0x20
)

// Mmap wraps the mmap syscall. addr=0 lets the kernel choose the VA.
// prot and flags follow Linux conventions; the MAZARIN_CONTIGUOUS flag
// can be OR'd into flags to request physically contiguous DMA pages.
func Mmap(addr uintptr, length, prot, flags int) (uintptr, error) {
	r1, _, errno := syscall.RawSyscall6(
		syscall.SYS_MMAP,
		addr,
		uintptr(length),
		uintptr(prot),
		uintptr(flags),
		^uintptr(0), // fd = -1
		0,           // offset = 0
	)
	if errno != 0 {
		return 0, errno
	}
	if int64(r1) < 0 {
		return 0, syscall.Errno(-int64(r1))
	}
	return r1, nil
}

// Munmap releases pages mapped by Mmap. If the range overlaps a
// MAZARIN_CONTIGUOUS clump, the kernel handles deferred release when
// I/O is still in flight.
func Munmap(addr uintptr, length int) error {
	r1, _, errno := syscall.RawSyscall6(
		syscall.SYS_MUNMAP,
		addr,
		uintptr(length),
		0, 0, 0, 0,
	)
	if errno != 0 {
		return errno
	}
	if int64(r1) < 0 {
		return syscall.Errno(-int64(r1))
	}
	return nil
}

// ContiguousBuffer holds the result of AllocContiguous: a DMA-capable
// buffer backed by physically contiguous pages.
type ContiguousBuffer struct {
	Addr uintptr // VA of the buffer
	Buf  []byte  // slice over the entire buffer
	Size int     // byte size (may be rounded up to page boundary)
}

// AllocContiguous allocates physically contiguous pages suitable for DMA.
// size is rounded up to a page boundary. The returned buffer can be passed
// directly to BlockSubmit.
func AllocContiguous(size int) (*ContiguousBuffer, error) {
	pageSize := 4096
	aligned := (size + pageSize - 1) &^ (pageSize - 1)

	flags := syscall.MAP_PRIVATE | mapAnonymous | constants.MAZARIN_CONTIGUOUS
	prot := syscall.PROT_READ | syscall.PROT_WRITE

	va, err := Mmap(0, aligned, prot, flags)
	if err != nil {
		return nil, err
	}

	return &ContiguousBuffer{
		Addr: va,
		Buf:  unsafe.Slice((*byte)(unsafe.Pointer(va)), aligned),
		Size: aligned,
	}, nil
}

// Free releases a ContiguousBuffer allocated by AllocContiguous.
func (cb *ContiguousBuffer) Free() error {
	return Munmap(cb.Addr, cb.Size)
}
