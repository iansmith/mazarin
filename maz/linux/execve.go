package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"mazzy/maz/linux/internal/execve"
	"mazzy/maz/linux/internal/fdtable"
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

	// 5c. Finalize the child's FD table BEFORE the SVC when the child's PID
	// is already known — the vfork path, where the kernel reserved it at
	// clone time (MAZ-156 root cause). The child starts running INSIDE
	// sys.CloneExec, so its first delegated syscall can reach getShepherd
	// before ANY post-SVC code here runs. The old post-SVC step 7a then
	// evicted the LIVE child's state as if it were a dead predecessor's —
	// destroying the correctly-primed table (closing its pipe fd1 write end,
	// so the parent read EOF-empty) and re-priming an orphan copy of the
	// parent's table (console fd1, parent cwd → the `child cwd=/` console
	// leak). Pre-SVC this sequence is race-free BY CONSTRUCTION: the child
	// does not exist yet.
	//
	// The transient's pre-execve dup3/chdir already primed
	// pendingChildFDTs[reservedPID] (so pipe Fork() refs are already paid);
	// intent/cwd are empty on this path (the ops were applied directly, not
	// buffered), leaving ApplyStartupIntent a no-op and CloseCloexecFDs the
	// only real work. On CloneExec failure the finalized pending table is
	// abandoned exactly as an unconsumed primed table is today — no new
	// leak class.
	reservedPID := sys.GetVforkReservedPID(req.CallerTID)
	if reservedPID != 0 {
		// Any state under the reserved PID at this point is definitively a
		// dead predecessor's: the allocator only reserves free PIDs and the
		// child cannot have run yet.
		h.evictStaleChildShepherd(reservedPID)
		childFDT := h.getOrCreatePendingChildFDT(reservedPID, fdt.Copy)
		childFDT.ApplyStartupIntent(intent, cwd)
		childFDT.CloseCloexecFDs(func(handle uint32) { h.fs.Close(handle) })
	}

	// Compute the child's stdio redirect mask (MAZ-149). For vfork the
	// pending table above is now FINAL, so the peek inside
	// childStdioRedirectMask reads exact fd 1/2 Kinds (intent is empty). For
	// bare execs it remains a simulation on the parent's table + intent.
	// Computed pre-SVC because the kernel must store it on the child
	// Shepherd before the child is enqueued.
	stdioMask := h.childStdioRedirectMask(req.CallerTID, fdt, intent)

	// 5d. Marshal the non-ELF params (faithful argv/envp + intent + cwd +
	// filename + redirect mask) and stage them into their own mmap region.
	paramsBlob, perr := linuxabi.MarshalCloneExecParams(
		linuxabi.PackArgv(ceReq.Argv), linuxabi.PackArgv(ceReq.Envp),
		ceReq.Intent, ceReq.Cwd, []byte(ceReq.Filename), req.CallerTID, req.CallerPID, stdioMask)
	if perr != nil {
		munmapStage(elfVA, elfPages)
		h.dropAbandonedPendingFDT(reservedPID)
		req.Reply(E2BIG)
		return
	}
	paramsVA, paramsPages, merr := mmapStage(paramsBlob)
	if merr != 0 {
		munmapStage(elfVA, elfPages)
		h.dropAbandonedPendingFDT(reservedPID)
		req.Reply(merr)
		return
	}

	// 6. Issue the combined clone+execve SVC. Returns child PID (positive) or a
	// negative errno (fork/clone ABI, preserved verbatim).
	ret := sys.CloneExec(elfVA, elfPages, len(elfData), paramsVA, paramsPages, len(paramsBlob))

	// 7. Unmap the staged regions now that the kernel has copied them.
	munmapStage(elfVA, elfPages)
	munmapStage(paramsVA, paramsPages)

	// 7a. On success: register the child in the reaper, and for the bare
	// (non-vfork) path only, build+stash the child's inherited FD table
	// (the vfork path finalized it pre-SVC at step 5c — MAZ-156).
	//
	// RegisterChild closes the wait4 race: for vfork, wakeVforkParent fires
	// inside DoCloneExecWork before sys.CloneExec returns, so the parent can
	// call wait4 before EventExecComplete is processed. Registering here
	// (synchronously, before any reply) ensures the reaper already knows
	// about the child when that wait4 arrives.
	if ret <= 0 {
		// Exec failed after step 5c primed the reserved PID's table: unwind
		// it. Left in place, its Fork()'d pipe writer refs strand readers
		// waiting for EOF, and a later kernel reuse of the PID would hand an
		// unrelated child this dead exec's table (review finding, MAZ-156).
		h.dropAbandonedPendingFDT(reservedPID)
	} else {
		// MAZ-156 forensics: one line per successful exec — the mask the
		// kernel stored, the reservation, and whether the child consumed its
		// pending table before we got here (both orders are correct for
		// vfork; this is the soak's verification probe for the exec family).
		// One h.mu snapshot for both fields.
		h.mu.Lock()
		pendingPrimed := h.pendingChildFDTs[int16(ret)] != nil
		childShepExists := h.shepherds[int16(ret)] != nil
		h.mu.Unlock()
		fmt.Printf("[lin:execve] sid=%d ret=%d mask=%#x reserved=%d pendingPrimed=%v childShepExists=%v\n",
			req.CallerPID, ret, stdioMask, reservedPID, pendingPrimed, childShepExists)

		if reservedPID != 0 && int16(ret) != reservedPID {
			// The kernel returned a DIFFERENT pid than it reserved — should
			// be impossible (createCloneExecThreadImpl adopts the reservation
			// verbatim). Log loudly, unwind the orphaned reserved-PID table,
			// and fall back to the legacy post-SVC prime under the actual pid
			// so the child at least gets an inherited table.
			fmt.Printf("[lin:execve] BUG? reserved=%d but ret=%d — legacy re-prime\n",
				reservedPID, ret)
			h.dropAbandonedPendingFDT(reservedPID)
		}
		if reservedPID == 0 || int16(ret) != reservedPID {
			// Bare (non-vfork) exec — the child PID is unknowable pre-SVC, so
			// the legacy post-SVC evict+prime remains, along with its
			// (narrower) first-syscall race; no current caller takes this
			// path with redirected stdio — or the impossible-case fallback
			// above. The tripwire fires if a racing child already consumed a
			// FRESH table here.
			if childShepExists {
				fmt.Printf("[lin:execve] WARN exec child sid=%d raced 7a (fresh table)\n", ret)
			}
			h.evictStaleChildShepherd(int16(ret))
			childFDT := h.getOrCreatePendingChildFDT(int16(ret), fdt.Copy)
			childFDT.ApplyStartupIntent(intent, cwd)
			childFDT.CloseCloexecFDs(func(handle uint32) { h.fs.Close(handle) })
		}
		// vfork with ret == reservedPID (the normal case): the child's table
		// was finalized pre-SVC at step 5c — nothing to evict, nothing to
		// prime, either consume order is correct.
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

// childStdioRedirectMask computes the MAZ-149 per-process stdio redirect mask
// for the child a sysExecve is about to create. Base table selection mirrors
// step 7a's getOrCreatePendingChildFDT: if the vfork transient already primed
// pendingChildFDTs[reservedPID] (its pre-execve dup3s were routed there by
// resolveTargetFDT), that IS the child's table; otherwise the child starts
// from a copy of the parent's live table. The buffered clone-window intent is
// then simulated on top (it is applied for real at step 7a).
//
// The pending-table peek holds h.mu (the map's lock); the table itself is
// quiescent by execve time — the transient issues its dup3s strictly before
// its execve, and post-MAZ-150 the reserved PID cannot be concurrently reused.
func (h *syscallHandler) childStdioRedirectMask(callerTID int16, parentFDT *fdtable.Table, intent []linuxabi.IntentOp) uint8 {
	base := parentFDT
	if reservedPID := sys.GetVforkReservedPID(callerTID); reservedPID != 0 {
		h.mu.Lock()
		if pending := h.pendingChildFDTs[reservedPID]; pending != nil {
			base = pending
		}
		h.mu.Unlock()
	}
	return base.StdioRedirectMaskAfterIntent(intent)
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
