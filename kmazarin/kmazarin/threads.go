
package main

import (
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/ds"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/util"
	"sync/atomic"
	"unsafe"
)

// Syscall return codes for assembly
const (
	SyscallReturnNormal = 0 // Return normally with value in x0
	SyscallReturnSwitch = 1 // Context switch to thread in x1
	SyscallReturnBlock  = 2 // Block current thread, switch to thread in x1
)

// Timer frequency (set from timer init)
var timerFrequencyHz uint64 = 62500000 // Default 62.5 MHz for QEMU

// kmazarinG0Addr holds kmazarin's g0 address, set at startup.
// The exception handler uses this to switch to a valid kmazarin g
// when handling syscalls from userspace (which has a different g).
var kmazarinG0Addr uint64

// ThreadState represents the state of a thread
type ThreadState int8

// Thread states enumeration
const (
	ThreadFree            ThreadState = 0 // Slot available
	ThreadRunning         ThreadState = 1 // Currently executing
	ThreadReady           ThreadState = 2 // Runnable, waiting to be scheduled
	ThreadBlockedFutex    ThreadState = 3 // Blocked on futex_wait
	ThreadSleeping        ThreadState = 4 // Blocked on nanosleep
	ThreadExited          ThreadState = 5 // Thread has exited (being cleaned up)
	ThreadBlockedSoftIRQ  ThreadState = 6 // Blocked waiting for soft IRQ
)

// MaxPriests is the maximum number of priest processes (userspace programs)
const MaxPriests = 32

// MaxThreads is the maximum number of threads supported
// Increased from 24 to 64 to support multiple priests with goroutines
const MaxThreads = 64

// ReservedKernelThreads is the number of thread slots reserved for kernel threads.
// These slots (0 to ReservedKernelThreads-1) are for kernel use only.
// Userspace threads get IDs from ReservedKernelThreads to MaxThreads-1 (shuffled).
const ReservedKernelThreads = 8

// ReservedKernelPriests is the number of priest slots reserved for the kernel.
// Slot 0 is for the kernel's "priest" entry (PID 0, used for kernel threads).
const ReservedKernelPriests = 1

// Priest represents a userspace process that runs Go code.
// Each priest has its own address space, Go runtime, and asyncPreempt function.
type Priest struct {
	PID              PriestId // Unique priest identifier
	AsyncPreemptAddr uint64   // Address of this priest's runtime.asyncPreempt function

	// Per-priest tick accounting — all thread ticks roll up here
	TotalTicksRunning   uint64 // Cumulative ticks across all threads of this priest
	TicksStartedRunning uint64 // When current thread of this priest started (0 = none running)
}

// Id implements the ds.Ider interface for Priest
func (p *Priest) Id() int32 {
	return int32(p.PID)
}

// ThreadContext holds saved CPU state for a thread
type ThreadContext struct {
	// General purpose registers x0-x30
	X [31]uint64

	// Special registers
	SP   uint64 // SP_EL0
	ELR  uint64 // Return address (ELR_EL1)
	SPSR uint64 // Processor state (SPSR_EL1)
}

// ThreadId is a unique thread identifier (0-31)
type ThreadId int16

// PriestId is a unique priest (userspace process) identifier (0-3)
type PriestId int16

// Thread represents a single thread (corresponds to a Go M)
type Thread struct {
	State         ThreadState // ThreadRunning, ThreadReady, etc.
	TID           ThreadId    // Unique thread ID from threadIdAllocator (0-31)
	PID           PriestId    // Priest (process) ID for ASID (-1 = kernel thread)
	FutexAddr     uint64      // Address being waited on (for ThreadBlockedFutex)
	MPtr          uint64      // Pointer to Go M struct
	GPtr          uint64      // Pointer to Go g struct (g0 for this M)
	EntryFunc     uint64      // Entry function (mstart)
	PageTableL0PA uintptr     // Physical address of L0 page table (0 = use kernel's)
	Context       ThreadContext

	// Preemption tracking - deadline-based (raw tick counts, no division in timer handler)
	LastSeenG              uint64 // g pointer seen at last timer tick
	StartTick              uint64 // timer tick when this THREAD started running
	GoroutineStart         uint64 // timer tick when this GOROUTINE started running
	ThreadPreemptDeadline  uint64 // timer tick when thread preemption should occur
	GoroutinePreemptDeadline uint64 // timer tick when goroutine preemption should occur
	PreemptElapsed         uint64 // elapsed ticks saved when thread switched away
	GoroutineElapsed       uint64 // elapsed goroutine ticks saved when thread switched away
	AsyncPreemptAddr       uint64 // Address of asyncPreempt function for this thread's runtime

	// Runtime accounting
	TotalTicksRunning   uint64 // Cumulative timer ticks this thread has been running
	TicksStartedRunning uint64 // Timer tick count when this thread started its current run (0 = not running)

	// Clone child protection - skip async preempt until clone setup completes
	InCloneSetup uint32 // 1 = thread is in clone setup (reading fn/gp/mp from stack), 0 = normal

	// WARNING: PriestIdx is a priest LIST INDEX (not a PID). Used ONLY by time-critical
	// code paths (timer IRQ top-half, preemption checks) that need O(1) access to
	// the priest's tick accounting without a PID→Priest lookup. This index is set
	// once at thread creation and never changes. Do NOT use for general priest
	// lookups — use GetPriestByPID(t.PID) instead.
	PriestIdx int16

	// Per-thread syscall state - prevents race condition when timer IRQ preempts mid-syscall
	// and another thread makes a syscall that overwrites global state.
	SyscallELR    uint64  // ELR for this thread's current syscall (return address)
	SyscallSPSR   uint64  // SPSR for this thread's current syscall (processor state)
	SyscallSwitch uintptr // Context switch target for clone (0 = no switch, else ptr to ThreadContext)
}

// Thread struct field offsets for assembly access.
// These are computed from unsafe.Offsetof() in initThreadOffsets() and MUST be
// initialized before any assembly code reads them (before timer IRQ is enabled).
var (
	ThreadContextOffset           uintptr // Offset of Context field within Thread struct
	ThreadLastSeenGOffset         uintptr
	ThreadStartTickOffset         uintptr
	ThreadGoroutineStartOffset    uintptr
	ThreadPreemptDeadlineOffset   uintptr
	ThreadGoroutineDeadlineOffset uintptr
	ThreadAsyncPreemptAddrOffset  uintptr
	ThreadInCloneSetupOffset      uintptr
)

// initThreadOffsets computes Thread struct field offsets using unsafe.Offsetof().
// MUST be called before any assembly code reads these offsets (before timer IRQ is enabled).
//
//go:nosplit
func initThreadOffsets() {
	var t Thread
	ThreadContextOffset = unsafe.Offsetof(t.Context)
	ThreadLastSeenGOffset = unsafe.Offsetof(t.LastSeenG)
	ThreadStartTickOffset = unsafe.Offsetof(t.StartTick)
	ThreadGoroutineStartOffset = unsafe.Offsetof(t.GoroutineStart)
	ThreadPreemptDeadlineOffset = unsafe.Offsetof(t.ThreadPreemptDeadline)
	ThreadGoroutineDeadlineOffset = unsafe.Offsetof(t.GoroutinePreemptDeadline)
	ThreadAsyncPreemptAddrOffset = unsafe.Offsetof(t.AsyncPreemptAddr)
	ThreadInCloneSetupOffset = unsafe.Offsetof(t.InCloneSetup)
}

// Id implements the ds.Ider interface
// Returns the TID (unique thread ID), NOT the slot index
// Id returns the thread's ID as int32.
// Uses pointer receiver to avoid copying the entire Thread struct (368 bytes).
func (t *Thread) Id() int32 {
	return int32(t.TID)
}

// ========== Static Allocation Data Structures ==========

// Backing arrays - statically allocated, zero-initialized
var threadListData [MaxThreads]Thread    // Stores Thread VALUES (not pointers)
var threadListInUse [MaxThreads]bool     // false = available (zero value)
var priestListData [MaxPriests]Priest    // Stores Priest VALUES (not pointers)
var priestListInUse [MaxPriests]bool     // false = available (zero value)
var readyQueueData [MaxThreads]ThreadId  // Stores TIDs (unique thread IDs)
var readyQueueInUse [MaxThreads]bool     // Tracks holes in ready queue
var blockedQueueData [MaxThreads]ThreadId // Stores TIDs (unique thread IDs)
var blockedQueueInUse [MaxThreads]bool   // Tracks holes in blocked queue
var sleepingQueueData [MaxThreads]ThreadId // Stores TIDs (unique thread IDs)
var sleepingQueueInUse [MaxThreads]bool  // Tracks holes in sleeping queue

// Static deadline queue backing arrays - used by timer top-half (nosplit path)
var staticDeadlineData [MaxThreads]int16
var staticDeadlineOrderBy [MaxThreads]uint64
var staticDeadlineQueue ds.StaticOrderedList

// ID allocator backing arrays - statically allocated
var threadIdStackData [MaxThreads]ThreadId // Backing array for thread ID allocator
var priestIdStackData [MaxPriests]PriestId // Backing array for priest ID allocator

// nextKernelThreadId is a counter for allocating kernel thread IDs (0 to ReservedKernelThreads-1).
// Kernel threads are identified by PID == 0 (the kernel priest) and get IDs from this counter.
// Starts at 0, incremented each time a kernel thread is created.
// Panics if exhausted (kernel has limited threads).
var nextKernelThreadId ThreadId = 0

// Data structures - will be initialized in InitThreads()
// DO NOT initialize slices here - Go's initialization order causes them to be length 0!
var threadList ds.StaticList[*Thread, Thread] // StaticList stores Thread VALUES, returns pointers
var priestList ds.StaticList[*Priest, Priest] // StaticList stores Priest VALUES, returns pointers

var readyQueue ds.StaticQueue[ThreadId]

var blockedQueue ds.StaticQueue[ThreadId]

var sleepingQueue ds.StaticQueue[ThreadId]

// ID allocators - initialized in InitIdAllocators()
var threadIdAllocator ds.StaticAllocator[ThreadId] // Manages unique thread IDs (0..MaxThreads-1)
var priestIdAllocator ds.StaticAllocator[PriestId] // Manages unique priest IDs (0..MaxPriests-1)

// ========== Scheduler Lock ==========

// schedulerLock protects ALL scheduler structures:
// - threadList, readyQueue, blockedQueue, sleepingQueue
// - threadIdAllocator, priestIdAllocator
// - CurrentThreadIdx, CurrentThread
// - All thread state transitions
//
// LOCK DISCIPLINE: This single lock prevents nested locking deadlocks.
// All scheduler operations must acquire this lock, never individual structure locks.
var schedulerLock ds.Spinlock

// ========== Thread State ==========

// Current thread index (-1 = none)
var CurrentThreadIdx int32 = -1

// CurrentThread is the current thread pointer (nil = none)
// Uses unsafe.Pointer to avoid GC write barriers in exception context.
// MUST use atomic.LoadPointer/StorePointer for all accesses.
// Direct assignments like "CurrentThread = x" will trigger write barriers!
// Exported for access from assembly (kirq package timer handler).
var CurrentThread unsafe.Pointer // Points to *Thread

// Global tick counter (incremented by timer)
var globalTickCounter uint64 = 0

// Thread preemption threshold in 10ms intervals
// Goroutine preemption is at 5 intervals (50ms), thread preemption is 2x that
const ThreadPreemptionThreshold = 10 // 100ms

// Syscall context switch signaling
// Primary storage is per-thread (Thread.SyscallSwitch) to prevent race conditions
// when timer IRQ preempts mid-syscall. Global is fallback during early boot before
// CurrentThread is set up.
// 0 = no switch, non-zero = pointer to ThreadContext to switch to
var syscallSwitchTarget uintptr = 0

// syscallELR stores the ELR_EL1 for the current syscall
// Primary storage is per-thread (Thread.SyscallELR) to prevent race conditions
// when timer IRQ preempts mid-syscall. Global is fallback during early boot before
// CurrentThread is set up.
var syscallELR uint64 = 0

// syscallSPSR stores the SPSR_EL1 for the current syscall
// Primary storage is per-thread (Thread.SyscallSPSR) to prevent race conditions
// when timer IRQ preempts mid-syscall. Global is fallback during early boot before
// CurrentThread is set up.
var syscallSPSR uint64 = 0

// threadsInitialized tracks if InitThreads has been called
var threadsInitialized bool = false

// deadlineQueue holds TimerDeadlines sorted by deadline time (ascending).
// Used for nanosleep and other timed wakeups.
// nil until InitDeadlineQueue is called.
var deadlineQueue *ds.OrderedList[*util.TimerDeadline]

// setSyscallELRInternal is called by assembly via ABI stub to store the current ELR
// Stores to the current thread's SyscallELR field to prevent race conditions.
// Falls back to global variable during early boot when CurrentThread is not yet set up.
//
//go:noinline
func setSyscallELRInternal(elr uint64) {
	t := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if t != nil {
		t.SyscallELR = elr
	} else {
		// Early boot: no threads yet, use global
		syscallELR = elr
	}
}

// setSyscallSPSRInternal is called by assembly via ABI stub to store the current SPSR
// Stores to the current thread's SyscallSPSR field to prevent race conditions.
// Falls back to global variable during early boot when CurrentThread is not yet set up.
//
//go:noinline
func setSyscallSPSRInternal(spsr uint64) {
	t := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if t != nil {
		t.SyscallSPSR = spsr
	} else {
		// Early boot: no threads yet, use global
		syscallSPSR = spsr
	}
}

// GetSyscallELR returns the ELR for the current syscall
// Called by clone to get the child's return address.
// Reads from the current thread's SyscallELR field to prevent race conditions.
// Falls back to global variable during early boot when CurrentThread is not yet set up.
//
//go:noinline
func GetSyscallELR() uint64 {
	t := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if t != nil {
		return t.SyscallELR
	}
	// Early boot: no threads yet, use global
	return syscallELR
}

// GetSyscallSPSR returns the SPSR for the current syscall
// Called by clone to get the child's processor state.
// Reads from the current thread's SyscallSPSR field to prevent race conditions.
// Falls back to global variable during early boot when CurrentThread is not yet set up.
//
//go:noinline
func GetSyscallSPSR() uint64 {
	t := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if t != nil {
		return t.SyscallSPSR
	}
	// Early boot: no threads yet, use global
	return syscallSPSR
}

// getSyscallSwitchTargetInternal returns the switch target and resets it
// Called from assembly via ABI stub after syscall dispatch
// Returns uint64: context pointer to switch to, or 0 for no switch.
// Reads from the current thread's SyscallSwitch field to prevent race conditions.
// Falls back to global variable during early boot when CurrentThread is not yet set up.
//
//go:noinline
func getSyscallSwitchTargetInternal() uint64 {
	var target uintptr

	t := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if t != nil {
		target = t.SyscallSwitch
		t.SyscallSwitch = 0 // Reset for next syscall
	} else {
		// Early boot: no threads yet, use global
		target = syscallSwitchTarget
		syscallSwitchTarget = 0 // Reset for next syscall
	}

	// Return context pointer directly (0 means no switch)
	return uint64(target)
}

// SetSyscallSwitchTarget sets the context pointer to switch to
// Called by syscall handlers that need to block
// target is a ThreadContext pointer, or 0 for no switch.
// Stores to the current thread's SyscallSwitch field to prevent race conditions.
// Falls back to global variable during early boot when CurrentThread is not yet set up.
//
//go:noinline
func SetSyscallSwitchTarget(target uintptr) {
	// Store to current thread's SyscallSwitch field
	// 0 unambiguously means "no switch" and non-zero is valid target
	t := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if t != nil {
		t.SyscallSwitch = target
	} else {
		// Early boot: no threads yet, use global
		syscallSwitchTarget = target
	}
}

// InitIdAllocators initializes the ID allocators for thread IDs and priest IDs.
// Must be called before any threads are created.
// Kernel thread IDs (0 to ReservedKernelThreads-1) are reserved.
// Userspace thread IDs (ReservedKernelThreads to MaxThreads-1) are shuffled.
//
//go:nosplit
func InitIdAllocators() {
	// Initialize thread ID allocator with kernel slots reserved
	// IDs 0..ReservedKernelThreads-1 are for kernel threads (not in shuffle pool)
	// IDs ReservedKernelThreads..MaxThreads-1 are shuffled for userspace
	threadIdAllocator.InitWithReserved(threadIdStackData[:], ReservedKernelThreads)

	// Initialize priest ID allocator with ID 0 reserved for kernel
	// IDs 1..MaxPriests-1 are shuffled and available for Acquire()
	priestIdAllocator.InitWithReserved(priestIdStackData[:], 1)

	// Reset kernel thread counter (starts at 0, used by AcquireKernelThreadId)
	nextKernelThreadId = 0
}

// AcquireKernelThreadId allocates the next kernel thread ID.
// Kernel threads belong to PID 0 (kernel priest) and get sequential IDs 0..ReservedKernelThreads-1.
// Panics if all kernel thread slots are exhausted.
//
//go:nosplit
func AcquireKernelThreadId() ThreadId {
	if nextKernelThreadId >= ThreadId(ReservedKernelThreads) {
		panic("kernel thread IDs exhausted")
	}
	id := nextKernelThreadId
	nextKernelThreadId++
	return id
}

// IsKernelThread returns true if this is a kernel thread (PID == 0, the kernel priest).
// Used by Clone syscall to determine which allocator to use.
//
//go:nosplit
func IsKernelThread(t *Thread) bool {
	return t != nil && t.PID == 0
}


// InitThreads initializes the thread management system
// Creates M0's thread as the current running thread
//
//go:nosplit
//go:noinline
func InitThreads() {
	if threadsInitialized {
		return
	}

	// Compute assembly-accessible offsets from struct layout
	initThreadOffsets()

	// Initialize spinlock timing based on detected timer frequency
	// This must happen before any spinlocks are used (including in ID allocators)
	ds.InitSpinlockTiming(timerFrequencyHz)

	// Initialize ID allocators first (reserves kernel slots)
	InitIdAllocators()

	// CRITICAL: Initialize data structure slices from backing arrays
	// Must be done here, NOT as global initializers (Go init order issue)
	threadList.Data = threadListData[:]
	threadList.InUse = threadListInUse[:]
	priestList.Data = priestListData[:]
	priestList.InUse = priestListInUse[:]
	readyQueue.Data = readyQueueData[:]
	readyQueue.InUse = readyQueueInUse[:]
	blockedQueue.Data = blockedQueueData[:]
	blockedQueue.InUse = blockedQueueInUse[:]
	sleepingQueue.Data = sleepingQueueData[:]
	sleepingQueue.InUse = sleepingQueueInUse[:]
	staticDeadlineQueue.Init(staticDeadlineData[:], staticDeadlineOrderBy[:])

	// Initialize reserved slots for kernel use
	threadList.InitReserved(ReservedKernelThreads)
	priestList.InitReserved(ReservedKernelPriests)

	// Save kmazarin's g address early - x28 should be pointing to kmazarin's g
	// at this point (early init runs on g0/m0).
	// This is used by the exception handler when handling userspace syscalls.
	kmazarinG0Addr = GetGRegister()

	// Initialize all thread DATA to free state (direct access OK during init)
	for i := 0; i < MaxThreads; i++ {
		threadListData[i].State = ThreadFree
		threadListData[i].TID = 0
	}

	// Acquire thread ID 0 for the kernel's entry thread
	firstThreadId := AcquireKernelThreadId()

	// Set up the kernel priest at reserved slot 0
	// This represents the "kernel process" that owns all kernel threads
	p0 := priestList.ReservedGet(0)
	p0.PID = 0
	p0.AsyncPreemptAddr = 0 // Will be set later by SetKmazarinAsyncPreemptAddr
	priestList.ReservedSet(0)

	// Set up thread 0 at reserved slot 0 - the kernel's "entry" thread
	// This thread represents the initial execution context and gets scheduled
	t0 := threadList.ReservedGet(0)
	t0.State = ThreadRunning
	t0.TID = firstThreadId // Should be 0
	t0.PID = 0             // Belongs to kernel priest (slot 0)
	t0.PageTableL0PA = 0   // Kernel uses TTBR1, no TTBR0 page table
	currentTick := ds.CurrentTime(0)
	t0.StartTick = currentTick
	t0.GoroutineStart = currentTick
	t0.ThreadPreemptDeadline = currentTick + kirq.ThreadPreemptTicks
	t0.GoroutinePreemptDeadline = currentTick + kirq.GoroutinePreemptTicks
	t0.PreemptElapsed = 0
	t0.GoroutineElapsed = 0
	t0.TicksStartedRunning = currentTick // Initialize runtime accounting
	t0.TotalTicksRunning = 0

	// CRITICAL: Copy global syscall state to t0 before setting CurrentThread.
	// If SetSyscallELR/SPSR were called before InitThreads (CurrentThread was nil),
	// they stored to globals. After we set CurrentThread = t0, GetSyscallELR/SPSR
	// will read from t0's fields. Without this copy, t0's fields would be 0,
	// causing the clone child to ERET with SPSR.M=0 (EL0) instead of EL1!
	t0.SyscallELR = syscallELR
	t0.SyscallSPSR = syscallSPSR
	t0.SyscallSwitch = syscallSwitchTarget

	threadList.ReservedSet(0)

	CurrentThreadIdx = 0
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(t0))
	threadsInitialized = true
}

// InitDeadlineQueue initializes the deadline queue.
// Call this after the heap is ready.
//
//go:noinline
func InitDeadlineQueue() {
	if deadlineQueue == nil {
		deadlineQueue = ds.NewOrderedList[*util.TimerDeadline](true) // ascending order
	}
}

// AddDeadline adds a deadline to the queue.
// Returns false if the queue is not initialized.
//
//go:nosplit
//go:noinline
func AddDeadline(deadline uint64, tid int16) bool {
	if deadlineQueue == nil {
		return false
	}
	action := NewWakeThreadAction(ThreadId(tid))
	td := util.NewTimerDeadline(deadline, action)
	deadlineQueue.Insert(td)
	return true
}

// ProcessDeadlines processes all deadlines that have passed.
// Wakes up sleeping threads whose deadlines have been reached.
// Uses the hardware timer counter (CNTVCT_EL0) for accurate timing.
//
//go:nosplit
func ProcessDeadlines() {
	if deadlineQueue == nil {
		return
	}
	currentTick := kirq.ReadCounterValue()
	for !deadlineQueue.IsEmpty() && deadlineQueue.Peek() <= currentTick {
		td := deadlineQueue.Pop()
		td.Execute()
	}
}

// AddDeadlineStatic adds a deadline to the static (nosplit-safe) deadline queue.
// Acquires schedulerLock and masks IRQs.
//
//go:nosplit
//go:noinline
func AddDeadlineStatic(deadline uint64, tid int16) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()
	staticDeadlineQueue.Insert(tid, deadline)
	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// processStaticDeadlinesSchedLockHeld processes expired deadlines from the static queue.
// REQUIRES: schedulerLock held, IRQs masked.
// All operations are nosplit-safe — no interface dispatch, no heap allocation.
//
// For each expired deadline, the thread is moved directly to the ready queue:
// - ThreadBlockedFutex → clear FutexAddr, pluck from blockedQueue, push to readyQueue
// - ThreadSleeping → pluck from sleepingQueue, push to readyQueue
//
//go:nosplit
func processStaticDeadlinesSchedLockHeld() {
	currentTick := kirq.ReadCounterValue()
	for !staticDeadlineQueue.IsEmpty() {
		tid, _ := staticDeadlineQueue.PopIfLess(currentTick)
		if tid == -1 {
			break // No more expired deadlines
		}
		// Find the thread by TID and wake it
		t := threadList.FindByIdAll(int32(tid))
		if t == nil {
			continue // Thread exited
		}
		if t.State == ThreadBlockedFutex {
			t.State = ThreadReady
			t.FutexAddr = 0
			blockedQueue.Pluck(ThreadId(tid))
			enqueueReadySchedLockHeld(t)
		} else if t.State == ThreadSleeping {
			t.State = ThreadReady
			sleepingQueue.Pluck(ThreadId(tid))
			enqueueReadySchedLockHeld(t)
		}
	}
}

// ProcessDeadlinesTopHalf processes expired deadlines from the static queue.
// Called from timer IRQ top-half (assembly). Acquires schedulerLock + masks IRQs.
//
//go:nosplit
//go:noinline
func ProcessDeadlinesTopHalf() {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()
	processStaticDeadlinesSchedLockHeld()
	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// IdleLoop is called when no threads are ready to run.
// It processes deadlines and uses WFI to wait for the next interrupt.
// Returns a pointer to a ready thread when one becomes available.
//
// CRITICAL: ProcessDeadlines and findReadyThread are protected by both DAIF
// and schedulerLock because this loop runs OUTSIDE exception context. The timer
// handler also calls ProcessDeadlines, so without protection we'd have re-entrant corruption.
//
//go:nosplit
func IdleLoop(sf *SchedulerFunc) *Thread {
	for {
		// Disable interrupts and acquire scheduler lock
		savedDAIF := sf.DisableAndSaveDAIF()
		schedulerLock.Lock()

		ProcessDeadlines()
		processStaticDeadlinesSchedLockHeld()
		ready := findReadyThreadSchedLockHeld()

		if sf.StateCheck != nil {
			sf.StateCheck("idle-loop-check")
		}

		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)

		if ready != nil {
			return ready
		}
		WaitForInterrupt()
	}
}

// KernelIdleLoop is the permanent idle loop for kernel thread 0 (m0/g0).
// Called from main() after all setup is complete and priest threads are on the
// ready queue. This function never returns.
//
// Thread 0 stays alive as a normal scheduled thread. The timer IRQ preempts it
// and context-switches to priest threads on the ready queue. When thread 0 gets
// scheduled back, it resumes here — processing deadlines and waiting for the
// next interrupt.
//
// LOCK DISCIPLINE: save DAIF → mask IRQs → acquire lock → process → release → restore → WFI
//
//go:nosplit
//go:noinline
func KernelIdleLoop() {
	for {
		savedDAIF := SaveAndDisableIRQs()
		schedulerLock.Lock()

		ProcessDeadlines()
		processStaticDeadlinesSchedLockHeld()

		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)

		WaitForInterrupt()
	}
}

// SaveThread0AndYield saves thread 0 (the current kernel thread) to the ready
// queue and picks the next ready thread to run. Called from YieldToReadyThread
// assembly after it has saved all registers into thread 0's ThreadContext.
//
// Returns the context pointer of the next thread (for ERET), or 0 if no thread
// is available (caller should just return normally).
//
// The assembly sets thread 0's ELR to its return address and SPSR to EL1t (0x4),
// so when thread 0 is scheduled back via timer preemption, it resumes at the
// instruction after the YieldToReadyThread call.
//
//go:nosplit
//go:noinline
func SaveThread0AndYield() uint64 {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t0 := threadList.Get(int(CurrentThreadIdx))
	if t0 == nil {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return 0
	}

	// Put thread 0 on ready queue
	t0.State = ThreadReady
	readyQueue.Pluck(t0.TID)
	enqueueReadySchedLockHeld(t0)

	// Update tick accounting for thread 0
	currentTime := kirq.ReadCounterValue()
	if t0.TicksStartedRunning != 0 {
		t0.TotalTicksRunning += currentTime - t0.TicksStartedRunning
	}
	t0.TicksStartedRunning = 0

	// Find next ready thread (prefer a priest, not kernel)
	next := findReadyThreadPreferDifferentPriestSchedLockHeld(t0.PID)
	if next == nil {
		// No thread available — undo the save
		readyQueue.Pluck(t0.TID)
		t0.State = ThreadRunning
		t0.TicksStartedRunning = currentTime
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return 0
	}

	// Switch current thread to the new one
	nextIdx := threadToIdx(next)
	CurrentThreadIdx = nextIdx
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(next))
	next.State = ThreadRunning
	next.StartTick = currentTime
	next.GoroutineStart = currentTime
	next.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
	next.GoroutinePreemptDeadline = currentTime + kirq.GoroutinePreemptTicks
	next.LastSeenG = next.Context.X[28]
	next.PreemptElapsed = 0
	next.GoroutineElapsed = 0
	next.TicksStartedRunning = currentTime

	// Switch TTBR0 if needed for userspace thread
	if next.PageTableL0PA != 0 && next.PageTableL0PA != t0.PageTableL0PA {
		kmem.SwitchTTBR0WithASID(next.PageTableL0PA, uint16(next.PID))
	}

	schedulerLock.Unlock()
	// Don't restore DAIF — ERET will restore from SPSR
	_ = savedDAIF

	return uint64(uintptr(unsafe.Pointer(&next.Context)))
}

// StartFirstThread waits for and starts the first ready thread.
// This is called from the kernel main when there are threads in the ready queue
// but no current thread is running. It uses IdleLoop to wait for a ready thread,
// then sets up CurrentThread/CurrentThreadIdx and returns the context pointer.
//
// Returns pointer to ThreadContext for assembly to load and ERET to.
// Never returns 0 - blocks until a thread is ready.
//
//go:nosplit
//go:noinline
func StartFirstThread() uint64 {
	return startFirstThreadImpl(&NormalSchedulerFunc)
}

// startFirstThreadImpl is the internal implementation that can use test schedulers.
//
//go:nosplit
//go:noinline
func startFirstThreadImpl(sf *SchedulerFunc) uint64 {
	// Wait for a ready thread
	thread := IdleLoop(sf)
	if thread == nil {
		// This should never happen - IdleLoop blocks until a thread is ready
		return 0
	}

	// CRITICAL: Skip kernel threads (PID=0) and find a priest thread (PID>0)
	// Kernel goroutines may have been preempted into the ready queue before
	// timer was disabled. We need to re-queue them and find a priest thread.
	for thread.PID == 0 {
		// Put kernel thread back at the BACK of the ready queue (not front!)
		// StartFirstThread needs a priest thread to ERET to userspace.
		// If we used enqueueReadySchedLockHeld here, kernel threads would go
		// to the front and we'd pick them again immediately — infinite loop.
		savedDAIF := sf.DisableAndSaveDAIF()
		schedulerLock.Lock()
		thread.State = ThreadReady
		readyQueue.PushNoDuplicate(thread.TID)
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)

		// Try to find another thread
		thread = IdleLoop(sf)
		if thread == nil {
			return 0
		}
	}
	// BEGIN CRITICAL SECTION - protect thread state modifications
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Set up current thread
	idx := threadToIdx(thread)
	CurrentThreadIdx = idx
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(thread))
	thread.State = ThreadRunning

	// Initialize preemption tracking
	currentTime := sf.CurrentTime(0)
	thread.StartTick = currentTime
	thread.GoroutineStart = currentTime
	thread.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
	thread.GoroutinePreemptDeadline = currentTime + kirq.GoroutinePreemptTicks
	thread.LastSeenG = thread.Context.X[28] // Use saved g
	thread.PreemptElapsed = 0
	thread.GoroutineElapsed = 0
	thread.TicksStartedRunning = currentTime

	// Switch TTBR0 to the thread's page table
	if thread.PageTableL0PA != 0 {
		kmem.SwitchTTBR0WithASID(thread.PageTableL0PA, uint16(thread.PID))
		// Note: TLB flush not needed due to ASID tagging
	}
	// END CRITICAL SECTION
	schedulerLock.Unlock()

	// Don't restore DAIF - the ERET will set SPSR which controls IRQ state
	_ = savedDAIF

	return uint64(uintptr(unsafe.Pointer(&thread.Context)))
}

// CloneThread creates a new thread for Go runtime's clone syscall.
// Uses static allocation - NO heap allocation.
//
// CRITICAL SECTION: Modifies threadList, nextTID, readyQueue. Protected by both
// DAIF and schedulerLock for multi-core safety.
//
//go:nosplit
//go:noinline
func CloneThread(sf *SchedulerFunc, stack, returnAddr, spsr, mp, gp, fn uint64) int16 {
	// Lazy initialization
	if !threadsInitialized {
		InitThreads()
	}

	// BEGIN CRITICAL SECTION - protect thread allocation and state
	var savedDAIF uint64
	if sf != nil {
		savedDAIF = sf.DisableAndSaveDAIF()
	} else {
		savedDAIF = SaveAndDisableIRQs()
	}
	schedulerLock.Lock()

	// First, get the parent thread to determine if this is a kernel or userspace thread
	var parent *Thread
	if CurrentThreadIdx >= 0 {
		parent = threadList.Get(int(CurrentThreadIdx))
	}

	// Determine if this is a kernel thread (parent PID == 0, the kernel priest)
	isKernel := parent != nil && parent.PID == 0

	// Acquire thread ID and slot from appropriate allocator
	var tid ThreadId
	var t *Thread
	if isKernel {
		// Kernel threads use reserved slots (TID matches slot index)
		tid = AcquireKernelThreadId() // Sequential kernel IDs (0..ReservedKernelThreads-1)
		t = threadList.ReservedGet(int(tid))
		if t == nil {
			schedulerLock.Unlock()
			if sf != nil {
				sf.EnableAndRestoreDAIF(savedDAIF)
			} else {
				RestoreIRQs(savedDAIF)
			}
			panic("kernel thread slot not available")
		}
		threadList.ReservedSet(int(tid))
	} else {
		// Userspace threads use normal allocation (shuffled IDs)
		tid = threadIdAllocator.Acquire()
		_, t = threadList.Allocate()
	}

	// Fill in thread context (via pointer)
	t.TID = tid // Unique ID from appropriate allocator
	// CRITICAL: Set state to Running, NOT Ready!
	// This thread will run immediately via SetSyscallSwitchTarget.
	// DoContextSwitch will handle putting the parent (A) on ready queue.
	t.State = ThreadRunning
	t.FutexAddr = 0
	t.MPtr = mp
	t.GPtr = gp
	t.EntryFunc = fn
	var currentTick uint64
	if sf != nil {
		currentTick = sf.CurrentTime(0)
	} else {
		currentTick = ds.CurrentTime(0)
	}
	t.StartTick = currentTick
	t.GoroutineStart = currentTick
	t.ThreadPreemptDeadline = currentTick + kirq.ThreadPreemptTicks
	t.GoroutinePreemptDeadline = currentTick + kirq.GoroutinePreemptTicks
	t.LastSeenG = gp
	t.PreemptElapsed = 0   // Fresh thread, no elapsed time yet
	t.GoroutineElapsed = 0 // Fresh goroutine, no elapsed time yet
	t.TotalTicksRunning = 0 // Fresh thread, no accumulated runtime yet
	t.TicksStartedRunning = currentTick // Thread runs immediately (State = Running)

	// CRITICAL: Set InCloneSetup to protect the clone child from async preempt
	// The child must read fn/gp/mp from the stack before async preempt can safely
	// push LR/R29 there. This flag will be cleared on the child's first syscall.
	t.InCloneSetup = 1

	// CRITICAL: Inherit page table, priest ID, and asyncPreempt address from parent thread!
	// Without this, cloned threads have PageTableL0PA=0 and won't
	// get TTBR0 switched when scheduled, causing page faults.
	// PID (priest ID) is used as ASID for TLB tagging.
	// AsyncPreemptAddr is inherited so all threads in the same process use the same
	// asyncPreempt function (kmazarin's for kernel threads, priest's for priest threads).
	if parent != nil {
		t.PageTableL0PA = parent.PageTableL0PA
		t.PID = parent.PID
		t.AsyncPreemptAddr = parent.AsyncPreemptAddr
		t.PriestIdx = parent.PriestIdx
	}

	// DO NOT add to ready queue - this thread runs immediately!
	// The parent thread (A) will be added to ready queue by DoContextSwitch.

	// Set up initial context for cloned thread (B)
	// CRITICAL: Child must "return" from clone syscall into clone.abi0!
	// Go runtime's clone.abi0 code after SVC expects:
	//   - x0 = 0 (child TID)
	//   - ELR = returnAddr (instruction after SVC in clone.abi0)
	//   - SP = new stack (with fn/gp/mp already pushed by parent)
	// clone.abi0 then loads fn/gp/mp from stack and calls the entry function.
	// Setting ELR = fn directly SKIPS this setup code, causing crashes!
	t.Context.X[0] = 0         // Child returns with TID = 0
	t.Context.SP = stack       // New stack (fn/gp/mp already on stack from parent)
	t.Context.ELR = returnAddr // Return INTO clone.abi0 (after SVC instruction)
	// Copy processor state from parent BUT clear DAIF.I bit to ensure IRQs enabled
	// This prevents a chain of context switches where IRQs stay permanently disabled.
	// DAIF.I is bit 7 (0x80) in PSTATE/SPSR.
	t.Context.SPSR = spsr & ^uint64(0x80)  // Same processor state but with IRQs enabled
	t.Context.X[28] = gp       // g register (also loaded from stack by clone.abi0)

	if sf != nil && sf.StateCheck != nil {
		sf.StateCheck("clone-thread-created")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	if sf != nil {
		sf.EnableAndRestoreDAIF(savedDAIF)
	} else {
		RestoreIRQs(savedDAIF)
	}

	// CRITICAL: Tell the syscall return path to switch to this new thread!
	// After SyscallDispatch returns, assembly will:
	// 1. Call GetSyscallSwitchTarget() - returns B's context pointer
	// 2. Call DoContextSwitch(framePtr, B's context):
	//    - Saves A's context from exception frame (including X0 = this TID)
	//    - Marks A as Ready, adds A to ready queue
	//    - Sets CurrentThread = B
	// 3. Copy B's context to exception frame
	// 4. ERET to B (starts at fn with new stack)
	//
	// This ensures B runs immediately and A gets fair CPU scheduling.
	SetSyscallSwitchTarget(uintptr(unsafe.Pointer(&t.Context)))

	// Return TID - this becomes A's return value when A eventually runs.
	// The exception frame's X0 will be set to this value, then saved to A's
	// context when DoContextSwitch runs.
	return int16(tid)
}

// ThreadExit marks a thread as exited and releases its slot.
// Releases both the unique TID and the slot index for reuse.
//
//go:nosplit
func ThreadExit() uintptr {
	return threadExitImpl(&NormalSchedulerFunc)
}

// threadExitImpl is the internal implementation with sf for testing
//
//go:nosplit
func threadExitImpl(sf *SchedulerFunc) uintptr {
	if CurrentThreadIdx < 0 {
		return 0 // No current thread
	}

	// BEGIN CRITICAL SECTION
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Get current thread using StaticList API
	t := threadList.Get(int(CurrentThreadIdx))
	if t != nil {
		// Mark as exited
		t.State = ThreadExited
		// Try to pluck from ready queue (may not be there)
		readyQueue.Pluck(t.TID)

		// Release unique thread ID back to allocator
		threadIdAllocator.Release(t.TID)

		// Release thread slot
		threadList.Release(int(CurrentThreadIdx))
	}

	// Find next ready thread, preferring a different priest for fairness
	exitingPID := PriestId(-1)
	if t != nil {
		exitingPID = t.PID
	}
	next := findReadyThreadPreferDifferentPriestSchedLockHeld(exitingPID)
	if next == nil {
		if sf.StateCheck != nil {
			sf.StateCheck("thread-exit-no-next")
		}
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0 // No threads remain
	}

	// Mark next thread as running
	currentTick := sf.CurrentTime(0)
	next.State = ThreadRunning
	next.StartTick = currentTick
	next.GoroutineStart = currentTick
	next.ThreadPreemptDeadline = currentTick + kirq.ThreadPreemptTicks
	next.GoroutinePreemptDeadline = currentTick + kirq.GoroutinePreemptTicks
	next.TicksStartedRunning = currentTick
	// Find the slot index for the next thread
	// CRITICAL: Use threadToIdx (pointer-based) not IndexOf (TID-based)
	// IndexOf skips reserved slots, so it returns -1 for kernel threads!
	CurrentThreadIdx = threadToIdx(next)

	if sf.StateCheck != nil {
		sf.StateCheck("thread-exit-switch")
	}

	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)
	return uintptr(unsafe.Pointer(&next.Context))
}

// CreateUserspaceThread allocates a new thread for a userspace process (like a priest).
// entryPoint: the PC to start executing at (ELR_EL1)
// stackPtr: the user stack pointer (SP_EL0)
// pageTableL0PA: physical address of the process's L0 page table
// priestId: the priest (process) ID, used as ASID for TLB tagging
// Returns the TID (thread ID) of the new thread.
//
//go:nosplit
func CreateUserspaceThread(entryPoint, stackPtr uint64, pageTableL0PA uintptr, asyncPreemptAddr uint64) int16 {
	return createUserspaceThreadImpl(&NormalSchedulerFunc, entryPoint, stackPtr, pageTableL0PA, asyncPreemptAddr)
}

// GetPriestByPID finds a priest by its PID.
// Returns nil if not found.
//
//go:nosplit
func GetPriestByPID(pid PriestId) *Priest {
	return priestList.FindById(int32(pid))
}

// SetKmazarinAsyncPreemptAddr sets the asyncPreempt address for all kmazarin threads.
// Called after kirq.SetAsyncPreemptWrapperAddr is initialized.
// Kernel threads (PID == 0) get this address for goroutine preemption.
//
//go:nosplit
func SetKmazarinAsyncPreemptAddr(addr uint64) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	// Update the kernel priest's asyncPreempt address
	p0 := priestList.ReservedGet(0)
	if p0 != nil {
		p0.AsyncPreemptAddr = addr
	}

	// Update all kernel threads (PID == 0)
	for i := 0; i < MaxThreads; i++ {
		if threadListInUse[i] && threadListData[i].PID == 0 {
			threadListData[i].AsyncPreemptAddr = addr
		}
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// RegisterAsyncPreemptAddr registers the asyncPreempt address for the current priest.
// Called by priests via the SysRegisterAsyncPreempt syscall.
// This enables goroutine-level preemption within the priest.
//
// Returns:
//   0 on success
//   -ENOENT (-2) if current thread is not a priest thread
//   -ESRCH (-3) if priest not found
//
//go:nosplit
//go:noinline
func RegisterAsyncPreemptAddr(asyncPreemptAddr uint64) int64 {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	// Get current thread
	if CurrentThreadIdx < 0 {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return -2 // ENOENT
	}

	t := threadList.Get(int(CurrentThreadIdx))
	if t == nil {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return -2 // ENOENT
	}

	// Check if this is a userspace priest thread (PID > 0)
	// Kernel threads (PID == 0) should use SetKmazarinAsyncPreemptAddr instead
	if t.PID <= 0 {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return -2 // ENOENT - not a userspace priest thread
	}

	priestId := t.PID

	// Find the priest and update its asyncPreempt address
	p := priestList.FindById(int32(priestId))
	if p == nil {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return -3 // ESRCH - priest not found
	}

	// Store in priest record
	p.AsyncPreemptAddr = asyncPreemptAddr

	// Update all threads belonging to this priest
	for i := 0; i < MaxThreads; i++ {
		if threadListInUse[i] && threadListData[i].PID == priestId {
			threadListData[i].AsyncPreemptAddr = asyncPreemptAddr
		}
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	return 0
}

// createUserspaceThreadImpl is the internal implementation with sf for testing
//
//go:nosplit
func createUserspaceThreadImpl(sf *SchedulerFunc, entryPoint, stackPtr uint64, pageTableL0PA uintptr, asyncPreemptAddr uint64) int16 {
	// BEGIN CRITICAL SECTION - protect all scheduling data structures:
	// priestIdAllocator, priestList, threadList, readyQueue
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Allocate priest ID and priest entry inside the critical section
	priestId := priestIdAllocator.Acquire()
	_, p := priestList.Allocate()
	p.PID = priestId
	p.AsyncPreemptAddr = asyncPreemptAddr

	// Allocate thread slot from static list (panics if exhausted)
	_, t := threadList.Allocate()

	// Acquire unique thread ID from allocator
	tid := threadIdAllocator.Acquire()

	// Fill in thread state
	t.TID = tid // Unique ID from allocator
	t.PID = priestId // Priest (process) ID for ASID
	t.State = ThreadReady // Not running yet - deadlines set when scheduled
	t.PageTableL0PA = pageTableL0PA
	t.StartTick = 0              // Set when scheduled
	t.GoroutineStart = 0         // Set when scheduled
	t.ThreadPreemptDeadline = 0  // Set when scheduled
	t.GoroutinePreemptDeadline = 0 // Set when scheduled
	t.PreemptElapsed = 0
	t.GoroutineElapsed = 0
	t.TotalTicksRunning = 0   // Fresh thread, no accumulated runtime yet
	t.TicksStartedRunning = 0 // Not running yet (will be set when scheduled)
	t.FutexAddr = 0
	t.MPtr = 0
	t.GPtr = 0
	t.EntryFunc = 0
	t.LastSeenG = 0

	// Copy asyncPreemptAddr from the Priest struct and set PriestIdx for O(1) access
	// The Priest gets this from ELF symbol lookup during process creation
	priest := priestList.FindById(int32(priestId))
	if priest != nil {
		t.AsyncPreemptAddr = priest.AsyncPreemptAddr
	} else {
		t.AsyncPreemptAddr = 0
	}
	// Set PriestIdx for O(1) priest lookup in timer handler
	t.PriestIdx = -1 // Default: no priest
	for pi := 0; pi < len(priestList.Data); pi++ {
		if priestList.InUse[pi] && priestList.Data[pi].PID == priestId {
			t.PriestIdx = int16(pi)
			break
		}
	}

	// Set up initial context for userspace execution
	// All general-purpose registers start at 0
	for i := 0; i < 31; i++ {
		t.Context.X[i] = 0
	}
	t.Context.SP = stackPtr    // User stack pointer (SP_EL0)
	t.Context.ELR = entryPoint  // Entry point (program counter)
	t.Context.SPSR = 0          // SPSR for EL0: M[3:0]=0000 (EL0t), M[4]=0 (AArch64)

	// DEBUG: Verify SP and L0PA were set correctly
	console.KPrintf("[CreateThread] TID=%d PID=%d SP=0x%x ELR=0x%x L0PA=0x%x\n", tid, priestId, t.Context.SP, t.Context.ELR, t.PageTableL0PA)

	// Add to ready queue (Pluck first just in case TID was reused)
	readyQueue.Pluck(tid)
	enqueueReadySchedLockHeld(t)

	if sf.StateCheck != nil {
		sf.StateCheck("create-userspace-thread-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	return int16(tid)
}

// threadToIdx converts a Thread pointer to its index in the threads array.
// Returns -1 if the pointer is nil or not in the array.
//
//go:nosplit
func threadToIdx(t *Thread) int32 {
	if t == nil {
		return -1
	}
	// Use threadList.Data slice which may point to test data or global threadListData
	if len(threadList.Data) == 0 {
		return -1
	}
	base := uintptr(unsafe.Pointer(&threadList.Data[0]))
	ptr := uintptr(unsafe.Pointer(t))
	if ptr < base {
		return -1
	}
	offset := ptr - base
	idx := int32(offset / unsafe.Sizeof(threadList.Data[0]))
	if idx < 0 || idx >= int32(len(threadList.Data)) {
		return -1
	}
	return idx
}

// enqueueReadySchedLockHeld pushes a thread onto the ready queue.
// Kernel threads (PID == 0) go to the FRONT for priority scheduling;
// all other threads go to the BACK (FIFO).
// REQUIRES: schedulerLock held.
//
//go:nosplit
func enqueueReadySchedLockHeld(t *Thread) {
	if t.PID == 0 {
		readyQueue.PushHeadNoDuplicate(t.TID)
	} else {
		readyQueue.PushNoDuplicate(t.TID)
	}
}

// enqueueReadyByTIDSchedLockHeld is like enqueueReadySchedLockHeld but looks up the
// thread by TID first. Used when only the TID is available.
// REQUIRES: schedulerLock held.
//
//go:nosplit
func enqueueReadyByTIDSchedLockHeld(tid ThreadId) {
	t := threadList.FindByIdAll(int32(tid))
	if t != nil && t.PID == 0 {
		readyQueue.PushHeadNoDuplicate(tid)
	} else {
		readyQueue.PushNoDuplicate(tid)
	}
}

// findReadyThread finds the next READY thread using FIFO scheduling
// Returns thread pointer, or nil if none found
// Internal function - use ThreadFindReady for external API
//
//go:nosplit
func findReadyThreadSchedLockHeld() *Thread {
	// Loop instead of recursion to avoid stack overflow in nosplit context
	for {
		if readyQueue.IsEmpty() {
			return nil
		}

		// Pop next thread ID (FIFO order)
		tid := readyQueue.Pop() // Panics if empty (should never happen due to IsEmpty check)

		// Get thread pointer - FindByIdAll searches ALL slots including reserved kernel threads
		t := threadList.FindByIdAll(int32(tid))
		if t == nil {
			panic("readyQueue contains invalid TID")
			return nil // Unreachable
		}

		// Validate thread state
		if t.State != ThreadReady {
			// Schedule problem: Thread in ready queue but not in Ready state
			// Move to correct queue based on actual state
			switch t.State {
			case ThreadBlockedFutex:
				blockedQueue.PushNoDuplicate(tid)
			case ThreadSleeping:
				sleepingQueue.PushNoDuplicate(tid)
			case ThreadRunning:
				// Already running - skip without pushing back (avoids duplicates)
			default:
				panic("Thread in ready queue with invalid state")
			}

			// Continue loop to try next thread
			continue
		}

		// Thread is valid and ready
		return t
	}
}

// findReadyThreadPreferDifferentPriestSchedLockHeld finds next ready thread, preferring a different priest.
// Used for timer preemption to promote fairness across priests.
// Falls back to any ready thread if only same-priest threads available.
//
//go:nosplit
func findReadyThreadPreferDifferentPriestSchedLockHeld(currentPID PriestId) *Thread {
	// First pass: find thread from DIFFERENT priest (scan without popping)
	for i := 0; i < len(readyQueue.Data); i++ {
		if !readyQueue.InUse[i] {
			continue
		}

		tid := readyQueue.Data[i]
		t := threadList.FindByIdAll(int32(tid))
		if t == nil || t.State != ThreadReady {
			continue // Skip invalid or stale entries
		}

		if t.PID != currentPID {
			readyQueue.Pluck(tid)
			return t
		}
	}

	// Fallback: any ready thread
	return findReadyThreadSchedLockHeld()
}

// ThreadFindReady finds the next READY thread
// Returns CONTEXT POINTER of ready thread, or 0 (nil) if none found
//
// NOTE: Returns context pointer (not index) so 0 unambiguously means "no switch".
// Thread index 0 is valid and would return a non-zero context pointer.
//
//go:nosplit
func ThreadFindReady() uintptr {
	t := findReadyThreadSchedLockHeld()
	if t == nil {
		return 0
	}
	return uintptr(unsafe.Pointer(&t.Context))
}

// ThreadBlockFutex marks current thread as blocked on futex and finds next thread
// Returns CONTEXT POINTER of next thread to run, or 0 (nil) if none
//
// CRITICAL: Only marks thread as blocked if there's another thread to switch to.
// If no ready thread exists, returns 0 WITHOUT marking current thread as blocked.
// This prevents busy-wait loops where futex returns success but thread can't block.
//
// NOTE: Returns context pointer (not index) so 0 unambiguously means "no switch".
// Thread index 0 is valid and would return a non-zero context pointer.
//
//go:nosplit
func ThreadBlockFutex(futexAddr uint64) uintptr {
	return threadBlockFutexImpl(&NormalSchedulerFunc, futexAddr)
}

// threadBlockFutexImpl is the internal implementation with sf for testing
//
//go:nosplit
func threadBlockFutexImpl(sf *SchedulerFunc, futexAddr uint64) uintptr {
	if CurrentThreadIdx < 0 {
		return 0
	}

	// BEGIN CRITICAL SECTION - protect thread state modification
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Get current thread using StaticList API
	t := threadList.Get(int(CurrentThreadIdx))
	if t == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Pluck current thread from ready queue if it's there
	// (It might not be if it was already running and not re-queued)
	readyQueue.Pluck(t.TID)

	// Find next ready thread FIRST - don't block if no one to switch to
	// Prefer a different priest for fairness
	next := findReadyThreadPreferDifferentPriestSchedLockHeld(t.PID)
	if next == nil {
		// No ready thread - can't block, thread continues running
		if sf.StateCheck != nil {
			sf.StateCheck("futex-block-no-next")
		}
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Found a ready thread - now safe to mark current as blocked
	t.State = ThreadBlockedFutex
	t.FutexAddr = futexAddr

	// Add current thread to blocked queue
	blockedQueue.PushNoDuplicate(t.TID)

	if sf.StateCheck != nil {
		sf.StateCheck("futex-block-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	// Return context pointer (not index) - allows switching to thread 0
	return uintptr(unsafe.Pointer(&next.Context))
}

// ThreadWakeFutex wakes threads blocked on the given futex address
// Returns number of threads woken
// Signature matches ksyscall linkname declaration (int32 args for ABI compatibility)
//
//go:nosplit
func ThreadWakeFutex(futexAddr uint64, maxWake int32) int32 {
	return threadWakeFutexImpl(&NormalSchedulerFunc, futexAddr, int16(maxWake))
}

// threadWakeFutexImpl is the internal implementation with sf for testing
//
//go:nosplit
func threadWakeFutexImpl(sf *SchedulerFunc, futexAddr uint64, maxWake int16) int32 {
	// BEGIN CRITICAL SECTION - protect thread state modifications
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	woken := int32(0)
	queueSize := blockedQueue.Size()

	// Scan blocked queue
	for i := 0; i < queueSize && woken < int32(maxWake); i++ {
		tid := blockedQueue.Pop()
		t := threadList.FindByIdAll(int32(tid)) // FindByIdAll to include kernel threads

		if t == nil {
			// Invalid TID in queue - skip it
			console.KPrintf("ThreadWakeFutex: invalid TID %d in blockedQueue\n", tid)
			continue
		}

		if t.FutexAddr == futexAddr {
			// Move to ready
			t.State = ThreadReady
			t.FutexAddr = 0
			// Pluck first to prevent duplicates
			readyQueue.Pluck(tid)
			enqueueReadySchedLockHeld(t)
			woken++
		} else {
			// Put back if not matching
			blockedQueue.PushNoDuplicate(tid)
		}
	}

	if sf.StateCheck != nil {
		sf.StateCheck("futex-wake-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	return woken
}

// ThreadBlockSleep marks current thread as sleeping
// Returns CONTEXT POINTER of next thread to run, or 0 (nil) if none
//
// CRITICAL: Only marks thread as sleeping if there's another thread to switch to.
// If no ready thread exists, returns 0 WITHOUT marking current thread as sleeping.
//
// NOTE: Returns context pointer (not index) so 0 unambiguously means "no switch".
// Thread index 0 is valid and would return a non-zero context pointer.
//
//go:nosplit
func ThreadBlockSleep(sf *SchedulerFunc) uintptr {
	if CurrentThreadIdx < 0 {
		return 0
	}

	// BEGIN CRITICAL SECTION - protect thread state modification
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Get current thread using StaticList API
	t := threadList.Get(int(CurrentThreadIdx))
	if t == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Pluck current thread from ready queue if it's there
	// (It might not be if it was already running and not re-queued)
	readyQueue.Pluck(t.TID)

	// Find next ready thread FIRST - don't sleep if no one to switch to
	// Prefer a different priest for fairness
	next := findReadyThreadPreferDifferentPriestSchedLockHeld(t.PID)
	if next == nil {
		// No ready thread - can't sleep, thread continues running
		// Timer IRQ will handle preemption when other threads become ready
		// DON'T push to readyQueue - thread is still Running, not Ready
		if sf.StateCheck != nil {
			sf.StateCheck("sleep-block-no-next")
		}
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Found a ready thread - now safe to mark current as sleeping
	t.State = ThreadSleeping

	// Add current thread to sleeping queue
	sleepingQueue.PushNoDuplicate(t.TID)

	if sf.StateCheck != nil {
		sf.StateCheck("sleep-block-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	// Return context pointer (not index) - allows switching to thread 0
	return uintptr(unsafe.Pointer(&next.Context))
}

// ThreadWakeSleeper moves a thread from sleeping to ready by TID
//
// NOTE: Currently called from ProcessDeadlines which is already protected
// by its callers (IdleLoop and timer handler), but we add protection
// defensively in case this is ever called directly.
//
//go:nosplit
func ThreadWakeSleeper(sf *SchedulerFunc, tidParam uintptr) {
	tid := int16(tidParam)

	// BEGIN CRITICAL SECTION - protect thread state modification
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Find thread by TID - use FindByIdAll to include kernel threads
	t := threadList.FindByIdAll(int32(tid))
	if t == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return
	}

	if t.State == ThreadSleeping {
		t.State = ThreadReady
		// Pluck first to prevent duplicates
		readyQueue.Pluck(ThreadId(tid))
		enqueueReadySchedLockHeld(t)
	}

	if sf.StateCheck != nil {
		sf.StateCheck("wake-sleeper-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)
}

// ThreadIncrementTick increments the global tick counter
// Called from timer interrupt
//
//go:nosplit
func ThreadIncrementTick() {
	globalTickCounter++
}

// GetCurrentThread returns the current thread index as uintptr
//
//go:nosplit
func GetCurrentThread() uintptr {
	if CurrentThreadIdx < 0 {
		return 0
	}
	return uintptr(CurrentThreadIdx)
}

// GetCurrentThreadTID returns the TID of the current thread.
// Returns -1 if no thread is running.
//
//go:nosplit
func GetCurrentThreadTID() ThreadId {
	if CurrentThreadIdx < 0 {
		return -1
	}
	t := threadList.Get(int(CurrentThreadIdx))
	if t == nil {
		return -1
	}
	return t.TID
}

// getCurrentThreadPIDForSpan returns the current thread's PID for span group lookup.
// This is called via linkname from ksyscall.GetCurrentSpanGroup().
// Returns 0 (kernel PID) if no thread is running (early init).
//
//go:nosplit
func getCurrentThreadPIDForSpan() int16 {
	t := (*Thread)(atomic.LoadPointer(&CurrentThread))
	if t == nil {
		return 0 // Fallback to kernel span group
	}
	return int16(t.PID)
}

// GetGlobalTickCounter returns the global tick counter
//
//go:nosplit
func GetGlobalTickCounter() uint64 {
	return globalTickCounter
}

// checkThreadPreemptionInternal is the wrapper called from assembly.
// Takes only framePtr and uses NormalSchedulerFunc internally.
//
//go:nosplit
//go:noinline
func checkThreadPreemptionInternal(framePtr uint64) uint64 {
	return checkThreadPreemptionImpl(&NormalSchedulerFunc, framePtr)
}

// checkThreadPreemptionImpl checks if the current thread should be preempted
// and performs the context switch if needed.
// Called from timer IRQ handler via ABI stub when NeedsThreadPreempt is set.
//
// framePtr: pointer to exception frame with saved registers
// Returns: pointer to new ThreadContext if switch happened, 0 otherwise
//
// CRITICAL: This is called from IRQ context after EOIR, with g switched to kmazarin's g0.
// The exception frame contains the interrupted thread's complete state.
//
//go:nosplit
//go:noinline
func checkThreadPreemptionImpl(sf *SchedulerFunc, framePtr uint64) uint64 {
	if CurrentThreadIdx < 0 {
		return 0
	}

	// BEGIN CRITICAL SECTION - protect thread state modifications
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Save current thread's context from exception frame
	SaveContextFromFrame(uintptr(framePtr))

	// Mark current thread as ready (preempted) and add to back of ready queue
	oldIdx := CurrentThreadIdx
	oldThread := threadList.Get(int(oldIdx))
	if oldThread == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	oldThread.State = ThreadReady
	// Pluck first in case oldThread is already in queue from a previous preemption
	readyQueue.Pluck(oldThread.TID)
	enqueueReadySchedLockHeld(oldThread)

	// Reset preemption tracking for the preempted thread
	oldThread.PreemptElapsed = 0
	oldThread.GoroutineElapsed = 0

	// Update runtime accounting for preempted thread
	currentTime := sf.CurrentTime(0)
	if oldThread.TicksStartedRunning != 0 {
		oldThread.TotalTicksRunning += currentTime - oldThread.TicksStartedRunning
	}
	oldThread.TicksStartedRunning = 0 // Mark as not running

	// Find next ready thread, preferring a different priest for fairness
	next := findReadyThreadPreferDifferentPriestSchedLockHeld(oldThread.PID)

	if next == nil {
		// No other ready thread - continue with current thread
		// Remove it from ready queue since we're continuing to run it
		readyQueue.Pluck(oldThread.TID)
		oldThread.State = ThreadRunning
		oldThread.StartTick = currentTime
		oldThread.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
		oldThread.GoroutinePreemptDeadline = currentTime + kirq.GoroutinePreemptTicks
		oldThread.TicksStartedRunning = currentTime
		if sf.StateCheck != nil {
			sf.StateCheck("preempt-no-next")
		}
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Priest-level tick accounting
	// Stop old priest's clock
	if oldThread.PriestIdx >= 0 {
		oldPriest := priestList.Get(int(oldThread.PriestIdx))
		if oldPriest != nil && oldPriest.TicksStartedRunning != 0 {
			oldPriest.TotalTicksRunning += currentTime - oldPriest.TicksStartedRunning
			// Only clear priest clock if switching to a DIFFERENT priest
			if next.PID != oldThread.PID {
				oldPriest.TicksStartedRunning = 0
			}
		}
	}

	// Start new priest's clock (only if different priest)
	if next.PriestIdx >= 0 && next.PID != oldThread.PID {
		newPriest := priestList.Get(int(next.PriestIdx))
		if newPriest != nil {
			newPriest.TicksStartedRunning = currentTime
		}
	}

	// Switch to new thread
	nextIdx := threadToIdx(next)
	CurrentThreadIdx = nextIdx
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(next))
	next.State = ThreadRunning
	next.StartTick = currentTime
	next.GoroutineStart = currentTime
	next.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
	next.GoroutinePreemptDeadline = currentTime + kirq.GoroutinePreemptTicks
	next.LastSeenG = next.Context.X[28] // Use saved g
	next.PreemptElapsed = 0             // Fresh time slice
	next.GoroutineElapsed = 0           // Fresh goroutine time slice
	next.TicksStartedRunning = currentTime // Mark when started running for accounting

	// CRITICAL: Switch TTBR0 if switching to a userspace thread with different page table
	// Without this, userspace threads run with wrong page table and crash!
	// Use the thread's PID as the ASID for TLB tagging - this allows TLB entries
	// from different processes to coexist without requiring flushes on every switch.
	if next.PageTableL0PA != 0 && next.PageTableL0PA != oldThread.PageTableL0PA {
		kmem.SwitchTTBR0WithASID(next.PageTableL0PA, uint16(next.PID))
	}

	if sf.StateCheck != nil {
		sf.StateCheck("preempt-switch-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	// CRITICAL: Do NOT restore DAIF here!
	// CurrentThreadIdx now points to the NEW thread. If we enable interrupts
	// and a timer IRQ fires, SaveContextFromFrame would save the KERNEL's
	// exception frame into the NEW thread's context, corrupting it.
	// The assembly will restore interrupt state via ERET with new SPSR.
	_ = savedDAIF // Keep compiler happy

	return uint64(uintptr(unsafe.Pointer(&next.Context)))
}

// Exception frame offsets (must match exceptions.s)
const (
	excFrameX0     = 0   // x0, x1, x2, ... stored sequentially
	excFrameX28    = 224 // x28 (g pointer)
	excFrameX29X30 = 232 // x29, x30 (FP, LR)
	excFrameSPEL0  = 288 // Saved SP_EL0
	excFrameELR    = 256 // ELR_EL1
	excFrameSPSR   = 264 // SPSR_EL1
)

// SaveContextFromFrame saves the current thread's context from an exception frame
//
//go:nosplit
//go:noinline
func SaveContextFromFrame(framePtr uintptr) {
	if CurrentThreadIdx < 0 {
		return
	}

	t := threadList.Get(int(CurrentThreadIdx))
	if t == nil {
		return
	}

	// Handle nil frame (can happen in tests)
	if framePtr == 0 {
		return
	}
	frame := (*[40]uint64)(unsafe.Pointer(framePtr))

	for i := 0; i < 28; i++ {
		t.Context.X[i] = frame[i]
	}

	t.Context.X[28] = frame[28]
	t.Context.X[29] = frame[29]
	t.Context.X[30] = frame[30]
	t.Context.SP = frame[36]
	t.Context.ELR = frame[32]
	t.Context.SPSR = frame[33]
}

// doContextSwitchABI0 is the ABI0 entry point for context switching
// Called from assembly via DoContextSwitch stub with 2 arguments.
// targetPtr is actually a pointer to ThreadContext (returned by getSyscallSwitchTargetInternal)
//
//go:nosplit
//go:noinline
func doContextSwitchABI0(framePtr uint64, targetPtr uint64) uint64 {
	// Find which thread owns this context
	targetCtx := (*ThreadContext)(unsafe.Pointer(uintptr(targetPtr)))

	// Find the thread index by matching context pointer
	// Use StaticList.Data slice which points to threadListData
	targetIdx := int32(-1)
	for i := int32(0); i < MaxThreads; i++ {
		if &threadList.Data[i].Context == targetCtx {
			targetIdx = i
			break
		}
	}

	if targetIdx < 0 {
		return 0
	}

	// Use NormalSchedulerFunc for production calls from assembly
	ctx := doContextSwitchImpl(&NormalSchedulerFunc, uintptr(framePtr), targetIdx)
	return uint64(uintptr(unsafe.Pointer(ctx)))
}

// doContextSwitchImpl performs a context switch from current thread to target
//
// CRITICAL SECTION: This function modifies CurrentThread/CurrentThreadIdx and
// thread state. Protected by both DAIF and schedulerLock.
//
//go:nosplit
//go:noinline
func doContextSwitchImpl(sf *SchedulerFunc, framePtr uintptr, targetIdx int32) *ThreadContext {
	// Save current thread's context
	SaveContextFromFrame(framePtr)

	// BEGIN CRITICAL SECTION - protect thread state modifications
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	oldIdx := CurrentThreadIdx
	CurrentThreadIdx = targetIdx
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(threadList.Get(int(targetIdx)))) // Get() for reserved slots

	// Get current time for preemption tracking
	currentTime := sf.CurrentTime(0)

	// Target thread is now running (it was already popped from ready queue
	// by the caller, so no need to remove it here)

	// Save preemption tracking state for old thread before switching
	// This preserves how long the current goroutine has been running
	if oldIdx >= 0 {
		oldThread := threadList.Get(int(oldIdx)) // Get() for reserved slots
		if oldThread != nil && oldThread.State == ThreadRunning {
			// Save elapsed time so we can restore it when this thread resumes
			oldThread.PreemptElapsed = currentTime - oldThread.StartTick
			oldThread.GoroutineElapsed = currentTime - oldThread.GoroutineStart

			// Update runtime accounting - add elapsed ticks to total
			// Only if TicksStartedRunning is non-zero (avoids huge value on first run)
			if oldThread.TicksStartedRunning != 0 {
				oldThread.TotalTicksRunning += currentTime - oldThread.TicksStartedRunning
			}
			oldThread.TicksStartedRunning = 0 // Mark as not running

			oldThread.State = ThreadReady
			// Pluck first in case oldThread is already in queue
			readyQueue.Pluck(oldThread.TID)
			enqueueReadySchedLockHeld(oldThread)

		}
	}

	// Priest-level tick accounting for context switches
	// Determine old priest PID to compare with new thread
	oldPID := PriestId(-1)
	if oldIdx >= 0 {
		oldThread := threadList.Get(int(oldIdx))
		if oldThread != nil {
			oldPID = oldThread.PID
			// Stop old priest's clock if switching to a different priest
			newThread := threadList.Get(int(targetIdx))
			if newThread != nil && newThread.PID != oldPID && oldThread.PriestIdx >= 0 {
				oldPriest := priestList.Get(int(oldThread.PriestIdx))
				if oldPriest != nil && oldPriest.TicksStartedRunning != 0 {
					oldPriest.TotalTicksRunning += currentTime - oldPriest.TicksStartedRunning
					oldPriest.TicksStartedRunning = 0
				}
			}
		}
	}

	// New thread becomes running
	newThread := threadList.Get(int(targetIdx)) // Get() for reserved slots
	if newThread == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return nil
	}
	newThread.State = ThreadRunning
	// Restore StartTick from saved elapsed time so preemption tracking
	// continues from where it left off. This ensures goroutines don't get
	// unlimited time by repeatedly triggering context switches.
	newThread.StartTick = currentTime - newThread.PreemptElapsed
	newThread.GoroutineStart = currentTime - newThread.GoroutineElapsed
	// Calculate remaining time for deadlines (threshold - elapsed = remaining)
	// If elapsed >= threshold, deadline will be at or before currentTime (immediate preemption)
	if newThread.PreemptElapsed < kirq.ThreadPreemptTicks {
		newThread.ThreadPreemptDeadline = currentTime + (kirq.ThreadPreemptTicks - newThread.PreemptElapsed)
	} else {
		newThread.ThreadPreemptDeadline = currentTime // Preempt immediately on next tick
	}
	if newThread.GoroutineElapsed < kirq.GoroutinePreemptTicks {
		newThread.GoroutinePreemptDeadline = currentTime + (kirq.GoroutinePreemptTicks - newThread.GoroutineElapsed)
	} else {
		newThread.GoroutinePreemptDeadline = currentTime // Preempt immediately on next tick
	}
	// Use saved x28 (g register) from context, NOT GPtr (g0).
	// This preserves the goroutine that was running when the thread was
	// preempted, allowing async preemption tracking to work correctly
	// when we resume this thread.
	newThread.LastSeenG = newThread.Context.X[28]

	// Mark when this thread started running for runtime accounting
	newThread.TicksStartedRunning = currentTime

	// Start new priest's clock if switching to a different priest
	if newThread.PID != oldPID && newThread.PriestIdx >= 0 {
		newPriest := priestList.Get(int(newThread.PriestIdx))
		if newPriest != nil {
			newPriest.TicksStartedRunning = currentTime
		}
	}

	// Switch TTBR0 if the new thread has a different page table
	// This is needed when switching between different userspace processes (priests)
	// Use the thread's PID as the ASID for TLB tagging.
	oldThread := threadList.Get(int(oldIdx)) // Get() for reserved slots
	oldPageTable := uintptr(0)
	if oldThread != nil {
		oldPageTable = oldThread.PageTableL0PA
	}
	// Switch TTBR0 if page table is different
	if newThread.PageTableL0PA != 0 && newThread.PageTableL0PA != oldPageTable {
		kmem.SwitchTTBR0WithASID(newThread.PageTableL0PA, uint16(newThread.PID))
	}

	if sf.StateCheck != nil {
		sf.StateCheck("context-switch-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	// CRITICAL: Do NOT restore DAIF here!
	// CurrentThreadIdx now points to the NEW thread. If we enable interrupts
	// and a timer IRQ fires, SaveContextFromFrame would save the KERNEL's
	// exception frame into the NEW thread's context, corrupting it.
	// The assembly will restore interrupt state via ERET with new SPSR.
	_ = savedDAIF // Keep compiler happy

	return &newThread.Context
}

// GetThreadContext returns a pointer to a thread's context by index
//
//go:nosplit
func GetThreadContext(idx uintptr) *ThreadContext {
	if idx >= MaxThreads {
		return nil
	}
	t := threadList.Get(int(idx))
	if t == nil {
		return nil
	}
	return &t.Context
}

// GetThread returns a pointer to a thread entry by index
//
//go:nosplit
func GetThread(idx uintptr) *Thread {
	if idx >= MaxThreads {
		return nil
	}
	return threadList.Get(int(idx))
}

// SaveCurrentThreadContext saves the current thread's context
//
//go:nosplit
func SaveCurrentThreadContext(
	x0, x1, x2, x3, x4, x5, x6, x7 uint64,
	x8, x9, x10, x11, x12, x13, x14, x15 uint64,
	x16, x17, x18, x19, x20, x21, x22, x23 uint64,
	x24, x25, x26, x27, x28, x29, x30 uint64,
	sp, elr, spsr uint64,
) {
	if CurrentThreadIdx < 0 {
		return
	}

	t := threadList.Get(int(CurrentThreadIdx))
	if t == nil {
		return
	}
	t.Context.X[0] = x0
	t.Context.X[1] = x1
	t.Context.X[2] = x2
	t.Context.X[3] = x3
	t.Context.X[4] = x4
	t.Context.X[5] = x5
	t.Context.X[6] = x6
	t.Context.X[7] = x7
	t.Context.X[8] = x8
	t.Context.X[9] = x9
	t.Context.X[10] = x10
	t.Context.X[11] = x11
	t.Context.X[12] = x12
	t.Context.X[13] = x13
	t.Context.X[14] = x14
	t.Context.X[15] = x15
	t.Context.X[16] = x16
	t.Context.X[17] = x17
	t.Context.X[18] = x18
	t.Context.X[19] = x19
	t.Context.X[20] = x20
	t.Context.X[21] = x21
	t.Context.X[22] = x22
	t.Context.X[23] = x23
	t.Context.X[24] = x24
	t.Context.X[25] = x25
	t.Context.X[26] = x26
	t.Context.X[27] = x27
	t.Context.X[28] = x28
	t.Context.X[29] = x29
	t.Context.X[30] = x30
	t.Context.SP = sp
	t.Context.ELR = elr
	t.Context.SPSR = spsr
}

// PrintTickDistribution prints the tick distribution for all active threads.
// Shows TID, PID, and TotalTicksRunning for each thread.
func PrintTickDistribution() {
	console.KPrint("\n=== Thread Tick Distribution ===\n")

	var totalTicks uint64
	activeCount := 0

	// First pass: count totals
	for i := 0; i < MaxThreads; i++ {
		if threadListInUse[i] {
			t := &threadListData[i]
			// For running thread, add current runtime to total
			ticks := t.TotalTicksRunning
			if t.TicksStartedRunning != 0 && t.State == ThreadRunning {
				ticks += kirq.TimerIRQCount - t.TicksStartedRunning
			}
			totalTicks += ticks
			activeCount++
		}
	}

	console.KPrintf("Active threads: %d, Total ticks: %d\n", activeCount, totalTicks)

	// Second pass: print each thread
	for i := 0; i < MaxThreads; i++ {
		if threadListInUse[i] {
			t := &threadListData[i]
			// For running thread, add current runtime to total
			ticks := t.TotalTicksRunning
			if t.TicksStartedRunning != 0 && t.State == ThreadRunning {
				ticks += kirq.TimerIRQCount - t.TicksStartedRunning
			}

			// Calculate percentage (avoid division by zero)
			var pct uint64
			if totalTicks > 0 {
				pct = (ticks * 100) / totalTicks
			}

			stateStr := "?"
			switch t.State {
			case ThreadRunning:
				stateStr = "RUN"
			case ThreadReady:
				stateStr = "RDY"
			case ThreadBlockedFutex:
				stateStr = "FTX"
			case ThreadSleeping:
				stateStr = "SLP"
			case ThreadExited:
				stateStr = "EXT"
			case ThreadBlockedSoftIRQ:
				stateStr = "IRQ"
			}

			console.KPrintf("  T%02d P%02d [%s] ticks=%d (%d%%)\n",
				t.TID, t.PID, stateStr, ticks, pct)
		}
	}
	console.KPrint("================================\n")

	// Per-priest tick distribution
	console.KPrint("\n=== Priest Tick Distribution ===\n")
	var priestTotalTicks uint64
	for i := 0; i < MaxPriests; i++ {
		if priestListInUse[i] {
			p := &priestListData[i]
			ticks := p.TotalTicksRunning
			if p.TicksStartedRunning != 0 {
				ticks += kirq.TimerIRQCount - p.TicksStartedRunning
			}
			priestTotalTicks += ticks
		}
	}
	for i := 0; i < MaxPriests; i++ {
		if priestListInUse[i] {
			p := &priestListData[i]
			ticks := p.TotalTicksRunning
			if p.TicksStartedRunning != 0 {
				ticks += kirq.TimerIRQCount - p.TicksStartedRunning
			}
			var pct uint64
			if priestTotalTicks > 0 {
				pct = (ticks * 100) / priestTotalTicks
			}
			console.KPrintf("  P%02d ticks=%d (%d%%)\n", p.PID, ticks, pct)
		}
	}
	console.KPrint("================================\n")
}
