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
//  4. creates the child's first thread (which atomically allocates the child
//     PID + Shepherd slot under schedulerLock),
//  5. wires parent/child links (MAZ-70), STORES the buffered intent on the
//     child Shepherd for shepherd-side application (MAZ-78/79), registers VA
//     spans + the IPC ring, and
//  6. delivers EventExecComplete to the parent (MAZ-71).
//
// It mirrors DoRunShepherdWork (runshepherd.go) — the proven shepherd-launch
// path — and lives in this package for the same reason: it needs the
// unexported ELF/address-space helpers (loadELF, copyPagesFromUser,
// parseELFHeader, setupUserStack via loadELF, buildSymbolTable, …).
//
// Error-teardown scope (MAZ-75 ships option (a): happy-path + best-effort).
// The kmem inverse-teardown primitives needed to fully unwind a partial init
// (FreeProcessPageTable, an UndoELFLoad handle, a partial-Spans cleanup) do
// not exist yet; building them is split out to MAZ-108. The two leak sites on
// the failure path below carry TODO(MAZ-108) markers describing exactly what
// leaks. Note the allocation order: the child PID + Shepherd slot are not
// created until CreateUserspaceThread, which runs AFTER the only realistic
// late-init failure (loadELF) — so a loadELF failure leaks only address-space
// memory, never a PID or Shepherd slot.
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
	ceE2BIG        int64 = -7  // E2BIG — too many buffered intent ops
	ceENAMETOOLONG int64 = -36 // ENAMETOOLONG — chdir target path too long
)

// DoCloneExecWork performs the combined clone+exec on the kernel worker
// thread (thread 0's growable stack, via the KernelSVCWorker relay). Returns
// the new child PID on success or a negative errno on failure.
func DoCloneExecWork(req *proc.CloneExecRequest) int64 {
	if req.CallerShepherd == nil {
		klog.Errf("[CloneExec] ERROR: nil CallerShepherd\n")
		return ceEFAULT
	}
	// Cap-check the buffered intent + cwd before allocating anything. These
	// come from the shepherd-side caller; rejecting an oversized request up
	// front keeps the failure free of teardown.
	if len(req.Intent) > proc.MaxStartupIntentOps {
		klog.Errf("[CloneExec] ERROR: intent ops %d > max %d\n", len(req.Intent), proc.MaxStartupIntentOps)
		return ceE2BIG
	}
	if len(req.Cwd) > proc.MaxStartupCwdBytes {
		klog.Errf("[CloneExec] ERROR: cwd len %d > max %d\n", len(req.Cwd), proc.MaxStartupCwdBytes)
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
		return errno
	}

	hdr := parseELFHeader(elfData)

	// Fresh address space for the child.
	childL0PA := kmem.CreateProcessPageTable()
	if childL0PA == 0 {
		klog.Errf("[CloneExec] ERROR: CreateProcessPageTable failed\n")
		return ceENOMEM
	}

	// Map the framebuffer + constraint shared pages into the child, the same
	// surface every shepherd gets.
	fbPA := gpu.GetFramebufferPA()
	fbSize := uintptr(gpu.GetFramebufferSize())
	if !kmem.MapUserFramebufferWithL0(fbPA, fbSize, childL0PA) {
		// TODO(MAZ-108): release childL0PA via kmem.FreeProcessPageTable once
		// it exists. Currently leaks ~16 KiB of page-table memory on this
		// failure path. See findings.md (MAZ-75) and MAZ-108.
		klog.Errf("[CloneExec] ERROR: MapUserFramebufferWithL0 failed\n")
		return ceENOMEM
	}
	if !kmem.MapUserConstraintPagesWithL0(childL0PA) {
		// TODO(MAZ-108): release childL0PA + undo the framebuffer mapping via
		// kmem.FreeProcessPageTable once it exists. Currently leaks the page
		// table + framebuffer mapping. See findings.md (MAZ-75) and MAZ-108.
		klog.Errf("[CloneExec] ERROR: MapUserConstraintPagesWithL0 failed\n")
		return ceENOMEM
	}

	InitKernelAttrManager()
	symTable := buildSymbolTable(elfData, &hdr)
	highestVA := findHighestVA(elfData, &hdr)

	// Load the ELF segments + user stack into the child page table. argv is
	// threaded through loadELF's extraArgs; faithful custom argv[0]/envp is
	// deferred to MAZ-79 (the refined MAZ-75 DoD only requires the child to
	// run the target binary — see task_plan.md). The child still gets the
	// mandatory mazzy runtime env (GODEBUG/GOMEMLIMIT/…) that setupUserStack
	// injects, which it needs to run correctly under this kernel.
	loadedProc, err := loadELF(elfData, filename, childL0PA, 0, cloneExecExtraArgs(req))
	if err != nil {
		// TODO(MAZ-108): undo loadELF's segment + stack mappings and release
		// childL0PA via kmem.UndoELFLoad + kmem.FreeProcessPageTable once they
		// exist. Currently leaks the page table plus whatever segment/stack
		// pages loadELF mapped before failing. No PID/Shepherd slot has been
		// allocated yet (CreateUserspaceThread runs below), so nothing else
		// needs reclaiming. See findings.md (MAZ-75) and MAZ-108.
		klog.Errf("[CloneExec] ERROR: loadELF failed err=%v\n", err)
		return ceENOEXEC
	}

	kmem.InvalidateAllICache()
	SetUserspaceActive()
	kmem.FinalUserspaceSync()

	// Create the child's first thread. This atomically allocates the child
	// PID + Shepherd slot under schedulerLock and enqueues the thread to the
	// ready queue (see createUserspaceThreadImpl). We locate the freshly
	// created Shepherd below by matching its page-table root, exactly as
	// DoRunShepherdWork does.
	CreateUserspaceThread(loadedProc.EntryPoint, loadedProc.StackTop, childL0PA)

	// Find the new child Shepherd by its L0PA, then wire parent/child links,
	// store the buffered intent, register VA spans for cleanup, and allocate
	// its IPC ring — mirrors DoRunShepherdWork's post-creation setup.
	//
	// Intent-visibility note: the child thread is already on the ready queue
	// when we set ParentPID / StartupIntent below, so a consumer racing the
	// child's first instruction could observe them unset. This is latent in
	// MAZ-75 — no consumer of StartupIntent exists yet (the startup stub that
	// reads it lands in MAZ-78/79), and the xfertest reads these fields only
	// after clone_exec returns. Closing the window (populate under
	// schedulerLock before the thread is enqueued) is Item 6 / MAZ-78/79.
	var newPID proc.ShepherdId
	proc.Shepherds.ForEach(func(p *proc.Shepherd) bool {
		if p.PageTableL0PA != childL0PA {
			return true // keep iterating
		}
		newPID = p.PID
		p.SymbolTable = symTable
		p.HighestVA = highestVA
		p.Filename = filename

		// Parent/child wiring (MAZ-70).
		p.ParentPID = req.CallerShepherd.PID

		// STORE the buffered in-child intent. The kernel only stores it; the
		// child's startup stub (MAZ-78/79) applies the dup3/close/F_SETFD ops
		// against the shepherd-side FD table and the chdir before exec'ing the
		// target entry. Trailing slots stay IntentNone (zeroed by Allocate).
		n := copy(p.StartupIntent[:], req.Intent)
		p.NumStartupIntent = uint32(n)
		c := copy(p.StartupCwd[:], req.Cwd)
		p.StartupCwdLen = uint32(c)

		// Allocate the child's IPC uring ring.
		uringID := allocateUringID()
		p.UringID = uringID
		allocateUringIPCRing(p, 0)
		registerUringID(uringID, int16(p.PID))

		// Register kernel-allocated VA ranges so CleanupShepherdPages reclaims
		// the ELF segments + stack + framebuffer on the child's death.
		// Constraint pages are intentionally NOT added — they are a
		// system-lifetime shared resource (PD_PINNED), released by no shepherd.
		for j := 0; j < loadedProc.SegmentCount; j++ {
			p.Spans.Add(loadedProc.SegmentSpans[j].VA, loadedProc.SegmentSpans[j].Size)
		}
		p.Spans.Add(loadedProc.StackBase, 64*1024)
		p.Spans.Add(UserFramebufferVA, UserFramebufferSize)

		return false // found it — stop
	})

	if newPID == 0 {
		// Should be impossible: CreateUserspaceThread just created a shepherd
		// with this L0PA. Treat as a hard error rather than returning a bogus
		// PID. The child thread is already on the ready queue and its page
		// table is live — this deep late-init failure is scoped to MAZ-108.
		klog.Errf("[CloneExec] ERROR: child shepherd not found after thread create L0PA=0x%x\n", childL0PA)
		return ceENOMEM
	}

	// Link the child into the parent's child list. Best-effort: an overflow
	// (parent already has MaxChildrenPerShepherd live children) leaves the
	// child running but unattached — wait4 won't observe it. MAZ-78/79
	// revisits this once the child lifecycle is fully wired.
	if aerr := req.CallerShepherd.AddChild(newPID); aerr != nil {
		klog.Errf("[CloneExec] WARN: AddChild(parent=%d child=%d) failed: %v\n",
			req.CallerShepherd.PID, newPID, aerr)
	}

	// Deliver EventExecComplete to the parent (MAZ-71). The event lands in the
	// parent's ring; it observes it when it next polls (wait4, MAZ-80). The
	// returned switch target is discarded — see kernelPublishProcessNotify.
	kernelPublishProcessNotify(req.CallerShepherd.PID, proc.NotificationEvent{
		Type:      proc.EventExecComplete,
		Pid:       int16(newPID),
		ParentPid: int16(req.CallerShepherd.PID),
	})

	klog.Criticalf("[CE]", "[CloneExec] ok child=%d parent=%d entry=0x%x\n",
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

// cloneExecExtraArgs converts the request's argv (minus argv[0], which loadELF
// supplies from filename) into the []string extraArgs that setupUserStack
// lays out. v1 fidelity note: setupUserStack additionally injects the shepherd
// number as argv[1] and the mandatory mazzy runtime env; faithful execve
// argv[0]/envp is deferred to MAZ-79. See task_plan.md.
func cloneExecExtraArgs(req *proc.CloneExecRequest) []string {
	if len(req.Argv) <= 1 {
		return nil
	}
	args := make([]string, 0, len(req.Argv)-1)
	for _, a := range req.Argv[1:] {
		args = append(args, string(a))
	}
	return args
}
