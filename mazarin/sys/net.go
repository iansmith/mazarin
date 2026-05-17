package sys

import (
	"mazzy/shared/mazzy"
)

// NetReadRxLatencyUs returns the kernel→net IRQ-delivery latency in
// microseconds for the given RX descriptor tag (MAZ-28 step 2).
//
// Call right after dequeuing a net io_uring CQE: the tag is the CQE's
// UserData (low bits); the result is the microseconds elapsed between
// the kernel IRQ top-half recording the IRQ timestamp and the syscall
// reading it.
//
// Returns 0 if the tag is out of range or the timestamp hasn't been
// recorded yet.
func NetReadRxLatencyUs(tag uint16) uint32 {
	r1, _, _ := RawSyscall(mazzy.SysNetReadRxLatencyUs,
		uintptr(tag), 0, 0, 0, 0, 0)
	return uint32(r1)
}
