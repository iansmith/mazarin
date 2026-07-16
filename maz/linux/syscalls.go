package main

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"mazzy/maz/linux/internal/cloneexec"
	"mazzy/maz/linux/internal/fdtable"
	"mazzy/maz/linux/internal/pipe"
	"mazzy/maz/linux/internal/wait"
	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/sys"
	"mazzy/shared/dlist"
	"mazzy/shared/ipc"
	"mazzy/shared/linuxabi"
	"mazzy/shared/sysid"
)

// isZapPath returns true for bleve scorch *.zap segment files.
// Diagnostic-only: gates the [lin:openat .zap] traces so we can
// compare resolved paths between create and reopen of the same
// segment (path-resolution-divergence hypothesis).
func isZapPath(p string) bool {
	return strings.HasSuffix(p, ".zap")
}

// syscallHandler processes delegated file syscalls for the linux shepherd.
// Per-shepherd filesystem state (FD tables, flocks, CWD) is tracked in
// ShepherdFilesystemData, keyed by caller SID.
//
// mu protects the cross-shepherd maps (shepherds, orphanHandles). Per-shepherd
// state is locked separately via ShepherdFilesystemData.mu, which is the lock
// held for the lifetime of a handler. flocks and cache have their own
// internal locking.
type syscallHandler struct {
	mu        sync.Mutex
	shepherds map[int16]*ShepherdFilesystemData
	flocks    *flockTable
	fs        fsclient.FSClient
	cache     *pageCache

	// orphanHandles tracks fs handles whose owning fd has been closed
	// but whose cached mmap pages still need a writeback path. Linux
	// semantics: mappings survive close, so the handle must too. Keyed
	// by sid → inum. Drained in sysMmapPageFlush after the last page
	// for an inum is removed, and on shepherd death.
	orphanHandles map[int16]map[uint32]uint32

	// cloneWindows holds the per-cloning-thread buffering windows (MAZ-118)
	// opened when a process-flavor clone (CLONE_THREAD clear) is forwarded
	// here (MAZ-78). A window stays open across the in-child setup syscalls
	// until the matching execve flushes it (MAZ-79). The parked clone caller
	// blocks in the kernel delegate path until that Reply; until execve lands
	// the only window-closing path is Abort on shepherd death (cleanupShepherd).
	//
	// cloneWindows is keyed by global TID; cloneTIDsBySID maps each caller SID
	// to the TIDs whose windows it owns, so a dying shepherd's still-open
	// windows can be aborted (windows are TID-keyed, cleanup is SID-keyed).
	//
	// cloneMu guards BOTH maps. The per-shepherd ShepherdFilesystemData.mu held
	// across handle() serializes one shepherd's syscalls, but different
	// shepherds run on different goroutines and would touch the single shared
	// Registry concurrently — and cloneexec.Registry is not goroutine-safe (see
	// its package doc). cloneMu is the wrapping mutex that doc prescribes.
	cloneMu        sync.Mutex
	cloneWindows   *cloneexec.Registry
	cloneTIDsBySID map[int16]map[int32]struct{}

	// reaper holds the wait4 child bookkeeping (live-child set, zombie set)
	// and the Linux wait-status encoding (MAZ-80). It is goroutine-safe: the
	// wait4 dispatch runs on the file-lane worker pool, while the child-exit /
	// exec-complete notifications that feed it are funneled onto the single
	// delegate-loop goroutine.
	reaper *wait.Reaper

	// waitMu guards waiters. wait4 dispatch (worker pool) stashes a parked
	// request; the delegate-loop child-exit handler wakes it. Separate from
	// reaper's internal lock so the two never nest.
	waitMu sync.Mutex
	// waiters holds parked wait4 callers keyed by (callerSID, callerTID): a
	// thread has at most one outstanding wait4. Each entry remembers the
	// parked SyscallRequest (replied to later) plus the original pid/options
	// so a child exit can re-run reaper.Decide for that waiter.
	waiters map[waiterKey]wait4Waiter

	// pendingChildFDTs holds the FD tables pre-built for children that have
	// been fork+exec'd but have not yet made their first syscall. It is
	// populated by sysExecve after sys.CloneExec() succeeds (keyed by child
	// SID = child PID) and consumed by getShepherd() on the child's first
	// contact. Protected by mu (the same lock that guards shepherds).
	//
	// This is the MAZ-63 Gap-1 fix: without it, getShepherd creates a fresh
	// empty table for the child, losing the parent's open FDs (including the
	// errpipe write-end that os/exec.Run() waits on for EOF).
	pendingChildFDTs map[int16]*fdtable.Table

	// displayFeed forwards KindStdout/KindStderr bytes to the linux-ui line
	// accumulator (MAZ-149). fd is 1 (stdout) or 2 (stderr) so the display
	// keeps them distinct. Set once at startup (startUringDelegateHandler);
	// nil until then (serial output still works via sys.UartWrite). The impl
	// must NOT block — it drops on a full display channel, matching the old
	// fire-and-forget stdout lane — and must own its bytes (the caller's data
	// page is reclaimed on Reply, so displayFeed copies).
	displayFeed func(fd byte, data []byte)

	// parkedPipeWriters registers every write request currently parked on a
	// full pipe (MAZ-149 writer-park backpressure), guarded by pipeParkMu.
	// It exists for the bounded-park timeout: pipeParkWatchdog sweeps entries
	// older than pipeWriterParkTimeout, drops their bytes (MarkDrop `@\`
	// breadcrumb on the buffer tail) and releases the writer. Removal from
	// this registry is the fulfilled-or-abandoned signal — wakePipeWriters
	// skips tokens no longer registered (the watchdog already replied).
	pipeParkMu        sync.Mutex
	parkedPipeWriters map[*pipeWriteWaiter]struct{}
}

// waiterKey identifies a single parked wait4 caller thread. A thread blocks on
// at most one wait4 at a time, so (SID, TID) is a unique key.
type waiterKey struct {
	sid int16
	tid int16
}

// wait4Waiter is a parked wait4 caller: the stashed request (whose Reply is
// deferred until a matching child exits) plus the wait4 selector so the
// child-exit handler can re-decide whether this exit satisfies the wait.
type wait4Waiter struct {
	req     sys.SyscallRequest
	pid     int64 // wait4 pid arg: >0 specific child, -1 any child
	options int   // wait4 options (WNOHANG etc.)
}

func newSyscallHandler(fs fsclient.FSClient) *syscallHandler {
	return &syscallHandler{
		shepherds:        make(map[int16]*ShepherdFilesystemData),
		flocks:           newFlockTable(),
		fs:               fs,
		cache:            newPageCache(),
		orphanHandles:    make(map[int16]map[uint32]uint32),
		cloneWindows:     cloneexec.New(),
		cloneTIDsBySID:   make(map[int16]map[int32]struct{}),
		reaper:           wait.New(),
		waiters:          make(map[waiterKey]wait4Waiter),
		pendingChildFDTs: make(map[int16]*fdtable.Table),

		parkedPipeWriters: make(map[*pipeWriteWaiter]struct{}),
	}
}

// markOrphanHandle records that fs handle is being kept alive past sysClose
// because cache pages for inum still reference it.
func (h *syscallHandler) markOrphanHandle(sid int16, inum, handle uint32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.orphanHandles[sid]
	if !ok {
		m = make(map[uint32]uint32)
		h.orphanHandles[sid] = m
	}
	m[inum] = handle
}

// closeOrphanHandleIfDrained closes the orphan fs handle for (sid, inum) if
// the cache no longer has pages for it. Called after RemoveBatch rounds.
func (h *syscallHandler) closeOrphanHandleIfDrained(sid int16, inum uint32) {
	h.mu.Lock()
	m, ok := h.orphanHandles[sid]
	if !ok {
		h.mu.Unlock()
		return
	}
	handle, ok := m[inum]
	if !ok {
		h.mu.Unlock()
		return
	}
	if h.cache.HasPagesFor(sid, inum) {
		h.mu.Unlock()
		return
	}
	delete(m, inum)
	if len(m) == 0 {
		delete(h.orphanHandles, sid)
	}
	h.mu.Unlock()
	// Close happens outside the lock — fsclient is self-locking and we
	// must not hold h.mu across an IPC round-trip.
	_ = h.fs.Close(handle)
}

// closeAllOrphanHandlesForSID closes every orphan handle for sid. Used in
// shepherd death cleanup after the cache is fully drained.
func (h *syscallHandler) closeAllOrphanHandlesForSID(sid int16) {
	h.mu.Lock()
	m, ok := h.orphanHandles[sid]
	if !ok {
		h.mu.Unlock()
		return
	}
	handles := make([]uint32, 0, len(m))
	for _, handle := range m {
		handles = append(handles, handle)
	}
	delete(h.orphanHandles, sid)
	h.mu.Unlock()
	// Close handles outside the lock.
	for _, handle := range handles {
		_ = h.fs.Close(handle)
	}
}

// getShepherd returns the per-shepherd filesystem state for the given SID,
// creating it lazily on first contact. The returned pointer is stable for
// the lifetime of the shepherd; callers acquire .mu on it for the duration
// of a handler.
// shepherdExists reports whether per-SID state already exists (MAZ-156
// forensics helper — does not create).
func (h *syscallHandler) shepherdExists(sid int16) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.shepherds[sid] != nil
}

func (h *syscallHandler) getShepherd(sid int16) *ShepherdFilesystemData {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.shepherds[sid]
	if s == nil {
		// MAZ-63 Gap-1: if sysExecve stashed a pre-built FDT for this child
		// (parent's table copied + cloexec-swept), use it instead of a fresh
		// empty table. This gives the child its inherited stdin/stdout/stderr
		// plus any other FDs the parent had open, with O_CLOEXEC FDs already
		// closed (so the errpipe write-end is gone and os/exec.Run() unblocks).
		//
		// MAZ-156: a fork/exec child can reach this consume BEFORE sysExecve's
		// post-CloneExec code runs (the child starts inside the SVC). That is
		// safe ONLY because the pending table is fully finalized pre-SVC
		// (execve.go step 5c) — do not move finalization back after the SVC.
		fdt := h.pendingChildFDTs[sid]
		if fdt != nil {
			delete(h.pendingChildFDTs, sid)
		} else {
			fdt = fdtable.New()
		}
		s = &ShepherdFilesystemData{
			SID:   sid,
			FDT:   fdt,
			Locks: dlist.New[*flockEntry](),
		}
		h.shepherds[sid] = s
	}
	return s
}

// cleanupShepherd closes all open FDs, releases all flocks, and removes
// the per-shepherd state for the given SID. Called on shepherd death.
func (h *syscallHandler) cleanupShepherd(sid int16) {
	// Abort any clone buffering windows still open for this shepherd's threads.
	// A process-flavor clone that never reached its matching execve (the caller
	// died first) leaves a parked window; without this the window would leak
	// (MAZ-78 DoD item 4). The parked caller itself is unblocked kernel-side by
	// CleanupDelegateForDeadShepherd — it is part of the dying shepherd.
	h.abortCloneWindowsForSID(sid)

	// Drop any wait4 waiters owned by this shepherd (and its child bookkeeping):
	// the parked caller is woken kernel-side with -ESRCH, so a stale stashed
	// request would otherwise double-Reply (MAZ-80).
	h.dropWait4Waiters(sid)

	h.mu.Lock()
	s := h.shepherds[sid]
	pendingFDT := h.pendingChildFDTs[sid]
	delete(h.pendingChildFDTs, sid)
	if s == nil {
		h.mu.Unlock()
		if pendingFDT != nil {
			// Child died before its first syscall: close any non-cloexec pipe ends
			// it inherited (cloexec ends were already swept at exec time). Without
			// this the shared pipe writer counts stay elevated and read ends waiting
			// for EOF (e.g. os/exec errpipe) hang forever.
			pendingFDT.Each(func(_ int, e *fdtable.Entry) bool {
				if e.Pipe != nil {
					e.Pipe.Close()
				}
				return true
			})
		}
		return
	}
	delete(h.shepherds, sid)
	h.mu.Unlock()
	h.tearDownShepherdFDState(sid, s)
}

// tearDownShepherdFDState closes s's fs handles, pipe ends, advisory locks,
// orphan handles, and cache for sid. The caller must have already removed s
// from h.shepherds under h.mu; this is the shared teardown tail for both death
// paths (cleanupShepherd on shepherd death, evictStaleChildShepherd on
// fork/exec PID reuse) so the two can't drift.
func (h *syscallHandler) tearDownShepherdFDState(sid int16, s *ShepherdFilesystemData) {
	// Abandon any pipe writes this shepherd still has parked (MAZ-149): the
	// kernel has already torn down the dead caller's delegate state, so a
	// later watchdog/wake Reply would target a stale — or worse, REUSED —
	// caller TID slot and corrupt an unrelated in-flight delegate (thread
	// IDs still recycle LIFO; MAZ-150 made only shepherd PIDs monotonic).
	// Registry removal IS the abandon signal — wakePipeWriters and the
	// watchdog skip unregistered tokens. No Reply is sent.
	h.dropParkedPipeWritersForSID(sid)

	// Take per-shepherd lock so any in-flight handler for this shepherd
	// (already holding s.mu) drains before we touch its FDT.
	s.mu.Lock()
	// Snapshot fs handles to close outside the lock. Stdio entries (fd 0/1/2)
	// carry Handle == 0, so the filter below skips them.
	handles := make([]uint32, 0, 8)
	s.FDT.Each(func(_ int, e *fdtable.Entry) bool {
		if e.Handle != 0 {
			handles = append(handles, e.Handle)
		}
		// A dying shepherd's still-open pipe ends (e.g. a redirected stdout
		// the process never explicitly close()'d before exit) must drop their
		// writer reference here — otherwise a reader blocked on this pipe
		// (e.g. the parent's os/exec.Output() capture) would wait for EOF
		// forever, since Dup3/Copy give each fd its own *pipe.End reference.
		if e.Pipe != nil {
			e.Pipe.Close()
			if e.Kind == fdtable.KindPipeWrite {
				wakePipeReaders(e.Pipe)
			}
		}
		return true
	})
	// Release all advisory locks.
	h.flocks.releaseAll(s.Locks)
	s.mu.Unlock()
	// Close fs handles outside per-shepherd lock — these are IPC calls.
	for _, handle := range handles {
		h.fs.Close(handle)
	}
	// Close any handles that were kept alive past close() because cached
	// mmap pages were still live. The kernel-driven death-cleanup path
	// (sysMmapPageFlush with fd=allFDs) already ran before this hook, so
	// the cache is fully drained — but the orphan-handle table tracks
	// handles independent of the cache.
	h.closeAllOrphanHandlesForSID(sid)
	h.cache.RemoveAll(sid)
}

// evictStaleChildShepherd tears down any leftover per-SID filesystem state for
// a PID that is about to be handed to a brand-new fork/exec child (MAZ-63).
//
// Why this is needed: a fork/exec child's exit is delivered as an
// EventChildExit process-notification (reaper bookkeeping only); unlike a
// shepherd death it does NOT drive cleanupShepherd, so the exited child's
// h.shepherds[pid] entry survives. The kernel PID allocator, however, frees the
// PID as soon as the child is reaped and can reissue it to the next fork/exec.
// When that happens, getShepherd(pid) would find the *previous* child's stale
// table (wrong stdio/cwd) and never consume the pendingChildFDTs[pid] the new
// child's transient just built — the redirected stdout / chdir silently vanish.
//
// sysExecve calls this for the reserved child PID BEFORE sys.CloneExec
// (step 5c) — the only point where it is genuinely race-free: the reserved
// PID is not a live PID (the allocator only hands out free ones) and the
// child does not exist yet, so any h.shepherds[pid] present is definitively
// the dead predecessor's. MAZ-156: calling this AFTER CloneExec (the old
// step 7a) destroyed the LIVE child's state whenever the child's first
// syscall beat the post-SVC code — the child starts running inside
// CloneExec. Only the bare-exec fallback still calls it post-SVC (child PID
// unknowable earlier; tripwire-logged). It deliberately does NOT touch
// pendingChildFDTs[pid] (the new child's table, keyed identically) or the
// parent-keyed clone-window / wait4-waiter state.
func (h *syscallHandler) evictStaleChildShepherd(sid int16) {
	h.mu.Lock()
	s := h.shepherds[sid]
	if s == nil {
		h.mu.Unlock()
		return
	}
	delete(h.shepherds, sid)
	h.mu.Unlock()
	h.tearDownShepherdFDState(sid, s)
}

// laneInversions counts MAZ-156 file-lane same-SID service-order inversions
// observed since boot. Expected to stay 0 once the ordered dispatcher is in;
// any increment is a regression of the same-SID FIFO guarantee.
var laneInversions atomic.Uint64

// handle dispatches a delegated syscall request to the appropriate handler.
//
// Per-shepherd serialization: the caller's ShepherdFilesystemData.mu is held
// for the lifetime of the dispatch so that one shepherd's syscalls remain
// ordered with respect to each other (matching the historical single-goroutine
// behavior). Different shepherds run on different goroutines and don't block
// on each other. The lock is never held across a fsclient call's response
// wait — fsclient has its own internal mutex that already serializes the
// underlying IPC.
func (h *syscallHandler) handle(req sys.SyscallRequest, seq uint64) {
	shep := h.getShepherd(req.CallerPID)
	shep.mu.Lock()
	defer shep.mu.Unlock()
	// MAZ-156 inversion canary: seq is the per-SID arrival order stamped by
	// the single delegate reader. Service order is shep.mu acquisition order
	// (we are inside it here). A stamp lower than one already serviced means
	// this SID's delegates were reordered between reader and lock — the
	// vfork dup3/chdir-vs-execve race. Monotonic-from-below check only:
	// shepherd-state eviction (evictStaleChildShepherd) resets the field to
	// zero, so gaps are legal; decreases never are.
	if seq < shep.lastDispatchSeq {
		laneInversions.Add(1)
		fmt.Printf("[MAZ-156] INVERSION sid=%d sysid=%d tid=%d seq=%d last=%d total=%d\n",
			req.CallerPID, req.SysID, req.CallerTID, seq, shep.lastDispatchSeq, laneInversions.Load())
	} else {
		shep.lastDispatchSeq = seq
	}
	switch req.SysID {
	// --- Process lifecycle ---
	case sysid.Clone:
		h.sysCloneProcess(req)
	case sysid.Execve:
		h.sysExecve(req)
	case sysid.Wait4:
		h.sysWait4(req)

	// --- Local-only syscalls ---
	case sysid.Close:
		h.sysClose(req)
	case sysid.Lseek:
		h.sysLseek(req)
	case sysid.Getcwd:
		h.sysGetcwd(req)
	case sysid.Chdir:
		h.sysChdir(req)
	case sysid.Fchdir:
		h.sysFchdir(req)
	case sysid.Ioctl:
		h.sysIoctl(req)
	case sysid.Fsync, sysid.Fdatasync:
		h.sysFsync(req)
	case sysid.Flock:
		h.sysFlock(req)
	case sysid.Dup3:
		h.sysDup3(req)
	case sysid.Fcntl:
		h.sysFcntl(req)
	case sysid.Pipe2:
		h.sysPipe2(req)

	// --- Metadata syscalls (via fs IPC) ---
	case sysid.Openat:
		h.sysOpenat(req)
	case sysid.Fstat:
		h.sysFstat(req)
	case sysid.Fstatat:
		h.sysFstatat(req)
	case sysid.Mkdirat:
		h.sysMkdirat(req)
	case sysid.Unlinkat:
		h.sysUnlinkat(req)
	case sysid.Renameat:
		h.sysRenameat(req)
	case sysid.Ftruncate:
		h.sysFtruncate(req)
	case sysid.Getdents64:
		h.sysGetdents64(req)
	case sysid.Faccessat:
		h.sysFaccessat(req)
	case sysid.Fchmodat:
		h.sysFchmodat(req)
	case sysid.Utimensat:
		h.sysUtimensat(req)
	case sysid.Readlinkat:
		h.sysReadlinkat(req)
	case sysid.Statfs:
		h.sysStatfs(req)
	case sysid.Fstatfs:
		h.sysFstatfs(req)

	// --- Data syscalls (via fs IPC) ---
	case sysid.Read:
		h.sysRead(req)
	case sysid.Write:
		h.sysWrite(req)
	case sysid.Writev:
		h.sysWritev(req)
	case sysid.Readv:
		h.sysReadv(req)
	case sysid.Pread64:
		h.sysPread64(req)
	case sysid.Pwrite64:
		h.sysPwrite64(req)
	case sysid.MmapPageFill:
		h.sysMmapPageFill(req)
	case sysid.MmapPageFlush:
		h.sysMmapPageFlush(req)

	default:
		req.Reply(ENOSYS)
	}
}

// ============================================================
// Process lifecycle
// ============================================================

// sysCloneProcess handles a process-flavor clone (CLONE_THREAD clear) forwarded
// here by the kernel dispatch gate (MAZ-78). Thread-flavor clones never reach
// the shepherd — they are served in-kernel by SyscallClone → CloneThread.
//
// It opens a MAZ-118 buffering window keyed by the cloning thread's TID and
// then WITHHOLDS the Reply: the os/exec idiom is clone() → a short run of
// in-child setup syscalls (dup3/close/fcntl/chdir) → execve(). The caller stays
// parked in the kernel delegate path with the window open; the matching execve
// (MAZ-79) flushes the buffered intent and replies to the parked clone. This is
// the deferred-Reply park model (mirrors MAZ-80), the same mechanism the stdin
// drainer uses to defer a read(0) reply.
//
// Defensive guard: a thread-flavor mask arriving here would be a dispatch bug
// (the kernel gate should have routed it to CloneThread); reply ENOSYS rather
// than open a window that no execve will ever flush. A duplicate Open for a TID
// that already has a window (reused before its prior clone resolved) likewise
// fails closed.
func (h *syscallHandler) sysCloneProcess(req sys.SyscallRequest) {
	if !linuxabi.IsProcessClone(req.Args[0]) {
		req.Reply(ENOSYS)
		return
	}
	tid := int32(req.CallerTID)

	h.cloneMu.Lock()
	if err := h.cloneWindows.Open(tid); err != nil {
		h.cloneMu.Unlock()
		req.Reply(EWOULDBLOCK) // EAGAIN: a window for this TID is already open
		return
	}
	tids := h.cloneTIDsBySID[req.CallerPID]
	if tids == nil {
		tids = make(map[int32]struct{})
		h.cloneTIDsBySID[req.CallerPID] = tids
	}
	tids[tid] = struct{}{}
	h.cloneMu.Unlock()

	// No req.Reply here: the caller stays parked until execve flushes the
	// window (MAZ-79) or the window is aborted on shepherd death.
}

// sysWait4 handles wait4(pid, *wstatus, options, *rusage) (MAZ-80).
//
// The reaper decides the outcome from the shepherd-side child bookkeeping:
//   - a reapable zombie  -> write the encoded status, Reply(wpid);
//   - ECHILD             -> Reply(ECHILD);
//   - WNOHANG, no zombie -> Reply(0) immediately;
//   - blocking, no zombie but a live child -> PARK (defer the Reply). The
//     caller stays blocked in the kernel delegate path until a matching
//     EventChildExit arrives and the delegate-loop handler reaps it.
//
// Parking is the deferred-Reply trick (no new primitive): stash the request
// by value and do NOT call Reply. wakeWait4OnChildExit later finishes it.
//
// rusage (req.Args[3]) is not yet populated — wait4 succeeds and leaves the
// caller's *rusage untouched. We deliberately do NOT fail non-NULL rusage:
// the common wait4(pid, &status, 0, &rusage) idiom would then break, which is
// worse than leaving rusage unpopulated. Filling it needs a second copy-back
// buffer through the kernel delegate path and is tracked as a follow-up
// (MAZ-125).
func (h *syscallHandler) sysWait4(req sys.SyscallRequest) {
	pid := int64(int32(req.Args[0])) // wait4 pid: sign-extend the 32-bit arg
	options := int(req.Args[2])
	parent := req.CallerPID

	// Hold waitMu across BOTH the Decide and the waiter insert so a child
	// exit cannot interleave between "decided to park" and "parked". Without
	// this, a zombie added (and its waiters scanned) in that window would miss
	// this waiter and leave the caller parked forever. wakeWait4OnChildExit
	// also takes waitMu before re-deciding, so the two are mutually exclusive.
	// Lock order is always waitMu -> reaper.mu (Decide takes reaper.mu).
	h.waitMu.Lock()
	out := h.reaper.Decide(int32(parent), pid, options)
	if out.MustPark {
		h.waiters[waiterKey{sid: parent, tid: req.CallerTID}] = wait4Waiter{
			req:     req,
			pid:     pid,
			options: options,
		}
		h.waitMu.Unlock()
		return
	}
	h.waitMu.Unlock()
	replyWait4(req, out)
}

// replyWait4 finishes a wait4 request that did NOT park: an error, a WNOHANG
// no-op, or a successful reap. On a reap it writes the 4-byte encoded status
// into the kernel-provided data page (which the kernel copies back to the
// caller's *wstatus on Reply with a positive return value).
func replyWait4(req sys.SyscallRequest, out wait.Outcome) {
	if out.Errno != 0 {
		req.Reply(int64(out.Errno))
		return
	}
	// WNOHANG no-op (WPid == 0): nothing reaped, nothing to write back.
	if out.WPid > 0 {
		writeWaitStatus(req, out.EncodedStatus)
	}
	req.Reply(int64(out.WPid))
}

// writeWaitStatus writes the encoded wait status as a little-endian 4-byte
// _C_int into the head of the delegate data page. The kernel copies these 4
// bytes back to the caller's *wstatus pointer (wait4 arg1) on Reply. If the
// caller passed a NULL *wstatus, the kernel allocated no data page and
// DataBuf() is short — guard on length.
func writeWaitStatus(req sys.SyscallRequest, encoded int) {
	buf := req.DataBuf()
	if len(buf) < waitStatusBytes {
		return
	}
	binary.LittleEndian.PutUint32(buf[:waitStatusBytes], uint32(int32(encoded)))
}

// waitStatusBytes is the size of the Linux wait status written back to the
// caller's *wstatus: a 4-byte _C_int.
const waitStatusBytes = 4

// wakeWait4OnChildExit is called on the delegate loop after a child exit has
// been recorded in the reaper. It re-decides every parked waiter belonging to
// the exited child's parent and resolves all that the exit now satisfies.
//
// A single exit can satisfy more than one waiter: an any-child waiter reaps the
// zombie, while a different waiter blocked on that same (now reaped, now gone)
// specific PID must resolve to ECHILD rather than stay parked until some
// unrelated future exit. Re-running Decide per waiter keeps each correct — the
// reap-exactly-once delete happens inside Decide, so only one waiter actually
// reaps the zombie and the rest see the post-reap state.
func (h *syscallHandler) wakeWait4OnChildExit(parent int32) {
	h.waitMu.Lock()
	defer h.waitMu.Unlock()
	for key, w := range h.waiters {
		if int32(key.sid) != parent {
			continue
		}
		out := h.reaper.Decide(parent, w.pid, w.options)
		if out.MustPark {
			continue // this exit does not satisfy this waiter
		}
		delete(h.waiters, key) // map delete during range is safe in Go
		replyWait4(w.req, out)
	}
}

// handleProcessNotification processes a kernel process-lifecycle event on the
// delegate-loop goroutine (MAZ-80). It updates the reaper bookkeeping and, for
// a child exit, wakes any parked wait4 the exit satisfies.
//
//   - EventExecComplete (a child finished execve and is running): register the
//     child as live so a later wait4 can tell "no such child" (ECHILD) apart
//     from "child alive, not yet exited" (block).
//   - EventChildExit (a child died): record the zombie with its RAW exit code,
//     then re-decide parked waiters for that parent and reap one if satisfied.
func (h *syscallHandler) handleProcessNotification(ev ipc.ProcessNotification) {
	switch ev.Type {
	case ipc.NotifyTypeExecComplete:
		h.reaper.RegisterChild(int32(ev.ParentPid), int32(ev.Pid))
	case ipc.NotifyTypeChildExit:
		h.reaper.AddZombie(int32(ev.ParentPid), int32(ev.Pid), int(ev.ExitStatus))
		h.wakeWait4OnChildExit(int32(ev.ParentPid))
		// Prompt EOF (MAZ-149): the exited child's FD state — including any
		// redirected-stdout pipe write-end — is torn down now so a parent
		// blocked on cmd.Output()'s capture pipe sees EOF. Without this the
		// pipe writer count stays elevated until the PID is reused (or never),
		// and the parent's read hangs. This runs on the delegate-loop goroutine
		// (serial with fork/exec) and reuses the same teardown as the PID-reuse
		// path; it removes only h.shepherds[pid] (not pendingChildFDTs), so a
		// concurrently-reused PID's freshly-primed table is untouched.
		h.evictStaleChildShepherd(int16(ev.Pid))
	}
}

// dropWait4Waiters drops every parked wait4 waiter owned by sid (as the
// waiting parent). Called on shepherd death: the parked caller is unblocked
// kernel-side by the delegate death-cleanup (-ESRCH), so the shepherd must
// discard its stale stashed request to avoid a double-Reply. Idempotent.
func (h *syscallHandler) dropWait4Waiters(sid int16) {
	h.waitMu.Lock()
	for key := range h.waiters {
		if key.sid == sid {
			delete(h.waiters, key)
		}
	}
	h.waitMu.Unlock()
	h.reaper.DropParent(int32(sid))
}

// abortCloneWindowsForSID drops every clone buffering window owned by sid's
// threads. Called from cleanupShepherd on shepherd death so a clone that never
// reached its execve does not leak a window (MAZ-78 DoD item 4). Idempotent.
func (h *syscallHandler) abortCloneWindowsForSID(sid int16) {
	h.cloneMu.Lock()
	defer h.cloneMu.Unlock()
	for tid := range h.cloneTIDsBySID[sid] {
		h.cloneWindows.Abort(tid)
	}
	delete(h.cloneTIDsBySID, sid)
}

// ============================================================
// Local-only syscalls
// ============================================================

func (h *syscallHandler) sysClose(req sys.SyscallRequest) {
	fdt := h.resolveTargetFDT(req)
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind == fdtable.KindStdin || e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		req.Reply(EOK)
		return
	}
	if e.Kind == fdtable.KindPipeRead || e.Kind == fdtable.KindPipeWrite {
		// Closing a write end decrements the shared writer count, so a reader
		// blocked on this pipe now drains to EOF. Wake any parked reader before
		// freeing the slot. Pipe ends carry no fs handle / WriteBuf, so they
		// skip releaseFDResources entirely.
		e.Pipe.Close()
		if e.Kind == fdtable.KindPipeWrite {
			wakePipeReaders(e.Pipe)
		}
		fdt.Free(fd)
		req.Reply(EOK)
		return
	}
	h.releaseFDResources(req.CallerPID, fd, e)
	fdt.Free(fd)
	req.Reply(EOK)
}

// releaseFDResources runs the close-time disposition for a single open file
// entry — flushing any pending write buffer and releasing (or orphaning) the
// fs-side handle — WITHOUT freeing the table slot. sysClose calls it then
// frees the slot; sysDup3 calls it for the entry it is about to overwrite at
// newfd. Stdio entries carry no handle/WriteBuf, so callers gate on Kind
// before invoking it.
func (h *syscallHandler) releaseFDResources(sid int16, fd int, e *fdtable.Entry) {
	if e.WriteBuf != nil && len(e.WriteBuf) > 0 {
		h.flushWriteBuf(sid, fd, e) // best-effort; ignore error on close
	}
	// Linux semantics: mmap'd pages survive close. If the cache still has
	// pages for this inode, leave the fs handle open so the eventual
	// munmap (or shepherd death) can flush dirty pages back to disk via
	// that handle. The page cache stores its own copy of `handle` in
	// every entry, so even after fdt.Free() the writeback path keeps
	// working. The handle is released by closeOrphanHandleIfDrained when
	// the last page for this inum is removed, or by death cleanup.
	if e.Handle != 0 {
		if h.cache.HasPagesFor(sid, e.Inum) {
			h.markOrphanHandle(sid, e.Inum, e.Handle)
		} else {
			h.fs.Close(e.Handle)
		}
	}
}

// getOrCreatePendingChildFDT returns h.pendingChildFDTs[pid], building it via
// fallback and storing it first if absent. Shared by resolveTargetFDT (a vfork
// transient's pre-execve FD/cwd setup) and sysExecve (picking up whichever
// table — freshly built or already primed by pre-execve setup — a just-forked
// child should get), so the map is only ever get-or-created in one place.
//
// childStdioRedirectMask (execve.go) deliberately does NOT use this helper:
// it needs a pre-SVC PEEK, and get-or-create would pay fdt.Copy()'s pipe
// writer-ref Fork() increments (and store the table) before knowing the exec
// succeeds — leaking writer refs on the failure paths. If this map's locking
// or key semantics change (e.g. MAZ-151), update that peek in lockstep.
func (h *syscallHandler) getOrCreatePendingChildFDT(pid int16, fallback func() *fdtable.Table) *fdtable.Table {
	h.mu.Lock()
	defer h.mu.Unlock()
	fdt := h.pendingChildFDTs[pid]
	if fdt == nil {
		fdt = fallback()
		h.pendingChildFDTs[pid] = fdt
	}
	return fdt
}

// resolveTargetFDT returns the FD table a delegated dup3/fcntl(F_SETFD)/chdir/
// close/fchdir/openat call should mutate. A vfork transient (running between
// its clone and the matching execve) shares its parent's SID for normal
// delegate routing (MAZ-127's in-kernel vfork model never gives it an
// identity of its own), so without this indirection the child's pre-execve
// FD-redirect/cwd setup would silently mutate the live parent's table instead
// of the child's. Ordinary (non-vfork) callers are unaffected:
// GetVforkReservedPID returns 0 and this is exactly
// h.getShepherd(req.CallerPID).FDT, same as before.
func (h *syscallHandler) resolveTargetFDT(req sys.SyscallRequest) *fdtable.Table {
	parentFDT := h.getShepherd(req.CallerPID).FDT
	reservedPID := sys.GetVforkReservedPID(req.CallerTID)
	if reservedPID == 0 {
		return parentFDT
	}
	return h.getOrCreatePendingChildFDT(reservedPID, parentFDT.Copy)
}

// sysDup3 implements dup3(oldfd, newfd, flags): newfd is made to refer to the
// same open file as oldfd, after closing whatever previously occupied newfd.
// EINVAL when oldfd == newfd, EBADF on a closed oldfd or out-of-range newfd.
// The displaced newfd is disposed exactly like sysClose via releaseFDResources.
func (h *syscallHandler) sysDup3(req sys.SyscallRequest) {
	sid := req.CallerPID
	fdt := h.resolveTargetFDT(req)
	oldfd := int(req.Args[0])
	newfd := int(req.Args[1])
	flags := int32(req.Args[2])

	newfd, errno := fdt.Dup3(oldfd, newfd, flags, func(displaced *fdtable.Entry) {
		// Give the displaced entry the same close-time disposition sysClose
		// would (WriteBuf flush + orphan/release of any fs handle).
		h.releaseFDResources(sid, newfd, displaced)
	})
	if errno != EOK {
		req.Reply(errno)
		return
	}
	req.Reply(int64(newfd)) // Linux dup3 returns the new descriptor on success
}

// fcntl command numbers (identical on ARM64 and amd64).
const (
	fSETFD    = 2 // F_SETFD — set fd flags (only FD_CLOEXEC is meaningful)
	fGETFD    = 1 // F_GETFD — get fd flags
	fGETFL    = 3 // F_GETFL — get file status flags (access mode)
	fSETFL    = 4 // F_SETFL — set file status flags
	fdCLOEXEC = 1 // FD_CLOEXEC bit returned/accepted by F_GETFD/F_SETFD
)

// sysFcntl implements the fcntl commands the shepherd path owns: F_SETFD /
// F_GETFD store and read the per-fd close-on-exec bit (MAZ-119), and the stdio
// fast paths (fd 0/1/2) for F_GETFD/F_SETFD/F_GETFL/F_SETFL mirror the kernel's
// in-kernel SyscallFcntl stub so the Go runtime's os.init() probes on
// stdin/stdout/stderr keep working once fcntl is delegated here. Any other
// command returns ENOSYS, matching the kernel stub for fd > 2.
func (h *syscallHandler) sysFcntl(req sys.SyscallRequest) {
	fdt := h.resolveTargetFDT(req)
	fd := int(req.Args[0])
	cmd := req.Args[1]
	arg := req.Args[2]

	e := fdt.Get(fd)

	switch cmd {
	case fGETFD:
		if e == nil {
			req.Reply(EBADF)
			return
		}
		if e.Cloexec {
			req.Reply(fdCLOEXEC)
		} else {
			req.Reply(EOK) // stdio defaults to no flags, matching the kernel stub
		}
	case fSETFD:
		if e == nil {
			req.Reply(EBADF)
			return
		}
		e.Cloexec = arg&fdCLOEXEC != 0
		req.Reply(EOK)
	case fGETFL:
		// Access mode for the stdio fds, mirroring the kernel stub: stdin is
		// read-only, stdout/stderr are write-only.
		if e == nil {
			req.Reply(EBADF)
			return
		}
		if e.Kind == fdtable.KindStdin {
			req.Reply(0) // O_RDONLY
			return
		}
		if e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
			req.Reply(1) // O_WRONLY
			return
		}
		req.Reply(int64(e.Flags))
	case fSETFL:
		// Status-flag changes are a no-op here (and the kernel stub pretends
		// to set them for stdio); only a closed fd is an error.
		if e == nil {
			req.Reply(EBADF)
			return
		}
		req.Reply(EOK)
	default:
		req.Reply(ENOSYS)
	}
}

func (h *syscallHandler) sysLseek(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	offset := int64(req.Args[1])
	whence := int(req.Args[2])

	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind == fdtable.KindStdin || e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		req.Reply(ESPIPE)
		return
	}

	var newOff int64
	switch whence {
	case 0: // SEEK_SET
		newOff = offset
	case 1: // SEEK_CUR
		newOff = e.Offset + offset
	case 2: // SEEK_END
		newOff = int64(e.Size) + offset
	default:
		req.Reply(EINVAL)
		return
	}
	if newOff < 0 {
		req.Reply(EINVAL)
		return
	}

	e.Offset = newOff
	req.Reply(newOff)
}

func (h *syscallHandler) sysGetcwd(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}
	cwd := fdt.Cwd
	n := copy(buf, cwd)
	if n < len(buf) {
		buf[n] = 0
		n++
	}
	req.Reply(int64(n))
}

func (h *syscallHandler) sysChdir(req sys.SyscallRequest) {
	fdt := h.resolveTargetFDT(req)
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := fdt.ResolvePath(path)

	isDir, _, err := h.fs.Resolve(absPath)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if !isDir {
		req.Reply(ENOTDIR)
		return
	}

	fdt.Cwd = absPath
	req.Reply(EOK)
}

func (h *syscallHandler) sysFchdir(req sys.SyscallRequest) {
	fdt := h.resolveTargetFDT(req)
	fd := int(req.Args[0])
	e := fdt.Get(fd)
	if e == nil || e.Kind == fdtable.KindNone {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindDir {
		req.Reply(ENOTDIR)
		return
	}
	fdt.Cwd = e.Path
	req.Reply(EOK)
}

func (h *syscallHandler) sysIoctl(req sys.SyscallRequest) {
	req.Reply(ENOTTY)
}

func (h *syscallHandler) sysFsync(req sys.SyscallRequest) {
	fd := int(req.Arg0())

	if fd >= 0 {
		fdt := h.getShepherd(req.CallerPID).FDT
		if e := fdt.Get(fd); e != nil && e.WriteBuf != nil && len(e.WriteBuf) > 0 {
			if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
				req.Reply(int64(errToErrno(err)))
				return
			}
		}

		// Flush dirty cached pages for this inode to ext2 BEFORE syncing.
		// The pwrite fast path writes directly to cached pages without
		// touching ext2. Without this flush, fdatasync/fsync would leave
		// stale data on disk, causing mmap coherence failures after re-mmap
		// (bbolt reads zeros for pages it just wrote).
		if e := fdt.Get(fd); e != nil {
			h.cache.FlushAllPagesForInum(req.CallerPID, e.Inum, func(handle uint32, offset int64, data []byte) (int, error) {
				return h.writePageByHandle(req.CallerPID, handle, offset, data)
			})
		}
	}

	if err := h.fs.Sync(); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

// sysFlock implements flock(fd, operation).
// arg0 = fd, arg1 = operation (LOCK_SH, LOCK_EX, LOCK_UN, optionally | LOCK_NB).
func (h *syscallHandler) sysFlock(req sys.SyscallRequest) {
	fd := int(req.Args[0])
	op := int(req.Args[1])
	shep := h.getShepherd(req.CallerPID)
	e := shep.FDT.Get(fd)
	if e == nil || e.Kind == fdtable.KindNone {
		req.Reply(EBADF)
		return
	}
	if e.Handle == 0 {
		// stdio fds don't support locking.
		req.Reply(EINVAL)
		return
	}
	nonblock := (op & flockNB) != 0
	kind := op &^ flockNB
	switch kind {
	case flockSH, flockEX:
		req.Reply(h.flocks.acquire(e.Handle, kind, nonblock, shep.Locks))
	case flockUN:
		req.Reply(h.flocks.release(e.Handle, shep.Locks))
	default:
		req.Reply(EINVAL)
	}
}

// pipe2FDBytes is the size of the pipefd[2] output: two int32 fds. Must match
// the kernel's copy-back length for sysid.Pipe2 (delegate.go pipe2FDBytes).
const pipe2FDBytes = 8

// sysPipe2 implements pipe2(pipefd[2], flags). It allocates a linked
// (read-end, write-end) fd pair backed by the internal/pipe data plane and
// writes the two fd numbers into the kernel-provided copy-back page;
// pipefd[0]=read, pipefd[1]=write. flags carries O_CLOEXEC / O_NONBLOCK,
// propagated onto both ends.
//
// It replies 0 on success — Linux pipe2 returns 0, not a byte count. The kernel
// copies the fixed pipe2FDBytes payload back on a zero (success) return for
// Pipe2 specifically (see delegate.go's copy-back path). On failure it replies a
// negative errno and writes nothing back.
func (h *syscallHandler) sysPipe2(req sys.SyscallRequest) {
	flags := int(req.Args[1])
	buf := req.DataBuf()
	if buf == nil || len(buf) < pipe2FDBytes {
		req.Reply(EFAULT)
		return
	}

	fdt := h.getShepherd(req.CallerPID).FDT
	rfd := fdt.Alloc(3)
	if rfd < 0 {
		req.Reply(EMFILE)
		return
	}
	// Reserve the read fd before allocating the write fd so Alloc doesn't hand
	// back the same slot twice.
	fdt.Put(rfd, &fdtable.Entry{Kind: fdtable.KindNone})
	wfd := fdt.Alloc(3)
	if wfd < 0 {
		fdt.Free(rfd)
		req.Reply(EMFILE)
		return
	}

	rEnd, wEnd := pipe.New(flags)
	cloexec := fdtable.CloexecFromFlags(int32(flags))
	fdt.Put(rfd, &fdtable.Entry{Kind: fdtable.KindPipeRead, Pipe: rEnd, Cloexec: cloexec})
	fdt.Put(wfd, &fdtable.Entry{Kind: fdtable.KindPipeWrite, Pipe: wEnd, Cloexec: cloexec})

	// pipefd[0] = read end, pipefd[1] = write end. The kernel copies these 8
	// bytes verbatim to the caller's pipefd array, read back as two _C_int.
	binary.LittleEndian.PutUint32(buf[0:4], uint32(rfd))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(wfd))
	req.Reply(EOK) // Linux pipe2 returns 0; the kernel copies pipefd back.
}

// pipeReadWaiter is a read request parked on an empty pipe. It is stored as the
// opaque token in pipe.End.Park; wakePipeReaders pulls it back from TakeWaiters
// and fulfills it once data arrives or the last writer closes (EOF).
type pipeReadWaiter struct {
	req sys.SyscallRequest
	end *pipe.End
}

// drainPipeInto reads from end into the request's data buffer and replies, on
// the assumption the buffer is readable or at EOF (the non-blocking caller has
// already established that). It returns false without replying only when the
// read reports ErrWouldBlock — letting the caller decide to EAGAIN or park.
func drainPipeInto(req sys.SyscallRequest, end *pipe.End) (replied bool) {
	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return true
	}
	count := int(req.Args[2])
	if count > len(buf) {
		count = len(buf)
	}
	n, err := end.Read(buf[:count])
	if err != nil {
		return false // ErrWouldBlock: empty buffer, a writer is still open.
	}
	req.Reply(int64(n)) // n>0 = data; n==0 = EOF (writers closed, drained).
	return true
}

// sysReadPipe serves a read() on a pipe read-end. It drains the shared buffer
// in FIFO order. When the buffer is empty it either reports EOF (all writers
// closed), returns EAGAIN (O_NONBLOCK), or parks the request (blocking) to be
// fulfilled by a later write or writer-close on the matching write-end. This
// blocking park is exercised end-to-end by MAZ-114.
func (h *syscallHandler) sysReadPipe(req sys.SyscallRequest, e *fdtable.Entry) {
	if drainPipeInto(req, e.Pipe) {
		// The drain freed buffer space — retry any writers parked on the
		// full pipe (MAZ-149 writer-park backpressure). No-op when nothing
		// is parked or the buffer is still full.
		h.wakePipeWriters(e.Pipe)
		return
	}
	// Empty buffer, a writer is still open.
	if e.Pipe.Nonblock() {
		req.Reply(EWOULDBLOCK)
		return
	}
	e.Pipe.Park(&pipeReadWaiter{req: req, end: e.Pipe}) // deferred Reply.
}

// pipeWriteWaiter is a write request parked on a FULL pipe (MAZ-149
// writer-park backpressure). It is stored both as the opaque token in
// pipe.End.ParkWriter (returned by TakeWriterWaiters once a reader drains
// free space) and in h.parkedPipeWriters (the bounded-park timeout registry).
// The request's data page stays mapped until Reply, so req.Data() remains
// valid for the retry — the same contract parked reads rely on.
type pipeWriteWaiter struct {
	req      sys.SyscallRequest
	end      *pipe.End
	parkedAt time.Time // original park time; re-parks keep it (total bound)
}

// pipeWriterParkTimeout bounds how long a blocking pipe write stays parked
// with no reader draining the buffer (MAZ-149 item 7). Unix semantics would
// block forever; the bound is the escape hatch for a wedged/vanished reader
// (a reader death does not currently signal parked writers — the timeout
// covers that case too). On expiry the bytes are DROPPED: the `@\` breadcrumb
// is requested via pipe.End.MarkDrop (applied at the NEXT READ, in the
// reader's dispatch context — the watchdog goroutine must not mutate the
// buffer itself), and the writer is released with a success count. 30s is
// far above the multi-second delegate stalls seen in boot storms.
const pipeWriterParkTimeout = 30 * time.Second

// pipeParkSweepInterval is how often pipeParkWatchdog scans the registry.
const pipeParkSweepInterval = 5 * time.Second

// parkPipeWriter parks w on its pipe's writer-waiter list and registers it
// for the timeout sweep. Deferred Reply: handle() returns without replying,
// releasing shep.mu — nothing is held while parked (mirrors sysReadPipe).
func (h *syscallHandler) parkPipeWriter(w *pipeWriteWaiter) {
	h.pipeParkMu.Lock()
	h.parkedPipeWriters[w] = struct{}{}
	h.pipeParkMu.Unlock()
	w.end.ParkWriter(w)
}

// dropParkedPipeWritersForSID abandons every parked pipe write owned by a
// dead shepherd, WITHOUT replying — the kernel has already cleaned the dead
// caller's delegate state, and its TID may already be REUSED by an unrelated
// delegate, so a late Reply would corrupt that delegate's return value (see
// tearDownShepherdFDState's call site). The stale tokens left on the pipes'
// writerWaiters lists are skipped by wakePipeWriters and the watchdog via
// the registry-liveness check.
func (h *syscallHandler) dropParkedPipeWritersForSID(sid int16) {
	h.pipeParkMu.Lock()
	for w := range h.parkedPipeWriters {
		if w.req.CallerPID == sid {
			delete(h.parkedPipeWriters, w)
		}
	}
	h.pipeParkMu.Unlock()
}

// wakePipeWriters retries writes parked on a full pipe once a reader drained
// free space — the symmetric counterpart of wakePipeReaders. Called from the
// pipe read path. A retry that finds the buffer full again (a racing writer
// refilled it) re-parks; a token the watchdog already abandoned (not in the
// registry) is skipped — its request was replied at timeout.
func (h *syscallHandler) wakePipeWriters(end *pipe.End) {
	for _, tok := range end.TakeWriterWaiters() {
		w, ok := tok.(*pipeWriteWaiter)
		if !ok {
			continue // only parkPipeWriter parks; be defensive.
		}
		h.pipeParkMu.Lock()
		_, live := h.parkedPipeWriters[w]
		delete(h.parkedPipeWriters, w)
		h.pipeParkMu.Unlock()
		if !live {
			continue // bounded-park timeout already dropped + replied this one.
		}
		n, err := w.end.Write(w.req.Data())
		if err != nil {
			h.parkPipeWriter(w) // still full: re-park (keeps original parkedAt).
			continue
		}
		// The retried write made data available — wake parked readers (none
		// and parked writers can coexist on one buf, but another pipe op may
		// have parked a reader between the drain and this retry).
		wakePipeReaders(w.end)
		w.req.Reply(int64(n))
	}
}

// pipeParkWatchdog enforces pipeWriterParkTimeout: writers parked longer than
// the bound are abandoned — bytes dropped, `@\` breadcrumb on the buffer tail,
// writer released with a success count (MAZ-149 item 7 drop semantics; the
// pretend-success mirrors the display-channel drop policy: console/capture
// byte loss is marked, not surfaced as an error). Runs for the shepherd's
// lifetime; started by startUringDelegateHandler.
func (h *syscallHandler) pipeParkWatchdog() {
	for {
		time.Sleep(pipeParkSweepInterval)
		now := time.Now()
		var expired []*pipeWriteWaiter
		h.pipeParkMu.Lock()
		for w := range h.parkedPipeWriters {
			if now.Sub(w.parkedAt) > pipeWriterParkTimeout {
				delete(h.parkedPipeWriters, w)
				expired = append(expired, w)
			}
		}
		h.pipeParkMu.Unlock()
		for _, w := range expired {
			// The stale token stays on buf.writerWaiters; wakePipeWriters
			// skips it via the registry-liveness check above.
			dropped := len(w.req.Data())
			w.end.MarkDrop()
			fmt.Printf("[linux] pipe writer-park timeout sid=%d: %d bytes dropped (@\\ marked)\n",
				w.req.CallerPID, dropped)
			w.req.Reply(int64(dropped))
		}
	}
}

// sysWritePipe serves a write() on a pipe write-end: it appends to the shared
// buffer (bounded by the pipe cap) and wakes any reader parked on the pipe.
// A full buffer EAGAINs the non-blocking case and PARKS the blocking case
// (MAZ-149 writer-park — real backpressure via deferred Reply; a later
// reader drain retries and replies). A partial write into a nearly-full
// buffer replies the short count — write(2) permits it, and the caller's
// write loop issues the remainder, which then parks here when full (this is
// what fixed the old >4 KiB spurious-EAGAIN, review finding #4).
func (h *syscallHandler) sysWritePipe(req sys.SyscallRequest, e *fdtable.Entry) {
	data := req.Data()
	n, err := e.Pipe.Write(data)
	if err != nil {
		// ErrWouldBlock: buffer full, nothing accepted.
		if e.Pipe.Nonblock() {
			req.Reply(EWOULDBLOCK)
			return
		}
		h.parkPipeWriter(&pipeWriteWaiter{req: req, end: e.Pipe, parkedAt: time.Now()})
		return
	}
	wakePipeReaders(e.Pipe)
	req.Reply(int64(n))
}

// wakePipeReaders fulfills readers parked on a pipe once it became readable
// (data written) or hit EOF (last writer closed). Each woken reader re-runs the
// read against the shared buffer and replies. Called after a pipe write and
// after a pipe write-end close.
func wakePipeReaders(end *pipe.End) {
	for _, tok := range end.TakeWaiters() {
		w, ok := tok.(*pipeReadWaiter)
		if !ok {
			continue // only sysReadPipe parks, always a *pipeReadWaiter; be defensive.
		}
		if !drainPipeInto(w.req, w.end) {
			// Still would-block (e.g. another reader drained the buffer first
			// and a writer remains): re-park rather than spuriously waking.
			w.end.Park(w)
		}
	}
}

// ============================================================
// Metadata syscalls (via fs IPC)
// ============================================================

func (h *syscallHandler) sysOpenat(req sys.SyscallRequest) {
	fdt := h.resolveTargetFDT(req)
	rawPath := req.PathString()
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), rawPath)
	if e != 0 {
		if isZapPath(rawPath) || isZapPath(absPath) {
			fmt.Printf("[lin:openat .zap RESOLVEAT FAIL] dirfd=%d raw=%q err=%d\n",
				int32(req.Args[0]), rawPath, e)
		}
		req.Reply(e)
		return
	}
	flags := int32(req.Args[2])
	mode := uint32(req.Args[3])

	handle, inum, ftype, size, err := h.fs.Open(absPath, uint32(flags), mode)
	if err != nil {
		if isZapPath(absPath) {
			fmt.Printf("[lin:openat .zap FAIL] abspath=%q flags=0x%x mode=0%o err=%v errno=%d\n",
				absPath, uint32(flags), mode, err, errToErrno(err))
		}
		req.Reply(int64(errToErrno(err)))
		return
	}

	newFD := fdt.Alloc(3)
	if newFD < 0 {
		h.fs.Close(handle)
		req.Reply(EMFILE)
		return
	}

	kind := fdtable.KindFile
	if ftype == fdtable.FtDir {
		kind = fdtable.KindDir
	}

	entry := &fdtable.Entry{
		Kind:    kind,
		Handle:  handle,
		Inum:    inum,
		Size:    size,
		Ftype:   ftype,
		Flags:   flags,
		Path:    absPath,
		Cloexec: fdtable.CloexecFromFlags(flags),
	}
	if kind == fdtable.KindFile {
		entry.WriteBuf = []byte{} // non-nil = buffering active; zero-capacity, allocates on first write
		entry.WriteBufOff = int64(size)
	}
	fdt.Put(newFD, entry)
	req.Reply(int64(newFD))
}

func (h *syscallHandler) sysFstat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}

	buf := req.DataBuf()
	if buf == nil || len(buf) < 128 {
		req.Reply(EFAULT)
		return
	}

	if e.Kind == fdtable.KindStdin || e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		fillStdioStatBuf(buf)
		req.Reply(128)
		return
	}

	fsErr, err := h.fs.Fstat(e.Handle, buf[:128])
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if fsErr != 0 {
		req.Reply(int64(fsErr))
		return
	}
	req.Reply(128)
}

func (h *syscallHandler) sysFstatat(req sys.SyscallRequest) {
	path := req.PathString()
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), path)
	if e != 0 {
		req.Reply(e)
		return
	}
	buf := req.DataBuf()
	if buf == nil || len(buf) < 128 {
		req.Reply(EFAULT)
		return
	}
	fsErr, err := h.fs.Stat(absPath, buf[:128])
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	if fsErr != 0 {
		req.Reply(int64(fsErr))
		return
	}
	req.Reply(128)
}

func (h *syscallHandler) sysMkdirat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	if err := h.fs.Mkdir(absPath, uint32(req.Args[2])); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysUnlinkat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	flags := uint32(req.Args[2])

	// Linux distinguishes unlink (rejects directories) from rmdir (requires
	// a directory). ext2.Remove handles both, so we stat first and enforce
	// the fdtable.AT_REMOVEDIR contract here. Without this Go's os.Remove cannot
	// detect that it should fall back to rmdir on a directory target.
	isDir, _, rerr := h.fs.Resolve(absPath)
	if rerr != nil {
		req.Reply(int64(errToErrno(rerr)))
		return
	}
	if (flags & fdtable.AT_REMOVEDIR) != 0 {
		if !isDir {
			req.Reply(ENOTDIR)
			return
		}
	} else {
		if isDir {
			req.Reply(EISDIR)
			return
		}
	}
	if err := h.fs.Remove(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysRenameat(req sys.SyscallRequest) {
	// Data page contains "oldpath\0newpath\0". Arg0 = offset of newpath.
	d := req.Data()
	if d == nil {
		req.Reply(EINVAL)
		return
	}
	newOff := int(req.Arg0())
	if newOff <= 0 || newOff >= len(d) {
		req.Reply(EINVAL)
		return
	}
	// Extract old path (up to first null).
	var oldPath string
	for i := 0; i < newOff; i++ {
		if d[i] == 0 {
			oldPath = string(d[:i])
			break
		}
	}
	// Extract new path (from newOff to next null).
	var newPath string
	for i := newOff; i < len(d); i++ {
		if d[i] == 0 {
			newPath = string(d[newOff:i])
			break
		}
	}
	if oldPath == "" || newPath == "" {
		req.Reply(EINVAL)
		return
	}
	// Resolve relative paths through the caller's CWD.
	fdt := h.getShepherd(req.CallerPID).FDT
	oldPath = fdt.ResolvePath(oldPath)
	newPath = fdt.ResolvePath(newPath)

	err := h.fs.Rename(oldPath, newPath)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(0)
}

func (h *syscallHandler) sysFtruncate(req sys.SyscallRequest) {
	shep := h.getShepherd(req.CallerPID)
	fd := int(req.Args[0])
	newSize := int64(req.Args[1])
	e := shep.FDT.Get(fd)
	if e == nil || e.Kind == fdtable.KindNone {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindFile {
		req.Reply(EINVAL)
		return
	}
	if err := h.fs.Truncate(e.Handle, newSize); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	// Invalidate cached pages beyond the new size.
	// KNOWN LEAK: the returned entries' handler-side PTEs and physical pages
	// are NOT torn down here — the kernel doesn't know to release them. They
	// persist until the next munmap of this fd or shepherd death (whichever
	// comes first). Proper fix needs a kernel-side "drop these VAs" IPC; for
	// now we log so we can see how often this leaks in practice.
	leaked := h.cache.RemoveRange(req.CallerPID, e.Inum, newSize, 1<<62)
	if len(leaked) > 0 {
		fmt.Printf("[ftruncate:LEAK] sid=%d fd=%d inum=%d newSize=%d leaked=%d cache entries (handler PTEs orphaned until munmap/death)\n",
			req.CallerPID, fd, e.Inum, newSize, len(leaked))
	}
	e.Size = uint32(newSize)
	req.Reply(EOK)
}

func (h *syscallHandler) sysGetdents64(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindDir {
		req.Reply(ENOTDIR)
		return
	}

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}

	// fs.maz packs as many dirents as fit in its shared data window, so
	// we need a scratch buffer big enough to receive the full response.
	// Allocate per-call (getdents64 is rare relative to read/write).
	scratch := make([]byte, h.fs.DataLen())
	dataLen, _, err := h.fs.ReadDir(e.Handle, int(e.Offset), scratch)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	if dataLen == 0 {
		req.Reply(EOK) // no more entries
		return
	}
	src := scratch[:dataLen]

	// fs.maz packs as many dirents as fit in its 65KB shared data window;
	// the user's buffer is typically 4KB. We must NOT advance e.Offset by
	// entryCount when the user buffer can't hold all of them — doing so
	// silently drops the dirents that didn't fit, breaking filepath.Walk
	// on directories with more than ~80 entries (Bug A in findings.md).
	//
	// Instead: walk the dirent records inside the truncated copy, count
	// how many actually fit, and advance offset by that count. The next
	// getdents64 call will pick up at the first dropped entry.
	delivered := deliveredDirents(src, len(buf))
	n := delivered.bytes
	if n > 0 {
		copy(buf[:n], src[:n])
	}
	e.Offset += int64(delivered.count)
	req.Reply(int64(n))
}

// deliveredDirents walks the linux_dirent64 records in src and returns
// how many full records fit inside maxBytes (rounded down to a record
// boundary), plus the byte length consumed by those records. Each
// record's reclen lives at bytes [16:18] of its header.
func deliveredDirents(src []byte, maxBytes int) struct {
	bytes int
	count int
} {
	var out struct {
		bytes int
		count int
	}
	off := 0
	for off+18 <= len(src) {
		// reclen is at offset 16 within the record (after ino[8] + offset[8]).
		reclen := int(uint16(src[off+16]) | uint16(src[off+17])<<8)
		if reclen <= 0 || off+reclen > len(src) {
			break // malformed or runs past the buffer
		}
		if off+reclen > maxBytes {
			break // doesn't fit in user buf
		}
		off += reclen
		out.count++
	}
	out.bytes = off
	return out
}

func (h *syscallHandler) sysFaccessat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	if err := h.fs.Access(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysFchmodat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	if err := h.fs.SetMode(absPath, uint32(req.Args[2])); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysUtimensat(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	absPath, e := fdt.ResolveAt(int32(req.Args[0]), req.PathString())
	if e != 0 {
		req.Reply(e)
		return
	}
	if err := h.fs.SetTimes(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(EOK)
}

func (h *syscallHandler) sysReadlinkat(req sys.SyscallRequest) {
	req.Reply(EINVAL) // no symlinks
}

// sysStatfs handles statfs(path, buf). We don't have per-volume metadata
// (the fs shepherd exposes no capacity IPC), so on a valid path we return
// synthetic-but-reasonable values for a single global filesystem.
func (h *syscallHandler) sysStatfs(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	path := req.PathString()
	if path == "" {
		req.Reply(EINVAL)
		return
	}
	absPath := fdt.ResolvePath(path)
	if _, _, err := h.fs.Resolve(absPath); err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	buf := req.DataBuf()
	if buf == nil || len(buf) < 120 {
		req.Reply(EFAULT)
		return
	}
	fillSyntheticStatfs(buf[:120])
	req.Reply(120)
}

// sysFstatfs handles fstatfs(fd, buf). Same synthetic values as statfs; the
// fd just needs to be a live file or directory descriptor.
func (h *syscallHandler) sysFstatfs(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	if e == nil || e.Kind == fdtable.KindNone {
		req.Reply(EBADF)
		return
	}
	buf := req.DataBuf()
	if buf == nil || len(buf) < 120 {
		req.Reply(EFAULT)
		return
	}
	fillSyntheticStatfs(buf[:120])
	req.Reply(120)
}

// fillSyntheticStatfs writes a 120-byte linux/arm64 + linux/amd64 struct
// statfs64 with synthetic values. All fields are 8-byte __fsword_t /
// fsblkcnt64_t on 64-bit Linux, so the layout is just 15 u64s.
//
// Layout (bytes 0..119):
//
//	 0:  f_type       = 0xEF53 (EXT2_SUPER_MAGIC — closest match to our ext2 ramdisk)
//	 8:  f_bsize      = 4096
//	16:  f_blocks     = 1<<20 (4 GiB total, synthetic)
//	24:  f_bfree      = 1<<19
//	32:  f_bavail     = 1<<19
//	40:  f_files      = 0
//	48:  f_ffree      = 0
//	56:  f_fsid       = 0 (two 32-bit zeros)
//	64:  f_namelen    = 255
//	72:  f_frsize     = 4096
//	80:  f_flags      = 0
//	88:  f_spare[4]   = 0
func fillSyntheticStatfs(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
	p := (*[15]uint64)(unsafe.Pointer(&buf[0]))
	p[0] = 0xEF53  // f_type
	p[1] = 4096    // f_bsize
	p[2] = 1 << 20 // f_blocks
	p[3] = 1 << 19 // f_bfree
	p[4] = 1 << 19 // f_bavail
	p[5] = 0       // f_files
	p[6] = 0       // f_ffree
	p[7] = 0       // f_fsid
	p[8] = 255     // f_namelen
	p[9] = 4096    // f_frsize
	p[10] = 0      // f_flags
	// p[11..14] — f_spare — already zero
}

// ============================================================
// Data syscalls (via fs IPC)
// ============================================================

// isStdinRead returns true if the request is a read on fd 0 (stdin).
// Used by the delegate handler to skip wrapping stdin reads with sidIncRef/sidDecRef
// since those have their own async refcount lifecycle.
func (h *syscallHandler) isStdinRead(req sys.SyscallRequest) bool {
	if req.SysID != sysid.Read {
		return false
	}
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	return e != nil && e.Kind == fdtable.KindStdin
}

func (h *syscallHandler) sysRead(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind == fdtable.KindStdin {
		// Queue the request for the stdin drainer (main goroutine).
		// Don't reply — the drainer will reply when input arrives.
		// sidIncRef here; sidDecRef happens in fulfillRead via stdinDecRefCh.
		sidIncRef(req.CallerPID)
		reqQueue.Enqueue(&readDataResponse{req: req, callerSID: req.CallerPID})
		return
	}
	if e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		req.Reply(EBADF)
		return
	}
	if e.Kind == fdtable.KindPipeWrite {
		req.Reply(EBADF) // reading the write end is invalid
		return
	}
	if e.Kind == fdtable.KindPipeRead {
		h.sysReadPipe(req, e)
		return
	}
	if e.Kind == fdtable.KindDir {
		req.Reply(EISDIR)
		return
	}
	if e.WriteBuf != nil && len(e.WriteBuf) > 0 {
		if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
			req.Reply(int64(errToErrno(err)))
			return
		}
	}

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}
	count := int(req.Args[2])
	if count > len(buf) {
		count = len(buf)
	}

	// Check page cache first — if all pages in range are cached, read
	// directly from cached pages (same physical pages as mmap'd memory).
	if entries := h.cache.LookupRange(req.CallerPID, e.Inum, e.Offset, count); len(entries) > 0 {
		if n, ok := readFromCachedPages(entries, e.Offset, buf[:count], count, int64(e.Size)); ok {
			e.Offset += int64(n)
			req.Reply(int64(n))
			return
		}
	}

	n, err := h.fs.Read(e.Handle, e.Offset, buf[:count])
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	e.Offset += int64(n)
	req.Reply(int64(n))
}

// flushWriteBuf drains e.WriteBuf to ext2 in 4KB chunks (the IPC window size).
// Called from sysClose, sysFsync, and sysRead before any pass-through I/O.
func (h *syscallHandler) flushWriteBuf(pid int16, fd int, e *fdtable.Entry) error {
	buf := e.WriteBuf
	off := e.WriteBufOff
	winSize := h.fs.DataLen()
	for len(buf) > 0 {
		chunk := buf
		if len(chunk) > winSize {
			chunk = chunk[:winSize]
		}
		written, err := h.fs.Write(e.Handle, off, chunk)
		if err != nil {
			return err
		}
		// Keep cached pages coherent with what we just wrote.
		if entries := h.cache.LookupRange(pid, e.Inum, off, written); len(entries) > 0 {
			updateCachedPages(entries, off, chunk[:written])
		}
		off += int64(written)
		buf = buf[written:]
	}
	// Reset to a non-nil zero-capacity slice: keeps "buffering enabled" sentinel
	// while releasing the backing pages to the GC with no pre-allocation.
	e.WriteBuf = []byte{}
	e.WriteBufOff = e.Offset
	return nil
}

// writeBufMaxBytes is the maximum number of bytes held in a write buffer before
// a forced flush. Bounds memory usage and limits data-loss window.
const writeBufMaxBytes = 1 << 20 // 1 MB

// flushOneBuffer scans all shepherd FD tables and flushes the first non-empty
// write buffer it finds. Called from the idle-flush hint handler; processes
// at most one buffer per call so the delegate handler stays responsive.
func (h *syscallHandler) flushOneBuffer() {
	for pid, shep := range h.shepherds {
		flushed := false
		shep.FDT.Each(func(fd int, e *fdtable.Entry) bool {
			if len(e.WriteBuf) == 0 {
				return true // keep scanning
			}
			h.flushWriteBuf(pid, fd, e) // ignore error; best-effort idle flush
			flushed = true
			return false // processed one buffer; stop
		})
		if flushed {
			return
		}
	}
}

func (h *syscallHandler) sysWrite(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	data := req.Data()

	e := fdt.Get(fd)
	if e == nil {
		// MAZ-149: a redirect-masked fd 1/2 whose entry is CLOSED lands here —
		// the mask is set for closed stdio fds precisely so the write reaches
		// the table (EBADF) instead of the console fast path printing it. A
		// closed fd>2 is plain EBADF as it always was.
		if fd <= 2 {
			// MAZ-156 forensics: a redirected stdio write hitting a CLOSED
			// entry is silent data loss — the stage-2 "stdout empty" shape.
			fmt.Printf("[lin:write] sid=%d fd=%d CLOSED entry, EBADF, %d bytes dropped\n",
				req.CallerPID, fd, len(data))
		}
		req.Reply(EBADF)
		return
	}
	if fd <= 2 && e.Kind == fdtable.KindPipeRead {
		// MAZ-156 forensics: redirected stdio fd pointing at a pipe READ end
		// is a mis-primed table — the write EBADFs silently below.
		fmt.Printf("[lin:write] sid=%d fd=%d is pipe READ end, EBADF, %d bytes dropped\n",
			req.CallerPID, fd, len(data))
	}
	if e.Kind == fdtable.KindStdout || e.Kind == fdtable.KindStderr {
		// MAZ-149 redesign: console fd 1/2 writes normally take the kernel
		// fast path (UART push + fire-and-forget display delegate) and never
		// reach sysWrite. This branch is the mask/table MISMATCH window: a
		// blocking delegate arrived (redirect bit set at exec time) but the
		// table now says console — e.g. the future MAZ-151 runtime
		// re-redirect transition. The kernel skipped its UART push for this
		// write, so emit to serial (fast interrupt-driven TX ring,
		// CR-before-LF) + the linux-ui display here, preserving the fd (1 vs
		// 2) so stderr stays distinct, then Reply. No double-emit either way.
		if len(data) > 0 {
			// suppressSerialCopy (SUPPRESS_SERIAL_STDIO_COPY) suppresses the
			// serial copy of delegated stdio; the linux-ui display still gets it.
			if !suppressSerialCopy {
				sys.UartWrite(addCRBeforeLF(data))
			}
			if h.displayFeed != nil {
				// The caller's data page is reclaimed on Reply; the display
				// channel is drained asynchronously, so hand it an owned copy.
				cp := make([]byte, len(data))
				copy(cp, data)
				h.displayFeed(byte(fd), cp)
			}
		}
		req.Reply(int64(len(data)))
		return
	}
	if e.Kind == fdtable.KindStdin {
		req.Reply(EBADF)
		return
	}
	if e.Kind == fdtable.KindPipeRead {
		req.Reply(EBADF) // writing the read end is invalid
		return
	}
	if e.Kind == fdtable.KindPipeWrite {
		h.sysWritePipe(req, e)
		return
	}
	if data == nil {
		req.Reply(ENOSYS)
		return
	}

	// Buffered path: accumulate sequential writes and defer IPC to fsync/close.
	if e.WriteBuf != nil {
		bufEnd := e.WriteBufOff + int64(len(e.WriteBuf))
		if e.Offset == bufEnd {
			// Contiguous — append and return immediately, no IPC to fs.
			e.WriteBuf = append(e.WriteBuf, data...)
			e.Offset += int64(len(data))
			if uint32(e.Offset) > e.Size {
				e.Size = uint32(e.Offset)
			}
			// Force flush once the buffer reaches the size cap.
			if len(e.WriteBuf) >= writeBufMaxBytes {
				if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
					req.Reply(int64(errToErrno(err)))
					return
				}
			}
			req.Reply(int64(len(data)))
			return
		}
		// Non-contiguous write (seek happened) — flush buffer first, then fall
		// through to the direct write path below.
		if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
			req.Reply(int64(errToErrno(err)))
			return
		}
	}

	// Direct write path: send data to ext2 immediately.
	// Also used for the first chunk after a seek flushes the buffer.
	written, err := h.fs.Write(e.Handle, e.Offset, data)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	// Keep cached pages coherent with on-disk data.
	if entries := h.cache.LookupRange(req.CallerPID, e.Inum, e.Offset, written); len(entries) > 0 {
		updateCachedPages(entries, e.Offset, data[:written])
	}

	e.Offset += int64(written)
	if uint32(e.Offset) > e.Size {
		e.Size = uint32(e.Offset)
	}
	if e.WriteBuf != nil {
		// Re-arm buffer starting at new position for future sequential writes.
		e.WriteBufOff = e.Offset
	}
	req.Reply(int64(written))
}

func (h *syscallHandler) sysWritev(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

func (h *syscallHandler) sysReadv(req sys.SyscallRequest) {
	req.Reply(ENOSYS)
}

// sysPread64 implements pread64(fd, buf, count, offset).
// Like read but uses the caller-supplied offset without changing the fd position.
func (h *syscallHandler) sysPread64(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindFile {
		req.Reply(ESPIPE)
		return
	}

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}
	count := int(req.Args[2])
	if count > len(buf) {
		count = len(buf)
	}
	offset := int64(req.Args[3])

	// Check page cache first — if all pages in range are cached, read
	// directly from cached pages (same physical pages as mmap'd memory).
	if entries := h.cache.LookupRange(req.CallerPID, e.Inum, offset, count); len(entries) > 0 {
		if n, ok := readFromCachedPages(entries, offset, buf[:count], count, int64(e.Size)); ok {
			req.Reply(int64(n))
			return
		}
	}

	n, err := h.fs.Read(e.Handle, offset, buf[:count])
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}
	req.Reply(int64(n))
}

// sysPwrite64 implements pwrite64(fd, buf, count, offset).
// Like write but uses the caller-supplied offset without changing the fd position.
func (h *syscallHandler) sysPwrite64(req sys.SyscallRequest) {
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Arg0())
	data := req.Data()

	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}
	if e.Kind != fdtable.KindFile {
		req.Reply(ESPIPE)
		return
	}
	if data == nil {
		req.Reply(EFAULT)
		return
	}

	offset := int64(req.Args[3])

	// Always write through ext2 first — this ensures the on-disk file is
	// up to date so that mmap page faults after re-mmap see the correct data.
	// The old "fast path" that bypassed ext2 broke mmap/pwrite coherence:
	// pwrite updated cached pages only, then bolt re-mmapped, new page faults
	// read from ext2 which still had zeros → bbolt panic.
	written, err := h.fs.Write(e.Handle, offset, data)
	if err != nil {
		req.Reply(int64(errToErrno(err)))
		return
	}

	// Update any cached pages that overlap so mmap reads without
	// re-faulting also see the new data.
	if entries := h.cache.LookupRange(req.CallerPID, e.Inum, offset, written); len(entries) > 0 {
		updateCachedPages(entries, offset, data[:written])
	}

	endPos := offset + int64(written)
	if uint32(endPos) > e.Size {
		e.Size = uint32(endPos)
	}
	req.Reply(int64(written))
}

// sysMmapPageFill handles kernel requests to fill a file-backed mmap page.
// The kernel allocates a physical frame and maps it into our address space,
// then sends this request so we read file data into it. The page stays mapped
// in both our address space and the faulting shepherd's — this provides page
// cache coherence so read/write through us see the same data as mmap'd memory.
func (h *syscallHandler) sysMmapPageFill(req sys.SyscallRequest) {
	// Args[0]=fd, Args[1]=fileOffset, Args[2]=count
	// CallerPID = shepherd that owns the fd
	fdt := h.getShepherd(req.CallerPID).FDT
	fd := int(req.Args[0])
	e := fdt.Get(fd)
	if e == nil {
		req.Reply(EBADF)
		return
	}

	// Flush any pending write buffer to ext2 before filling the page.
	// sysWrite buffers sequential writes in e.WriteBuf without writing to ext2
	// immediately. If the caller wrote data then mmap'd the same fd, the page
	// fault arrives here before the buffer is flushed — ext2 would return zeros.
	if len(e.WriteBuf) > 0 {
		if err := h.flushWriteBuf(req.CallerPID, fd, e); err != nil {
			fmt.Printf("[mmap-fill] sid=%d fd=%d flush err: %v\n", req.CallerPID, fd, err)
		}
	}

	buf := req.DataBuf()
	if buf == nil {
		req.Reply(EFAULT)
		return
	}

	offset := int64(req.Args[1])
	count := int(req.Args[2])
	if count > len(buf) {
		count = len(buf)
	}

	// Zero the page first — the kernel allocates physical frames from the
	// buddy allocator without zeroing, and if the file is shorter than a
	// full page (sparse/truncated), the tail would contain stale data.
	for i := range buf[:count] {
		buf[i] = 0
	}

	n, err := h.fs.Read(e.Handle, offset, buf[:count])
	if err != nil {
		fmt.Printf("[mmap-fill] sid=%d fd=%d offset=%d READ ERR: %v\n", req.CallerPID, fd, offset, err)
		req.Reply(int64(errToErrno(err)))
		return
	}
	// Verbose mmap-fill trace disabled — UART saturation starved CPU-bound shepherds.

	// Record this page in the cache so read/pread/write/pwrite can
	// see mmap'd data without going through ext2. Keyed by inode (not
	// fd) so the page survives close per Linux semantics.
	pageAlignedOffset := int64(req.Args[1]) &^ 0xFFF
	h.cache.Add(req.CallerPID, e.Inum, pageAlignedOffset, req.DataVA(), e.Handle)

	req.Reply(int64(n))
}

// sysMmapPageFlush handles kernel requests to flush dirty cached pages and
// return handler VAs for PTE cleanup. The kernel sends this on munmap or
// shepherd death. The handler:
//  1. Flushes dirty pages to ext2 (first round only — subsequent rounds
//     have no dirty pages left)
//  2. Removes up to 511 page cache entries
//  3. Writes their handler VAs into the response page
//  4. Replies with the number of VAs written
//
// The kernel reads the response page, unmaps those handler PTEs, and sends
// another round if count == 511.
//
// Args[0]=fd (0xFFFFFFFF = all fds, for death cleanup)
// Args[1]=callerSID
// DataVA/DataLen = response page (handler writes VAs here)
//
// Response page layout:
//
//	[0:4]    uint32   count (0..511)
//	[4:8]    uint32   reserved
//	[8:4096] [511]uint64  handler VAs to unmap
const maxFlushVAsPerRound = 511

func (h *syscallHandler) sysMmapPageFlush(req sys.SyscallRequest) {
	callerSID := int16(req.Args[1])
	fd := int(req.Args[0])
	allFDs := fd == 0xFFFFFFFF
	// Args[2]/Args[3] = file-offset range to constrain the flush+remove.
	// Length == 0 means "all pages for the inode" (death cleanup or
	// legacy callers). For partial munmap the kernel passes the unmapped
	// VA range translated to file offsets so pages outside it stay live.
	startOffset := int64(req.Args[2])
	rangeLen := int64(req.Args[3])
	rangeBound := rangeLen > 0

	responseBuf := req.DataBuf()
	if responseBuf == nil || len(responseBuf) < 4096 {
		req.Reply(EINVAL)
		return
	}

	// The kernel sends fd from its file-mapping metadata. To address the
	// page cache (now inode-keyed) we resolve fd→inum via FDT. If the fd
	// has already been freed by sysClose (close-before-munmap, atypical
	// for bbolt/scorch but possible in pathological orders), we fall
	// back to the all-fds drain for this sid — better than leaking.
	var inum uint32
	inumKnown := false
	if !allFDs {
		fdt := h.getShepherd(callerSID).FDT
		if e := fdt.Get(fd); e != nil {
			inum = e.Inum
			inumKnown = true
		}
	}

	// Flush all pages to ext2. We flush everything (not just pages marked
	// dirty via syscalls) because user writes through the mmap VA don't
	// generate syscalls, so we have no way to track which pages changed.
	// Range-bounded flush is an optimization; flushing the whole inode is
	// always safe (just extra IO).
	flushByHandle := func(handle uint32, offset int64, data []byte) (int, error) {
		return h.writePageByHandle(callerSID, handle, offset, data)
	}
	if allFDs || !inumKnown {
		if !allFDs {
			inums := h.cache.InumsFor(callerSID)
			fmt.Printf("[pageCache:FALLBACK_ALLFDS] sid=%d fd=%d inumCount=%d (fd not in fdt, draining all)\n", callerSID, fd, len(inums))
			for _, inum := range inums {
				fmt.Printf("[pageCache:DRAIN] sid=%d inum=%d\n", callerSID, inum)
			}
		}
		h.cache.FlushAllPagesForSID(callerSID, flushByHandle)
	} else {
		h.cache.FlushAllPagesForInum(callerSID, inum, flushByHandle)
	}

	// Remove up to 511 entries and write their VAs to the response page.
	// Range-bounded removal is critical: a partial munmap must NOT drop
	// cache entries for pages still mapped in the caller's address space.
	var removed []pageCacheEntry
	if allFDs || !inumKnown {
		removed = h.cache.RemoveAllBatch(callerSID, maxFlushVAsPerRound)
	} else if rangeBound {
		removed = h.cache.RemoveRangeOffsetBatch(callerSID, inum, startOffset, rangeLen, maxFlushVAsPerRound)
		h.closeOrphanHandleIfDrained(callerSID, inum)
	} else {
		removed = h.cache.RemoveRangeBatch(callerSID, inum, maxFlushVAsPerRound)
		// If this round drained the last pages for the inum, close the
		// fs handle that was kept alive past sysClose for writeback.
		h.closeOrphanHandleIfDrained(callerSID, inum)
	}

	// Write response: count + VA array
	count := uint32(len(removed))
	*(*uint32)(unsafe.Pointer(&responseBuf[0])) = count
	*(*uint32)(unsafe.Pointer(&responseBuf[4])) = 0 // reserved

	for i, entry := range removed {
		offset := 8 + i*8
		*(*uint64)(unsafe.Pointer(&responseBuf[offset])) = uint64(entry.VA)
	}

	// Verbose mmap-flush trace disabled — UART saturation.
	req.Reply(int64(count))
}

// writePageToExt2 writes a single page to ext2 for the given (sid, fd, offset).
func (h *syscallHandler) writePageToExt2(sid int16, fd int, offset int64, data []byte) (int, error) {
	fdt := h.getShepherd(sid).FDT
	e := fdt.Get(fd)
	if e == nil || e.Kind != fdtable.KindFile {
		return 0, nil // skip non-file fds
	}
	written, err := h.fs.Write(e.Handle, offset, data)
	if err != nil {
		return 0, err
	}
	endPos := offset + int64(written)
	if uint32(endPos) > e.Size {
		e.Size = uint32(endPos)
	}
	return written, nil
}

// writePageByHandle writes a single page to ext2 using a stored fs handle
// directly, bypassing the FDT. Used by mmap flush paths because the cache
// is keyed by inode and stores its own copy of the handle — the original
// fd may have been closed (Linux semantics: mmap survives close) and the
// FDT lookup would fail. The fs handle stays alive across close so long
// as cache pages reference it (sysClose checks HasPagesFor).
func (h *syscallHandler) writePageByHandle(sid int16, handle uint32, offset int64, data []byte) (int, error) {
	if handle == 0 {
		return 0, nil
	}
	written, err := h.fs.Write(handle, offset, data)
	if err != nil {
		return 0, err
	}
	// We don't update e.Size here — if the fd is still open and the size
	// changed, a subsequent fstat will go through to ext2 which returns
	// the authoritative size. Keeping e.Size stale is harmless because
	// only the original fd's reads use it, and they typically don't
	// straddle the just-flushed mmap region.
	_ = sid
	return written, nil
}

// ============================================================
// Helpers
// ============================================================

// fillStdioStatBuf writes a minimal stat struct for stdin/stdout/stderr.
// Arch-specific: see stat_arm64.go / stat_amd64.go / stat_riscv64.go.

// errToErrno converts an fsclient error to a negative errno value.
func errToErrno(err error) int32 {
	if err == nil {
		return 0
	}
	if errno, ok := fsclient.IsErrno(err); ok {
		return errno
	}
	return -5 // EIO
}
