package ksyscall

// isInKernelClone reports whether sysID is clone(2) and the clone should be
// handled in-kernel (never delegated to the shepherd).
//
// Both THREAD-flavor clones (CLONE_THREAD set) and PROCESS-flavor clones
// (CLONE_THREAD clear) are now handled in-kernel via kernel-emulated vfork (MAZ-127):
//
//   - THREAD clones spawn a new thread via CloneThread, matching the original design.
//   - PROCESS clones (vfork) spawn a transient child-context thread and suspend the
//     parent, awaiting the child's execve to wake the parent. This is the vfork fix
//     for the Go os/exec fork+execve chain (MAZ-127).
//
// Before MAZ-127, process clones were delegated to the linux shepherd's buffering-
// window path (MAZ-118), but that path parked the clone caller and withheld the
// reply until execve flushed — incompatible with rawVforkSyscall, which returns
// twice from a single SVC (child gets x0=0, parent gets childPID). The vfork model
// requires the clone SVC to return immediately: child at PC (x0=0), parent suspended.
//
//go:nosplit
func isInKernelClone(sysID SysID, flags uint64) bool {
	return sysID == SysIDClone // Both thread-flavor and process-flavor: handle in-kernel
}
