package sys

import (
	"errors"
	"mazzy/shared/hid"
	"mazzy/shared/mazzy"
	"syscall"
	"unsafe"
)

// ShepherdInfo returns information about all running shepherds.
// Each entry includes the shepherd's PID, thread count, thread IDs,
// launch filename, and number of mapped pages.
func ShepherdInfo() ([]hid.ShepherdInfoEntry, error) {
	var buf [32]hid.ShepherdInfoEntry
	// Touch every page of the buffer to ensure demand faults fire before
	// the kernel's CopyToUser writes to these addresses.
	entrySize := unsafe.Sizeof(buf[0])
	for i := range buf {
		p := (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(&buf[0])) + uintptr(i)*entrySize))
		*p = 0
	}
	r1, _, errno := syscall.RawSyscall6(
		mazzy.SysShepherdInfo,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, 0, 0, 0,
	)
	if errno != 0 {
		return nil, errors.New("ShepherdInfo failed")
	}
	n := int(r1)
	if n > len(buf) {
		n = len(buf)
	}
	return buf[:n], nil
}
