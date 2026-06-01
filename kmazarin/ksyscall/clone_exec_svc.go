// clone_exec_svc.go — SyscallCloneExec SVC entry (MAZ-120).
//
// This is the SVC-handler half of the combined clone+execve path. The
// shepherd-side caller (maz/linux's execve dispatch) has staged two regions
// into its own address space — the target ELF and a marshaled params region
// (argv/envp/intent/cwd/filename) — and issues the SysCloneExec SVC. This
// handler validates the scalar args, reads + unmarshals the params region out
// of the caller's address space, builds a fully-populated proc.CloneExecRequest
// (the ELF VA range is passed by reference; the worker copies it), and hands it
// to the kernel worker via submitCloneExec.
//
// It mirrors SyscallRunShepherd (runshepherd.go:39-105): validate scalars, read
// caller data, build the request, submit, then SetSyscallSwitchTarget on the
// returned context pointer. The result — the new child PID (positive) or a
// negative errno (Linux fork/clone ABI) — is injected into the blocked thread's
// x0 by the worker when DoCloneExecWork completes; the SVC itself returns 0
// (the switch-target contract, same as RunShepherd).
package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/proc"
	"mazzy/shared/linuxabi"
)

// SyscallCloneExec is the SVC entry for the combined clone+execve (MAZ-120).
//
// arg0 = elfVA       (page-aligned; raw ELF image in the caller's address space)
// arg1 = elfPages    (number of 4KB pages spanning the ELF region)
// arg2 = elfBytes    (actual ELF file size)
// arg3 = paramsVA    (page-aligned; marshaled params region in caller's space)
// arg4 = paramsPages (number of 4KB pages spanning the params region)
// arg5 = paramsBytes (used length within the params region)
//
// Returns 0 on a successful submit (the child PID / negative errno comes back
// via the worker's wakeBlockedThread), or a negative errno on a synchronous
// reject. The negative-errno convention matches the fork/clone ABI the
// userspace wrapper (mazarin/sys.CloneExec) and DoCloneExecWork already use.
//
//go:noinline
func SyscallCloneExec(arg0, arg1, arg2, arg3, arg4, arg5 uint64) int64 {
	elfVA := uintptr(arg0)
	elfPages := int(arg1)
	elfBytes := int(arg2)
	paramsVA := uintptr(arg3)
	paramsPages := int(arg4)
	paramsBytes := int(arg5)

	// EFAULT — null region pointers.
	if elfVA == 0 || paramsVA == 0 {
		return ceEFAULT
	}
	// EINVAL — both regions must be page-aligned (copyPagesFromUser walks them
	// page-by-page; the worker's unmapUserPages walks the ELF by page count).
	if elfVA&0xFFF != 0 || paramsVA&0xFFF != 0 {
		return ceEINVAL
	}
	// ENOEXEC — ELF sizing bounds. DoCloneExecWork re-checks, but reject a
	// malformed request at the boundary rather than dragging garbage out of the
	// caller's address space (mirrors SyscallRunShepherd's early bounds).
	const maxCloneExecPages = 8192 // 32MB, same ceiling as RunShepherd
	if elfPages < 1 || elfPages > maxCloneExecPages {
		return ceENOEXEC
	}
	if elfBytes < 64 || elfBytes > elfPages*4096 {
		return ceENOEXEC
	}
	// E2BIG — the params region is bounded at a Linux-like ARG_MAX so a runaway
	// argv/envp is rejected here instead of overflowing the child's user stack.
	if paramsBytes < linuxabi.CloneExecParamsHeaderSize || paramsBytes > linuxabi.CloneExecArgMax {
		return ceE2BIG
	}
	if paramsPages < 1 || paramsBytes > paramsPages*4096 {
		return ceEINVAL
	}

	shepherd := proc.CurrentShepherd()
	if shepherd == nil {
		return ceEFAULT
	}

	// Read the marshaled params out of the caller's address space (byte-by-byte
	// via copyPagesFromUser, same as the ELF and readPackedArgs). We are in the
	// caller's SVC context, so the caller's L0 is the right page table to walk.
	paramsBlob := copyPagesFromUser(paramsVA, paramsBytes, shepherd.PageTableL0PA)
	if paramsBlob == nil {
		return ceEFAULT
	}
	params, err := linuxabi.UnmarshalCloneExecParams(paramsBlob)
	if err != nil {
		klog.Errf("[CloneExec] ERROR: malformed params blob len=%d: %v\n", paramsBytes, err)
		return ceEFAULT
	}
	// Cap-check intent/cwd up front (DoCloneExecWork re-checks authoritatively).
	if len(params.Intent) > proc.MaxStartupIntentOps {
		return ceE2BIG
	}
	if len(params.Cwd) > proc.MaxStartupCwdBytes {
		return ceENAMETOOLONG
	}

	// Retrieve the reserved PID and transient TID for this vfork child (MAZ-127).
	// Both must be captured NOW while we are still in the transient thread's SVC
	// context — DoCloneExecWork runs on the kernel worker (thread 0), so
	// GetCurrentThreadTID() there would return the wrong TID.
	reservedPIDValue := GetVforkReservedPID()
	var reservedPID proc.ShepherdId
	var transientTID int32
	if reservedPIDValue != 0 {
		reservedPID = proc.ShepherdId(reservedPIDValue)
		transientTID = int32(GetCurrentThreadTID())
	}

	ctxPtr := submitCloneExec(proc.CloneExecRequest{
		ELFStartVA:     elfVA,
		ELFNumBytes:    elfBytes,
		ELFNumPages:    elfPages,
		CallerL0PA:     shepherd.PageTableL0PA,
		CallerShepherd: shepherd,
		Argv:           params.Argv,
		Envp:           params.Envp,
		Intent:         params.Intent,
		Cwd:            params.Cwd,
		ReservedPID:    reservedPID,
		TransientTID:   transientTID,
		Filename:       params.Filename,
	})
	if ctxPtr == 0 {
		// Worker busy or no thread to switch to — EAGAIN, matching RunShepherd's
		// "busy" branch (negated to the fork-ABI's negative-errno convention).
		klog.Criticalf("[CE]", "[CloneExec] ERROR: worker busy or no thread (params=%d bytes)\n", paramsBytes)
		return ceEAGAIN
	}
	SetSyscallSwitchTarget(ctxPtr)
	return 0
}
