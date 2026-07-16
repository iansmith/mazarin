package sys

import "mazzy/shared/mazzy"

// GetVforkReservedPID looks up the reserved child PID for a thread TID. If tid
// is currently a vfork transient (running between its clone and the matching
// execve), this returns the PID reserved for its eventual child; otherwise 0.
//
// The linux shepherd uses this to detect when a delegated dup3/fcntl/chdir
// came from a vfork transient — which shares its parent's SID for normal
// delegate routing — so the FD/cwd mutation can be routed onto the child's
// own FD table instead of corrupting the live parent's.
func GetVforkReservedPID(tid int16) int16 {
	r1, _, _ := RawSyscall(mazzy.SysGetVforkReservedPID, uintptr(tid), 0, 0, 0, 0, 0)
	return int16(r1)
}
