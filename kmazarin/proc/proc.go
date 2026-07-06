// Package proc provides per-process state shared between kmazarin's internal
// packages (ksyscall, kmem, kirq) without circular imports.
//
// The key type is Shepherd, which represents a userspace process. Previously
// Shepherd lived in kmazarin/kmazarin (the main package), forcing ksyscall and
// kmem to use go:linkname hacks to access per-process state. Moving Shepherd
// here breaks the cycle: proc imports nothing from kmazarin, so any package
// can import proc freely.
package proc


// SignalNSIG is the number of signals (1-64 + sentinel), matching Linux _NSIG.
const SignalNSIG = 65

// ShepherdSignalAction records a signal handler for a shepherd.
// Layout matches Go runtime's sigactiont struct.
type ShepherdSignalAction struct {
	Handler  uint64
	Flags    uint64
	Restorer uint64
	Mask     uint64
}

// ShepherdId is a unique shepherd (userspace process) identifier; valid
// values are 0 (kernel) and the [MinPID, MaxPID] PID range.
type ShepherdId int32

// Shepherd represents a userspace process that runs Go code.
// Each shepherd has its own address space and Go runtime.
type Shepherd struct {
	PID                   ShepherdId // Unique shepherd identifier
	_reservedAsyncPreempt uint64   // Padding (was AsyncPreemptAddr — now unused)

	// Filename is the ELF filename used to launch this shepherd (e.g., "/rachel.elf").
	// Stored at launch time for introspection via ShepherdInfo syscall.
	Filename string

	// Per-shepherd tick accounting — all thread ticks roll up here
	TotalTicksRunning   uint64 // Cumulative ticks across all threads of this shepherd
	TicksStartedRunning uint64 // When current thread of this shepherd started (0 = none running)

	// Thread tracking for shepherd cleanup
	ThreadCount int32 // Number of live threads belonging to this shepherd

	// Per-process userspace memory management.
	// BumpPointer is the next VA to hand out. Zero means uninitialized;
	// lazily set to userMmapStart on first mmap call.
	BumpPointer uint64

	// IPCBumpPointer is a separate bump pointer for cross-shepherd VA
	// allocations (IPC data pages, mailbox pages, shared pages, mmap page
	// fill). Uses a different VA range (ipcDataVAStart) to avoid collisions
	// with Go's heap allocator, which also allocates from userMmapStart.
	// Zero means uninitialized.
	IPCBumpPointer uint64

	// Spans tracks reserved VA ranges for this process (mmap hint reservations,
	// MAP_FIXED mappings, etc.).
	Spans LockedSpanGroup

	// PageTableL0PA is the physical address of this shepherd's L0 page table.
	// Used for page table walks during cleanup on shepherd exit.
	PageTableL0PA uintptr

	// SignalActions is the per-shepherd signal action table.
	// Index 0 is unused (signal numbers are 1-based).
	// Each shepherd has its own table so handlers are isolated between processes.
	SignalActions [SignalNSIG]ShepherdSignalAction

	// SymbolTable caches the shepherd's ELF symbol name → VA address mapping.
	// Built during SyscallLaunch. Consumed by SysGetOwnExports, which serializes
	// it to userspace so mazdl can register the shepherd's host exports for
	// plugin binding (see mazarin/mazdl/host_register_mazarin.go).
	SymbolTable map[string]uint64

	// HighestVA tracks the highest VA address used by the shepherd's loaded segments.
	HighestVA uint64

	// EpollFd is the magic fd for the epoll instance (0x7ef when created, 0 = uninitialized).
	// Set by SyscallEpollCreate, cleared by SyscallClose.
	EpollFd int32

	// EventFd is the magic fd for the eventfd (0x7ee when created, 0 = uninitialized).
	// Set by SyscallEventfd, cleared by SyscallClose.
	EventFd int32

	// EventDataPtr is the ev.Data value from epoll_ctl(EPOLL_CTL_ADD, eventfd, &ev).
	// Stock netpoll_epoll.go stores &netpollEventFd in ev.Data when registering the
	// eventfd with epoll. SyscallEpollCtl captures this so SyscallEpollPwait can
	// return a synthetic EPOLLIN event with the correct Data — which causes the
	// runtime's netpoll() to call read(eventfd) and reset netpollWakeSig back to 0.
	EventDataPtr uint64

	// Netpoll waiter — TID of thread blocked in SyscallEpollPwait (0 = none).
	// Set by SyscallEpollPwait before blocking, cleared on wake.
	// Read by SyscallWrite when fd matches the eventfd — the write wakes the
	// sleeping thread, implementing Go's netpollBreak mechanism.
	NetpollWaiterTID int32

	// EventFdPending matches Linux eventfd semantics: writes accumulate.
	// On real Linux, writing to an eventfd increments a counter. If the
	// eventfd is in an epoll set, the next epoll_wait returns immediately
	// because the fd is readable. In Mazzy, SyscallWrite(fd=11) sets this
	// flag when NetpollWaiterTID==0 (nobody in epoll_wait yet). When
	// SyscallEpollPwait enters, it checks this flag and returns immediately
	// instead of blocking — matching the Linux behavior where a prior
	// eventfd write causes the next epoll_wait to return.
	EventFdPending uint32

	// Ready indicates this shepherd is ready to accept delegated work.
	// Set by SysSetReady, checked by the kernel before delegating syscalls.
	// 0 = not ready, 1 = ready.
	Ready int32

	// UringID is the 64-bit sequential identifier for this shepherd's IPC uring.
	// Assigned at shepherd launch, 0 = not yet assigned.
	UringID uint64

	// UringRingPA is the physical address of the first of 3 ring pages.
	// 0 = ring not allocated. The second and third pages follow at PA+4096, PA+8192.
	UringRingPA uintptr

	// DMAClumps tracks physically contiguous page ranges allocated by this
	// shepherd via mmap(MAZARIN_CONTIGUOUS). Used by BlockSubmit for VA→PA
	// resolution and by munmap/death for safe deferred page release.
	// Fixed-size array (not heap-allocated) so registration is nosplit-safe
	// inside SyscallMmap on the g0 stack.
	//
	// clumpSpin protects DMAClumps/NumDMAClumps against TOCTOU between
	// FindClumpByVA (returns a pointer into the array) and the subsequent
	// InFlight bump. RemoveClump swaps array elements, so a pointer from
	// FindClumpByVA can point to the wrong clump if RemoveClump runs
	// concurrently. Under cooperative scheduling this window is closed
	// (no yield between lookup and bump), but the lock guards against
	// future SMP or interrupt-driven reentrancy.
	clumpSpin    int32
	DMAClumps    [MaxDMAClumps]DMAClump
	NumDMAClumps int32

	// FileMappings tracks file-backed mmap regions. When a page fault occurs
	// in one of these ranges, the kernel reads file data and maps it read-only.
	FileMappings [MaxFileMappings]FileMapping

	// MAZ-70: Linux-emulation process bookkeeping. Empty / zero for boot-time
	// kernel-launched shepherds; populated for forked Linux processes. Methods
	// to manipulate these fields live in shepherd_state.go.

	// ParentPID is the PID of this process's parent. 0 means no parent
	// (boot-time launched shepherd).
	ParentPID ShepherdId

	// Children holds the PIDs of this shepherd's live children.
	// Manipulate via AddChild / RemoveChild; iterate via EachChild.
	// The slot ordering is implementation-defined.
	Children    [MaxChildrenPerShepherd]ShepherdId
	NumChildren int32

	// Zombie + ExitStatus implement Linux-style zombie reaping. While
	// Zombie is true, the shepherd has exited but the parent has not yet
	// called wait4 — ExitStatus is the recorded exit code.
	Zombie     bool
	ExitStatus int32

	// Environ + EnvironLen hold the raw environ block (null-separated
	// bytes) for use at the next execve. Manipulate via SetEnviron /
	// GetEnviron.
	Environ    [MaxEnvironBytes]byte
	EnvironLen uint32

	// MAZ-75: buffered in-child intent from clone_exec. Set by the kernel
	// during DoCloneExecWork; consumed and cleared by the new shepherd's
	// startup stub (landed via MAZ-78/79) BEFORE exec'ing the target ELF.
	// Empty / zero for shepherds spawned outside clone_exec (boot-time
	// launched, etc.). See kmazarin/proc/clone_exec.go for the type defs
	// and the storage-vs-application split.
	StartupIntent    [MaxStartupIntentOps]CloneExecIntentOp
	NumStartupIntent uint32
	StartupCwd       [MaxStartupCwdBytes]byte
	StartupCwdLen    uint32

	// StdioRedirectMask (MAZ-149) — per-process stdio redirect flag: bit 0 =
	// fd 1 redirected away from the console, bit 1 = fd 2. Computed by the
	// linux shepherd's sysExecve from the child's final FD-table state and
	// stored here at creation (SetStartupState, under schedulerLock, before
	// the child is enqueued) so SyscallWrite can split console writes (kernel
	// UART fast path + fire-and-forget display delegate) from redirected ones
	// (blocking delegate the shepherd routes to the capture pipe) without a
	// shepherd round trip. Zero for boot-launched shepherds (console stdio).
	// Runtime dup3/close re-redirect maintenance is MAZ-151.
	StdioRedirectMask uint8
}

// Id implements the ds.Ider interface for Shepherd.
func (p *Shepherd) Id() int32 {
	return int32(p.PID)
}

// KernelShepherd holds the per-process state for the kernel shepherd (PID 0).
// PID 0 is out of [MinPID, MaxPID] so it cannot live in ShepherdStorage; it
// gets its own slot. All kernel threads (PID == 0) belong to this shepherd.
var KernelShepherd Shepherd

// Shepherds is the sparse PID-keyed live-shepherd storage. PIDs in
// [MinPID, MaxPID] live here; PID 0 lives in KernelShepherd.
//
// Concurrency: callers hold schedulerLock (in the kmazarin package) before
// invoking methods on Shepherds.
var Shepherds = NewShepherdStorage()

// GetCurrentShepherd is a function hook registered by the main package at boot.
// It returns a pointer to the Shepherd for the currently running thread, or nil
// for kernel threads (PID 0) and calls before boot registration.
//
// The registered function must be //go:nosplit safe.
var GetCurrentShepherd func() *Shepherd

// CurrentShepherd calls the registered GetCurrentShepherd hook.
// Safe to call before registration: returns nil.
//
//go:nosplit
func CurrentShepherd() *Shepherd {
	f := GetCurrentShepherd
	if f == nil {
		return nil
	}
	return f()
}

// FindShepherdBySID looks up a shepherd by PID. Returns nil if no live
// shepherd with that PID exists. Wrapper over Shepherds.Get (plus the
// KernelShepherd special-case for PID 0).
//
//go:nosplit
func FindShepherdBySID(pid ShepherdId) *Shepherd {
	if pid == 0 {
		return &KernelShepherd
	}
	if shep, ok := Shepherds.Get(pid); ok {
		return shep
	}
	return nil
}
