//go:build qemuvirt && aarch64

package main

import (
	"kmazarin/console"
	"kmazarin/ds"
	"kmazarin/kirq"
	"kmazarin/util"
	"unsafe"
)

// debugPrint writes a single character to console for debugging
// Uses console abstraction which provides spinlock protection
//
//go:nosplit
func debugPrint(c byte) {
	console.WriteByte(c)
}

// Syscall return codes for assembly
const (
	SyscallReturnNormal = 0 // Return normally with value in x0
	SyscallReturnSwitch = 1 // Context switch to thread in x1
	SyscallReturnBlock  = 2 // Block current thread, switch to thread in x1
)

// Timer frequency (set from timer init)
var timerFrequencyHz uint64 = 62500000 // Default 62.5 MHz for QEMU

// Thread states
const (
	ThreadFree         = 0 // Slot available
	ThreadRunning      = 1 // Currently executing
	ThreadReady        = 2 // Runnable, waiting to be scheduled
	ThreadBlockedFutex = 3 // Blocked on futex_wait
	ThreadSleeping     = 4 // Blocked on nanosleep
)

// MaxThreads is the maximum number of threads supported
// Using fixed array to avoid heap allocation during early boot
const MaxThreads = 16

// ThreadContext holds saved CPU state for a thread
type ThreadContext struct {
	// General purpose registers x0-x30
	X [31]uint64

	// Special registers
	SP   uint64 // SP_EL0
	ELR  uint64 // Return address (ELR_EL1)
	SPSR uint64 // Processor state (SPSR_EL1)
}

// Thread represents a single thread (corresponds to a Go M)
type Thread struct {
	State     int32  // ThreadRunning, ThreadReady, etc.
	TID       int32  // Thread ID (returned by clone)
	FutexAddr uint64 // Address being waited on (for ThreadBlockedFutex)
	MPtr      uint64 // Pointer to Go M struct
	GPtr      uint64 // Pointer to Go g struct (g0 for this M)
	EntryFunc uint64 // Entry function (mstart)
	Context   ThreadContext

	// Preemption tracking
	LastSeenG uint64 // g pointer seen at last timer tick
	StartTick uint64 // globalTickCounter when this run started

	// Ready queue node - for O(1) removal from readyQueue
	// nil when thread is not in the ready queue
	readyNode *util.DNode[*Thread]
}

// Fixed-size thread array - no heap allocation needed
var threads [MaxThreads]Thread

// Current thread index (-1 = none)
var currentThreadIdx int32 = -1

// Current thread pointer (nil = none)
// This is maintained alongside currentThreadIdx for assembly access.
// Assembly can read this pointer directly instead of calculating from index.
var currentThread *Thread = nil

// Ready queue - threads waiting to be scheduled
// Uses DLinkedList for O(1) enqueue/dequeue.
// nil until InitReadyQueue is called (after heap is ready).
var readyQueue *util.DLinkedList[*Thread]

// Next TID to assign
var nextTID int32 = 1

// Global tick counter (incremented by timer)
var globalTickCounter uint64 = 0

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
//go:linkname GetSyscallELR kmazarin/ksyscall.GetSyscallELR
func GetSyscallELR() uint64 {
	return syscallELR
}

// GetSyscallSPSR returns the SPSR for the current syscall
// Called by clone to get the child's processor state
//
//go:noinline
//go:linkname GetSyscallSPSR kmazarin/ksyscall.GetSyscallSPSR
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
	// Return context pointer directly (0 means no switch)
	return uint64(target)
}

// SetSyscallSwitchTarget sets the context pointer to switch to
// Called by syscall handlers that need to block
// target is a ThreadContext pointer, or 0 for no switch
//
//go:noinline
func SetSyscallSwitchTarget(target uintptr) {
	// Store context pointer directly
	// ThreadBlockFutex/ThreadBlockSleep now return context pointers,
	// so 0 unambiguously means "no switch" and non-zero is valid target
	syscallSwitchTarget = target
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

	// Initialize all threads as free
	for i := 0; i < MaxThreads; i++ {
		threads[i].State = ThreadFree
		threads[i].TID = 0
	}

	// Set up thread 0 for M0 (the main thread)
	threads[0].State = ThreadRunning
	threads[0].TID = nextTID
	threads[0].StartTick = globalTickCounter
	nextTID++

	currentThreadIdx = 0
	currentThread = &threads[0]
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

// InitReadyQueue initializes the ready queue.
// Call this after the heap is ready.
//
//go:noinline
func InitReadyQueue() {
	if readyQueue == nil {
		readyQueue = util.NewDLinkedList[*Thread]()
	}
}

// AddDeadline adds a deadline to the queue.
// Returns false if the queue is not initialized.
//
//go:nosplit
//go:noinline
//go:linkname AddDeadline kmazarin/ksyscall.AddDeadline
func AddDeadline(deadline uint64, threadIdx int32) bool {
	if deadlineQueue == nil {
		return false
	}
	action := NewWakeThreadAction(threadIdx)
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
// Returns the index of a ready thread when one becomes available.
//
// CRITICAL: ProcessDeadlines and threadFindReadyIdx are protected by IRQ masking
// because this loop runs OUTSIDE exception context. The timer handler also calls
// ProcessDeadlines, so without protection we'd have re-entrant corruption.
//
//go:nosplit
func IdleLoop() int32 {
	for {
		// Disable IRQs while checking deadlines and ready queue
		// This prevents timer IRQ from re-entering ProcessDeadlines
		savedDAIF := SaveAndDisableIRQs()
		ProcessDeadlines()
		ready := threadFindReadyIdx()
		RestoreIRQs(savedDAIF)

		if ready >= 0 {
			return ready
		}
		WaitForInterrupt()
	}
}

// CloneThread creates a new thread for Go runtime's clone syscall.
// Uses fixed array - NO heap allocation.
//
// CRITICAL SECTION: Modifies threads[], nextTID, readyQueue. Although currently
// only called from SVC context (hardware IRQ masked), we explicitly disable IRQs
// as defensive programming.
//
//go:nosplit
//go:noinline
//go:linkname CloneThread kmazarin/ksyscall.CloneThread
func CloneThread(stack, returnAddr, spsr, mp, gp, fn uint64) int32 {
	// Lazy initialization
	if !threadsInitialized {
		InitThreads()
	}

	// BEGIN CRITICAL SECTION - protect thread allocation and state
	savedDAIF := SaveAndDisableIRQs()

	// Find a free slot
	slot := int32(-1)
	for i := int32(0); i < MaxThreads; i++ {
		if threads[i].State == ThreadFree {
			slot = i
			break
		}
	}

	if slot < 0 {
		RestoreIRQs(savedDAIF)
		return -1 // No free slots
	}

	// Allocate TID
	tid := nextTID
	nextTID++

	// Initialize the thread
	t := &threads[slot]
	t.State = ThreadReady
	t.TID = tid
	t.FutexAddr = 0
	t.MPtr = mp
	t.GPtr = gp
	t.EntryFunc = fn
	t.StartTick = globalTickCounter
	t.LastSeenG = gp
	t.readyNode = nil

	// Add to ready queue if available
	if readyQueue != nil {
		t.readyNode = readyQueue.PushBack(t)
	}

	// END CRITICAL SECTION
	RestoreIRQs(savedDAIF)

	// Set up initial context for cloned thread
	t.Context.X[0] = 0         // Clone returns 0 to child
	t.Context.SP = stack       // New stack
	t.Context.ELR = returnAddr // Return to instruction after SVC
	t.Context.SPSR = spsr      // Same processor state as parent
	t.Context.X[28] = gp       // g register valid immediately

	// CRITICAL: Write mp, gp, fn to the stack so clone wrapper's ldur instructions work.
	stackPtr := unsafe.Pointer(uintptr(stack))
	*(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 8)) = mp
	*(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 16)) = gp
	*(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 24)) = fn
	*(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 32)) = 1234 // Magic number

	return tid
}

// threadToIdx converts a Thread pointer to its index in the threads array.
// Returns -1 if the pointer is nil or not in the array.
//
//go:nosplit
func threadToIdx(t *Thread) int32 {
	if t == nil {
		return -1
	}
	base := uintptr(unsafe.Pointer(&threads[0]))
	ptr := uintptr(unsafe.Pointer(t))
	if ptr < base {
		return -1
	}
	offset := ptr - base
	idx := int32(offset / unsafe.Sizeof(threads[0]))
	if idx < 0 || idx >= MaxThreads {
		return -1
	}
	return idx
}

// threadFindReadyIdx finds the next READY thread
// Returns thread index, or -1 if none found
// Internal function - use ThreadFindReady for external API
//
//go:nosplit
func threadFindReadyIdx() int32 {
	// If readyQueue is available and non-empty, use it
	if readyQueue != nil && !readyQueue.IsEmpty() {
		front := readyQueue.Front()
		if front != nil {
			return threadToIdx(front.Value)
		}
	}

	// Fall back to scanning array (before readyQueue init, or if empty)
	start := currentThreadIdx + 1
	if start < 0 {
		start = 0
	}

	for i := int32(0); i < MaxThreads; i++ {
		idx := (start + i) % MaxThreads
		if threads[idx].State == ThreadReady {
			return idx
		}
	}
	return -1
}

// ThreadFindReady finds the next READY thread
// Returns CONTEXT POINTER of ready thread, or 0 (nil) if none found
//
// NOTE: Returns context pointer (not index) so 0 unambiguously means "no switch".
// Thread index 0 is valid and would return a non-zero context pointer.
//
//go:nosplit
func ThreadFindReady() uintptr {
	idx := threadFindReadyIdx()
	if idx < 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&threads[idx].Context))
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
	if currentThreadIdx < 0 {
		return 0
	}

	// BEGIN CRITICAL SECTION - protect thread state modification
	savedDAIF := SaveAndDisableIRQs()

	// Find next ready thread FIRST - don't block if no one to switch to
	next := threadFindReadyIdx()
	if next < 0 {
		// No ready thread - don't block current thread, let it spin/retry
		// This will be handled by timer interrupts eventually
		RestoreIRQs(savedDAIF)
		return 0
	}

	// Found a ready thread - now safe to mark current as blocked
	threads[currentThreadIdx].State = ThreadBlockedFutex
	threads[currentThreadIdx].FutexAddr = futexAddr

	// END CRITICAL SECTION
	RestoreIRQs(savedDAIF)

	// Return context pointer (not index) - allows switching to thread 0
	return uintptr(unsafe.Pointer(&threads[next].Context))
}

// ThreadWakeFutex wakes threads blocked on the given futex address
// Returns number of threads woken
//
//go:nosplit
func ThreadWakeFutex(futexAddr uint64, maxWake int32) int32 {
	// BEGIN CRITICAL SECTION - protect thread state modifications
	savedDAIF := SaveAndDisableIRQs()

	woken := int32(0)

	for i := int32(0); i < MaxThreads && woken < maxWake; i++ {
		if threads[i].State == ThreadBlockedFutex && threads[i].FutexAddr == futexAddr {
			threads[i].State = ThreadReady
			threads[i].FutexAddr = 0
			// Add to ready queue if available
			if readyQueue != nil && threads[i].readyNode == nil {
				threads[i].readyNode = readyQueue.PushBack(&threads[i])
			}
			woken++
		}
	}

	// END CRITICAL SECTION
	RestoreIRQs(savedDAIF)

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
//go:linkname ThreadBlockSleep kmazarin/ksyscall.ThreadBlockSleep
func ThreadBlockSleep() uintptr {
	if currentThreadIdx < 0 {
		return 0
	}

	// BEGIN CRITICAL SECTION - protect thread state modification
	savedDAIF := SaveAndDisableIRQs()

	// Find next ready thread FIRST - don't sleep if no one to switch to
	next := threadFindReadyIdx()
	if next < 0 {
		// No ready thread - don't mark as sleeping
		RestoreIRQs(savedDAIF)
		return 0
	}

	// Found a ready thread - now safe to mark current as sleeping
	threads[currentThreadIdx].State = ThreadSleeping

	// END CRITICAL SECTION
	RestoreIRQs(savedDAIF)

	// Return context pointer (not index) - allows switching to thread 0
	return uintptr(unsafe.Pointer(&threads[next].Context))
}

// ThreadWakeSleeper moves a thread from sleeping to ready by index
//
// NOTE: Currently called from ProcessDeadlines which is already protected
// by its callers (IdleLoop and timer handler), but we add protection
// defensively in case this is ever called directly.
//
//go:nosplit
func ThreadWakeSleeper(idx uintptr) {
	if idx >= MaxThreads {
		return
	}

	// BEGIN CRITICAL SECTION - protect thread state modification
	savedDAIF := SaveAndDisableIRQs()

	if threads[idx].State == ThreadSleeping {
		threads[idx].State = ThreadReady
		// Add to ready queue if available
		if readyQueue != nil && threads[idx].readyNode == nil {
			threads[idx].readyNode = readyQueue.PushBack(&threads[idx])
		}
	}

	// END CRITICAL SECTION
	RestoreIRQs(savedDAIF)
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
//go:linkname GetCurrentThread kmazarin/ksyscall.GetCurrentThread
func GetCurrentThread() uintptr {
	if currentThreadIdx < 0 {
		return 0
	}
	return uintptr(currentThreadIdx)
}

// GetGlobalTickCounter returns the global tick counter
//
//go:nosplit
func GetGlobalTickCounter() uint64 {
	return globalTickCounter
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
	if currentThreadIdx < 0 {
		return
	}

	t := &threads[currentThreadIdx]
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
// targetPtr is actually a pointer to ThreadContext (returned by getSyscallSwitchTargetInternal)
//
//go:nosplit
//go:noinline
func doContextSwitchABI0(framePtr uint64, targetPtr uint64) uint64 {
	// Find which thread owns this context
	targetCtx := (*ThreadContext)(unsafe.Pointer(uintptr(targetPtr)))

	// Find the thread index by matching context pointer
	targetIdx := int32(-1)
	for i := int32(0); i < MaxThreads; i++ {
		if &threads[i].Context == targetCtx {
			targetIdx = i
			break
		}
	}

	if targetIdx < 0 {
		return 0
	}

	ctx := doContextSwitchImpl(uintptr(framePtr), targetIdx)
	return uint64(uintptr(unsafe.Pointer(ctx)))
}

// doContextSwitchImpl performs a context switch from current thread to target
//
// CRITICAL SECTION: This function modifies currentThread/currentThreadIdx and
// thread state. Although currently only called from SVC context (where IRQs are
// hardware-masked), we explicitly disable IRQs as defensive programming.
//
//go:nosplit
//go:noinline
func doContextSwitchImpl(framePtr uintptr, targetIdx int32) *ThreadContext {
	// Save current thread's context
	SaveContextFromFrame(framePtr)

	// BEGIN CRITICAL SECTION - protect thread state modifications
	savedDAIF := SaveAndDisableIRQs()

	oldIdx := currentThreadIdx
	currentThreadIdx = targetIdx
	currentThread = &threads[targetIdx]

	// Remove target thread from ready queue (it's now running)
	if readyQueue != nil && threads[targetIdx].readyNode != nil {
		readyQueue.Remove(threads[targetIdx].readyNode)
		threads[targetIdx].readyNode = nil
	}

	// New thread becomes running
	threads[targetIdx].State = ThreadRunning
	threads[targetIdx].StartTick = globalTickCounter
	threads[targetIdx].LastSeenG = threads[targetIdx].GPtr

	// Old thread goes to ready if it was running
	if oldIdx >= 0 && threads[oldIdx].State == ThreadRunning {
		threads[oldIdx].State = ThreadReady
		// Add old thread to ready queue
		if readyQueue != nil && threads[oldIdx].readyNode == nil {
			threads[oldIdx].readyNode = readyQueue.PushBack(&threads[oldIdx])
		}
	}

	// END CRITICAL SECTION
	RestoreIRQs(savedDAIF)

	return &threads[targetIdx].Context
}

// GetThreadContext returns a pointer to a thread's context by index
//
//go:nosplit
func GetThreadContext(idx uintptr) *ThreadContext {
	if idx >= MaxThreads {
		return nil
	}
	return &threads[idx].Context
}

// GetThread returns a pointer to a thread entry by index
//
//go:nosplit
func GetThread(idx uintptr) *Thread {
	if idx >= MaxThreads {
		return nil
	}
	return &threads[idx]
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
	if currentThreadIdx < 0 {
		return
	}

	t := &threads[currentThreadIdx]
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
