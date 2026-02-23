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

// PriestId is a unique priest (userspace process) identifier (0-MaxPriests-1).
type PriestId int16

// Priest represents a userspace process that runs Go code.
// Each priest has its own address space, Go runtime, and asyncPreempt function.
type Priest struct {
	PID              PriestId // Unique priest identifier
	AsyncPreemptAddr uint64   // Address of this priest's runtime.asyncPreempt function

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
