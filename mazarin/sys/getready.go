package sys

import (
	"mazzy/shared/mazzy"
	"mazzy/shep"
)

// GetReady checks whether the shepherd identified by si has signaled ready
// via SetReady. Returns true if the shepherd is found and ready.
func GetReady(si shep.Id) bool {
	r1, _, _ := RawSyscall(mazzy.SysGetReady,
		uintptr(si.Sid()),
		0,
		0, 0, 0, 0,
	)
	return r1 == 1
}
