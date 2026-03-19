package ksyscall

import (
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"sync/atomic"
)

// SyscallSetReady sets or clears the calling shepherd's Ready flag.
// The Ready flag indicates the shepherd is ready to accept delegated work
// (e.g., fs.maz signals it's ready to handle LoadFile requests).
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

	serial.RawUARTPuts("[SetReady] P")
	serial.RawUARTDecimal(uint64(shepherd.PID))
	serial.RawUARTPuts(" ready=")
	serial.RawUARTDecimal(arg0)
	serial.RawUARTPuts("\r\n")

	return 0
}
