package main

import (
	"syscall"
	"unsafe"

	"mazzy/maz/linux/internal/execve"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/shared/linuxabi"
)

// sysExecve handles a delegated execve(path, argv, envp) (MAZ-120). The kernel
// delegate packing (delegate.go packExecveDataPage) flattened the child's
// path/argv/envp into the request's data page, framed as a CloneExecParams blob
// (Filename = path, Argv/Envp = the flattened vectors, Intent/Cwd empty). This
// handler:
//
//  1. decodes path/argv/envp from the data page,
//  2. flushes the clone buffering window (MAZ-118) for the caller TID to get the
//     buffered FD intent + chdir target (empty if there was no preceding clone),
//  3. resolves the target path against the caller's cwd,
//  4. builds the request (execve.Build — enforces the argv/intent/cwd caps),
//  5. loads the target ELF off disk and stages it + the marshaled params into
//     anonymous mmap pages the kernel reads via copyPagesFromUser,
//  6. issues the kernel-internal clone_exec SVC (sys.CloneExec),
//  7. munmaps the staged pages AFTER the SVC returns, and
//  8. replies the child PID (or a negative errno) to the parked caller.
//
// The buffered dup3/close/F_SETFD/chdir intent is carried across the SVC and
// stored on the child; APPLYING it inside the child is MAZ-113 (out of scope
// here). Until then the child runs with faithful argv[0]+env but the FD
// redirections / cwd change are no-ops.
func (h *syscallHandler) sysExecve(req sys.SyscallRequest) {
	// 1. Decode the flattened path/argv/envp from the data page.
	data := req.Data()
	if data == nil {
		req.Reply(EFAULT)
		return
	}
	params, err := linuxabi.UnmarshalCloneExecParams(data)
	if err != nil {
		req.Reply(EFAULT)
		return
	}
	path := string(params.Filename)
	if path == "" || len(params.Argv) == 0 {
		req.Reply(EINVAL)
		return
	}

	// 2. Flush the clone buffering window for this TID (empty if there was no
	// preceding process-flavor clone; ENOEXEC if the window was poisoned).
	intent, cwd, ferr := h.flushCloneWindow(req.CallerPID, int32(req.CallerTID))
	if ferr != EOK {
		req.Reply(ferr)
		return
	}

	// 3. Resolve the target path against the caller's cwd.
	fdt := h.getShepherd(req.CallerPID).FDT
	resolved := execve.ResolvePath(path, fdt.Cwd)

	// 4. Build the request (caps the argv/intent/cwd; never truncates).
	ceReq, berr := execve.Build(resolved, params.Argv, params.Envp, intent, cwd)
	if berr != nil {
		req.Reply(execveBuildErrno(berr))
		return
	}

	// 5a. Load the target ELF off disk.
	elfData, rerr := h.fs.ReadFile(ceReq.Filename)
	if rerr != nil {
		req.Reply(ENOENT)
		return
	}
	if len(elfData) < 64 {
		req.Reply(ENOEXEC)
		return
	}

	// 5b. Stage the ELF into anonymous mmap pages (the kernel reads them via
	// copyPagesFromUser, like RunShepherd; it unmaps them itself, and we munmap
	// again after the SVC — a no-op double-unmap, matching maz/fs's pattern).
	elfVA, elfPages, merr := mmapStage(elfData)
	if merr != 0 {
		req.Reply(merr)
		return
	}

	// 5c. Marshal the non-ELF params (faithful argv/envp + intent + cwd +
	// filename) and stage them into their own mmap region.
	paramsBlob, perr := linuxabi.MarshalCloneExecParams(
		linuxabi.PackArgv(ceReq.Argv), linuxabi.PackArgv(ceReq.Envp),
		ceReq.Intent, ceReq.Cwd, []byte(ceReq.Filename), req.CallerTID, req.CallerPID)
	if perr != nil {
		munmapStage(elfVA, elfPages)
		req.Reply(E2BIG)
		return
	}
	paramsVA, paramsPages, merr := mmapStage(paramsBlob)
	if merr != 0 {
		munmapStage(elfVA, elfPages)
		req.Reply(merr)
		return
	}

	// 6. Issue the combined clone+execve SVC. Returns child PID (positive) or a
	// negative errno (fork/clone ABI, preserved verbatim).
	ret := sys.CloneExec(elfVA, elfPages, len(elfData), paramsVA, paramsPages, len(paramsBlob))

	// 7. Unmap the staged regions now that the kernel has copied them.
	munmapStage(elfVA, elfPages)
	munmapStage(paramsVA, paramsPages)

	// 7a. On success: build the child's inherited FD table (parent copy +
	// cwd + cloexec sweep), stash it for the child's first syscall, and
	// synchronously register the child in the reaper.
	//
	// childFDT is built here (not before step 4) so that pipe.End.Fork()
	// writer-count increments are only paid on the success path — building
	// it speculatively and then discarding on ELF-load / mmap failures
	// would leak pipe writer references, keeping read ends from seeing EOF.
	//
	// RegisterChild closes the wait4 race: for vfork, wakeVforkParent fires
	// inside DoCloneExecWork before sys.CloneExec returns, so the parent can
	// call wait4 before EventExecComplete is processed. Registering here
	// (synchronously, before any reply) ensures the reaper already knows
	// about the child when that wait4 arrives.
	if ret > 0 {
		// The kernel PID allocator may have just reissued this PID from a
		// previously-reaped fork/exec child whose linux-shepherd FD state was
		// never torn down (child exits arrive as reaper-only process
		// notifications, not shepherd deaths). Evict that stale state now, before
		// priming the new child's table, so getShepherd doesn't hand the child
		// its dead predecessor's stdio/cwd. See evictStaleChildShepherd.
		h.evictStaleChildShepherd(int16(ret))

		// If the vfork transient already did pre-execve FD/cwd setup (dup3,
		// fcntl(F_SETFD), chdir, close — resolveTargetFDT routed those onto
		// the child's own table instead of the parent's), that table is
		// already sitting in pendingChildFDTs. A bare fork+exec with no
		// in-child intent never touches it, so fall back to a fresh copy of
		// the parent's current table.
		childFDT := h.getOrCreatePendingChildFDT(int16(ret), fdt.Copy)
		childFDT.ApplyStartupIntent(intent, cwd)
		childFDT.CloseCloexecFDs(func(handle uint32) { h.fs.Close(handle) })
		h.reaper.RegisterChild(int32(req.CallerPID), int32(ret))
	}

	// 8. If this was a vfork execve (CallerTID is a transient vfork thread) and it
	// succeeded, reap the transient thread instead of replying to it. The parent was
	// already woken by wakeVforkParent inside DoCloneExecWork; the transient must
	// not return to userspace (it would write errno=0 to the errpipe and confuse
	// os/exec). On failure we still reply to the transient so it can write the real
	// errno to the errpipe, letting the parent's cmd.Start() return the correct error.
	// For bare execves (non-vfork), ReapVforkTransient returns false and we fall
	// through to req.Reply.
	if ret > 0 && sys.ReapVforkTransient(req.CallerTID) {
		return
	}
	req.Reply(ret)
}

// flushCloneWindow flushes sid/tid's clone buffering window under cloneMu and
// returns the buffered intent + cwd. A TID with no open window (a bare execve
// with no preceding process-flavor clone) yields empty intent and EOK — that is
// legal. A poisoned window (an unbufferable syscall arrived between clone and
// execve) yields ENOEXEC. The successful TID is dropped from its SID's set.
func (h *syscallHandler) flushCloneWindow(sid int16, tid int32) ([]linuxabi.IntentOp, []byte, int64) {
	h.cloneMu.Lock()
	defer h.cloneMu.Unlock()
	if !h.cloneWindows.IsOpen(tid) {
		return nil, nil, EOK
	}
	ops, cwd, err := h.cloneWindows.Flush(tid)
	h.dropCloneTID(sid, tid)
	if err != nil {
		return nil, nil, ENOEXEC
	}
	return ops, cwd, EOK
}

// dropCloneTID removes tid from sid's clone-TID set. Caller holds cloneMu.
func (h *syscallHandler) dropCloneTID(sid int16, tid int32) {
	if tids := h.cloneTIDsBySID[sid]; tids != nil {
		delete(tids, tid)
		if len(tids) == 0 {
			delete(h.cloneTIDsBySID, sid)
		}
	}
}

// execveBuildErrno maps an execve.Build cap error to the Linux errno.
func execveBuildErrno(err error) int64 {
	switch err {
	case execve.ErrIntentOverflow:
		return E2BIG
	case execve.ErrCwdOverflow:
		return ENAMETOOLONG
	case execve.ErrEmptyArgv:
		return EINVAL
	default:
		return EINVAL
	}
}

// mmapStage allocates page-aligned anonymous memory via mem.Mmap, copies data
// into it, and returns (va, numPages, errno). The caller munmaps via
// munmapStage. errno is 0 on success or a negative errno on failure.
func mmapStage(data []byte) (uintptr, int, int64) {
	numPages := (len(data) + 4095) / 4096
	if numPages == 0 {
		numPages = 1
	}
	total := numPages * 4096
	va, err := mem.Mmap(0, total,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|mmapAnonymous)
	if err != nil {
		return 0, 0, ENOMEM
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(va)), total)
	copy(dst, data)
	return va, numPages, 0
}

// munmapStage releases a region staged by mmapStage. Safe to call after the
// kernel has already unmapped the pages (the second unmap is a no-op).
func munmapStage(va uintptr, numPages int) {
	if va == 0 || numPages <= 0 {
		return
	}
	mem.Munmap(va, numPages*4096)
}

// mmapAnonymous is MAP_ANONYMOUS (0x20); syscall.MAP_ANONYMOUS is Linux-only
// and this shepherd builds for a non-Linux toolchain host, mirroring mem's
// own mapAnonymous constant.
const mmapAnonymous = 0x20
