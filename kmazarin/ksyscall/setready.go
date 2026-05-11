package ksyscall

import (
	"mazzy/kmazarin/proc"
	"sync/atomic"
)

// SyscallSetReady sets or clears the calling shepherd's Ready flag.
// The Ready flag indicates the shepherd is ready to accept delegated work
// (e.g., a shepherd signals it's ready to handle delegated work).
//
// arg0 = 1 to set ready, 0 to clear
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallSetReady(arg0, _, _, _, _, _ uint64) int64 {
	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return -1 // EPERM
	}

	var val int32
	if arg0 != 0 {
		val = 1
	}
	atomic.StoreInt32(&shepherd.Ready, val)
	return 0
}
