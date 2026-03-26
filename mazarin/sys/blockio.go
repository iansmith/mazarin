package sys

import (
	"syscall"
	"unsafe"
)

const sysBlockRead = 0x1016

// BlockRead reads disk sectors into buf.
// startLBA is the first sector, numSectors is the count.
// buf must be at least numSectors * 512 bytes.
//
// Only callable by the shepherd that registered for BlockVirtualIRQ.
func BlockRead(startLBA, numSectors uint64, buf []byte) error {
	r1, _, errno := RawSyscall(sysBlockRead,
		uintptr(startLBA),
		uintptr(numSectors),
		uintptr(unsafe.Pointer(&buf[0])),
		0, 0, 0)

	if errno != 0 {
		return errno
	}
	if int64(r1) < 0 {
		return syscall.Errno(-int64(r1))
	}
	return nil
}

// RegisterDMAPool registers a contiguous range of userspace pages for direct
// DMA I/O (zero-copy block reads/writes). After registration, BlockRead calls
// with buffers inside this range will DMA directly into the userspace pages
// instead of copying through kernel buffers.
//
// baseAddr must be page-aligned (4KB). numPages is the number of contiguous
// 4KB pages to register (max 256 = 1MB).
//
// Only callable by the block device owner shepherd.
func RegisterDMAPool(baseAddr uintptr, numPages int) error {
	r1, _, errno := RawSyscall(sysRegisterDMAPool,
		uintptr(baseAddr),
		uintptr(numPages),
		0, 0, 0, 0)

	if errno != 0 {
		return errno
	}
	if int64(r1) < 0 {
		return syscall.Errno(-int64(r1))
	}
	return nil
}

// UnregisterDMAPool unregisters the caller's DMA page pool.
// Unpins pages and decrements their ref counts.
//
// Only callable by the block device owner shepherd.
func UnregisterDMAPool() error {
	r1, _, errno := RawSyscall(sysUnregisterDMAPool,
		0, 0, 0, 0, 0, 0)

	if errno != 0 {
		return errno
	}
	if int64(r1) < 0 {
		return syscall.Errno(-int64(r1))
	}
	return nil
}

// BlockSubmit submits an async block I/O request. Returns immediately with
// an IOTag identifying the request. The completion is delivered as an event
// on the block device's soft IRQ slot (via WaitSoftIRQ).
//
// requestType: 0 = read, 1 = write
// startLBA: first sector to read/write
// numSectors: number of sectors (must fit in one DMA pool page)
// buf: buffer in the registered DMA pool (data goes directly to/from this buffer)
//
// Returns the IOTag (>= 0) on success, or an error.
// The completion event has: Type=IOTag, Code=status (0=ok), Value=usedLen.
//
// Only callable by the block device owner shepherd with a registered DMA pool.
func BlockSubmit(requestType uint32, startLBA uint64, numSectors uint64, buf []byte) (uint16, error) {
	r1, _, errno := RawSyscall(sysBlockSubmit,
		uintptr(requestType),
		uintptr(startLBA),
		uintptr(numSectors),
		uintptr(unsafe.Pointer(&buf[0])),
		0, 0)

	if errno != 0 {
		return 0xFFFF, errno
	}
	ret := int64(r1)
	if ret < 0 {
		return 0xFFFF, syscall.Errno(-ret)
	}
	return uint16(ret), nil
}
