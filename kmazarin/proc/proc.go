// Package proc provides per-process state shared between kmazarin's internal
// packages (ksyscall, kmem, kirq) without circular imports.
//
// The key type is Priest, which represents a userspace process. Previously
// Priest lived in kmazarin/kmazarin (the main package), forcing ksyscall and
// kmem to use go:linkname hacks to access per-process state. Moving Priest
// here breaks the cycle: proc imports nothing from kmazarin, so any package
// can import proc freely.
package proc

// MaxPriests is the maximum number of priest processes (userspace programs).
const MaxPriests = 32

// SignalNSIG is the number of signals (1-64 + sentinel), matching Linux _NSIG.
const SignalNSIG = 65

// PriestSignalAction records a signal handler for a priest.
// Layout matches Go runtime's sigactiont struct.
type PriestSignalAction struct {
	Handler  uint64
	Flags    uint64
	Restorer uint64
	Mask     uint64
}

// PriestId is a unique priest (userspace process) identifier (0-MaxPriests-1).
type PriestId int16

// MaxIPCPending is the maximum number of queued IPC requests per priest.
const MaxIPCPending = 8

// IPCRequest represents a pending IPC request from a client priest.
type IPCRequest struct {
	SenderPID    PriestId // PID of the sending priest
	SenderTID    int16    // TID of the sending thread (blocked in ThreadBlockedIPC)
	RequestVA    uint64   // VA of the request data in the receiver's address space
	RequestPages uint32   // Number of pages transferred
}

// Priest represents a userspace process that runs Go code.
// Each priest has its own address space and Go runtime.
type Priest struct {
	PID                   PriestId // Unique priest identifier
	_reservedAsyncPreempt uint64   // Padding (was AsyncPreemptAddr — now unused)

	// Per-priest tick accounting — all thread ticks roll up here
	TotalTicksRunning   uint64 // Cumulative ticks across all threads of this priest
	TicksStartedRunning uint64 // When current thread of this priest started (0 = none running)

	// Thread tracking for priest cleanup
	ThreadCount int32 // Number of live threads belonging to this priest

	// Per-process userspace memory management.
	// BumpPointer is the next VA to hand out. Zero means uninitialized;
	// lazily set to userMmapStart on first mmap call.
	BumpPointer uint64

	// Spans tracks reserved VA ranges for this process (mmap hint reservations,
	// MAP_FIXED mappings, etc.).
	Spans LockedSpanGroup

	// PageTableL0PA is the physical address of this priest's L0 page table.
	// Used for page table walks during cleanup on priest exit.
	PageTableL0PA uintptr

	// SignalActions is the per-priest signal action table.
	// Index 0 is unused (signal numbers are 1-based).
	// Each priest has its own table so handlers are isolated between processes.
	SignalActions [SignalNSIG]PriestSignalAction

	// SymbolTable caches the priest's ELF symbol name → VA address mapping.
	// Built during SyscallLaunch so that SysLoadMaz can resolve .maz imports
	// against the priest's real functions at load time.
	SymbolTable map[string]uint64

	// HighestVA tracks the highest VA address used by the priest's loaded segments.
	// Used by SysLoadMaz to determine where to place .maz segments.
	HighestVA uint64

	// IPC request queue — ring buffer of pending requests from client priests.
	// Producers: SysIPCCall (enqueues). Consumer: SysIPCRecv (dequeues).
	IPCQueue         [MaxIPCPending]IPCRequest
	IPCQueueHead     uint32  // Next slot to dequeue from
	IPCQueueTail     uint32  // Next slot to enqueue into
	IPCRecvTID       int16   // TID of thread blocked in SysIPCRecv (-1 = none)
	IPCRecvResultPtr uintptr // VA of IPCRecvResult in receiver's address space (stashed while blocked)
}

// Id implements the ds.Ider interface for Priest.
func (p *Priest) Id() int32 {
	return int32(p.PID)
}

// PriestListData is the backing array for the priest list, indexed by list slot
// (NOT necessarily by PID — use Thread.PriestIdx for O(1) access).
// Exported so kmazarin/kmazarin and other packages can access it directly.
var PriestListData [MaxPriests]Priest

// PriestListInUse tracks which slots in PriestListData are occupied.
var PriestListInUse [MaxPriests]bool

// GetCurrentPriest is a function hook registered by the main package at boot.
// It returns a pointer to the Priest for the currently running thread, or nil
// for kernel threads (PID 0) and calls before boot registration.
//
// The registered function must be //go:nosplit safe.
var GetCurrentPriest func() *Priest

// CurrentPriest calls the registered GetCurrentPriest hook.
// Safe to call before registration: returns nil.
//
//go:nosplit
func CurrentPriest() *Priest {
	f := GetCurrentPriest
	if f == nil {
		return nil
	}
	return f()
}

// FindPriestByPID looks up a priest by PID without requiring the kmazarin/kmazarin package.
// Returns nil if no priest with the given PID is found.
//
//go:nosplit
func FindPriestByPID(pid PriestId) *Priest {
	for i := 0; i < MaxPriests; i++ {
		if PriestListInUse[i] && PriestListData[i].PID == pid {
			return &PriestListData[i]
		}
	}
	return nil
}
