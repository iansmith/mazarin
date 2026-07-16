// clone_exec.go — kernel-side combined clone+execve handler (MAZ-75).
//
// DoCloneExecWork is the worker-thread half of the clone_exec syscall. The
// linux shepherd buffers the in-child setup ops (dup3/close/chdir/F_SETFD)
// between a process-flavor clone and its matching execve, then flushes them
// in a single kernel-internal clone_exec call carrying the pre-loaded target
// ELF, argv/envp, the parent identity, and the buffered intent. This handler:
//
//  1. copies + validates the target ELF out of the caller's address space,
//  2. builds a fresh address space (page table + framebuffer + constraint),
//  3. loads the ELF segments + user stack,
//  4. creates the child's first thread via CreateCloneExecThread, which
//     atomically allocates the child PID + Shepherd slot AND populates the
//     race-sensitive startup state (parent identity + buffered intent + chdir
//     target) under schedulerLock before the child is enqueued (MAZ-112),
//  5. registers the non-race-sensitive child setup (introspection fields, VA
//     spans + the IPC ring) lock-free on the worker thread, and
//  6. delivers EventExecComplete to the parent (MAZ-71).
//
// It mirrors DoRunShepherdWork (runshepherd.go) — the proven shepherd-launch
// path — and lives in this package for the same reason: it needs the
// unexported ELF/address-space helpers (loadELF, copyPagesFromUser,
// parseELFHeader, setupUserStack via loadELF, buildSymbolTable, …).
//
// Error teardown (MAZ-108): each late-init failure branch (framebuffer/
// constraint map, loadELF) calls kmem.FreeProcessPageTable(childL0PA), which
// reclaims the child L0, its on-demand L1/L2/L3 tables, and any segment/stack
// frames mapped before the failure — in one walk, no per-segment undo handle.
// Note the allocation order: the child PID + Shepherd slot are not created
// until CreateCloneExecThread, which runs AFTER the only realistic late-init
// failure (loadELF) — so a loadELF failure leaks neither a PID nor a Shepherd
// slot, only address-space memory, which FreeProcessPageTable now reclaims.
package ksyscall

import (
	"mazzy/kmazarin/device/virtio/gpu"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
)

// clone_exec returns the new child PID (positive) on success, or a negative
// errno on failure — the Linux fork/clone/execve ABI. This intentionally
// differs from DoRunShepherdWork, which returns positive errXxx codes: a
// fork-flavored caller interprets a positive return as a child PID, so a
// positive "error" code would be misread as a (huge) PID. Negative errnos
// keep success and failure unambiguous for the eventual shepherd caller.
const (
	ceEFAULT       int64 = -14 // EFAULT — bad address (ELF copy / null caller)
	ceENOEXEC      int64 = -8  // ENOEXEC — bad ELF format or wrong machine
	ceENOMEM       int64 = -12 // ENOMEM — page table / mapping / shepherd alloc failed
	ceE2BIG        int64 = -7  // E2BIG — too many buffered intent ops / params over ARG_MAX
	ceENAMETOOLONG int64 = -36 // ENAMETOOLONG — chdir target path too long
	ceEINVAL       int64 = -22 // EINVAL — misaligned region / bad page count (SVC entry)
	ceEAGAIN       int64 = -11 // EAGAIN — kernel worker busy / no thread to switch to
)

// maybeWakeVforkParentOnFailure wakes the parked vfork parent when a
// clone_exec fails before reaching the success-path wake below. Without this,
// KernelSVCWorker.run() wakes only the transient thread — the parent hangs
// indefinitely. Safe from the kernel worker thread (growable stack, not nosplit).
func maybeWakeVforkParentOnFailure(req *proc.CloneExecRequest, errno int64) {
	if req.IsVforkCall() {
		wakeVforkParent(int16(req.TransientTID), errno)
	}
}

// DoCloneExecWork performs the combined clone+exec on the kernel worker
// thread (thread 0's growable stack, via the KernelSVCWorker relay). Returns
// the new child PID on success or a negative errno on failure.
func DoCloneExecWork(req *proc.CloneExecRequest) int64 {
	if req.CallerShepherd == nil {
		klog.Errf("[CloneExec] ERROR: nil CallerShepherd\n")
		maybeWakeVforkParentOnFailure(req, ceEFAULT)
		return ceEFAULT
	}
	// Reject malformed ELF sizing before any memory op: copyPagesFromUser does
	// make([]byte, ELFNumBytes) (a negative length panics the kernel worker)
	// and unmapUserPages walks ELFNumPages. The in-kernel caller (MAZ-79)
	// derives these from a real file, but a kernel primitive must not panic on
	// a bad request — this mirrors the bounds SyscallRunShepherd enforces at
	// its SVC entry.
	if req.ELFNumBytes <= 0 || req.ELFNumPages <= 0 {
		klog.Errf("[CloneExec] ERROR: invalid ELF sizing bytes=%d pages=%d\n",
			req.ELFNumBytes, req.ELFNumPages)
		maybeWakeVforkParentOnFailure(req, ceENOEXEC)
		return ceENOEXEC
	}
	// Cap-check the buffered intent + cwd before allocating anything. These
	// come from the shepherd-side caller; rejecting an oversized request up
	// front keeps the failure free of teardown.
	if len(req.Intent) > proc.MaxStartupIntentOps {
		klog.Errf("[CloneExec] ERROR: intent ops %d > max %d\n", len(req.Intent), proc.MaxStartupIntentOps)
		maybeWakeVforkParentOnFailure(req, ceE2BIG)
		return ceE2BIG
	}
	if len(req.Cwd) > proc.MaxStartupCwdBytes {
		klog.Errf("[CloneExec] ERROR: cwd len %d > max %d\n", len(req.Cwd), proc.MaxStartupCwdBytes)
		maybeWakeVforkParentOnFailure(req, ceENAMETOOLONG)
		return ceENAMETOOLONG
	}

	filename := "/cloneexec.elf"
	if len(req.Filename) > 0 {
		filename = string(req.Filename)
	}

	// Pre-validate the ELF header from just the first page before copying the
	// whole image. A bogus header (wrong magic / class / machine) is rejected
	// after a single-page copy instead of dragging potentially megabytes of
	// garbage out of the caller's address space (risk #5 in findings.md). This
	// pre-validation runs before any reclaimable allocation, so a rejected
	// target consumes no PID, no page table, and no Shepherd slot. The caller's
	// raw ELF pages are released exactly once below, whatever the outcome.
	headerBytes := min(req.ELFNumBytes, 4096)
	header := copyPagesFromUser(req.ELFStartVA, headerBytes, req.CallerL0PA)

	var elfData []byte
	errno := validateCloneExecELFHeader(header, req.ELFNumBytes)
	if errno == 0 {
		// Header is loadable — copy the full image.
		elfData = copyPagesFromUser(req.ELFStartVA, req.ELFNumBytes, req.CallerL0PA)
		if elfData == nil {
			errno = ceEFAULT
		}
	}
	unmapUserPages(req.ELFStartVA, req.ELFNumPages, req.CallerL0PA, req.CallerShepherd.PID)
	if errno != 0 {
		klog.Errf("[CloneExec] ERROR: ELF header pre-validation failed errno=%d\n", errno)
		maybeWakeVforkParentOnFailure(req, errno)
		return errno
	}

	hdr := parseELFHeader(elfData)

	// Fresh address space for the child.
	childL0PA := kmem.CreateProcessPageTable()
	if childL0PA == 0 {
		klog.Errf("[CloneExec] ERROR: CreateProcessPageTable failed\n")
		maybeWakeVforkParentOnFailure(req, ceENOMEM)
		return ceENOMEM
	}

	// Map the framebuffer + constraint shared pages into the child, the same
	// surface every shepherd gets.
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	if !kmem.MapUserFramebufferWithL0(fbPA, fbSize, childL0PA) {
		kmem.FreeProcessPageTable(childL0PA) // reclaim the child page table (MAZ-108)
		klog.Errf("[CloneExec] ERROR: MapUserFramebufferWithL0 failed\n")
		maybeWakeVforkParentOnFailure(req, ceENOMEM)
		return ceENOMEM
	}
	if !kmem.MapUserConstraintPagesWithL0(childL0PA) {
		kmem.FreeProcessPageTable(childL0PA) // reclaim page table + framebuffer mapping (MAZ-108)
		klog.Errf("[CloneExec] ERROR: MapUserConstraintPagesWithL0 failed\n")
		maybeWakeVforkParentOnFailure(req, ceENOMEM)
		return ceENOMEM
	}

	InitKernelAttrManager()
	symTable := buildSymbolTable(elfData, &hdr)
	highestVA := findHighestVA(elfData, &hdr)

	// Load the ELF segments + user stack into the child page table with the
	// FAITHFUL argv[0]/envp (MAZ-120): the caller's argv verbatim (argv[0] =
	// program name, no shepherd-number argv[1]) and the caller's envp merged
	// with the mandatory mazzy runtime env (GODEBUG=gccheckmark=1 / GOMEMLIMIT /
	// … — mandatory wins on conflict, which the child needs to run correctly
	// under this kernel). cloneExecFaithful tolerates a nil/empty Argv by
	// falling back to a single-element argv built from the filename.
	loadedProc, err := loadELF(elfData, filename, childL0PA, 0, nil, cloneExecFaithful(req, filename))
	if err != nil {
		// Reclaim the child page table plus every segment/stack frame loadELF
		// mapped before failing — a single FreeProcessPageTable walk covers the
		// L0, the on-demand L1/L2/L3 tables, and the leaf frames (MAZ-108). No
		// PID/Shepherd slot has been allocated yet (CreateUserspaceThread runs
		// below), so nothing else needs reclaiming.
		kmem.FreeProcessPageTable(childL0PA)
		klog.Errf("[CloneExec] ERROR: loadELF failed err=%v\n", err)
		maybeWakeVforkParentOnFailure(req, ceENOEXEC)
		return ceENOEXEC
	}

	kmem.InvalidateAllICache()
	SetUserspaceActive()
	kmem.FinalUserspaceSync()

	// Create the child's first thread. This atomically allocates the child PID
	// + Shepherd slot under schedulerLock, populates the race-sensitive startup
	// state (ParentPID + buffered intent + chdir target) via SetStartupState,
	// and enqueues the thread to the ready queue — all under the lock (see
	// createCloneExecThreadImpl). Returning the PID directly lets us skip the
	// O(n) L0PA scan DoRunShepherdWork uses.
	//
	// MAZ-112 intent-visibility: populating the race-sensitive fields before
	// enqueue closes the window in which a consumer racing the child's first
	// instruction (MAZ-113 is the future reader) could observe ParentPID /
	// StartupIntent / StartupCwd unset. The non-race-sensitive fields below
	// (SymbolTable / HighestVA / Filename, the uring trio, Spans) stay here on
	// the worker thread, OUTSIDE schedulerLock — AllocUringIPCRing is not
	// nosplit, allocates pages, and takes the buddy lock, so it must never run
	// under the scheduler spinlock.
	// parentPID is the SID of the real parent process. For vfork calls, this is
	// req.ParentSID (forkexectest); for boot/non-vfork, it falls back to the
	// CallerShepherd's PID (linux delegate). Using the correct parentPID ensures
	// the child's ParentPID field is set so EventChildExit reaches the right shepherd.
	parentPID := req.CallerShepherd.PID
	if req.ParentSID != 0 {
		parentPID = req.ParentSID
	}
	_, newPID := CreateCloneExecThread(loadedProc.EntryPoint, loadedProc.StackTop, childL0PA,
		parentPID, req.ReservedPID, req.Intent, req.Cwd, req.StdioRedirectMask)

	// O(1) lookup of the freshly created Shepherd (Allocate just registered it
	// under newPID). ok is guaranteed true — the slot was allocated under the
	// lock moments ago — but the nil-check documents the invariant and avoids a
	// nil deref if it is ever violated.
	p, ok := proc.Shepherds.Get(newPID)
	if !ok {
		klog.Errf("[CloneExec] ERROR: child shepherd %d not found after thread create\n", newPID)
		maybeWakeVforkParentOnFailure(req, ceENOMEM)
		return ceENOMEM
	}

	// Non-race-sensitive child setup: introspection fields, the IPC ring, and
	// the VA spans for death-time cleanup. None of these can be observed by the
	// child before its runtime boots (it cannot IPC or die first), so they stay
	// lock-free here.
	p.SymbolTable = symTable
	p.HighestVA = highestVA
	p.Filename = filename

	// Allocate the child's IPC uring ring.
	uringID := allocateUringID()
	p.UringID = uringID
	allocateUringIPCRing(p, 0)
	registerUringID(uringID, int16(p.PID))

	// Register kernel-allocated VA ranges so CleanupShepherdPages reclaims
	// the ELF segments + stack on the child's death. The framebuffer and the
	// constraint pages are intentionally NOT added — both are system-lifetime
	// shared resources mapped into every shepherd from a single global PA, so
	// Phase 1 must never releasePageByPA them (the first shepherd to die would
	// free the shared pages; later deaths would underflow). Phase 2 still unmaps
	// their PTEs from the dying shepherd. (MAZ-127: this child is the first
	// shepherd that actually dies, which is why the latent framebuffer
	// double-free surfaced here — see runshepherd.go for the full note.)
	for j := 0; j < loadedProc.SegmentCount; j++ {
		p.Spans.Add(loadedProc.SegmentSpans[j].VA, loadedProc.SegmentSpans[j].Size)
	}
	p.Spans.Add(loadedProc.StackBase, 64*1024)

	// Link the child into the parent's child list. Best-effort: an overflow
	// (parent already has MaxChildrenPerShepherd live children) leaves the
	// child running but unattached — wait4 won't observe it. MAZ-78/79
	// revisits this once the child lifecycle is fully wired.
	if aerr := req.CallerShepherd.AddChild(newPID); aerr != nil {
		klog.Errf("[CloneExec] WARN: AddChild(parent=%d child=%d) failed: %v\n",
			req.CallerShepherd.PID, newPID, aerr)
	}

	// On vfork success (MAZ-127): wake the parked parent with the child PID.
	// The transient thread (current thread) is reaped (never returns to userspace).
	// On non-vfork paths (boot), this is a no-op (no reserved PID or parent linkage).
	if req.IsVforkCall() {
		// Vfork success: wake the parked parent. Use req.TransientTID — this
		// function runs on the kernel worker (thread 0), so GetCurrentThreadTID()
		// here would return the worker's TID, not the transient's.
		wakeVforkParent(int16(req.TransientTID), int64(newPID))
	}

	// Deliver EventExecComplete to the linux shepherd (MAZ-71). The linux shepherd
	// manages reaper.RegisterChild and wait4 bookkeeping for all shepherds —
	// it reads from its OWN ring (LinuxDelegateSID), not the parent's ring.
	// The event carries ParentPid for routing inside the linux shepherd.
	// The returned switch target is discarded — see kernelPublishProcessNotify.
	linuxSID := proc.ShepherdId(LinuxDelegateSID())
	if linuxSID >= 0 {
		kernelPublishProcessNotify(linuxSID, proc.NotificationEvent{
			Type:      proc.EventExecComplete,
			Pid:       int16(newPID),
			ParentPid: int16(req.CallerShepherd.PID),
		})
	}

	// MAZ-149: fork/exec success trace on the interrupt-driven ring (Logf), not
	// the slow direct-poll path (Criticalf). Now that fork/exec is working, a
	// fork/exec-heavy workload must not inflate the slow UART path.
	klog.Logf("[CloneExec] ok child=%d parent=%d entry=0x%x\n",
		newPID, req.CallerShepherd.PID, loadedProc.EntryPoint)
	return int64(newPID)
}

// validateCloneExecELFHeader checks the ELF identity from the first page of
// the target image. Returns 0 if it is a loadable 64-bit ELF for this
// architecture, or a negative errno otherwise (ENOEXEC for a bad format,
// EFAULT if the header copy itself failed). totalBytes is the full declared
// image size, checked against the 64-byte ELF-header minimum.
func validateCloneExecELFHeader(header []byte, totalBytes int) int64 {
	if header == nil {
		return ceEFAULT
	}
	if totalBytes < 64 || len(header) < 64 {
		return ceENOEXEC
	}
	hdr := parseELFHeader(header)
	if hdr.Magic != ELF_MAGIC {
		return ceENOEXEC
	}
	if hdr.Class != ELF_CLASS64 || hdr.Machine != elfExpectedMachine {
		return ceENOEXEC
	}
	return 0
}

// cloneExecFaithful builds the faithful argv/envp layout setupUserStack lays out
// for the execve child (MAZ-120): the caller's full Argv (argv[0] = program
// name) and Envp verbatim, never the shepherd-launch {filename, shepherdStr}.
//
// A faithful execve always carries argv[0] (the program name); but if a caller
// hands an empty Argv, fall back to a single-element argv built from the
// resolved filename so the child still has a valid argv[0] rather than argc=0.
func cloneExecFaithful(req *proc.CloneExecRequest, filename string) *cloneExecLayout {
	argv := req.Argv
	if len(argv) == 0 {
		argv = [][]byte{[]byte(filename)}
	}
	return &cloneExecLayout{
		Argv: argv,
		Envp: req.Envp,
	}
}
