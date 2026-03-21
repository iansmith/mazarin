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
	id := int16(sid)
	for i := 0; i < proc.MaxShepherds; i++ {
		if proc.ShepherdListInUse[i] && proc.ShepherdListData[i].PID == proc.ShepherdId(id) {
			return int64(atomic.LoadInt32(&proc.ShepherdListData[i].Ready))
		}
	}
	return 0 // not found
}
