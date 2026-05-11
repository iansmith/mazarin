// Package proc provides per-process state shared between kmazarin's internal
// packages (ksyscall, kmem, kirq) without circular imports.
//
// The key type is Shepherd, which represents a userspace process. Previously
// Shepherd lived in kmazarin/kmazarin (the main package), forcing ksyscall and
// kmem to use go:linkname hacks to access per-process state. Moving Shepherd
// here breaks the cycle: proc imports nothing from kmazarin, so any package
// can import proc freely.
package proc


// MaxShepherds is the maximum number of shepherd processes (userspace programs).
const MaxShepherds = 32

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

// ShepherdId is a unique shepherd (userspace process) identifier (0-MaxShepherds-1).
type ShepherdId int16

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
}

// Id implements the ds.Ider interface for Shepherd.
func (p *Shepherd) Id() int32 {
	return int32(p.PID)
}

// ShepherdListData is the backing array for the shepherd list, indexed by list slot
// (NOT necessarily by PID — use Thread.ShepherdIdx for O(1) access).
// Exported so kmazarin/kmazarin and other packages can access it directly.
var ShepherdListData [MaxShepherds]Shepherd

// ShepherdListInUse tracks which slots in ShepherdListData are occupied.
var ShepherdListInUse [MaxShepherds]bool

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

// FindShepherdBySID looks up a shepherd by PID without requiring the kmazarin/kmazarin package.
// Returns nil if no shepherd with the given PID is found.
//
//go:nosplit
func FindShepherdBySID(pid ShepherdId) *Shepherd {
	for i := 0; i < MaxShepherds; i++ {
		if ShepherdListInUse[i] && ShepherdListData[i].PID == pid {
			return &ShepherdListData[i]
		}
	}
	return nil
}
