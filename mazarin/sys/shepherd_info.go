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
	return readAllShepherdEntries(func(buf []hid.ShepherdInfoEntry) (int, error) {
		// Touch every entry of the buffer to ensure demand faults fire
		// before the kernel's CopyToUser writes to these addresses.
		for i := range buf {
			buf[i].SID = 0
		}
		r1, _, errno := syscall.RawSyscall6(
			mazzy.SysShepherdInfo,
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(len(buf)),
			0, 0, 0, 0,
		)
		if errno != 0 {
			return 0, errors.New("ShepherdInfo failed")
		}
		return min(int(r1), len(buf)), nil
	})
}

// readAllShepherdEntries runs fetch over a buffer and returns what it wrote.
func readAllShepherdEntries(fetch func([]hid.ShepherdInfoEntry) (int, error)) ([]hid.ShepherdInfoEntry, error) {
	buf := make([]hid.ShepherdInfoEntry, 32)
	n, err := fetch(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}
