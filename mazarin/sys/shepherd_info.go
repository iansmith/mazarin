package sys

import (
	"errors"
	"mazzy/shared/hid"
	"syscall"
	"unsafe"
)

// ShepherdInfo returns information about all running shepherds.
// Each entry includes the shepherd's PID, thread count, thread IDs,
// launch filename, and number of mapped pages.
func ShepherdInfo() ([]hid.ShepherdInfoEntry, error) {
	var buf [32]hid.ShepherdInfoEntry
	r1, _, errno := syscall.RawSyscall6(
		sysShepherdInfo,
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
