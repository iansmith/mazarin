package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"sync/atomic"
	"unsafe"
)

const maxShepherdNameLen = 64

// SyscallGetReady checks whether a named shepherd has set its Ready flag.
//
// arg0 = pointer to shepherd name string (e.g., "disk")
// arg1 = length of name string
// Returns: 1 if ready, 0 if not ready or not found, negative errno on error.
//
//go:noinline
func SyscallGetReady(nameBufPtr, nameLen, _, _, _, _ uint64) int64 {
	if nameLen == 0 || nameLen > maxShepherdNameLen {
		return -22 // EINVAL
	}

	var nameBuf [maxShepherdNameLen]byte
	if !kmem.CopyFromUser(nameBuf[:nameLen], uintptr(nameBufPtr), int(nameLen)) {
		return -14 // EFAULT
	}
	name := *(*string)(unsafe.Pointer(&nameBuf))
	name = name[:nameLen]

	// Match against shepherd filenames. Shepherd filenames are stored as
	// "/<name>.elf" — we match if the filename is "/<name>.elf".
	target := "/" + name + ".elf"
	for i := 0; i < proc.MaxShepherds; i++ {
		if proc.ShepherdListInUse[i] && proc.ShepherdListData[i].Filename == target {
			return int64(atomic.LoadInt32(&proc.ShepherdListData[i].Ready))
		}
	}

	return 0 // not found = not ready
}
