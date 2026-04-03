package sys

import (
	"errors"
	"mazzy/shared/mazzy"
)

// SharePages maps a page from the caller's address space into a target
// shepherd's space. The callerVA need not be page-aligned — the offset is
// preserved. Returns the target's VA on success.
func SharePages(targetSID int, callerVA uintptr) (uintptr, error) {
	r1, _, errno := RawSyscall(mazzy.SysSharePages,
		uintptr(targetSID),
		callerVA,
		0, 0, 0, 0)
	if errno != 0 || int64(r1) < 0 {
		return 0, errors.New("SharePages failed")
	}
	return r1, nil
}
