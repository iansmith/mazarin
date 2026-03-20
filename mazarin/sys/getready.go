package sys

import "unsafe"

const sysGetReady = 0x1034

// GetReady checks whether the named shepherd has signaled ready via SetReady.
// The name is the shepherd's base name (e.g., "disk", "rachel"), not the
// full filename. Returns true if the shepherd is found and ready.
func GetReady(name string) bool {
	nameBytes := []byte(name)
	r1, _, _ := RawSyscall(sysGetReady,
		uintptr(unsafe.Pointer(&nameBytes[0])),
		uintptr(len(nameBytes)),
		0, 0, 0, 0,
	)
	return r1 == 1
}
