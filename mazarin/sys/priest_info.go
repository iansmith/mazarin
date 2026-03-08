package sys

import (
	"errors"
	"mazzy/shared/hid"
	"syscall"
	"unsafe"
)

// PriestInfo returns information about all running priests.
// Each entry includes the priest's PID, thread count, thread IDs,
// launch filename, and number of mapped pages.
func PriestInfo() ([]hid.PriestInfoEntry, error) {
	var buf [32]hid.PriestInfoEntry
	r1, _, errno := syscall.RawSyscall6(
		sysPriestInfo,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, 0, 0, 0,
	)
	if errno != 0 {
		return nil, errors.New("PriestInfo failed")
	}
	n := int(r1)
	if n > len(buf) {
		n = len(buf)
	}
	return buf[:n], nil
}
