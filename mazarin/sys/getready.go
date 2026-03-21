package sys

const sysGetReady = 0x1034

// GetReady checks whether the shepherd with the given SID has signaled ready
// via SetReady. Returns true if the shepherd is found and ready.
func GetReady(sid int) bool {
	r1, _, _ := RawSyscall(sysGetReady,
		uintptr(sid),
		0,
		0, 0, 0, 0,
	)
	return r1 == 1
}
