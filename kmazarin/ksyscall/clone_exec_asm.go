package ksyscall

import (
	"mazzy/kmazarin/proc"
	_ "unsafe" // for go:linkname
)

// submitCloneExec submits a clone_exec request to the kernel worker (MAZ-75).
// The shepherd-side caller (eventually maz/linux's execve dispatch from
// MAZ-79, reached via an SVC handler analogous to SyscallRunShepherd) issues
// this with a fully-populated CloneExecRequest.
//
// Matching the KernelSVCWorker contract (see kernel_worker.go), submitCloneExec
// returns a thread-switch CONTEXT POINTER (uintptr), NOT the result: the
// calling thread is blocked in ThreadBlockedKernelWork and the SVC handler
// must hand the returned ctxPtr to SetSyscallSwitchTarget. The actual result
// — the new child PID on success, or a negative errno on failure (Linux
// fork/clone ABI) — is injected into the blocked thread's x0 by
// wakeBlockedThread when DoCloneExecWork completes. A zero return means the
// worker was busy or no thread was available to switch to.
//
// Implementation lives in the kmazarin/kmazarin (main) package per the
// existing kernel-internal IPC convention — same shape as submitRunShepherd
// (see bridge_asm.go, kernel_worker.go for the pattern).
//
//go:linkname submitCloneExec main.SubmitCloneExec
func submitCloneExec(req proc.CloneExecRequest) uintptr

// kernelPublishProcessNotify is the ksyscall→main bridge for delivering a
// process-lifecycle event (MAZ-71) to a target shepherd's default uring ring.
// DoCloneExecWork uses it to deliver EventExecComplete to the parent. The
// implementation is nosplit and acquires schedulerLock internally; the
// returned (result, ctxPtr) pair is an immediate-switch optimization that
// DoCloneExecWork discards — the event is written into the target ring before
// any switch, so the parent observes it when it next polls (e.g. in wait4,
// MAZ-80). Same linkname-bridge shape as CreateUserspaceThread.
//
//go:linkname kernelPublishProcessNotify main.KernelPublishProcessNotify
func kernelPublishProcessNotify(targetSID proc.ShepherdId, ev proc.NotificationEvent) (int64, uintptr)
