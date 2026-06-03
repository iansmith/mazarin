package sys

import (
	"syscall"

	"mazzy/shared/mazzy"
)

// CloneExec issues the kernel-internal combined clone+execve SVC (MAZ-120). The
// caller (the linux shepherd's execve dispatch) has already staged two regions
// into its own address space:
//
//   - the target ELF image, at elfVA spanning elfPages pages (elfBytes used),
//   - the marshaled non-ELF params (argv/envp/intent/cwd/filename), at
//     paramsVA spanning paramsPages pages (paramsBytes used), produced by
//     linuxabi.MarshalCloneExecParams.
//
// The kernel reads BOTH regions out of the caller's address space (via
// copyPagesFromUser, like RunShepherd) and unmaps the ELF pages; the caller
// munmaps both regions only AFTER this returns (the SVC blocks until the worker
// has copied them). The return value is the Linux fork/clone ABI verbatim: the
// new child PID (positive) on success, or a NEGATIVE errno on failure. It is
// NOT remapped to the positive errXxx codes RunShepherd uses — a fork-flavored
// caller interprets a positive return as a PID, so a positive "error" would be
// misread as a (huge) PID.
//
// SVC argument layout:
//
//	arg0 = elfVA       (page-aligned)
//	arg1 = elfPages
//	arg2 = elfBytes
//	arg3 = paramsVA    (page-aligned)
//	arg4 = paramsPages
//	arg5 = paramsBytes (used length within the params region)
func CloneExec(elfVA uintptr, elfPages, elfBytes int, paramsVA uintptr, paramsPages, paramsBytes int) int64 {
	r1, _, _ := syscall.RawSyscall6(
		mazzy.SysCloneExec,
		elfVA,
		uintptr(elfPages),
		uintptr(elfBytes),
		paramsVA,
		uintptr(paramsPages),
		uintptr(paramsBytes),
	)
	return int64(r1)
}

// ReapVforkTransient tells the kernel to terminate the transient vfork thread
// with the given TID without resuming it (MAZ-63). Called by the linux
// shepherd's execve handler after a successful vfork execve so the transient
// thread never returns to userspace and writes errno=0 to the errpipe.
// Returns true if the thread was found and reaped (it was a vfork transient),
// false if the TID was not a vfork transient thread (bare execve path).
//
// SVC argument layout:
//
//	arg0 = tid (int16 — the transient thread's kernel TID)
func ReapVforkTransient(tid int16) bool {
	r1, _, _ := syscall.RawSyscall(mazzy.SysReapVforkTransient, uintptr(tid), 0, 0)
	return int64(r1) == 0
}
