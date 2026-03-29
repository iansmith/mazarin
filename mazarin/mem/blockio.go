package mem

import (
	"mazzy/shared/mazzy"
	"syscall"
	"unsafe"
)

// BlockSubmit submits an async block I/O request. Returns immediately with
// an IOTag identifying the request. The completion is delivered as an event
// on the block device's soft IRQ slot (via WaitSoftIRQ).
//
// requestType: 0 = read, 1 = write
// startLBA: first sector to read/write
// numSectors: number of sectors (must fit in one page = 4096 bytes)
// buf: buffer in a MAZARIN_CONTIGUOUS DMA clump (data goes directly to/from this buffer)
// targetSID: shepherd that owns the DMA clump containing buf.
//
//	Pass 0 to use the caller's own clumps.
//
// Returns the IOTag (>= 0) on success, or an error.
// The completion event has: Type=IOTag, Code=status (0=ok), Value=usedLen.
//
// Only callable by the block device owner shepherd.
func BlockSubmit(requestType uint32, startLBA, numSectors uint64, buf []byte, targetSID int) (uint16, error) {
	r1, _, errno := syscall.RawSyscall6(mazzy.SysBlockSubmit,
		uintptr(requestType),
		uintptr(startLBA),
		uintptr(numSectors),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(targetSID),
		0)

	if errno != 0 {
		return 0xFFFF, errno
	}
	ret := int64(r1)
	if ret < 0 {
		return 0xFFFF, syscall.Errno(-ret)
	}
	return uint16(ret), nil
}
