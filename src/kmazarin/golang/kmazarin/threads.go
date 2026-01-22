
package main

import (
	"kmazarin/console"
	"kmazarin/ds"
	"kmazarin/kirq"
	"kmazarin/kmem"
	"kmazarin/util"
	"sync/atomic"
	"unsafe"
)

// debugPrint writes a single character for debugging.
// Safe to call from any context including nosplit functions.
// Uses direct UART breadcrumb to avoid stack growth.
//
//go:nosplit
func debugPrint(c byte) {
	Breadcrumb(c)
}

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
const MaxPriests = 4

// MaxThreads is the maximum number of threads supported
// Each priest can have multiple threads (8 threads per priest)
const MaxThreads = 8 * MaxPriests // 32 threads total

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

	// Preemption tracking
	LastSeenG      uint64 // g pointer seen at last timer tick
	StartTick      uint64 // globalTickCounter when this run started
	PreemptElapsed uint64 // elapsed ticks saved when thread switched away
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
var readyQueueData [MaxThreads]ThreadId  // Stores TIDs (unique thread IDs)
var readyQueueInUse [MaxThreads]bool     // Tracks holes in ready queue
var blockedQueueData [MaxThreads]ThreadId // Stores TIDs (unique thread IDs)
var blockedQueueInUse [MaxThreads]bool   // Tracks holes in blocked queue
var sleepingQueueData [MaxThreads]ThreadId // Stores TIDs (unique thread IDs)
var sleepingQueueInUse [MaxThreads]bool  // Tracks holes in sleeping queue

// ID allocator backing arrays - statically allocated
var threadIdStackData [MaxThreads]ThreadId // Backing array for thread ID allocator
var priestIdStackData [MaxPriests]PriestId // Backing array for priest ID allocator

// Data structures - will be initialized in InitThreads()
// DO NOT initialize slices here - Go's initialization order causes them to be length 0!
var threadList ds.StaticList[*Thread, Thread] // StaticList stores Thread VALUES, returns pointers

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
// Set by syscall handlers (futex, nanosleep) when they need to block
// Checked by assembly after DispatchSyscall returns
// 0 = no switch, non-zero = pointer to ThreadContext to switch to
var syscallSwitchTarget uintptr = 0

// syscallELR stores the ELR_EL1 for the current syscall
// Set by assembly before calling DispatchSyscall
// Used by clone to get the proper return address for child threads
var syscallELR uint64 = 0

// syscallSPSR stores the SPSR_EL1 for the current syscall
// Set by assembly before calling DispatchSyscall
// Used by clone to get the proper processor state for child threads
var syscallSPSR uint64 = 0

// threadsInitialized tracks if InitThreads has been called
var threadsInitialized bool = false

// deadlineQueue holds TimerDeadlines sorted by deadline time (ascending).
// Used for nanosleep and other timed wakeups.
// nil until InitDeadlineQueue is called.
var deadlineQueue *ds.OrderedList[*util.TimerDeadline]

// setSyscallELRInternal is called by assembly via ABI stub to store the current ELR
//
//go:noinline
func setSyscallELRInternal(elr uint64) {
	syscallELR = elr
}

// setSyscallSPSRInternal is called by assembly via ABI stub to store the current SPSR
//
//go:noinline
func setSyscallSPSRInternal(spsr uint64) {
	syscallSPSR = spsr
}

// GetSyscallELR returns the ELR for the current syscall
// Called by clone to get the child's return address
//
//go:noinline
func GetSyscallELR() uint64 {
	return syscallELR
}

// GetSyscallSPSR returns the SPSR for the current syscall
// Called by clone to get the child's processor state
//
//go:noinline
func GetSyscallSPSR() uint64 {
	return syscallSPSR
}

// getSyscallSwitchTargetInternal returns the switch target and resets it
// Called from assembly via ABI stub after syscall dispatch
// Returns uint64: context pointer to switch to, or 0 for no switch
//
//go:noinline
func getSyscallSwitchTargetInternal() uint64 {
	target := syscallSwitchTarget
	syscallSwitchTarget = 0 // Reset for next syscall
	Breadcrumb('G')
	if target != 0 {
		Breadcrumb('+')
	} else {
		Breadcrumb('-')
	}
	// Return context pointer directly (0 means no switch)
	return uint64(target)
}

// SetSyscallSwitchTarget sets the context pointer to switch to
// Called by syscall handlers that need to block
// target is a ThreadContext pointer, or 0 for no switch
//
//go:noinline
func SetSyscallSwitchTarget(target uintptr) {
	Breadcrumb('S')
	if target != 0 {
		Breadcrumb('+')
	} else {
		Breadcrumb('-')
	}
	// Store context pointer directly
	// ThreadBlockFutex/ThreadBlockSleep now return context pointers,
	// so 0 unambiguously means "no switch" and non-zero is valid target
	syscallSwitchTarget = target
}

// InitIdAllocators initializes the ID allocators for thread IDs and priest IDs.
// Must be called before any threads are created.
//
//go:nosplit
func InitIdAllocators() {
	// Initialize thread ID allocator with backing array (initializes and seeds with IDs 0..MaxThreads-1)
	threadIdAllocator.Init(threadIdStackData[:])

	// Initialize priest ID allocator with backing array (initializes and seeds with IDs 0..MaxPriests-1)
	priestIdAllocator.Init(priestIdStackData[:])
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

	// Initialize spinlock timing based on detected timer frequency
	// This must happen before any spinlocks are used (including in ID allocators)
	ds.InitSpinlockTiming(timerFrequencyHz)

	// Initialize ID allocators first
	InitIdAllocators()

	// CRITICAL VERIFICATION: The first thread ID must be 0 (for the kernel's initial thread)
	// If this is not 0, the ID allocator initialization is broken.
	firstThreadId := threadIdAllocator.Acquire()
	if firstThreadId != 0 {
		panic("FATAL: First thread ID is not 0")
	}

	// CRITICAL VERIFICATION: The first priest ID must be 0 (for the kernel's initial priest)
	// This will become important with ASID and preemptible kernel threads.
	// If this is not 0, the ID allocator initialization is broken.
	firstPriestId := priestIdAllocator.Acquire()
	if firstPriestId != 0 {
		panic("FATAL: First priest ID is not 0")
	}

	// CRITICAL: Initialize data structure slices from backing arrays
	// Must be done here, NOT as global initializers (Go init order issue)
	threadList.Data = threadListData[:]
	threadList.InUse = threadListInUse[:]
	readyQueue.Data = readyQueueData[:]
	readyQueue.InUse = readyQueueInUse[:]
	blockedQueue.Data = blockedQueueData[:]
	blockedQueue.InUse = blockedQueueInUse[:]
	sleepingQueue.Data = sleepingQueueData[:]
	sleepingQueue.InUse = sleepingQueueInUse[:]

	// Save kmazarin's g address early - x28 should be pointing to kmazarin's g
	// at this point (early init runs on g0/m0).
	// This is used by the exception handler when handling userspace syscalls.
	kmazarinG0Addr = GetGRegister()

	// Initialize all thread DATA to free state (direct access OK during init)
	for i := 0; i < MaxThreads; i++ {
		threadListData[i].State = ThreadFree
		threadListData[i].TID = 0
	}

	// Allocate thread 0 for M0 (the main thread) using StaticList API
	// This marks slot 0 as in use in threadListInUse
	// We expect the first allocation to be slot 0
	_, t0 := threadList.Allocate()

	// Set up thread 0 via the returned pointer
	t0.State = ThreadRunning
	t0.TID = firstThreadId // Use the acquired ID (verified to be 0)
	t0.PID = -1            // Kernel thread, no ASID needed (PageTableL0PA = 0)
	t0.StartTick = globalTickCounter

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
	// Debug: show TID being added
	Breadcrumb('[')
	Breadcrumb('A')
	Breadcrumb('d')
	Breadcrumb(':')
	Breadcrumb('0' + byte((tid/10)%10))
	Breadcrumb('0' + byte(tid%10))
	Breadcrumb(']')
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

// IdleLoop is called when no threads are ready to run.
// It processes deadlines and uses WFI to wait for the next interrupt.
// Returns a pointer to a ready thread when one becomes available.
//
// CRITICAL: ProcessDeadlines and threadFindReadyIdx are protected by both DAIF
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
		ready := threadFindReadyIdx()

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

// CloneThread creates a new thread for Go runtime's clone syscall.
// Uses static allocation - NO heap allocation.
//
// CRITICAL SECTION: Modifies threadList, nextTID, readyQueue. Protected by both
// DAIF and schedulerLock for multi-core safety.
//
//go:nosplit
//go:noinline
func CloneThread(sf *SchedulerFunc, stack, returnAddr, spsr, mp, gp, fn uint64) int16 {
	// Breadcrumb at entry
	Breadcrumb('~')

	// Lazy initialization
	if !threadsInitialized {
		Breadcrumb('I') // Init
		InitThreads()
		Breadcrumb('i') // init done
	}

	// DEBUG: Check array size vs StaticList slice size
	Breadcrumb('[')
	arraySize := len(threadListInUse)
	sliceSize := len(threadList.InUse)

	// Output sizes as hex (using last 4 hex digits only)
	Breadcrumb('A') // Array size follows
	Breadcrumb(':')
	hexChars := "0123456789ABCDEF"
	Breadcrumb(hexChars[(arraySize>>12)&0xF])
	Breadcrumb(hexChars[(arraySize>>8)&0xF])
	Breadcrumb(hexChars[(arraySize>>4)&0xF])
	Breadcrumb(hexChars[arraySize&0xF])
	Breadcrumb('+')

	Breadcrumb('S') // Slice size follows
	Breadcrumb(':')
	Breadcrumb(hexChars[(sliceSize>>12)&0xF])
	Breadcrumb(hexChars[(sliceSize>>8)&0xF])
	Breadcrumb(hexChars[(sliceSize>>4)&0xF])
	Breadcrumb(hexChars[sliceSize&0xF])
	Breadcrumb('+')

	if arraySize != sliceSize {
		Breadcrumb('M') // MISMATCH!
	} else if sliceSize >= 512 {
		Breadcrumb('K') // OK
	} else if sliceSize < 100 {
		Breadcrumb('!')  // Too small!
	} else {
		Breadcrumb('?')  // Unexpected
	}

	// DEBUG: Print fn value using breadcrumbs
	Breadcrumb('f')
	Breadcrumb('n')
	Breadcrumb('=')
	for i := 60; i >= 0; i -= 4 {
		nibble := (fn >> i) & 0xF
		Breadcrumb(hexChars[nibble])
	}
	Breadcrumb(' ')

	// BEGIN CRITICAL SECTION - protect thread allocation and state
	Breadcrumb(']')
	var savedDAIF uint64
	if sf != nil {
		savedDAIF = sf.DisableAndSaveDAIF()
	} else {
		savedDAIF = SaveAndDisableIRQs()
	}
	schedulerLock.Lock()

	// Allocate thread slot from static list (panics if exhausted)
	// Returns (slot_index, *Thread)
	_, t := threadList.Allocate()

	// Acquire unique thread ID from allocator
	tid := threadIdAllocator.Acquire()

	// Fill in thread context (via pointer)
	t.TID = tid // Unique ID from allocator
	// CRITICAL: Set state to Running, NOT Ready!
	// This thread will run immediately via SetSyscallSwitchTarget.
	// DoContextSwitch will handle putting the parent (A) on ready queue.
	t.State = ThreadRunning
	t.FutexAddr = 0
	t.MPtr = mp
	t.GPtr = gp
	t.EntryFunc = fn
	if sf != nil {
		t.StartTick = sf.CurrentTime(0)
	} else {
		t.StartTick = ds.CurrentTime(0)
	}
	t.LastSeenG = gp
	t.PreemptElapsed = 0 // Fresh thread, no elapsed time yet

	// CRITICAL: Inherit page table AND priest ID from parent thread!
	// Without this, cloned threads have PageTableL0PA=0 and won't
	// get TTBR0 switched when scheduled, causing page faults.
	// PID (priest ID) is used as ASID for TLB tagging.
	if CurrentThreadIdx >= 0 {
		parent := threadList.Nth(int(CurrentThreadIdx))
		if parent != nil {
			t.PageTableL0PA = parent.PageTableL0PA
			t.PID = parent.PID
		}
	}

	// DO NOT add to ready queue - this thread runs immediately!
	// The parent thread (A) will be added to ready queue by DoContextSwitch.

	// Set up initial context for cloned thread (B)
	// CRITICAL: B starts at entry function 'fn', NOT at returnAddr!
	// This is pthread_create semantics, not fork semantics.
	// Go runtime expects the new thread to start at the entry function.
	t.Context.X[0] = 0     // First argument to fn (or could be used for return value)
	t.Context.SP = stack   // New stack
	t.Context.ELR = fn     // START at entry function, not "return from clone"
	t.Context.SPSR = spsr  // Same processor state as parent
	t.Context.X[28] = gp   // g register valid immediately

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

	Breadcrumb('B') // B for "switching to B"

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
	t := threadList.Nth(int(CurrentThreadIdx))
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

	// Find next ready thread
	next := threadFindReadyIdx()
	if next == nil {
		if sf.StateCheck != nil {
			sf.StateCheck("thread-exit-no-next")
		}
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0 // No threads remain
	}

	// Mark next thread as running
	next.State = ThreadRunning
	next.StartTick = sf.CurrentTime(0)
	// Find the slot index for the next thread
	CurrentThreadIdx = int32(threadList.IndexOf(int32(next.TID)))

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
func CreateUserspaceThread(entryPoint, stackPtr uint64, pageTableL0PA uintptr) int16 {
	// Acquire a priest ID for this new userspace process
	priestId := priestIdAllocator.Acquire()
	return createUserspaceThreadImpl(&NormalSchedulerFunc, entryPoint, stackPtr, pageTableL0PA, priestId)
}

// createUserspaceThreadImpl is the internal implementation with sf for testing
//
//go:nosplit
func createUserspaceThreadImpl(sf *SchedulerFunc, entryPoint, stackPtr uint64, pageTableL0PA uintptr, priestId PriestId) int16 {
	// BEGIN CRITICAL SECTION - protect thread allocation and state
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Allocate thread slot from static list (panics if exhausted)
	_, t := threadList.Allocate()

	// Acquire unique thread ID from allocator
	tid := threadIdAllocator.Acquire()

	// Fill in thread state
	t.TID = tid // Unique ID from allocator
	t.PID = priestId // Priest (process) ID for ASID
	t.State = ThreadReady
	t.PageTableL0PA = pageTableL0PA
	t.StartTick = sf.CurrentTime(0)
	t.PreemptElapsed = 0
	t.FutexAddr = 0
	t.MPtr = 0
	t.GPtr = 0
	t.EntryFunc = 0
	t.LastSeenG = 0

	// Set up initial context for userspace execution
	// All general-purpose registers start at 0
	for i := 0; i < 31; i++ {
		t.Context.X[i] = 0
	}
	t.Context.SP = stackPtr    // User stack pointer (SP_EL0)
	t.Context.ELR = entryPoint  // Entry point (program counter)
	t.Context.SPSR = 0          // SPSR for EL0: M[3:0]=0000 (EL0t), M[4]=0 (AArch64)

	// Add to ready queue
	readyQueue.Push(tid)

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

// threadFindReadyIdx finds the next READY thread using FIFO scheduling
// Returns thread pointer, or nil if none found
// Internal function - use ThreadFindReady for external API
//
//go:nosplit
func threadFindReadyIdx() *Thread {
	// Loop instead of recursion to avoid stack overflow in nosplit context
	for {
		if readyQueue.IsEmpty() {
			return nil
		}

		// Pop next thread ID (FIFO order)
		tid := readyQueue.Pop() // Panics if empty (should never happen due to IsEmpty check)

		// Get thread pointer - FindById uses pointer receiver now, no copy
		t := threadList.FindById(int32(tid))
		if t == nil {
			panic("readyQueue contains invalid TID")
			return nil // Unreachable
		}

		// Validate thread state
		if t.State != ThreadReady {
			// Schedule problem: Thread in ready queue but not in Ready state
			// Debug output removed to keep function nosplit-safe
			// Move to correct queue based on actual state
			switch t.State {
			case ThreadBlockedFutex:
				blockedQueue.Push(tid)
			case ThreadSleeping:
				sleepingQueue.Push(tid)
			case ThreadRunning:
				// Already running? Put back in ready queue
				readyQueue.Push(tid)
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

// ThreadFindReady finds the next READY thread
// Returns CONTEXT POINTER of ready thread, or 0 (nil) if none found
//
// NOTE: Returns context pointer (not index) so 0 unambiguously means "no switch".
// Thread index 0 is valid and would return a non-zero context pointer.
//
//go:nosplit
func ThreadFindReady() uintptr {
	t := threadFindReadyIdx()
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
	t := threadList.Nth(int(CurrentThreadIdx))
	if t == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Pluck current thread from ready queue if it's there
	// (It might not be if it was already running and not re-queued)
	readyQueue.Pluck(t.TID)

	// Find next ready thread FIRST - don't block if no one to switch to
	next := threadFindReadyIdx()
	if next == nil {
		// No ready thread - put current thread back in ready queue and don't block
		// This will be handled by timer interrupts eventually
		readyQueue.Push(t.TID)
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
	blockedQueue.Push(t.TID)

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
		t := threadList.FindById(int32(tid))

		if t == nil {
			// Invalid TID in queue - skip it
			console.KPrintf("ThreadWakeFutex: invalid TID %d in blockedQueue\n", tid)
			continue
		}

		if t.FutexAddr == futexAddr {
			// Move to ready
			t.State = ThreadReady
			t.FutexAddr = 0
			readyQueue.Push(tid) // Panics if exhausted
			woken++
		} else {
			// Put back if not matching
			blockedQueue.Push(tid)
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
	t := threadList.Nth(int(CurrentThreadIdx))
	if t == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Pluck current thread from ready queue if it's there
	// (It might not be if it was already running and not re-queued)
	readyQueue.Pluck(t.TID)

	// Find next ready thread FIRST - don't sleep if no one to switch to
	next := threadFindReadyIdx()
	if next == nil {
		// No ready thread - put current thread back in ready queue and don't sleep
		readyQueue.Push(t.TID)
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
	sleepingQueue.Push(t.TID)

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

	// Find thread by TID (TID = slot index)
	t := threadList.FindById(int32(tid))
	if t == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return
	}

	if t.State == ThreadSleeping {
		t.State = ThreadReady
		// Add to ready queue
		readyQueue.Push(ThreadId(tid))
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
	t := threadList.Nth(int(CurrentThreadIdx))
	if t == nil {
		return -1
	}
	return t.TID
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
	Breadcrumb('T') // Thread preemption check
	Breadcrumb('[')

	if CurrentThreadIdx < 0 {
		Breadcrumb('-')
		Breadcrumb(']')
		return 0
	}

	Breadcrumb('0' + byte(CurrentThreadIdx))
	Breadcrumb('>')

	// BEGIN CRITICAL SECTION - protect thread state modifications
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Save current thread's context from exception frame
	SaveContextFromFrame(uintptr(framePtr))

	// Mark current thread as ready (preempted) and add to back of ready queue
	oldIdx := CurrentThreadIdx
	oldThread := threadList.Nth(int(oldIdx))
	if oldThread == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		Breadcrumb('N')
		Breadcrumb(']')
		return 0
	}

	oldThread.State = ThreadReady
	readyQueue.Push(oldThread.TID) // Add to back of ready queue (FIFO)

	// Reset preemption tracking for the preempted thread
	oldThread.PreemptElapsed = 0

	// Find next ready thread
	next := threadFindReadyIdx()
	if next == nil {
		// No other ready thread - continue with current thread
		// But we already added it to ready queue, so we need to remove it
		// This is a bit awkward with the queue-based system
		// For now, just leave it there and continue running
		oldThread.State = ThreadRunning
		oldThread.StartTick = sf.CurrentTime(0)
		if sf.StateCheck != nil {
			sf.StateCheck("preempt-no-next")
		}
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		Breadcrumb('S') // Same thread (no other available)
		Breadcrumb(']')
		return 0
	}

	// Switch to new thread
	nextIdx := threadToIdx(next)
	CurrentThreadIdx = nextIdx
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(next))
	next.State = ThreadRunning
	next.StartTick = sf.CurrentTime(0)
	next.LastSeenG = next.Context.X[28] // Use saved g
	next.PreemptElapsed = 0             // Fresh time slice

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
	sf.EnableAndRestoreDAIF(savedDAIF)

	Breadcrumb('0' + byte(nextIdx)) // Print new thread index
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

	t := threadList.Nth(int(CurrentThreadIdx))
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
	Breadcrumb('D')
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
		Breadcrumb('N')
		return 0
	}
	Breadcrumb('0' + byte(targetIdx))

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
	atomic.StorePointer(&CurrentThread, unsafe.Pointer(threadList.Nth(int(targetIdx))))

	// Get current time for preemption tracking
	currentTime := sf.CurrentTime(0)

	// Target thread is now running (it was already popped from ready queue
	// by the caller, so no need to remove it here)

	// Save preemption tracking state for old thread before switching
	// This preserves how long the current goroutine has been running
	if oldIdx >= 0 {
		oldThread := threadList.Nth(int(oldIdx))
		if oldThread != nil && oldThread.State == ThreadRunning {
			// Save elapsed time so we can restore it when this thread resumes
			oldThread.PreemptElapsed = currentTime - oldThread.StartTick
			oldThread.State = ThreadReady
			// Add old thread to ready queue
			readyQueue.Push(oldThread.TID)
		}
	}

	// New thread becomes running
	newThread := threadList.Nth(int(targetIdx))
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
	// Use saved x28 (g register) from context, NOT GPtr (g0).
	// This preserves the goroutine that was running when the thread was
	// preempted, allowing async preemption tracking to work correctly
	// when we resume this thread.
	newThread.LastSeenG = newThread.Context.X[28]

	// Switch TTBR0 if the new thread has a different page table
	// This is needed when switching between different userspace processes (priests)
	// Use the thread's PID as the ASID for TLB tagging.
	oldThread := threadList.Nth(int(oldIdx))
	oldPageTable := uintptr(0)
	if oldThread != nil {
		oldPageTable = oldThread.PageTableL0PA
	}
	if newThread.PageTableL0PA != 0 && newThread.PageTableL0PA != oldPageTable {
		// Switch to the new thread's page table with ASID = PID
		kmem.SwitchTTBR0WithASID(newThread.PageTableL0PA, uint16(newThread.PID))
	}

	if sf.StateCheck != nil {
		sf.StateCheck("context-switch-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	// DEBUG: Print ELR value before returning
	Breadcrumb('E')
	Breadcrumb('L')
	Breadcrumb('R')
	Breadcrumb('=')
	hexChars := "0123456789ABCDEF"
	elr := newThread.Context.ELR
	for i := 60; i >= 0; i -= 4 {
		nibble := (elr >> i) & 0xF
		Breadcrumb(hexChars[nibble])
	}
	Breadcrumb(' ')

	return &newThread.Context
}

// GetThreadContext returns a pointer to a thread's context by index
//
//go:nosplit
func GetThreadContext(idx uintptr) *ThreadContext {
	if idx >= MaxThreads {
		return nil
	}
	t := threadList.Nth(int(idx))
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
	return threadList.Nth(int(idx))
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

	t := threadList.Nth(int(CurrentThreadIdx))
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
