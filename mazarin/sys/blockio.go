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
