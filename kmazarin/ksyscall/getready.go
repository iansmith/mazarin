package ksyscall

import (
	"mazzy/kmazarin/proc"
	"sync/atomic"
)

// SyscallGetReady checks whether a shepherd has set its Ready flag.
//
// arg0 = shepherd SID (PID)
// Returns: 1 if ready, 0 if not ready or not found, negative errno on error.
//
//go:noinline
func SyscallGetReady(sid, _, _, _, _, _ uint64) int64 {
	if p := proc.FindShepherdBySID(proc.ShepherdId(sid)); p != nil {
		return int64(atomic.LoadInt32(&p.Ready))
	}
	return 0 // not found
}
