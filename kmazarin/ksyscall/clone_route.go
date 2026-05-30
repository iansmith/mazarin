package ksyscall

import "mazzy/shared/linuxabi"

// isThreadFlavorClone reports whether sysID is clone(2) requesting THREAD
// creation (CLONE_THREAD set in the flag mask, which clone passes in arg0).
//
// It is the clone analogue of IsMagicFdSyscall: the dispatch gate uses it to
// KEEP a thread-flavor clone out of the userspace-delegation path even when the
// linux shepherd has registered sysid.Clone as a handler. A thread clone must
// always reach the in-kernel SyscallClone → CloneThread path (it is how every
// shepherd's Go runtime spawns OS threads — delegating it would deadlock boot).
// A PROCESS-flavor clone (CLONE_THREAD clear) is NOT excluded here, so it flows
// on to DelegateSyscall, which forwards it to the shepherd's buffering-window
// path (MAZ-118) and parks the caller until the matching execve replies.
//
//go:nosplit
func isThreadFlavorClone(sysID SysID, flags uint64) bool {
	return sysID == SysIDClone && !linuxabi.IsProcessClone(flags)
}
