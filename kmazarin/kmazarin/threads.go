
package main

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/console"
	"mazzy/kmazarin/ds"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/ktime"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/kmazarin/util"
	"mazzy/shared/constants"
	"sync/atomic"
	"unsafe"
)

// Syscall return codes for assembly
const (
	SyscallReturnNormal = 0 // Return normally with value in x0
	SyscallReturnSwitch = 1 // Context switch to thread in x1
	SyscallReturnBlock  = 2 // Block current thread, switch to thread in x1
)

// ============================================================================
// SMP Debug Output - Shows per-CPU scheduling activity
// ============================================================================
//
// Output format (compact to minimize nosplit stack usage):
//   R<cpu><tid><pid>  - Thread TID runs on CPU with shepherd PID
//   S<cpu><tid><from> - CPU stole TID from CPU <from>
//   I<cpu><irq>       - CPU received IRQ number
//
// Example output:
//   R0 08 01  - CPU 0 running thread 0x08, shepherd 0x01
//   S1 0A 0   - CPU 1 stole thread 0x0A from CPU 0
//   I0 001B   - CPU 0 received IRQ 0x1B

// SMPDebugEnabled controls verbose SMP debug output.
// Set to true to see per-CPU scheduling, work stealing, and IRQ handling.
const SMPDebugEnabled = false

// smpDebugPrintRun prints "R<cpu><tid><pid>" when a thread starts running.
// Uses Breadcrumb for minimal overhead.
func smpDebugPrintRun(cpuID uint64, tid ThreadId, pid ShepherdId) {
	if !SMPDebugEnabled {
		return
	}
	serial.PollWrite('R')
	serial.PollWrite('0' + byte(cpuID))
	serial.PollWrite(' ')
	serial.RawUARTHex8(uint8(tid))
	serial.PollWrite(' ')
	serial.RawUARTHex8(uint8(pid))
	serial.PollWrite('\r')
	serial.PollWrite('\n')
}

// smpDebugPrintSteal prints "S<cpu><tid><from>" when work is stolen.
// Uses Breadcrumb for minimal overhead.
func smpDebugPrintSteal(thisCPU uint64, tid ThreadId, victimCPU uint64) {
	if !SMPDebugEnabled {
		return
	}
	serial.PollWrite('S')
	serial.PollWrite('0' + byte(thisCPU))
	serial.PollWrite(' ')
	serial.RawUARTHex8(uint8(tid))
	serial.PollWrite(' ')
	serial.PollWrite('0' + byte(victimCPU))
	serial.PollWrite('\r')
	serial.PollWrite('\n')
}

// smpDebugPrintIRQ prints "I<cpu><irq>" when an IRQ fires.
// Uses Breadcrumb for minimal overhead.
func smpDebugPrintIRQ(cpuID uint64, irqNum uint32) {
	if !SMPDebugEnabled {
		return
	}
	serial.PollWrite('I')
	serial.PollWrite('0' + byte(cpuID))
	serial.PollWrite(' ')
	serial.RawUARTHex16(uint16(irqNum))
	serial.PollWrite('\r')
	serial.PollWrite('\n')
}

// Timer frequency (set from timer init)
var timerFrequencyHz uint64 = 62500000 // Default 62.5 MHz for QEMU

// kmazarinG0Addr holds kmazarin's g0 address, set at startup.
// The exception handler uses this to switch to a valid kmazarin g
// when handling syscalls from userspace (which has a different g).
var kmazarinG0Addr uint64

// irqSavedSPSR is used by the ARM64 IRQ handler to pass the SPSR value
// from early in the handler to right before ERET. Writing SPSR_EL1 as late
// as possible minimizes the window for QEMU TCG translation artifacts.
var irqSavedSPSR uint64

// kmazarinFSBase holds kmazarin's FS_BASE MSR value (kernel TLS base, x86_64 only).
// Set during InitThreads. Exception handlers WRMSR to this value before writing
// to the TLS slot, so the write targets mapped kernel memory instead of potentially
// unmapped user memory. Without this, a page fault from userspace with FS_BASE
// pointing to a demand-paged TLS page causes a nested #PF → #DF → triple fault.
var kmazarinFSBase uint64

// savedExcFSBase holds the FS_BASE value saved at exception entry (x86_64 only).
// Used by exception_return to restore the original FS_BASE after handling.
// Safe as a global because interrupts are disabled during exception handling.
var savedExcFSBase uint64

// timerDiagCount is incremented by the assembly timer handler on each timer
// interrupt (before any Go code runs). Used to diagnose timer hangs.
var timerDiagCount uint64

// timerCtxSwitchCount counts timer interrupts that resulted in a context switch.
var timerCtxSwitchCount uint64

// timerNoSwitchCount counts timer interrupts where no switch happened.
var timerNoSwitchCount uint64

// timerHandlerDoneCount counts timer interrupts where TimerIRQHandler completed
// (rearm happened). If this trails timerDiagCount, the Go handler is hanging.
var timerHandlerDoneCount uint64

// scanTimerCount is incremented by ProcessDeadlinesTopHalf on every timer IRQ.
// ProcessDeadlinesTopHalf is called from the timer top-half on all 3 architectures
// (ARM64 line 975, x86_64 line 770, RISC-V line 761 in their exceptions_*.s files).
// kirq.TimerIRQCount is only incremented by kirq.TimerIRQHandlerAsm which is NOT
// called on x86_64, so this counter is arch-universal.
var scanTimerCount uint64

// scanLastTick records the scanTimerCount value at the last A/D bit scan.
// The scan runs every ~1000 timer IRQ ticks (~10 seconds at 10ms/tick).
var scanLastTick uint64

// syscallDiagCount counts total syscalls from user threads.
var syscallDiagCount uint64

// excStackTopForSyscall is the kernel exception stack top address.
// Used by the SYSCALL entry handler to switch from the user stack to a
// valid kernel stack. SYSCALL (unlike INT) does NOT switch stacks via TSS,
// so we must do it manually.
var excStackTopForSyscall uint64

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
	ThreadBlockedLoadMaz  ThreadState = 7 // Blocked waiting for .maz load to complete
	ThreadBlockedDelegate     ThreadState = 10 // Caller blocked waiting for delegated syscall reply
	ThreadBlockedDelegateRecv ThreadState = 11 // Handler blocked waiting for delegated syscall request
	ThreadBlockedDirtyNotify  ThreadState = 12 // Blocked waiting for constraint dirty notification
	ThreadBlockedInputEvent   ThreadState = 13 // Blocked waiting for input focus event
)

// MaxShepherds is the maximum number of shepherd processes (userspace programs).
// Defined in proc package; aliased here for local convenience.
const MaxShepherds = proc.MaxShepherds

// MaxThreads is the maximum number of threads supported
const MaxThreads = 512

// threadArraySize is the fixed backing array size from the shared constant.
// Using a value larger than MaxThreads avoids .noptrbss layout changes when
// MaxThreads is tuned, which reduces churn in the ELF binary layout.
const threadArraySize = constants.ThreadPoolSize

// ReservedKernelThreads is the number of thread slots reserved for kernel threads.
// These slots (0 to ReservedKernelThreads-1) are for kernel use only.
// Userspace threads get IDs from ReservedKernelThreads to MaxThreads-1 (shuffled).
const ReservedKernelThreads = 8

// ReservedKernelShepherds is the number of shepherd slots reserved for the kernel.
// Slot 0 is for the kernel's "shepherd" entry (PID 0, used for kernel threads).
const ReservedKernelShepherds = 1

// startingTicksProgram is the CNTVCT_EL0 value when all shepherds are launched.
// Zero means not yet set — skip all timed-shutdown accounting.
var startingTicksProgram uint64

// shutdownTicksThreshold is 60 seconds in raw counter ticks.
// Set once startingTicksProgram is established (needs SystemTimerFrequency).
var shutdownTicksThreshold uint64


// ResetTickAccounting zeroes all thread and shepherd tick accumulators and sets
// TicksStartedRunning to startTime for any currently-running thread/shepherd.
// MUST be called with IRQs disabled (no timer preemption during reset).
//
//go:nosplit
func ResetTickAccounting(startTime uint64) {
	for i := 0; i < MaxThreads; i++ {
		if threadListInUse[i] {
			threadListData[i].TotalTicksRunning = 0
			if threadListData[i].State == ThreadRunning {
				threadListData[i].TicksStartedRunning = startTime
			} else {
				threadListData[i].TicksStartedRunning = 0
			}
		}
	}
	for i := 0; i < MaxShepherds; i++ {
		if proc.ShepherdListInUse[i] {
			proc.ShepherdListData[i].TotalTicksRunning = 0
			proc.ShepherdListData[i].TicksStartedRunning = 0
		}
	}
	// The currently running shepherd needs its clock started
	ct := GetCurrentThread()
	if ct != nil && ct.ShepherdIdx >= 0 {
		p := &proc.ShepherdListData[ct.ShepherdIdx]
		if proc.ShepherdListInUse[ct.ShepherdIdx] {
			p.TicksStartedRunning = startTime
		}
	}
}

// FixCloneThreadIFFlags forces interrupts-enabled in saved processor state for
// all existing threads. Must be called after EnableIRQs() and before
// KernelIdleLoop.
//
// Go runtime creates clone threads (sysmon, templateThread) during init when
// interrupts are disabled. On AMD64, these threads inherit IF=0 in RFLAGS.
// When the timer scheduler later picks them, they run without interrupts —
// if such a thread picks up a non-blocking goroutine, the system
// freezes permanently because the timer can never preempt it.
//
// ARM64 and RISC-V SetupForCloneChild already fix this (ARM64 clears DAIF.I,
// RISC-V sets SPIE), but we do it here uniformly for safety.
//
//go:nosplit
func FixCloneThreadIFFlags() {
	savedDAIF := SaveAndDisableIRQs()
	for i := 0; i < MaxThreads; i++ {
		if threadListInUse[i] {
			threadListData[i].Context.FixIRQEnabled()
		}
	}
	RestoreIRQs(savedDAIF)
}

// Shepherd and ShepherdId are defined in the proc package and aliased here so
// existing code in this file can use the short names unchanged.
type Shepherd = proc.Shepherd
type ShepherdId = proc.ShepherdId

// ThreadContext is defined in thread_context_<arch>.go (per-architecture).

// ThreadId is a unique thread identifier (0-31)
type ThreadId int16

// Thread represents a single thread (corresponds to a Go M)
type Thread struct {
	State         ThreadState // ThreadRunning, ThreadReady, etc.
	TID           ThreadId    // Unique thread ID from threadIdAllocator (0-31)
	PID           ShepherdId    // Shepherd (process) ID for ASID (-1 = kernel thread)
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
	// Runtime accounting
	TotalTicksRunning   uint64 // Cumulative timer ticks this thread has been running
	TicksStartedRunning uint64 // Timer tick count when this thread started its current run (0 = not running)

	// Clone child protection - skip async preempt until clone setup completes
	InCloneSetup uint32 // 1 = thread is in clone setup (reading fn/gp/mp from stack), 0 = normal

	// CloneNeedsParentRegs: when set, DoContextSwitch copies the parent's full
	// register state to this thread's Context before returning. This ensures the
	// clone child inherits all registers (X1-X27, X29, X30) from the parent,
	// with only X0, X28, SP, and SPSR overridden for clone semantics.
	// Set in CloneThread, cleared in doContextSwitchImpl after the copy.
	CloneNeedsParentRegs uint32

	// WARNING: ShepherdIdx is a shepherd LIST INDEX (not a PID). Used ONLY by time-critical
	// code paths (timer IRQ top-half, preemption checks) that need O(1) access to
	// the shepherd's tick accounting without a PID→Shepherd lookup. This index is set
	// once at thread creation and never changes. Do NOT use for general shepherd
	// lookups — use GetShepherdByPID(t.PID) instead.
	ShepherdIdx int16

	// HomeCPU is the preferred CPU for this thread (soft affinity).
	// Threads wake to their HomeCPU's local queue for cache locality.
	// Set when thread is created, updated when stolen by another CPU.
	// -1 = no affinity (use current CPU), 0-7 = specific CPU ID.
	HomeCPU int8

	// StolenFromCPU tracks the CPU this thread was stolen from (for debug output).
	// Set by work stealing, cleared after debug print. -1 = not stolen.
	StolenFromCPU int8

	// Per-thread syscall state - prevents race condition when timer IRQ preempts mid-syscall
	// and another thread makes a syscall that overwrites global state.
	SyscallELR    uint64  // ELR for this thread's current syscall (return address)
	SyscallSPSR   uint64  // SPSR for this thread's current syscall (processor state)
	SyscallSwitch uintptr // Context switch target for clone (0 = no switch, else ptr to ThreadContext)

	// Saved callee-saved registers for clone on AMD64.
	// The standard Go runtime's clone keeps mp/gp/fn in R13/R9/R12 (callee-saved)
	// rather than storing them on the child stack (which ARM64 does).
	// These are saved from the exception frame by the syscall handler.
	SyscallR12 uint64 // fn (entry function pointer)
	SyscallR13 uint64 // mp (m pointer)
	SyscallR9  uint64 // gp (g pointer)

	// CLONE_SETTLS support (AMD64): the newtls value from the clone syscall.
	// On Linux, CLONE_SETTLS sets the child's FS_BASE from this value.
	// Without this, the child inherits the parent's FS_BASE and corrupts the
	// parent's TLS when it writes its g pointer to FS:-8.
	SyscallCloneTLS uint64

	// Soft IRQ blocking state — saved so the wake path can rewind the SVC
	// and re-execute SyscallWaitSoftIRQ with the correct first argument.
	// On ARM64/RISC-V the return value overwrites the arg0 register (X0/a0),
	// so we must restore it before re-executing the SVC instruction.
	// On x86_64, RAX serves as both syscall number and return value, so we
	// also save the original syscall number for restoration on rewind.
	SoftIRQSlotArg    uint64
	SoftIRQSyscallNum uint64

	// Signal delivery state
	PendingSignals  uint64 // Bitmask of pending signals (bit N = signal N+1)
	SignalSP        uint64 // gsignal stack top (stack grows down from here)
	SignalStackBase uint64 // gsignal stack bottom
	SignalStackSize uint64 // gsignal stack size in bytes
	SignalUctxAddr  uint64 // Address of ucontext in current signal frame
	SignalFaultAddr uint64 // Fault address for hardware signal (SIGSEGV, etc.)
	SignalSiCode    int32  // si_code for siginfo (e.g., SEGV_MAPERR)
	InSignalHandler uint32 // 1 = executing signal handler, 0 = normal
	SigreturnPending uint32 // 1 = rt_sigreturn called, load Context for ERET
}

// Thread struct field offsets for assembly access.
// These are computed from unsafe.Offsetof() in initThreadOffsets() and MUST be
// initialized before any assembly code reads them (before timer IRQ is enabled).
var (
	ThreadContextOffset         uintptr // Offset of Context field within Thread struct
	ThreadStartTickOffset       uintptr
	ThreadPreemptDeadlineOffset uintptr
	ThreadInCloneSetupOffset    uintptr
	ThreadSigreturnPendingOffset  uintptr
	ThreadInSignalHandlerOffset   uintptr
)

// initThreadOffsets computes Thread struct field offsets using unsafe.Offsetof().
// MUST be called before any assembly code reads these offsets (before timer IRQ is enabled).
//
//go:nosplit
func initThreadOffsets() {
	var t Thread
	ThreadContextOffset = unsafe.Offsetof(t.Context)
	ThreadStartTickOffset = unsafe.Offsetof(t.StartTick)
	ThreadPreemptDeadlineOffset = unsafe.Offsetof(t.ThreadPreemptDeadline)
	ThreadInCloneSetupOffset = unsafe.Offsetof(t.InCloneSetup)
	ThreadSigreturnPendingOffset = unsafe.Offsetof(t.SigreturnPending)
	ThreadInSignalHandlerOffset = unsafe.Offsetof(t.InSignalHandler)
}

// Id implements the ds.Ider interface
// Returns the TID (unique thread ID), NOT the slot index
// Id returns the thread's ID as int32.
// Uses pointer receiver to avoid copying the entire Thread struct (368 bytes).
func (t *Thread) Id() int32 {
	return int32(t.TID)
}

// threadLookupByTID does a direct linear scan of the backing arrays to find a thread by TID.
// This avoids the generic StaticList.FindByIdAll which uses an indirect Id() call
// that adds 48 bytes to the nosplit stack chain.
//
//go:nosplit
func threadLookupByTID(tid int32) *Thread {
	for i := 0; i < threadArraySize; i++ {
		if threadListInUse[i] && int32(threadListData[i].TID) == tid {
			return &threadListData[i]
		}
	}
	return nil
}

// threadLookupByPID finds the first thread belonging to the shepherd with the given PID.
// Returns nil if no matching thread is found. Used by SyscallKill to deliver signals
// to a process by PID.
//
//go:nosplit
func threadLookupByPID(pid int16) *Thread {
	for i := 0; i < threadArraySize; i++ {
		if threadListInUse[i] && int16(threadListData[i].PID) == pid {
			return &threadListData[i]
		}
	}
	return nil
}

// hasReadyThreadForPID returns true if any thread with the given PID is in
// ThreadReady state, excluding excludeTID.  Used by threadBlockFutexImpl to
// prevent blocking the last runnable thread of a shepherd.
// REQUIRES: schedulerLock held.
//
//go:nosplit
func hasReadyThreadForPID(pid ShepherdId, excludeTID ThreadId) bool {
	for i := 0; i < MaxThreads; i++ {
		if threadListInUse[i] &&
			threadListData[i].PID == pid &&
			threadListData[i].TID != excludeTID &&
			threadListData[i].State == ThreadReady {
			return true
		}
	}
	return false
}

// WakeThreadForSignal moves a blocked thread to the ready queue so that
// pending signals are delivered at the next context switch.  Called from
// SyscallKill / SyscallTgkill after setting PendingSignals.
//
// Only transitions ThreadBlockedFutex, ThreadSleeping, and
// ThreadBlockedSoftIRQ — if the thread is already Running or Ready the
// signal will be delivered at the next context switch naturally.
//
//go:nosplit
func WakeThreadForSignal(t *Thread) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	switch t.State {
	case ThreadBlockedFutex:
		t.State = ThreadReady
		t.FutexAddr = 0
		blockedQueue.Pluck(t.TID)
		enqueueReadySchedLockHeld(t)
		atomic.AddUint64(&dbgSignalWokeFutex, 1)
	case ThreadSleeping:
		t.State = ThreadReady
		sleepingQueue.Pluck(t.TID)
		enqueueReadySchedLockHeld(t)
	case ThreadBlockedSoftIRQ:
		t.State = ThreadReady
		clearSoftIRQSlotForTID(t.TID)
		enqueueReadySchedLockHeld(t)
	case ThreadBlockedLoadMaz:
		// Defer signal delivery until LoadMaz completes — the worker
		// goroutine will wake this thread with the result. Waking
		// prematurely would return a half-filled MazLoadResult.
	case ThreadBlockedDelegate:
		// Defer signal delivery until delegated syscall reply arrives.
	case ThreadBlockedDelegateRecv:
		// Wake the handler thread so it can handle the signal.
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
	case ThreadBlockedDirtyNotify:
		// Wake so signal is delivered; WaitDirty will re-check queue on resume.
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
	case ThreadBlockedInputEvent:
		// Wake so signal is delivered; WaitInputEvent will re-check on resume.
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
	}
	// ThreadRunning / ThreadReady: signal delivered at next context switch

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// clearSoftIRQSlotForTID scans softIRQSlotData and clears the blockedTID
// field of any slot that matches the given TID.  This is necessary when
// waking a thread out of ThreadBlockedSoftIRQ via signal delivery.
//
// REQUIRES: schedulerLock held.
//
//go:nosplit
func clearSoftIRQSlotForTID(tid ThreadId) {
	for i := 0; i < maxSoftIRQSlots; i++ {
		if softIRQSlotData[i].blockedTID == tid {
			softIRQSlotData[i].blockedTID = -1
			return
		}
	}
}

// ========== Static Allocation Data Structures ==========

// Backing arrays - statically allocated, zero-initialized
var threadListData [threadArraySize]Thread // Stores Thread VALUES (not pointers)
var threadListInUse [threadArraySize]bool  // false = available (zero value)
// shepherdListData and shepherdListInUse are now proc.ShepherdListData and proc.ShepherdListInUse.
var readyQueueData [threadArraySize]ThreadId  // Stores TIDs (unique thread IDs)
var readyQueueInUse [threadArraySize]bool     // Tracks holes in ready queue
var blockedQueueData [threadArraySize]ThreadId // Stores TIDs (unique thread IDs)
var blockedQueueInUse [threadArraySize]bool   // Tracks holes in blocked queue
var sleepingQueueData [threadArraySize]ThreadId // Stores TIDs (unique thread IDs)
var sleepingQueueInUse [threadArraySize]bool  // Tracks holes in sleeping queue

// Static deadline queue backing arrays - used by timer top-half (nosplit path)
var staticDeadlineData [threadArraySize]int16
var staticDeadlineOrderBy [threadArraySize]uint64
var staticDeadlineQueue ds.StaticOrderedList

// ID allocator backing arrays - statically allocated
var threadIdStackData [threadArraySize]ThreadId // Backing array for thread ID allocator
var shepherdIdStackData [proc.MaxShepherds]proc.ShepherdId // Backing array for shepherd ID allocator

// nextKernelThreadId is a counter for allocating kernel thread IDs (0 to ReservedKernelThreads-1).
// Kernel threads are identified by PID == 0 (the kernel shepherd) and get IDs from this counter.
// Starts at 0, incremented each time a kernel thread is created.
// Panics if exhausted (kernel has limited threads).
var nextKernelThreadId ThreadId = 0

// Data structures - will be initialized in InitThreads()
// DO NOT initialize slices here - Go's initialization order causes them to be length 0!
var threadList ds.StaticList[*Thread, Thread] // StaticList stores Thread VALUES, returns pointers
var shepherdList ds.StaticList[*proc.Shepherd, proc.Shepherd] // StaticList stores Shepherd VALUES, returns pointers

var readyQueue ds.StaticQueue[ThreadId]

var blockedQueue ds.StaticQueue[ThreadId]

var sleepingQueue ds.StaticQueue[ThreadId]

// ID allocators - initialized in InitIdAllocators()
var threadIdAllocator ds.StaticAllocator[ThreadId] // Manages unique thread IDs (0..MaxThreads-1)
var shepherdIdAllocator ds.StaticAllocator[proc.ShepherdId] // Manages unique shepherd IDs (0..MaxShepherds-1)

// ========== Scheduler Lock ==========

// schedulerLock protects ALL scheduler structures:
// - threadList, readyQueue, blockedQueue, sleepingQueue
// - threadIdAllocator, shepherdIdAllocator
// - CurrentThread (via atomic operations)
// - All thread state transitions
//
// LOCK DISCIPLINE: This single lock prevents nested locking deadlocks.
// All scheduler operations must acquire this lock, never individual structure locks.
var schedulerLock ds.Spinlock

// ========== Thread State ==========

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
//go:nosplit
//go:noinline
func setSyscallELRInternal(elr uint64) {
	t := GetCurrentThread()
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
//go:nosplit
//go:noinline
func setSyscallSPSRInternal(spsr uint64) {
	t := GetCurrentThread()
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
	t := GetCurrentThread()
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
	t := GetCurrentThread()
	if t != nil {
		return t.SyscallSPSR
	}
	// Early boot: no threads yet, use global
	return syscallSPSR
}

// setSyscallCloneRegsInternal saves R12/R13/R9 from the exception frame.
// On AMD64, the standard Go runtime's clone keeps mp/gp/fn in callee-saved
// registers instead of storing them on the child stack (like ARM64 does).
//
//go:nosplit
//go:noinline
func setSyscallCloneRegsInternal(r12, r13, r9 uint64) {
	t := GetCurrentThread()
	if t != nil {
		t.SyscallR12 = r12
		t.SyscallR13 = r13
		t.SyscallR9 = r9
	}
}

// GetSyscallCloneRegs returns the saved R12/R13/R9 for clone on AMD64.
// Returns (fn, mp, gp) matching the Go runtime's register assignment.
//
//go:noinline
func GetSyscallCloneRegs() (r12, r13, r9 uint64) {
	t := GetCurrentThread()
	if t != nil {
		return t.SyscallR12, t.SyscallR13, t.SyscallR9
	}
	return 0, 0, 0
}

// SetSyscallCloneTLS stores the CLONE_SETTLS newtls value for the current
// thread's pending clone operation. Called from SyscallClone on AMD64.
// The value is consumed by doContextSwitchImpl when CloneNeedsParentRegs is set.
//
//go:nosplit
func SetSyscallCloneTLS(tls uint64) {
	t := GetCurrentThread()
	if t != nil {
		t.SyscallCloneTLS = tls
	}
}

// getSyscallSwitchTargetInternal returns the switch target and resets it
// Called from assembly via ABI stub after syscall dispatch
// Returns uint64: context pointer to switch to, or 0 for no switch.
// Reads from the current thread's SyscallSwitch field to prevent race conditions.
// Falls back to global variable during early boot when CurrentThread is not yet set up.
//
//go:nosplit
//go:noinline
func getSyscallSwitchTargetInternal() uint64 {
	var target uintptr

	t := GetCurrentThread()
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
	t := GetCurrentThread()
	if t != nil {
		t.SyscallSwitch = target
	} else {
		// Early boot: no threads yet, use global
		syscallSwitchTarget = target
	}
}

// InitIdAllocators initializes the ID allocators for thread IDs and shepherd IDs.
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

	// Initialize shepherd ID allocator with ID 0 reserved for kernel
	// IDs 1..MaxShepherds-1 are shuffled and available for Acquire()
	shepherdIdAllocator.InitWithReserved(shepherdIdStackData[:], 1)

	// Reset kernel thread counter (starts at 0, used by AcquireKernelThreadId)
	nextKernelThreadId = 0
}

// AcquireKernelThreadId allocates the next kernel thread ID.
// Kernel threads belong to PID 0 (kernel shepherd) and get sequential IDs 0..ReservedKernelThreads-1.
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

// IsKernelThread returns true if this is a kernel thread (PID == 0, the kernel shepherd).
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

	// Initialize per-CPU data structures for SMP safety
	InitPerCPU()
	initPerCPUOffsets()

	// Initialize spinlock timing based on detected timer frequency
	// This must happen before any spinlocks are used (including in ID allocators)
	ds.InitSpinlockTiming(timerFrequencyHz)

	// Initialize ID allocators first (reserves kernel slots)
	InitIdAllocators()

	// CRITICAL: Initialize data structure slices from backing arrays
	// Must be done here, NOT as global initializers (Go init order issue)
	threadList.Data = threadListData[:]
	threadList.InUse = threadListInUse[:]
	shepherdList.Data = proc.ShepherdListData[:]
	shepherdList.InUse = proc.ShepherdListInUse[:]
	readyQueue.Data = readyQueueData[:]
	readyQueue.InUse = readyQueueInUse[:]
	blockedQueue.Data = blockedQueueData[:]
	blockedQueue.InUse = blockedQueueInUse[:]
	sleepingQueue.Data = sleepingQueueData[:]
	sleepingQueue.InUse = sleepingQueueInUse[:]
	staticDeadlineQueue.Init(staticDeadlineData[:], staticDeadlineOrderBy[:])

	// Initialize reserved slots for kernel use
	threadList.InitReserved(ReservedKernelThreads)
	shepherdList.InitReserved(ReservedKernelShepherds)

	// Save kmazarin's g address early - x28 should be pointing to kmazarin's g
	// at this point (early init runs on g0/m0).
	// This is used by the exception handler when handling userspace syscalls.
	kmazarinG0Addr = GetGRegister()

	// Save kernel FS_BASE for exception handlers (x86_64 only; no-op on ARM64/RISC-V).
	// Must happen while FS_BASE still points to kernel TLS, before any userspace runs.
	platformSaveKernelTLS()

	// Save the exception stack top for SYSCALL entry stack switch.
	_, _, excTop, _ := platformCPU0Stacks()
	excStackTopForSyscall = excTop

	// Initialize all thread DATA to free state (direct access OK during init)
	for i := 0; i < MaxThreads; i++ {
		threadListData[i].State = ThreadFree
		threadListData[i].TID = 0
	}

	// Acquire thread ID 0 for the kernel's entry thread
	firstThreadId := AcquireKernelThreadId()

	// Set up the kernel shepherd at reserved slot 0
	// This represents the "kernel process" that owns all kernel threads
	p0 := shepherdList.ReservedGet(0)
	p0.PID = 0
	shepherdList.ReservedSet(0)

	// Set up thread 0 at reserved slot 0 - the kernel's "entry" thread
	// This thread represents the initial execution context and gets scheduled
	t0 := threadList.ReservedGet(0)
	initThread0Context(&t0.Context)
	t0.State = ThreadRunning
	t0.TID = firstThreadId              // Should be 0
	t0.PID = 0             // Belongs to kernel shepherd (slot 0)
	t0.PageTableL0PA = initThread0PageTable() // Arch-specific: kernel page table PA
	currentTick := ds.CurrentTime(0)
	t0.StartTick = currentTick
	t0.ThreadPreemptDeadline = currentTick + kirq.ThreadPreemptTicks
	t0.PreemptElapsed = 0
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

	// Thread 0 runs on CPU 0 (boot CPU)
	t0.HomeCPU = 0
	t0.StolenFromCPU = -1

	threadList.ReservedSet(0)

	// Register the proc.GetCurrentShepherd hook so ksyscall/kmem can access
	// per-process state without a go:linkname to main.
	proc.GetCurrentShepherd = currentShepherdImpl

	// Use SetCurrentThreadGlobal to update both per-CPU and global CurrentThread
	SetCurrentThreadGlobal(t0)
	threadsInitialized = true
}

// currentShepherdImpl is the registered implementation of proc.GetCurrentShepherd.
// Returns nil for kernel threads (ShepherdIdx < 0) and early-init calls.
//
//go:nosplit
func currentShepherdImpl() *proc.Shepherd {
	t := GetCurrentThread()
	if t == nil {
		return nil
	}
	idx := int(t.ShepherdIdx)
	if idx < 0 || idx >= proc.MaxShepherds || !proc.ShepherdListInUse[idx] {
		return nil
	}
	return &proc.ShepherdListData[idx]
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
	// Remove any existing deadline for this thread to prevent duplicates.
	// A thread can accumulate multiple entries if woken by futex before its
	// previous deadline fires, then calls nanosleep/futex_wait again.
	staticDeadlineQueue.Remove(tid)
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
		// Timer deadlines are encoded as negative slot IDs: -(slot+2).
		// Decode and push events to the slot, waking whatever thread is blocked.
		if tid <= -2 {
			sec, nsec := ktime.GetTime()
			PushTimerEventAndWake(sec, nsec)
			continue
		}

		// Find the thread by TID and wake it
		t := threadLookupByTID(int32(tid))
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
			atomic.AddUint64(&dbgDeadlineWokeSleeper, 1)

			// When waking a sleeping thread (e.g., sysmon from usleep),
			// also wake that shepherd's netpoll waiter if one exists.
			// This implements the kernel-level event delivery that Linux's
			// epoll provides: when sysmon wakes, the netpoll thread should
			// also wake so findRunnable can check timers and run queues.
			if t.PID != 0 {
				p := proc.FindShepherdBySID(proc.ShepherdId(t.PID))
				if p != nil {
					waiterTID := p.NetpollWaiterTID
					if waiterTID != 0 && int32(tid) != waiterTID {
						wt := threadLookupByTID(waiterTID)
						if wt != nil && wt.State == ThreadSleeping {
							wt.State = ThreadReady
							sleepingQueue.Pluck(ThreadId(int16(waiterTID)))
							enqueueReadySchedLockHeld(wt)
						}
					}
				}
			}
		} else {
			// Deadline fired but thread in unexpected state — dropped
			serial.RawUARTPuts("Dx")
			serial.RawUARTDecimal(uint64(tid))
			serial.RawUARTPuts(":")
			serial.RawUARTDecimal(uint64(t.State))
			serial.RawUARTPuts(" ")
		}
	}
}

// ProcessDeadlinesTopHalf processes expired deadlines from the static queue.
// Called from timer IRQ top-half (assembly). Acquires schedulerLock + masks IRQs.
//
//go:nosplit
//go:noinline
func ProcessDeadlinesTopHalf() {
	// Increment arch-universal scan counter (used by KernelIdleLoop A/D scan).
	// This is the only call site that fires on all 3 architectures.
	cnt := atomic.AddUint64(&scanTimerCount, 1)
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()
	processStaticDeadlinesSchedLockHeld()
	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	// Flush any pending console ring data to userspace.
	// This was in KernelIdleLoop but the idle loop is starved when many
	// userspace threads are cycling through futex/sleep deadlines.
	// Moving it here ensures the stdio shepherd gets woken every timer tick.
	if softIRQConsole != nil {
		softIRQConsole.CheckPendingWake()
	}

	// Compact stats every ~10 seconds (assuming ~100 Hz timer)
	if cnt%1000 == 0 {
		serial.RawUARTPuts("\n[E] svc=")
		serial.RawUARTDecimal(atomic.LoadUint64(&ksyscall.TotalSVCCount))
		serial.RawUARTPuts(" fw=")
		serial.RawUARTDecimal(atomic.LoadUint64(&ksyscall.FutexWaitBlocked))
		serial.RawUARTPuts("/")
		serial.RawUARTDecimal(atomic.LoadUint64(&ksyscall.FutexWakeCalls))
		printThreadStateSummary()
		// Kernel goroutine preemption counters (seen/notWanted/unsafe/injected)
		seen, notWanted, unsafePt, injected := getKGPCounters()
		if seen > 0 || injected > 0 {
			serial.RawUARTPuts(" KGP=")
			serial.RawUARTDecimal(seen)
			serial.RawUARTPuts("/")
			serial.RawUARTDecimal(notWanted)
			serial.RawUARTPuts("/")
			serial.RawUARTDecimal(unsafePt)
			serial.RawUARTPuts("/")
			serial.RawUARTDecimal(injected)
		}
		// Thread preemption: timer context switches / no-switch / NeedsThreadPreempt sets
		tcs := atomic.LoadUint64(&timerCtxSwitchCount)
		tns := atomic.LoadUint64(&timerNoSwitchCount)
		serial.RawUARTPuts(" T=")
		serial.RawUARTDecimal(tcs)
		serial.RawUARTPuts("/")
		serial.RawUARTDecimal(tns)
		// Boost thread 0 diagnostics: attempts/successes/lastFailState
		ba := atomic.LoadUint64(&dbgBoostAttempt)
		bs := atomic.LoadUint64(&dbgBoostSuccess)
		if ba > 0 {
			serial.RawUARTPuts(" B0=")
			serial.RawUARTDecimal(ba)
			serial.RawUARTPuts("/")
			serial.RawUARTDecimal(bs)
			serial.RawUARTPuts("/s")
			serial.RawUARTDecimal(atomic.LoadUint64(&dbgBoostFailState))
		}
		serial.RawUARTPuts(" IL=")
		serial.RawUARTDecimal(dbgIdleCount)
		// Timer preemption assembly diagnostics: reached/hit/nothit
		trc := atomic.LoadUint64(&kirq.DbgTimerReachedCheck)
		tdh := atomic.LoadUint64(&kirq.DbgTimerDeadlineHit)
		tdn := atomic.LoadUint64(&kirq.DbgTimerDeadlineNotHit)
		serial.RawUARTPuts("\n TP=")
		serial.RawUARTDecimal(trc)
		serial.RawUARTPuts("/")
		serial.RawUARTDecimal(tdh)
		serial.RawUARTPuts("/")
		serial.RawUARTDecimal(tdn)
		// Exception handler path: el0/el1h/svc/notset
		serial.RawUARTPuts(" EH=")
		serial.RawUARTDecimal(atomic.LoadUint64(&dbgTimerEL0))
		serial.RawUARTPuts("/")
		serial.RawUARTDecimal(atomic.LoadUint64(&dbgTimerSkipEL1h))
		serial.RawUARTPuts("/")
		serial.RawUARTDecimal(atomic.LoadUint64(&dbgTimerSkipSVC))
		serial.RawUARTPuts("/")
		serial.RawUARTDecimal(atomic.LoadUint64(&dbgTimerPreemptNotSet))
		// checkThreadPreemptionImpl: switch/noNext
		serial.RawUARTPuts(" PS=")
		serial.RawUARTDecimal(atomic.LoadUint64(&dbgPreemptSwitchCount))
		serial.RawUARTPuts("/")
		serial.RawUARTDecimal(atomic.LoadUint64(&dbgPreemptNoNextCount))
		// A/D scan deltas since last [E] dump
		printADScanCounters()
		// Input device IRQ counts: kbd/mouse/tablet
		printInputIRQCounters()
		// Per-shepherd GC cycle counts
		printGCCounters()
	}
	// Heartbeat: print '.' every ~5 seconds to confirm timer is alive
	if cnt%500 == 0 {
		serial.RawUART('.')
	}
}

// printADScanCounters prints A/D scan delta counters for the [E] event dump.
// NOT nosplit — breaks the nosplit chain so it doesn't add to the timer IRQ
// nosplit budget (same pattern as printGCCounters).
func printADScanCounters() {
	adRuns, adAccessed, adTotal := kmem.ReadAndResetScanDeltas()
	serial.RawUARTPuts(" AD=")
	serial.RawUARTDecimal(adRuns)
	serial.RawUARTPuts("/")
	serial.RawUARTDecimal(adAccessed)
	serial.RawUARTPuts("/")
	serial.RawUARTDecimal(adTotal)
}

// printGCCounters prints per-shepherd GC cycle counts, kernel heap size,
// and per-type page breakdown.
// NOT nosplit — this gets its own stack check so it doesn't add to the
// timer IRQ nosplit chain budget.
//
// WARNING: This runs from the timer top-half (~every 10 seconds) and performs
// significant serial UART output — iterating page type arrays and writing
// multiple strings per type. Could cause timer ISR overruns if many page types
// are active, since UART writes are blocking. Mitigated by go:noinline keeping
// it out of the nosplit chain (stack can grow if needed), and by being called
// infrequently (every ~1000 ticks).
//
//go:noinline
func printGCCounters() {
	// Kernel heap size: pages/MBm, page faults
	khPages := kmem.KernelHeapPageCount()
	serial.RawUARTPuts(" kh=")
	serial.RawUARTDecimal(khPages)
	serial.RawUARTPuts("/")
	serial.RawUARTDecimal(khPages / 256)
	serial.RawUARTPuts("m pf=")
	serial.RawUARTDecimal(kmem.KernelPageFaultCount())
	// Per-type page breakdown (only non-zero types)
	byType := kmem.PagesByType()
	serial.RawUARTPuts(" [")
	first := true
	typeNames := [...]string{"kh", "kpt", "ks", "mmio", "fb", "vq", "ut", "uro", "ud", "uh", "us", "upt", "ipc", "fil", "bs", "drv", "vdso"}
	for i, cnt := range byType {
		if cnt == 0 {
			continue
		}
		if !first {
			serial.RawUARTPuts(",")
		}
		first = false
		if i < len(typeNames) {
			serial.RawUARTPuts(typeNames[i])
		} else {
			serial.RawUARTPuts("?")
		}
		serial.RawUARTPuts("=")
		serial.RawUARTDecimal(cnt)
	}
	serial.RawUARTPuts("]")
	// Per-shepherd GC cycle counts
	hasGC := false
	for i := 0; i < len(ksyscall.GCCountBySID); i++ {
		gc := atomic.LoadUint64(&ksyscall.GCCountBySID[i])
		if gc > 0 {
			if !hasGC {
				serial.RawUARTPuts(" GC=")
				hasGC = true
			} else {
				serial.RawUARTPuts(",")
			}
			serial.RawUARTDecimal(uint64(i))
			serial.RawUARTPuts(":")
			serial.RawUARTDecimal(gc)
		}
	}
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
// Called from main() after all setup is complete and shepherd threads are on the
// ready queue. This function never returns.
//
// Thread 0 stays alive as a normal scheduled thread. The timer IRQ preempts it
// and context-switches to shepherd threads on the ready queue. When thread 0 gets
// scheduled back, it resumes here — processing deadlines and waiting for the
// next interrupt.
//
// LOCK DISCIPLINE: save DAIF → mask IRQs → acquire lock → process → release → restore → WFI
//
var dbgIdleCount uint64
var wfiCount uint64
var trapReturnCount uint64
var dbgZeroProgressCount uint64
var dbgZeroProgressT0Count uint64 // zero-progress for TID 0 only (idle loop, expected)
var dbgZPLastTID uint64           // TID of last non-TID0 zero-progress thread
var dbgZPLastPC uint64            // PC of last non-TID0 zero-progress thread
var dbgZPLastPS uint64            // Processor state of last non-TID0 zero-progress thread
var dbgBadPC uint64               // DEBUG: kernel PC saved for userspace thread
// Timer preemption path diagnostics (written by exceptions_{arm64,riscv64}.s)
var dbgTimerEL0 uint64          // timer IRQ interrupted EL0 (userspace)
var dbgTimerSkipEL1h uint64     // skipped: EL1h (exception handler mode)
var dbgTimerSkipSVC uint64      // skipped: svcDepth > 0
var dbgTimerPreemptNotSet uint64 // reached check but NeedsThreadPreempt was 0
var dbgBadPS uint64
var dbgBadTID uint64
var dbgBadPCCount uint64
var dbgBoostAttempt uint64        // times boostThread0ForPendingWork was called
var dbgBoostSuccess uint64        // times boost succeeded (thread 0 was Ready)
var dbgBoostFailState uint64      // thread 0 state when boost failed (last value)

//go:nosplit
func printThreadStateSummary() {
	var nReady, nFutex, nSleep, nSoftIRQ, nRunning int
	for i := 0; i < threadArraySize; i++ {
		if !threadListInUse[i] {
			continue
		}
		switch threadListData[i].State {
		case ThreadReady:
			nReady++
		case ThreadRunning:
			nRunning++
		case ThreadBlockedFutex:
			nFutex++
		case ThreadSleeping:
			nSleep++
		case ThreadBlockedSoftIRQ:
			nSoftIRQ++
		}
	}
	serial.RawUARTPuts(" R=")
	serial.RawUARTDecimal(uint64(nReady))
	serial.RawUARTPuts(" F=")
	serial.RawUARTDecimal(uint64(nFutex))
	serial.RawUARTPuts(" S=")
	serial.RawUARTDecimal(uint64(nSleep))
	serial.RawUARTPuts(" I=")
	serial.RawUARTDecimal(uint64(nSoftIRQ))
	serial.RawUARTPuts(" X=")
	serial.RawUARTDecimal(uint64(nRunning))
	// Show TIDs of Ready threads (up to 8)
	if nReady > 0 {
		serial.RawUARTPuts(" Rt[")
		shown := 0
		for i := 0; i < threadArraySize && shown < 8; i++ {
			if threadListInUse[i] && threadListData[i].State == ThreadReady {
				if shown > 0 {
					serial.RawUART(',')
				}
				serial.RawUARTDecimal(uint64(threadListData[i].TID))
				serial.RawUART('/')
				serial.RawUARTDecimal(uint64(threadListData[i].PID))
				shown++
			}
		}
		serial.RawUART(']')
	}
	serial.RawUARTPuts("\n")
}

//go:noinline
func KernelIdleLoop() {
	for {
		dbgIdleCount++
		if dbgIdleCount <= 3 {
			serial.RawUARTPuts("\n[IL")
			serial.RawUARTDecimal(dbgIdleCount)
			serial.RawUARTPuts("]")
		}
		// Check for pending kernel work and execute it directly on this
		// goroutine's growable stack. Both atomic flag and BlockedTID are checked.
		DispatchLoadMazWork()
		DispatchRunMazWork()
		DispatchRunShepherdWork()

		// NOTE: runtime.Gosched() was removed here. When Gosched runs Go's
		// internal goroutine scheduler, goroutines doing SVC sched_yield cause
		// OS-level context switches that save thread 0 inside a random goroutine.
		// When thread 0 resumes, it's in that goroutine — not the idle loop —
		// and the dispatch calls above are never reached again.
		// Without Gosched, thread 0 is always saved inside this loop (by timer
		// preemption or YieldToReadyThread). When restored, it resumes here
		// and reaches the dispatch calls on the next iteration.
		// Bottom-half goroutines get CPU time via Go's async preemption or
		// when they're woken by channel sends and scheduled by the runtime.

		// Bridge IRQ flags to bottom-half channels.
		// IRQ handlers set atomic flags; we convert them to channel sends
		// so the channel-based bottom-half goroutines wake up.
		if atomic.SwapUint32(&uartRxPending, 0) == 1 {
			select {
			case uartRxEventChan <- struct{}{}:
			default:
			}
		}
		if atomic.SwapUint32(&DeadlinePending, 0) == 1 {
			select {
			case deadlineEventChan <- struct{}{}:
			default:
			}
		}
		if atomic.SwapUint32(&kmem.PageTrackingPending, 0) == 1 {
			select {
			case pageTrackingEventChan <- struct{}{}:
			default:
			}
		}
		// Flush any pending console ring data to userspace.
		if softIRQConsole != nil {
			softIRQConsole.CheckPendingWake()
		}

		// Periodic A/D bit scan. Clears hardware Accessed bits on all mapped
		// userspace pages and propagates Dirty state into PageDescriptors.
		// This is the "clock" algorithm reference-bit sweep — foundation for
		// page replacement.
		//
		// scanTimerCount is incremented in ProcessDeadlinesTopHalf (called from
		// the timer top-half on all 3 archs). The timer fires every ~100ms of
		// virtual time, but QEMU TCG runs ~30x slower than real time, so each
		// tick is ~3 seconds of wall-clock time. Threshold of 3 gives a scan
		// roughly every ~9 seconds wall-clock in QEMU TCG.
		currentTick := atomic.LoadUint64(&scanTimerCount)
		if currentTick-scanLastTick >= 300 {
			scanLastTick = currentTick
			for i := 0; i < MaxShepherds; i++ {
				if !proc.ShepherdListInUse[i] {
					continue
				}
				kmem.ScanAccessedBits(proc.ShepherdId(i))
			}
		}

		// Process deadlines with IRQs disabled (critical section)
		savedDAIF := SaveAndDisableIRQs()
		schedulerLock.Lock()
		ProcessDeadlines()
		processStaticDeadlinesSchedLockHeld()
		hasReady := hasAnyReadyThreads()

		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)

		if hasReady {
			// A thread is ready - yield to it.
			// YieldToReadyThread saves thread 0's context, enqueues it,
			// and ERETSs to the next ready thread. Thread 0 resumes here
			// when it's scheduled back via timer preemption.
			YieldToReadyThread()
			// Resumed from preemption — loop back to process deadlines
			continue
		}

		// No ready threads — wait for an interrupt (timer tick, etc.)
		// IRQs MUST be enabled for WFI so the timer interrupt can fire
		wfiCount++
		if wfiCount <= 3 {
			serial.RawUARTPuts("[WFI]")
		}
		EnableIRQs()
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
	// Check m.locks — do not yield if the Go runtime is in a critical section
	// (e.g., lock2, mallocgc). Yielding with locks held causes expensive
	// context switches that prevent forward progress during early boot.
	// Without this check, the futex overlay's yield-after-spin triggers
	// full save/restore cycles even though the thread should just retry.
	gmOff := kirq.PreemptGMOffset
	mLocksOff := kirq.PreemptMLocksOffset
	if gmOff != 0 || mLocksOff != 0 { // offsets initialized
		gRaw := GetGRegister()
		if gRaw != 0 {
			mPtr := *(*uintptr)(unsafe.Pointer(uintptr(gRaw) + gmOff))
			if mPtr != 0 {
				locks := *(*int32)(unsafe.Pointer(mPtr + mLocksOff))
				if locks != 0 {
					return 0
				}
			}
		}
	}

	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t0 := GetCurrentThread()
	if t0 == nil {
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return 0
	}

	// Thread 0 is the idle thread — NOT enqueued in the ready queue.
	// It runs opportunistically between preemptions of other threads.
	// Save context so it can be restored, but don't queue it.
	t0.State = ThreadReady
	pluckFromAllQueues(t0.TID) // safety: ensure not in queue from prior path

	// Update tick accounting for thread 0
	currentTime := kirq.ReadCounterValue()
	if t0.TicksStartedRunning != 0 {
		t0.TotalTicksRunning += currentTime - t0.TicksStartedRunning
	}
	t0.TicksStartedRunning = 0

	// Find next ready thread (prefer a shepherd, not kernel)
	next := findReadyThreadPreferDifferentShepherdSchedLockHeld(t0.PID)
	if next == nil {
		// No thread available — continue running thread 0
		t0.State = ThreadRunning
		t0.TicksStartedRunning = currentTime
		schedulerLock.Unlock()
		RestoreIRQs(savedDAIF)
		return 0
	}

	// Shepherd-level tick accounting: stop old shepherd, start new shepherd
	if next.PID != t0.PID {
		if t0.ShepherdIdx >= 0 {
			oldShepherd := shepherdList.Get(int(t0.ShepherdIdx))
			if oldShepherd != nil && oldShepherd.TicksStartedRunning != 0 {
				oldShepherd.TotalTicksRunning += currentTime - oldShepherd.TicksStartedRunning
				oldShepherd.TicksStartedRunning = 0
			}
		}
		if next.ShepherdIdx >= 0 {
			newShepherd := shepherdList.Get(int(next.ShepherdIdx))
			if newShepherd != nil {
				newShepherd.TicksStartedRunning = currentTime
			}
		}
	}

	// Switch current thread to the new one (updates both per-CPU and global)
	SetCurrentThreadGlobal(next)
	next.State = ThreadRunning
	next.StartTick = currentTime
	next.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
	next.PreemptElapsed = 0
	next.TicksStartedRunning = currentTime

	// Switch TTBR0 if needed for userspace thread
	if next.PageTableL0PA != 0 && next.PageTableL0PA != t0.PageTableL0PA {
		kmem.SwitchTTBR0WithASID(next.PageTableL0PA, uint16(next.PID))
	}

	// Save info for debug output (before unlock to ensure we have valid data)
	debugCPU := GetCPUID()
	debugTID := next.TID
	debugPID := next.PID
	debugStolenFrom := next.StolenFromCPU
	next.StolenFromCPU = -1 // Clear after reading

	schedulerLock.Unlock()
	// Don't restore DAIF — ERET will restore from SPSR
	_ = savedDAIF

	// Debug output after lock release (safe to call non-nosplit functions)
	if debugStolenFrom >= 0 {
		smpDebugPrintSteal(debugCPU, debugTID, uint64(debugStolenFrom))
	}
	smpDebugPrintRun(debugCPU, debugTID, debugPID)

	if next.Context.GetPC() == 0 {
		serial.RawUARTPuts("[BUG] Yield RIP=0 TID=")
		serial.RawUARTHex64(uint64(next.TID))
		serial.PollWrite('\n')
		for {
			WaitForInterrupt()
		}
	}

	// Deliver pending signals before ERET to this thread.
	if next.PendingSignals != 0 && next.InSignalHandler == 0 {
		DeliverPendingSignal(next)
	}

	return uint64(uintptr(unsafe.Pointer(&next.Context)))
}

// StartFirstThread waits for and starts the first ready thread.
// This is called from the kernel main when there are threads in the ready queue
// but no current thread is running. It uses IdleLoop to wait for a ready thread,
// then sets up CurrentThread and returns the context pointer.
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

	// CRITICAL: Skip kernel threads (PID=0) and find a shepherd thread (PID>0)
	// Kernel goroutines may have been preempted into the ready queue before
	// timer was disabled. We need to re-queue them and find a shepherd thread.
	for thread.PID == 0 {
		// Put kernel thread back at the BACK of the ready queue (not front!)
		// StartFirstThread needs a shepherd thread to ERET to userspace.
		// Use direct per-CPU push to avoid priority front-insertion for PID=0.
		savedDAIF := sf.DisableAndSaveDAIF()
		schedulerLock.Lock()
		thread.State = ThreadReady
		// Enqueue to current CPU's queue at the back
		perCPU := GetPerCPU()
		perCPU.LocalReadyQueue.PushNoDuplicate(thread.TID)
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

	// Set up current thread (updates both per-CPU and global)
	SetCurrentThreadGlobal(thread)
	thread.State = ThreadRunning

	// Initialize preemption tracking
	currentTime := sf.CurrentTime(0)
	thread.StartTick = currentTime
	thread.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
	thread.PreemptElapsed = 0
	thread.TicksStartedRunning = currentTime

	// Switch TTBR0 to the thread's page table
	if thread.PageTableL0PA != 0 {
		kmem.SwitchTTBR0WithASID(thread.PageTableL0PA, uint16(thread.PID))
		// Note: TLB flush not needed due to ASID tagging
	}
	// END CRITICAL SECTION
	schedulerLock.Unlock()

	// DEBUG: Print thread details before ERET
	console.KPrintf("[StartFirst] TID=%d PID=%d PC=0x%x SP=0x%x PState=0x%x L0PA=0x%x\n",
		thread.TID, thread.PID, thread.Context.GetPC(), thread.Context.GetSP(), thread.Context.GetProcessorState(), thread.PageTableL0PA)

	// DEBUG: Verify stack page is mapped by walking the page table
	stackAddr := uintptr(thread.Context.GetSP())
	stackPA := kmem.WalkUserPageTableWithL0(stackAddr, thread.PageTableL0PA)
	console.KPrintf("[StartFirst] StackWalk: VA=0x%x PA=0x%x\n", stackAddr, stackPA)

	// DEBUG: Read L0[255] directly via linear map
	const koff = uintptr(0xFFFFFFFF00000000)
	l0VA := thread.PageTableL0PA + koff
	l0e255 := *(*uint64)(unsafe.Pointer(l0VA + 255*8))
	l0e0 := *(*uint64)(unsafe.Pointer(l0VA + 0*8))
	console.KPrintf("[StartFirst] L0[0]=0x%x L0[255]=0x%x\n", l0e0, l0e255)

	// Don't restore DAIF - the ERET will set SPSR which controls IRQ state
	_ = savedDAIF

	// Deliver pending signals before ERET to this thread.
	if thread.PendingSignals != 0 && thread.InSignalHandler == 0 {
		DeliverPendingSignal(thread)
	}

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
	parent := GetCurrentThread()

	// Determine if this is a kernel thread (parent PID == 0, the kernel shepherd)
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
		// Userspace threads use normal allocation (shuffled IDs).
		// The Go runtime parks idle Ms via futex_wait; they accumulate but
		// are reused when work becomes available. We do NOT reap them because
		// the runtime expects parked M's to stay alive - it doesn't check
		// futex_wake return values and would have inconsistent scheduler state.
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
	t.ThreadPreemptDeadline = currentTick + kirq.ThreadPreemptTicks
	t.PreemptElapsed = 0 // Fresh thread, no elapsed time yet
	t.TotalTicksRunning = 0 // Fresh thread, no accumulated runtime yet
	t.TicksStartedRunning = currentTick // Thread runs immediately (State = Running)

	// CRITICAL: Set InCloneSetup to protect the clone child during setup.
	// The child must read fn/gp/mp from the stack before any preemption.
	// This flag will be cleared on the child's first syscall.
	t.InCloneSetup = 1

	// CRITICAL: Inherit page table and shepherd ID from parent thread!
	// Without this, cloned threads have PageTableL0PA=0 and won't
	// get TTBR0 switched when scheduled, causing page faults.
	// PID (shepherd ID) is used as ASID for TLB tagging.
	if parent != nil {
		t.PageTableL0PA = parent.PageTableL0PA
		t.PID = parent.PID
		t.ShepherdIdx = parent.ShepherdIdx

		// Increment shepherd's thread count for userspace threads (PID > 0)
		if parent.PID > 0 && parent.ShepherdIdx >= 0 {
			shepherd := &proc.ShepherdListData[parent.ShepherdIdx]
			if proc.ShepherdListInUse[parent.ShepherdIdx] {
				shepherd.ThreadCount++
			}
		}
	}

	// Capture gsignal stack bounds for signal delivery.
	// Path: m → m.gsignal → gsignal.stack.hi / gsignal.stack.lo
	// Use safe accessors — direct dereference fails on RISC-V (SUM=0).
	if t.MPtr != 0 {
		mPtr := uintptr(t.MPtr)
		gsignalVal, ok := kmem.ReadUserUint64(mPtr + kirq.PreemptMGsignalOffset)
		gsignalPtr := uintptr(gsignalVal)
		if ok && gsignalPtr != 0 {
			stackHiVal, ok1 := kmem.ReadUserUint64(gsignalPtr + kirq.PreemptStackHiOffset)
			stackLoVal, ok2 := kmem.ReadUserUint64(gsignalPtr + kirq.PreemptStackLoOffset)
			if ok1 && ok2 {
				t.SignalSP = stackHiVal       // Stack grows down
				t.SignalStackBase = stackLoVal
				t.SignalStackSize = stackHiVal - stackLoVal
			}
		}
	}

	// Set HomeCPU to current CPU for cache locality.
	// Cloned threads will prefer to run on the CPU that created them.
	t.HomeCPU = int8(GetCPUID())
	t.StolenFromCPU = -1

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
	t.Context.SetupForCloneChild(stack, returnAddr, gp, spsr)

	// Signal that doContextSwitchImpl should copy the parent's full register
	// state to this child. SetupForCloneChild only sets X0, X28, SP, ELR, SPSR;
	// the remaining registers (X1-X27, X29, X30) must come from the parent's
	// exception frame, which is saved by SaveContextFromFrame in doContextSwitchImpl.
	t.CloneNeedsParentRegs = 1

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
	t := GetCurrentThread()
	if t == nil {
		return 0 // No current thread
	}

	// BEGIN CRITICAL SECTION
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Mark as exited
	t.State = ThreadExited
	// Try to pluck from ready queue (may not be there)
	pluckFromAllQueues(t.TID)

	// Release unique thread ID back to allocator
	threadIdAllocator.Release(t.TID)

	// Release thread slot
	threadList.Release(int(threadToIdx(t)))

	// Decrement shepherd thread count and release shepherd if this was the last thread
	exitingPID := t.PID
	if t.PID > 0 && t.ShepherdIdx >= 0 && proc.ShepherdListInUse[t.ShepherdIdx] {
		shepherd := &proc.ShepherdListData[t.ShepherdIdx]
		shepherd.ThreadCount--
		if shepherd.ThreadCount <= 0 {
			// Last thread of this shepherd - release the shepherd
			releaseShepherdSchedLockHeld(t.ShepherdIdx, exitingPID)
		}
	}

	// Find next ready thread, preferring a different shepherd for fairness
	next := findReadyThreadPreferDifferentShepherdSchedLockHeld(exitingPID)
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
	next.ThreadPreemptDeadline = currentTick + kirq.ThreadPreemptTicks
	next.TicksStartedRunning = currentTick
	// Update both per-CPU and global CurrentThread
	SetCurrentThreadGlobal(next)

	// Save info for debug output (before unlock)
	debugCPU := GetCPUID()
	debugTID := next.TID
	debugPID := next.PID
	debugStolenFrom := next.StolenFromCPU
	next.StolenFromCPU = -1

	if sf.StateCheck != nil {
		sf.StateCheck("thread-exit-switch")
	}

	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	// Debug output after lock release
	if debugStolenFrom >= 0 {
		smpDebugPrintSteal(debugCPU, debugTID, uint64(debugStolenFrom))
	}
	smpDebugPrintRun(debugCPU, debugTID, debugPID)

	return uintptr(unsafe.Pointer(&next.Context))
}

// threadExitInternal is the ABI0-compatible wrapper around threadExitImpl.
// Called from exception handler assembly via ThreadExitAsm stub.
// Returns uint64 (pointer to next ThreadContext, or 0 if no threads left).
//
//go:nosplit
func threadExitInternal() uint64 {
	return uint64(threadExitImpl(&NormalSchedulerFunc))
}

// TerminateShepherd kills all threads belonging to a shepherd and cleans up resources.
// Walks all thread slots, exits every thread with matching PID (except current),
// then exits the current thread last. Calls releaseShepherdSchedLockHeld when
// the last thread exits.
// Returns pointer to next ready thread's ThreadContext (or 0 if none remain).
//
//go:nosplit
func TerminateShepherd(pid ShepherdId, status int64) uintptr {
	// Clean up delegation resources BEFORE acquiring the scheduler lock.
	// NOT nosplit, which breaks the nosplit chain and avoids exceeding
	// the 792-byte stack limit. Safe because the delegate data structures are
	// protected by IRQ disabling (we're in SVC handler context).
	terminateShepherdDelegateCleanup(int16(pid))
	CleanupSoftIRQSlotsForShepherd(int16(pid))
	CleanupInputFocusForShepherd(int16(pid))
	return terminateShepherdImpl(&NormalSchedulerFunc, pid, status)
}

// terminateShepherdDelegateCleanup reclaims delegation resources for a dying shepherd
// and wakes any orphaned caller threads whose handler shepherd is dying.
// NOT nosplit — breaks the nosplit chain in TerminateShepherd.
func terminateShepherdDelegateCleanup(pid int16) {
	orphanCount := ksyscall.CleanupDelegateForDeadShepherd(pid)
	for i := 0; i < orphanCount; i++ {
		tid := ksyscall.DelegateOrphanedCallerTIDs[i]
		cpid := ksyscall.DelegateOrphanedCallerSIDs[i]
		WakeDelegateCallerThread(cpid, int32(tid), -3) // -ESRCH
	}
}

// terminateShepherdImpl is the internal implementation of TerminateShepherd.
//
//go:nosplit
func terminateShepherdImpl(sf *SchedulerFunc, pid ShepherdId, status int64) uintptr {
	current := GetCurrentThread()

	// BEGIN CRITICAL SECTION
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Count threads killed (for diagnostic output after lock release)
	var killed int

	// Walk all thread slots, kill non-current threads belonging to this shepherd
	for i := 0; i < MaxThreads; i++ {
		if !threadListInUse[i] {
			continue
		}
		t := &threadListData[i]
		if t.PID != pid {
			continue
		}
		if t == current {
			continue // Handle calling thread last
		}

		// Mark as exited
		t.State = ThreadExited

		// Pluck from whatever queue it's in
		pluckFromAllQueues(t.TID)
		blockedQueue.Pluck(t.TID)
		sleepingQueue.Pluck(t.TID)
		clearSoftIRQSlotForTID(t.TID)

		// Release resources
		threadIdAllocator.Release(t.TID)
		threadList.Release(i)
		killed++
	}

	// Now handle calling thread (same as threadExitImpl)
	if current != nil && current.PID == pid {
		current.State = ThreadExited
		pluckFromAllQueues(current.TID)
		threadIdAllocator.Release(current.TID)
		threadList.Release(int(threadToIdx(current)))
		killed++
	}

	// Find the shepherd and release it
	shepherdIdx := int16(-1)
	for i := int16(0); i < int16(MaxShepherds); i++ {
		if proc.ShepherdListInUse[i] && proc.ShepherdListData[i].PID == pid {
			shepherdIdx = i
			break
		}
	}
	if shepherdIdx >= 0 {
		// Force ThreadCount to 0 and release
		proc.ShepherdListData[shepherdIdx].ThreadCount = 0
		releaseShepherdSchedLockHeld(shepherdIdx, pid)
	}

	// Find next ready thread
	next := findReadyThreadPreferDifferentShepherdSchedLockHeld(pid)
	if next == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)

		// Diagnostic output
		serial.RawUARTPuts("[EXIT] shepherd PID=")
		serial.RawUARTDecimal(uint64(pid))
		serial.RawUARTPuts(" status=")
		serial.RawUARTDecimal(uint64(status))
		serial.RawUARTPuts(" threads_killed=")
		serial.RawUARTDecimal(uint64(killed))
		serial.RawUARTPuts("\r\n")
		return 0
	}

	// DO NOT call SetCurrentThreadGlobal(next) here!
	// CurrentThread still points to the dying thread. This is intentional:
	// the SVC return path will call DoContextSwitch, which:
	// 1. Calls SaveContextFromFrame on the dying thread (harmless — slot released)
	// 2. Sees oldThread.PageTableL0PA != newThread.PageTableL0PA → switches TTBR0
	// 3. Calls SetCurrentThreadGlobal(newThread) to complete the transition
	// If we set CurrentThread here, DoContextSwitch would save the dying shepherd's
	// exception frame into the NEW thread's context (corrupting it) and skip the
	// TTBR0 switch (old == new).

	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	// Diagnostic output after lock release
	serial.RawUARTPuts("[EXIT] shepherd PID=")
	serial.RawUARTDecimal(uint64(pid))
	serial.RawUARTPuts(" status=")
	serial.RawUARTDecimal(uint64(status))
	serial.RawUARTPuts(" threads_killed=")
	serial.RawUARTDecimal(uint64(killed))
	serial.RawUARTPuts("\r\n")

	return uintptr(unsafe.Pointer(&next.Context))
}

// terminateShepherdInternal is the ABI0-compatible wrapper for TerminateShepherd.
// Called from assembly via TerminateShepherdAsm tail-call stub.
// Args: pid (uint64), status (int64)
// Returns: pointer to next ThreadContext (or 0 if none remain).
//
//go:nosplit
func terminateShepherdInternal(pid uint64, status int64) uint64 {
	return uint64(terminateShepherdImpl(&NormalSchedulerFunc, ShepherdId(pid), status))
}

// releaseShepherdSchedLockHeld releases a shepherd when its last thread exits.
// MUST be called with schedulerLock held.
// Performs TLB shootdown for the ASID before releasing the shepherd ID,
// enabling aggressive ASID reuse to expose bugs.
//
//go:nosplit
func releaseShepherdSchedLockHeld(shepherdIdx int16, pid ShepherdId) {
	if shepherdIdx < 0 || !proc.ShepherdListInUse[shepherdIdx] {
		return // Invalid or already released
	}

	// CRITICAL: TLB shootdown before releasing the ASID.
	// When this ASID is reused by a new shepherd, we must ensure no stale
	// TLB entries remain that could cause incorrect address translations.
	// TLBI ASIDE1IS broadcasts to all CPUs in the inner shareable domain.
	kmem.TlbiASIDE1IS(uint16(pid))

	// Read l0PA and spans pointer BEFORE zeroing the shepherd struct.
	// CleanupShepherdPages needs these to walk the page tables.
	l0PA := proc.ShepherdListData[shepherdIdx].PageTableL0PA
	spans := &proc.ShepherdListData[shepherdIdx].Spans

	// Free all physical pages owned by this shepherd (Linux-style VMA + PT walk).
	// Must happen before zeroing the shepherd struct (which would clear Spans/l0PA).
	kmem.CleanupShepherdPages(pid, spans, l0PA)

	// Release the shepherd slot
	proc.ShepherdListInUse[shepherdIdx] = false

	// Zero the shepherd struct for security (prevent info leaks)
	proc.ShepherdListData[shepherdIdx] = proc.Shepherd{}

	// Release the shepherd ID back to the allocator for immediate reuse.
	// Because StaticAllocator uses LIFO (stack), this ID will be the next
	// one allocated, enabling aggressive reuse to find bugs.
	shepherdIdAllocator.Release(pid)
}

// CreateUserspaceThread allocates a new thread for a userspace process (like a shepherd).
// entryPoint: the PC to start executing at (ELR_EL1)
// stackPtr: the user stack pointer (SP_EL0)
// pageTableL0PA: physical address of the process's L0 page table
// shepherdId: the shepherd (process) ID, used as ASID for TLB tagging
// Returns the TID (thread ID) of the new thread.
//
//go:nosplit
func CreateUserspaceThread(entryPoint, stackPtr uint64, pageTableL0PA uintptr) int16 {
	return createUserspaceThreadImpl(&NormalSchedulerFunc, entryPoint, stackPtr, pageTableL0PA)
}

// GetShepherdByPID finds a shepherd by its PID.
// Returns nil if not found.
//
//go:nosplit
func GetShepherdByPID(pid ShepherdId) *Shepherd {
	return shepherdList.FindById(int32(pid))
}

// createUserspaceThreadImpl is the internal implementation with sf for testing
//
//go:nosplit
func createUserspaceThreadImpl(sf *SchedulerFunc, entryPoint, stackPtr uint64, pageTableL0PA uintptr) int16 {
	// BEGIN CRITICAL SECTION - protect all scheduling data structures:
	// shepherdIdAllocator, shepherdList, threadList, readyQueue
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Allocate shepherd ID and shepherd entry inside the critical section
	shepherdId := shepherdIdAllocator.Acquire()
	_, p := shepherdList.Allocate()
	p.PID = shepherdId
	p.PageTableL0PA = pageTableL0PA
	p.ThreadCount = 1 // This shepherd starts with one thread

	// Allocate thread slot from static list (panics if exhausted)
	_, t := threadList.Allocate()

	// Acquire unique thread ID from allocator
	tid := threadIdAllocator.Acquire()

	// Fill in thread state
	t.TID = tid // Unique ID from allocator
	t.PID = shepherdId // Shepherd (process) ID for ASID
	t.State = ThreadReady // Not running yet - deadlines set when scheduled
	t.PageTableL0PA = pageTableL0PA
	t.StartTick = 0             // Set when scheduled
	t.ThreadPreemptDeadline = 0 // Set when scheduled
	t.PreemptElapsed = 0
	t.TotalTicksRunning = 0   // Fresh thread, no accumulated runtime yet
	t.TicksStartedRunning = 0 // Not running yet (will be set when scheduled)
	t.FutexAddr = 0
	t.MPtr = 0
	t.GPtr = 0
	t.EntryFunc = 0
	t.CloneNeedsParentRegs = 0 // Clear in case slot was reused from a clone child

	// Set ShepherdIdx for O(1) shepherd lookup in timer handler
	t.ShepherdIdx = -1 // Default: no shepherd
	for pi := 0; pi < len(shepherdList.Data); pi++ {
		if shepherdList.InUse[pi] && shepherdList.Data[pi].PID == shepherdId {
			t.ShepherdIdx = int16(pi)
			break
		}
	}

	// Set up initial context for userspace execution
	t.Context.SetupForUserspace(entryPoint, stackPtr)

	// Set HomeCPU to current CPU for cache locality
	t.HomeCPU = int8(GetCPUID())
	t.StolenFromCPU = -1

	// Add to ready queue (Pluck first just in case TID was reused)
	pluckFromAllQueues(tid)
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
// Uses per-CPU queues: thread goes to its HomeCPU's local queue.
// Kernel threads (PID == 0) go to the FRONT for priority scheduling;
// all other threads go to the BACK (FIFO).
// REQUIRES: schedulerLock held.
//
//go:nosplit
func enqueueReadySchedLockHeld(t *Thread) {
	enqueueReadyToHomeCPU(t)
}

// enqueueReadyToHomeCPU adds thread to its HomeCPU's local queue.
// Falls back to current CPU if HomeCPU is invalid.
// REQUIRES: schedulerLock held (protects all per-CPU queues).
//
//go:nosplit
func enqueueReadyToHomeCPU(t *Thread) {
	targetCPU := t.HomeCPU
	cpuCount := int8(GetCPUCount())

	// Fall back to current CPU if HomeCPU is invalid
	if targetCPU < 0 || targetCPU >= cpuCount {
		targetCPU = int8(GetCPUID())
	}

	perCPU := GetPerCPUByID(uint64(targetCPU))

	// Kernel threads (PID=0) still get priority (front of queue)
	if t.PID == 0 {
		perCPU.LocalReadyQueue.PushHeadNoDuplicate(t.TID)
	} else {
		perCPU.LocalReadyQueue.PushNoDuplicate(t.TID)
	}
}

// enqueueReadyToCurrentCPU adds thread to current CPU's local queue.
// Used when no HomeCPU affinity is desired (e.g., newly created threads).
// REQUIRES: schedulerLock held.
//
//go:nosplit
func enqueueReadyToCurrentCPU(t *Thread) {
	cpuID := GetCPUID()
	t.HomeCPU = int8(cpuID) // Set affinity to current CPU

	perCPU := GetPerCPU()

	if t.PID == 0 {
		perCPU.LocalReadyQueue.PushHeadNoDuplicate(t.TID)
	} else {
		perCPU.LocalReadyQueue.PushNoDuplicate(t.TID)
	}
}

// enqueueReadyByTIDSchedLockHeld is like enqueueReadySchedLockHeld but looks up the
// thread by TID first. Used when only the TID is available.
// REQUIRES: schedulerLock held.
//
//go:nosplit
func enqueueReadyByTIDSchedLockHeld(tid ThreadId) {
	t := threadLookupByTID(int32(tid))
	if t != nil {
		enqueueReadyToHomeCPU(t)
	}
}

// pluckFromAllQueues removes a thread from any per-CPU ready queue.
// Searches all CPU queues since the thread could be on any of them.
// REQUIRES: schedulerLock held (protects all per-CPU queues).
//
//go:nosplit
func pluckFromAllQueues(tid ThreadId) {
	cpuCount := GetCPUCount()
	for i := uint64(0); i < cpuCount; i++ {
		perCPU := GetPerCPUByID(i)
		perCPU.LocalReadyQueue.Pluck(tid)
	}
}

// hasAnyReadyThreads checks if any per-CPU queue has ready threads.
// Used by idle loop to check if work is available.
// REQUIRES: schedulerLock held (protects all per-CPU queues).
//
//go:nosplit
func hasAnyReadyThreads() bool {
	cpuCount := GetCPUCount()
	for i := uint64(0); i < cpuCount; i++ {
		perCPU := GetPerCPUByID(i)
		if !perCPU.LocalReadyQueue.IsEmpty() {
			return true
		}
	}
	return false
}

// ============================================================================
// Work Stealing Functions
// ============================================================================

// findReadyThreadWithStealing checks local queue first, then steals from others.
// This is the main entry point for finding a ready thread with SMP support.
// REQUIRES: schedulerLock held (protects all per-CPU queues).
//
//go:nosplit
func findReadyThreadWithStealing() *Thread {
	myPerCPU := GetPerCPU()

	// 1. Check local queue first (cache-friendly path)
	for !myPerCPU.LocalReadyQueue.IsEmpty() {
		tid := myPerCPU.LocalReadyQueue.Pop()

		t := threadLookupByTID(int32(tid))
		if t != nil && t.State == ThreadReady {
			return t
		}
		// Thread not valid - try next in local queue
	}

	// 2. Local queue empty - try work stealing from other CPUs
	return stealWorkFromOtherCPUs()
}

// stealWorkFromOtherCPUs tries to steal a thread from another CPU's queue.
// Round-robins through other CPUs, stealing from the back (oldest thread).
// Updates stolen thread's HomeCPU to the current CPU.
// REQUIRES: schedulerLock held (protects all per-CPU queues).
//
//go:nosplit
func stealWorkFromOtherCPUs() *Thread {
	myID := GetCPUID()
	cpuCount := GetCPUCount()

	// Round-robin through other CPUs
	for i := uint64(1); i < cpuCount; i++ {
		targetCPU := (myID + i) % cpuCount
		victim := GetPerCPUByID(targetCPU)

		// Only steal if victim has more than 1 thread (leave them at least one)
		if victim.LocalReadyQueue.Size() > 1 {
			// Steal from BACK (oldest = likely cache-cold anyway)
			tid := victim.LocalReadyQueue.PopBack()

			t := threadLookupByTID(int32(tid))
			if t != nil && t.State == ThreadReady {
				// Update HomeCPU to current CPU (new affinity)
				t.HomeCPU = int8(myID)
				// Mark that this thread was stolen (for debug output after lock release)
				t.StolenFromCPU = int8(targetCPU)
				return t
			}
			// Thread became invalid - continue trying other CPUs
		}
	}

	return nil
}

// findReadyThreadSchedLockHeld finds the next READY thread using per-CPU queues with work stealing.
// Checks local queue first, then steals from other CPUs.
// Returns thread pointer, or nil if none found.
// Internal function - use ThreadFindReady for external API.
//
//go:nosplit
func findReadyThreadSchedLockHeld() *Thread {
	return findReadyThreadWithStealing()
}

// findReadyThreadPreferDifferentShepherdSchedLockHeld finds next ready thread, preferring a different shepherd.
// Used for timer preemption to promote fairness across shepherds.
// Falls back to any ready thread if only same-shepherd threads available.
// Uses per-CPU queues with work stealing.
// REQUIRES: schedulerLock held (protects all per-CPU queues).
//
//go:nosplit
func findReadyThreadPreferDifferentShepherdSchedLockHeld(currentPID ShepherdId) *Thread {
	myPerCPU := GetPerCPU()
	myID := GetCPUID()
	cpuCount := GetCPUCount()

	// First pass: look for a different shepherd in local queue
	q := &myPerCPU.LocalReadyQueue
	idx := q.Head()
	for seen := 0; seen < len(q.Data); seen++ {
		if q.InUse[idx] {
			tid := q.Data[idx]
			t := threadLookupByTID(int32(tid))
			if t != nil && t.State == ThreadReady && t.PID != currentPID {
				q.PluckAt(idx)
				return t
			}
		}
		idx = (idx + 1) % len(q.Data)
	}

	// Second pass: look for a different shepherd on other CPUs (steal)
	for i := uint64(1); i < cpuCount; i++ {
		targetCPU := (myID + i) % cpuCount
		victim := GetPerCPUByID(targetCPU)

		vq := &victim.LocalReadyQueue
		vidx := vq.Head()
		for vseen := 0; vseen < len(vq.Data); vseen++ {
			if vq.InUse[vidx] {
				tid := vq.Data[vidx]
				t := threadLookupByTID(int32(tid))
				if t != nil && t.State == ThreadReady && t.PID != currentPID {
					vq.PluckAt(vidx)
					t.HomeCPU = int8(myID) // Update affinity
					return t
				}
			}
			vidx = (vidx + 1) % len(vq.Data)
		}
	}

	// Fallback: any ready thread from local queue
	for !myPerCPU.LocalReadyQueue.IsEmpty() {
		tid := myPerCPU.LocalReadyQueue.Pop()

		t := threadLookupByTID(int32(tid))
		if t != nil && t.State == ThreadReady {
			return t
		}
	}

	// Final fallback: steal anything
	return stealWorkFromOtherCPUs()
}

// findReadyUserspaceThreadSchedLockHeld is like findReadyThreadPreferDifferentShepherdSchedLockHeld
// but only returns actual userspace threads (PID > 0).
// This is used by context-switch paths where the current thread is userspace,
// preferring to stay in userspace to avoid cross-privilege ERET issues on ARM64.
// Callers fall back to findReadyThreadSchedLockHeld when this returns nil,
// which naturally gives kernel thread 0 CPU time when no userspace threads are ready.
//
// NOTE: Filters by PID > 0 (not PageTableL0PA != 0). On AMD64, kernel threads
// have non-zero PageTableL0PA because there's no TTBR0/TTBR1 split.
//
//go:nosplit
func findReadyUserspaceThreadSchedLockHeld(currentPID ShepherdId) *Thread {
	myPerCPU := GetPerCPU()

	// Scan local queue for a userspace thread from a different shepherd.
	// Filter by PID > 0 (not just PageTableL0PA != 0) because on AMD64,
	// kernel threads also have non-zero PageTableL0PA (single CR3, no
	// TTBR0/TTBR1 split). Without the PID check, kernel threads at the
	// head of the queue are mistakenly returned, starving userspace shepherds.
	q := &myPerCPU.LocalReadyQueue
	idx := q.Head()
	for seen := 0; seen < len(q.Data); seen++ {
		if q.InUse[idx] {
			tid := q.Data[idx]
			t := threadLookupByTID(int32(tid))
			if t != nil && t.State == ThreadReady && t.PID > 0 && t.PID != currentPID {
				q.PluckAt(idx)
				return t
			}
		}
		idx = (idx + 1) % len(q.Data)
	}

	// Fallback: any userspace thread from local queue (even same shepherd)
	idx = q.Head()
	for seen := 0; seen < len(q.Data); seen++ {
		if q.InUse[idx] {
			tid := q.Data[idx]
			t := threadLookupByTID(int32(tid))
			if t != nil && t.State == ThreadReady && t.PID > 0 {
				q.PluckAt(idx)
				return t
			}
		}
		idx = (idx + 1) % len(q.Data)
	}

	return nil
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
	// If current thread is userspace, skip kernel threads — the SVC return
	// goes through el0_return/ERET which needs EL0 context.
	// If current thread is kernel, allow any thread type.
	current := GetCurrentThread()
	if current != nil && current.PageTableL0PA != 0 && t.PageTableL0PA == 0 {
		enqueueReadySchedLockHeld(t)
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
// CRITICAL: The expectedVal parameter is used to re-check the futex value under
// the scheduler lock. This prevents the classic futex missed-wakeup race where:
//   1. Thread A checks value, sees it matches expected
//   2. Thread B wakes the futex (but A isn't blocked yet)
//   3. Thread A marks itself blocked (will never be woken)
//
// By re-checking under the lock, we ensure the check and block are atomic.
// Returns 0 if value changed (caller should return EAGAIN).
//
//go:nosplit
func ThreadBlockFutex(futexAddr uint64, expectedVal uint32) uintptr {
	return threadBlockFutexImpl(&NormalSchedulerFunc, futexAddr, expectedVal)
}

// idleWaitForReadyThread is the single-CPU idle loop for the scheduler.
// When a futex_wait caller blocks and no other thread is ready, this function
// yields the CPU via WFI (Wait For Interrupt) until the timer tick wakes a
// thread. The 4ms timer ISR fires during WFI, processes the deadline queue
// (waking threads whose nanosleep timeouts have expired, e.g. sysmon), and
// we re-check the ready queue.
//
// This is the equivalent of Linux's idle task. On Linux, futex_wait removes
// the thread from the run queue and the CPU runs the idle task until an
// interrupt (timer, device) makes a thread runnable. Here, we do the same
// thing inline: block the caller, WFI, process deadlines, check for ready
// threads.
//
// Without this, the last thread would spin on EAGAIN from futex_wait. The
// tight spin prevents QEMU's IO thread from delivering timer interrupts,
// so deadline-based wakeups (nanosleep, timed futex) never fire.
//
// Must be called with schedulerLock held.
// Returns with schedulerLock held.
//
//go:nosplit
func idleWaitForReadyThread(sf *SchedulerFunc, savedDAIF uint64, callerSID ShepherdId) (*Thread, uint64) {
	for {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		EnableIRQs()
		WaitForInterrupt()
		savedDAIF = sf.DisableAndSaveDAIF()
		schedulerLock.Lock()
		processStaticDeadlinesSchedLockHeld()
		var next *Thread
		if callerSID > 0 {
			next = findReadyUserspaceThreadSchedLockHeld(-1)
			if next == nil {
				next = findReadyThreadSchedLockHeld()
			}
		} else {
			next = findReadyThreadPreferDifferentShepherdSchedLockHeld(callerSID)
		}
		if next != nil {
			return next, savedDAIF
		}
	}
}

// threadBlockFutexImpl is the internal implementation with sf for testing
//
//go:nosplit
func threadBlockFutexImpl(sf *SchedulerFunc, futexAddr uint64, expectedVal uint32) uintptr {
	t := GetCurrentThread()
	if t == nil {
		return 0
	}

	// BEGIN CRITICAL SECTION - protect thread state modification
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Re-check the futex value under the lock to prevent missed wakeup race.
	// If the value changed since the caller checked, a wake may have occurred.
	// Use safe accessor — direct dereference fails on RISC-V (SUM=0).
	currentVal, ok := kmem.ReadUserUint32(uintptr(futexAddr))
	if !ok {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}
	if currentVal != expectedVal {
		// Value changed - a wake likely happened, don't block
		if sf.StateCheck != nil {
			sf.StateCheck("futex-block-value-changed")
		}
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Pluck current thread from ready queue if it's there
	// (It might not be if it was already running and not re-queued)
	pluckFromAllQueues(t.TID)

	// Find next ready thread. If current thread is userspace (PID > 0),
	// pick the next userspace thread in FIFO order (pass PID -1 to disable
	// shepherd preference — see timer preemption comment for rationale).
	// Fall back to any thread (including kernel thread 0).
	// NOTE: Use PID > 0 (not PageTableL0PA != 0) — see checkThreadPreemptionImpl.
	var next *Thread
	if t.PID > 0 {
		next = findReadyUserspaceThreadSchedLockHeld(-1)
		if next == nil {
			next = findReadyThreadSchedLockHeld()
		}
	} else {
		next = findReadyThreadPreferDifferentShepherdSchedLockHeld(t.PID)
	}
	if next == nil {
		// No ready thread — process nanosleep/futex deadlines while we hold
		// the scheduler lock. This can wake sleeping threads (e.g. sysmon)
		// immediately, avoiding a round-trip through WFI.
		processStaticDeadlinesSchedLockHeld()
		if t.PID > 0 {
			next = findReadyUserspaceThreadSchedLockHeld(-1)
			if next == nil {
				next = findReadyThreadSchedLockHeld()
			}
		} else {
			next = findReadyThreadPreferDifferentShepherdSchedLockHeld(t.PID)
		}
	}
	// Block unconditionally — match Linux futex_wait behavior.
	// The thread blocks and the scheduler picks the next ready thread
	// regardless of PID. Timer preemption ensures the lock holder
	// (which was preempted into the ready queue) eventually runs,
	// releases the lock, and calls futex_wake to wake us.
	t.State = ThreadBlockedFutex
	t.FutexAddr = futexAddr
	blockedQueue.PushNoDuplicate(t.TID)

	if next == nil {
		if sf.StateCheck != nil {
			sf.StateCheck("futex-block-no-next")
		}
		next, savedDAIF = idleWaitForReadyThread(sf, savedDAIF, t.PID)
	}

	if sf.StateCheck != nil {
		sf.StateCheck("futex-block-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	// Return context pointer for context switch to the next thread
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

	// Get caller's PID to scope futex matching — each shepherd has its own
	// address space, so the same VA in different shepherds is a different futex.
	callerSID := ShepherdId(-1)
	if caller := GetCurrentThread(); caller != nil {
		callerSID = caller.PID
	}

	woken := int32(0)
	queueSize := blockedQueue.Size()

	// Scan blocked queue
	for i := 0; i < queueSize && woken < int32(maxWake); i++ {
		tid := blockedQueue.Pop()
		t := threadLookupByTID(int32(tid)) // FindByIdAll to include kernel threads

		if t == nil {
			// Invalid TID in queue - skip it
			console.KPrintf("ThreadWakeFutex: invalid TID %d in blockedQueue\n", tid)
			continue
		}

		if t.FutexAddr == futexAddr && t.PID == callerSID {
			// Move to ready
			t.State = ThreadReady
			t.FutexAddr = 0
			// Pluck first to prevent duplicates
			pluckFromAllQueues(tid)
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

// ThreadBlockSleep marks current thread as sleeping and context-switches
// to the next ready thread. The caller (SyscallNanosleep) must have already
// added a deadline to the static queue before calling this.
//
// The thread is unconditionally marked sleeping. If no ready thread exists,
// idleWaitForReadyThread WFI-loops until the timer ISR wakes a thread
// (typically via the caller's own nanosleep deadline or sysmon's).
//
// Returns CONTEXT POINTER of next thread to run (always non-zero).
//
//go:nosplit
func ThreadBlockSleep(sf *SchedulerFunc) uintptr {
	t := GetCurrentThread()
	if t == nil {
		return 0
	}

	// BEGIN CRITICAL SECTION - protect thread state modification
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Pluck current thread from ready queue if it's there
	// (It might not be if it was already running and not re-queued)
	pluckFromAllQueues(t.TID)

	// Find next ready thread. If the current thread is userspace (PID > 0),
	// prefer userspace threads first, then fall back to any thread (including
	// kernel thread 0). The SVC return path handles EL0→EL1 transitions
	// correctly via DoContextSwitch + SPSR in the target ThreadContext.
	// NOTE: Use PID > 0 (not PageTableL0PA != 0) — see checkThreadPreemptionImpl.
	var next *Thread
	if t.PID > 0 {
		next = findReadyUserspaceThreadSchedLockHeld(-1)
		if next == nil {
			next = findReadyThreadSchedLockHeld()
		}
	} else {
		next = findReadyThreadPreferDifferentShepherdSchedLockHeld(t.PID)
	}
	if next == nil {
		// No ready thread — process nanosleep/futex deadlines while we hold
		// the scheduler lock. This can wake sleeping threads (e.g. sysmon)
		// immediately, avoiding a round-trip through WFI.
		processStaticDeadlinesSchedLockHeld()
		if t.PID > 0 {
			next = findReadyUserspaceThreadSchedLockHeld(-1)
			if next == nil {
				next = findReadyThreadSchedLockHeld()
			}
		} else {
			next = findReadyThreadPreferDifferentShepherdSchedLockHeld(t.PID)
		}
	}
	// Block unconditionally — the caller has already added a deadline.
	// Mark sleeping so the timer ISR can process the deadline and wake us.
	t.State = ThreadSleeping
	sleepingQueue.PushNoDuplicate(t.TID)

	if next == nil {
		if sf.StateCheck != nil {
			sf.StateCheck("sleep-block-no-next")
		}
		next, savedDAIF = idleWaitForReadyThread(sf, savedDAIF, t.PID)
	}


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
	t := threadLookupByTID(int32(tid))
	if t == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return
	}

	if t.State == ThreadSleeping {
		t.State = ThreadReady
		// Pluck first to prevent duplicates
		pluckFromAllQueues(ThreadId(tid))
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

// GetCurrentThreadTID returns the TID of the current thread.
// Returns -1 if no thread is running.
//
//go:nosplit
func GetCurrentThreadTID() ThreadId {
	t := GetCurrentThread()
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
//go:nosplit
func checkThreadPreemptionInternal(framePtr uint64) uint64 {
	return checkThreadPreemptionImpl(&NormalSchedulerFunc, framePtr)
}

// checkThreadPreemptionImpl checks if the current thread should be preempted
// and performs the context switch if needed.
// tryPickupWorkIdleCPU is called when an idle CPU (no CurrentThread) receives a timer interrupt.
// It checks if there's work available on the local queue (or steals from other CPUs) and
// starts running the first available thread.
// Returns: context pointer to switch to, or 0 if no work available.
//
// CRITICAL for SMP: Secondary CPUs start idle and enter WFI. When timer interrupts wake them,
// they need this function to pick up work that may have been enqueued to their local queues.
//
//go:nosplit
func tryPickupWorkIdleCPU(sf *SchedulerFunc) uint64 {
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Try to find work (local queue first, then steal)
	next := findReadyThreadWithStealing()
	if next == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0 // No work, return to idle (WFI)
	}

	// Found work! Set up this thread to run on this CPU
	pluckFromAllQueues(next.TID)
	next.State = ThreadRunning
	next.HomeCPU = int8(GetCPUID()) // Bind to this CPU

	currentTime := sf.CurrentTime(0)
	next.StartTick = currentTime
	next.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
	next.TicksStartedRunning = currentTime

	// Start shepherd clock if needed
	if next.ShepherdIdx >= 0 {
		shepherd := shepherdList.Get(int(next.ShepherdIdx))
		if shepherd != nil && shepherd.TicksStartedRunning == 0 {
			shepherd.TicksStartedRunning = currentTime
		}
	}

	// Set as current thread (updates both per-CPU and global)
	SetCurrentThreadGlobal(next)

	// Memory barrier to ensure visibility to other CPUs
	asm.Dsb()

	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	if next.Context.GetPC() == 0 {
		serial.RawUARTPuts("[BUG] Pickup RIP=0 TID=")
		serial.RawUARTHex64(uint64(next.TID))
		serial.PollWrite('\n')
		for {
			WaitForInterrupt()
		}
	}

	// NOTE: Signal delivery moved to caller (checkThreadPreemptionImpl)
	// to reduce nosplit stack depth. tryPickupWorkIdleCPU adds 80 bytes
	// to the chain which pushes BuildSignalFrame's page walks over limit.

	return uint64(uintptr(unsafe.Pointer(&next.Context)))
}

// Called from timer IRQ handler via ABI stub when NeedsThreadPreempt is set.
//
// framePtr: pointer to exception frame with saved registers
// Returns: pointer to new ThreadContext if switch happened, 0 otherwise
//
// hasPendingKernelWork returns true if any LoadMaz/RunMaz/RunShepherd work
// is waiting to be dispatched by thread 0.
//
//go:nosplit
func hasPendingKernelWork() bool {
	return atomic.LoadUint32(&loadMazPending) != 0 ||
		atomic.LoadUint32(&runMazPending) != 0 ||
		atomic.LoadUint32(&runShepherdPending) != 0
}

// Thread0HasPendingWork returns true if the current thread is thread 0
// AND there is pending kernel dispatch work (LoadMaz/RunMaz/RunShepherd).
// Used by SyscallSchedYield to skip OS-level thread switches so that
// Go's internal goroutine scheduler can reach the dispatcher goroutine.
//
//go:nosplit
func Thread0HasPendingWork() bool {
	t := GetCurrentThread()
	if t == nil || t.TID != 0 {
		return false
	}
	return hasPendingKernelWork()
}

// boostThread0ForPendingWork forces a context switch to thread 0 when
// kernel dispatch work is pending. Returns thread 0's context pointer
// on success, or 0 if thread 0 is not in the ready queue.
//
//go:nosplit
func boostThread0ForPendingWork(sf *SchedulerFunc, oldThread *Thread, framePtr uint64) uint64 {
	atomic.AddUint64(&dbgBoostAttempt, 1)
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()
	thread0 := threadLookupByTID(0)
	if thread0 != nil && thread0.State == ThreadReady {
		atomic.AddUint64(&dbgBoostSuccess, 1)
		// Save preempted thread's context and enqueue it
		SaveContextFromFrame(uintptr(framePtr))
		oldThread.State = ThreadReady
		pluckFromAllQueues(oldThread.TID)
		enqueueReadySchedLockHeld(oldThread)
		oldThread.PreemptElapsed = 0

		currentTime := sf.CurrentTime(0)

		// Stop old thread's runtime clock
		if oldThread.TicksStartedRunning != 0 {
			oldThread.TotalTicksRunning += currentTime - oldThread.TicksStartedRunning
		}
		oldThread.TicksStartedRunning = 0

		// Pluck thread 0 and make it the active thread
		pluckFromAllQueues(thread0.TID)
		SetCurrentThreadGlobal(thread0)
		thread0.State = ThreadRunning
		thread0.StartTick = currentTime
		thread0.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
		thread0.PreemptElapsed = 0
		thread0.TicksStartedRunning = currentTime

		// Switch page table if needed (userspace → kernel)
		if thread0.PageTableL0PA != 0 && thread0.PageTableL0PA != oldThread.PageTableL0PA {
			kmem.SwitchTTBR0WithASID(thread0.PageTableL0PA, uint16(thread0.PID))
		}

		schedulerLock.Unlock()
		// Don't restore DAIF — ERET will restore from the saved context
		return uint64(uintptr(unsafe.Pointer(&thread0.Context)))
	}
	if thread0 != nil {
		atomic.StoreUint64(&dbgBoostFailState, uint64(thread0.State))
	}
	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)
	return 0
}

// CRITICAL: This is called from IRQ context after EOIR, with g switched to kmazarin's g0.
// The exception frame contains the interrupted thread's complete state.
//
//go:nosplit
//go:noinline
func checkThreadPreemptionImpl(sf *SchedulerFunc, framePtr uint64) uint64 {
	// Timed shutdown: after shutdownTicksThreshold raw ticks, print stats and exit
	if startingTicksProgram != 0 && shutdownTicksThreshold != 0 {
		now := kirq.ReadCounterValue()
		if now-startingTicksProgram >= shutdownTicksThreshold {
			// Clear threshold to prevent re-entrant Exit() calls
			// (Exit re-enables IRQs briefly for WFI, which can re-enter this path)
			shutdownTicksThreshold = 0
			Exit()
		}
	}

	oldThread := GetCurrentThread()
	if oldThread == nil {
		// Idle CPU (no current thread) - check if there's work to pick up
		// This is critical for SMP: secondary CPUs start idle and need to
		// pick up work from their local queues when timer interrupts wake them.
		ctxPtr := tryPickupWorkIdleCPU(sf)
		if ctxPtr != 0 {
			// Deliver pending signals at this stack depth (shallower than inside
			// tryPickupWorkIdleCPU) to stay within nosplit stack budget.
			next := GetCurrentThread()
			if next != nil && next.PendingSignals != 0 && next.InSignalHandler == 0 {
				DeliverPendingSignal(next)
			}
		}
		return ctxPtr
	}

	// Don't preempt thread 0 while it's dispatching LoadMaz/RunMaz/RunShepherd work.
	// Without this, the userspace-preferring scheduler starves thread 0
	// after completing one request, preventing subsequent requests from
	// being dispatched.
	if oldThread.TID == 0 && (atomic.LoadUint32(&loadMazDispatching) != 0 ||
		atomic.LoadUint32(&runMazDispatching) != 0 ||
		atomic.LoadUint32(&runShepherdDispatching) != 0) {
		return 0
	}

	// Boost thread 0 when kernel work is pending. The userspace-preferring
	// scheduler never picks thread 0 while any userspace thread is ready,
	// so pending LoadMaz/RunMaz/RunShepherd requests can stall indefinitely.
	// When a pending flag is set and we're preempting a non-thread-0 thread,
	// force a switch to thread 0 so it can dispatch the work.
	if oldThread.TID != 0 && hasPendingKernelWork() {
		if ctxPtr := boostThread0ForPendingWork(sf, oldThread, framePtr); ctxPtr != 0 {
			return ctxPtr
		}
	}

	// BEGIN CRITICAL SECTION - protect thread state modifications
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	// Check if the thread made progress since last preemption/schedule.
	// Compare exception frame PC (where timer interrupted) with saved context PC
	// (where thread was scheduled to resume). Same PC = zero progress.
	frame := (*[40]uint64)(unsafe.Pointer(uintptr(framePtr)))
	if frame[excFramePCIndex] == oldThread.Context.GetPC() {
		if oldThread.TID == 0 {
			atomic.AddUint64(&dbgZeroProgressT0Count, 1)
		} else {
			atomic.AddUint64(&dbgZeroProgressCount, 1)
			// Store last zero-progress details for periodic diagnostic dump
			atomic.StoreUint64(&dbgZPLastTID, uint64(uint16(oldThread.TID)))
			atomic.StoreUint64(&dbgZPLastPC, frame[excFramePCIndex])
			atomic.StoreUint64(&dbgZPLastPS, frame[excFramePSIndex])
		}
	}

	// Save current thread's context from exception frame
	SaveContextFromFrame(uintptr(framePtr))

	// DEBUG: detect kernel-address PC saved for a userspace thread.
	// This would mean preemption happened while the thread was inside
	// a syscall handler, which svcDepth should have prevented.
	// Store in globals (zero nosplit overhead) — printed by EVT periodic dump.
	if oldThread.PID > 0 && frame[excFramePCIndex] >= 0xFFFFFFFF00000000 {
		atomic.StoreUint64(&dbgBadPC, frame[excFramePCIndex])
		atomic.StoreUint64(&dbgBadPS, frame[excFramePSIndex])
		atomic.StoreUint64(&dbgBadTID, uint64(uint16(oldThread.TID)))
		atomic.AddUint64(&dbgBadPCCount, 1)
	}

	oldThread.State = ThreadReady

	// Thread 0 is the idle thread — NOT enqueued. It runs opportunistically
	// between preemptions of other threads. All other threads go to TAIL.
	if oldThread.TID != 0 {
		pluckFromAllQueues(oldThread.TID)
		GetPerCPU().LocalReadyQueue.PushNoDuplicate(oldThread.TID) // TAIL
	} else {
		pluckFromAllQueues(oldThread.TID) // safety: ensure not in queue
	}

	// Reset preemption tracking for the preempted thread
	oldThread.PreemptElapsed = 0

	// Update runtime accounting for preempted thread
	currentTime := sf.CurrentTime(0)
	if oldThread.TicksStartedRunning != 0 {
		oldThread.TotalTicksRunning += currentTime - oldThread.TicksStartedRunning
	}
	oldThread.TicksStartedRunning = 0 // Mark as not running

	// Opportunistic idle thread scheduling:
	// When preempting a non-idle thread, switch to thread 0 first so it can
	// do one idle loop iteration (IRQ bridging, dispatch, deadlines). Thread 0
	// runs for microseconds then yields to the next ready thread.
	// Sequence: ... → T0 → A (100ms) → T0 → B (100ms) → T0 → ...
	// Two extra context switches per quantum, negligible vs 100ms tick.
	var next *Thread
	if oldThread.TID != 0 {
		// Non-idle thread preempted: run thread 0 if available
		thread0 := threadLookupByTID(0)
		if thread0 != nil && thread0.State == ThreadReady {
			next = thread0
		} else {
			next = findReadyThreadSchedLockHeld()
		}
	} else {
		// Thread 0 preempted: pick next real thread from queue
		next = findReadyThreadSchedLockHeld()
	}

	if next == nil {
		// No ready thread — continue current
		oldThread.State = ThreadRunning
		oldThread.StartTick = currentTime
		oldThread.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
		oldThread.TicksStartedRunning = currentTime
		atomic.AddUint64(&dbgPreemptNoNextCount, 1)
		if sf.StateCheck != nil {
			sf.StateCheck("preempt-no-next")
		}
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	n := atomic.AddUint64(&dbgPreemptSwitchCount, 1)
	if n <= 5 {
		serial.RawUARTPuts("[P:")
		serial.RawUARTDecimal(uint64(uint16(oldThread.TID)))
		serial.RawUART('>')
		serial.RawUARTDecimal(uint64(uint16(next.TID)))
		serial.RawUART(']')
	}
	// Shepherd-level tick accounting
	// Stop old shepherd's clock
	if oldThread.ShepherdIdx >= 0 {
		oldShepherd := shepherdList.Get(int(oldThread.ShepherdIdx))
		if oldShepherd != nil && oldShepherd.TicksStartedRunning != 0 {
			oldShepherd.TotalTicksRunning += currentTime - oldShepherd.TicksStartedRunning
			// Only clear shepherd clock if switching to a DIFFERENT shepherd
			if next.PID != oldThread.PID {
				oldShepherd.TicksStartedRunning = 0
			}
		}
	}

	// Start new shepherd's clock (only if different shepherd)
	if next.ShepherdIdx >= 0 && next.PID != oldThread.PID {
		newShepherd := shepherdList.Get(int(next.ShepherdIdx))
		if newShepherd != nil {
			newShepherd.TicksStartedRunning = currentTime
		}
	}

	// Switch to new thread (updates both per-CPU and global)
	SetCurrentThreadGlobal(next)
	next.State = ThreadRunning
	next.StartTick = currentTime
	next.ThreadPreemptDeadline = currentTime + kirq.ThreadPreemptTicks
	next.PreemptElapsed = 0 // Fresh time slice
	next.TicksStartedRunning = currentTime // Mark when started running for accounting

	// CRITICAL: Switch TTBR0 if switching to a userspace thread with different page table
	// Without this, userspace threads run with wrong page table and crash!
	// Use the thread's PID as the ASID for TLB tagging - this allows TLB entries
	// from different processes to coexist without requiring flushes on every switch.
	if next.PageTableL0PA != 0 && next.PageTableL0PA != oldThread.PageTableL0PA {
		kmem.SwitchTTBR0WithASID(next.PageTableL0PA, uint16(next.PID))
	}

	// Note: No debug output in preemption path - too deep in nosplit call chain.
	// Debug output happens in other scheduler paths (futex wake, thread exit).
	next.StolenFromCPU = -1 // Clear stolen flag (no debug print possible here)

	if sf.StateCheck != nil {
		sf.StateCheck("preempt-switch-complete")
	}

	// END CRITICAL SECTION
	schedulerLock.Unlock()
	// CRITICAL: Do NOT restore DAIF here!
	// CurrentThread now points to the NEW thread. If we enable interrupts
	// and a timer IRQ fires, SaveContextFromFrame would save the KERNEL's
	// exception frame into the NEW thread's context, corrupting it.
	// The assembly will restore interrupt state via ERET with new SPSR.
	_ = savedDAIF // Keep compiler happy

	if next.Context.GetPC() == 0 {
		serial.RawUARTPuts("[BUG] Preempt RIP=0 TID=")
		serial.RawUARTHex64(uint64(next.TID))
		serial.PollWrite('\n')
		for {
			WaitForInterrupt()
		}
	}

	// Deliver pending signals before ERET to this thread.
	if next.PendingSignals != 0 && next.InSignalHandler == 0 {
		DeliverPendingSignal(next)
	}

	return uint64(uintptr(unsafe.Pointer(&next.Context)))
}

// Exception frame offsets (must match exceptions.s)

// SaveContextFromFrame is defined in save_context_<arch>.go (per-architecture).
// doContextSwitchABI0 is defined in save_context_<arch>.go (per-architecture).

// doContextSwitchImpl performs a context switch from current thread to target
//
// CRITICAL SECTION: This function modifies CurrentThread and thread state.
// Protected by both DAIF and schedulerLock.
//
//go:nosplit
//go:noinline
func doContextSwitchImpl(sf *SchedulerFunc, framePtr uintptr, targetIdx int32) *ThreadContext {
	// Timed shutdown: after shutdownTicksThreshold raw ticks, print stats and exit
	if startingTicksProgram != 0 && shutdownTicksThreshold != 0 {
		now := kirq.ReadCounterValue()
		if now-startingTicksProgram >= shutdownTicksThreshold {
			shutdownTicksThreshold = 0
			Exit()
		}
	}

	// Save current thread's context
	SaveContextFromFrame(framePtr)

	// BEGIN CRITICAL SECTION - protect thread state modifications
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	oldThread := GetCurrentThread()
	newThread := threadList.Get(int(targetIdx)) // Get() for reserved slots

	// Clone child register inheritance: copy parent's full register state to the
	// clone child, preserving only the clone-specific overrides (X0=0, X28=new g,
	// SP=new stack, SPSR with IRQs enabled). Without this, the child starts with
	// all registers zeroed except those 5 fields, causing crashes when clone.abi0
	// child code uses inherited callee-saved registers (e.g., X20 for memclr).
	if newThread != nil && newThread.CloneNeedsParentRegs != 0 && oldThread != nil {
		// Save the clone-specific overrides that SetupForCloneChild already set
		childRetVal := newThread.Context.GetReturnValue() // 0 (child TID)
		childSP := newThread.Context.GetSP()              // new stack
		childGReg := newThread.Context.GetGRegister()     // new g pointer
		childPState := newThread.Context.GetProcessorState() // parent state with IRQs enabled
		// Copy ALL registers from parent (just saved by SaveContextFromFrame)
		newThread.Context = oldThread.Context
		// Restore clone-specific overrides
		newThread.Context.SetReturnValue(childRetVal)
		newThread.Context.SetSP(childSP)
		// Only override g register if it was explicitly provided (non-zero).
		// For the kernel overlay (INT $0x80), gp is passed as a syscall arg.
		// For userspace SYSCALL (x86_64), gp is NOT in the syscall args — the
		// Go runtime loads R14=gp before SYSCALL, and it survives in the parent's
		// saved register state. Keeping the parent's R14 gives the child the
		// correct goroutine pointer.
		if childGReg != 0 {
			newThread.Context.SetGRegister(childGReg)
		}
		newThread.Context.SetProcessorState(childPState)

		// CLONE_SETTLS (AMD64): set the child's FS_BASE from the parent's saved
		// clone TLS value. On Linux, clone with CLONE_SETTLS sets the child's
		// FS_BASE before it runs, so the child writes its g pointer to its OWN
		// TLS area (FS:-8), not the parent's. Without this, the child corrupts
		// the parent's g pointer in TLS, leading to GC crashes (#GP).
		if oldThread.SyscallCloneTLS != 0 {
			newThread.Context.SetCloneTLS(oldThread.SyscallCloneTLS)
			oldThread.SyscallCloneTLS = 0 // consumed
		}

		newThread.CloneNeedsParentRegs = 0
	}

	// Update both per-CPU and global CurrentThread
	SetCurrentThreadGlobal(newThread)

	// Get current time for preemption tracking
	currentTime := sf.CurrentTime(0)

	// Target thread is now running (it was already popped from ready queue
	// by the caller, so no need to remove it here)

	// Save preemption tracking state for old thread before switching.
	// Runtime tick accounting is updated unconditionally (regardless of state)
	// because the caller may have already changed the state (e.g., futex sets
	// ThreadBlockedFutex before calling context switch). If we only update when
	// ThreadRunning, blocked threads keep a stale TicksStartedRunning and the
	// print function adds phantom time.
	oldPID := ShepherdId(-1)
	if oldThread != nil {
		oldPID = oldThread.PID

		// Always accumulate ticks and clear TicksStartedRunning
		if oldThread.TicksStartedRunning != 0 {
			oldThread.TotalTicksRunning += currentTime - oldThread.TicksStartedRunning
		}
		oldThread.TicksStartedRunning = 0 // Mark as not running

		if oldThread.State == ThreadRunning {
			// Save elapsed time so we can restore it when this thread resumes
			oldThread.PreemptElapsed = currentTime - oldThread.StartTick

			oldThread.State = ThreadReady
					// Pluck first in case oldThread is already in queue
			pluckFromAllQueues(oldThread.TID)
			enqueueReadySchedLockHeld(oldThread)
		}

		// Shepherd-level tick accounting: stop old shepherd's clock if switching to a different shepherd
		if newThread != nil && newThread.PID != oldPID && oldThread.ShepherdIdx >= 0 {
			oldShepherd := shepherdList.Get(int(oldThread.ShepherdIdx))
			if oldShepherd != nil && oldShepherd.TicksStartedRunning != 0 {
				oldShepherd.TotalTicksRunning += currentTime - oldShepherd.TicksStartedRunning
				oldShepherd.TicksStartedRunning = 0
			}
		}
	}

	// New thread becomes running
	if newThread == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
		return nil
	}
	// (SVC switch breadcrumbs removed for performance)
	newThread.State = ThreadRunning
	// Restore StartTick from saved elapsed time so preemption tracking
	// continues from where it left off.
	newThread.StartTick = currentTime - newThread.PreemptElapsed
	// Calculate remaining time for deadline (threshold - elapsed = remaining)
	// If elapsed >= threshold, deadline will be at or before currentTime (immediate preemption)
	if newThread.PreemptElapsed < kirq.ThreadPreemptTicks {
		newThread.ThreadPreemptDeadline = currentTime + (kirq.ThreadPreemptTicks - newThread.PreemptElapsed)
	} else {
		newThread.ThreadPreemptDeadline = currentTime // Preempt immediately on next tick
	}

	// Mark when this thread started running for runtime accounting
	newThread.TicksStartedRunning = currentTime

	// Start new shepherd's clock if switching to a different shepherd
	if newThread.PID != oldPID && newThread.ShepherdIdx >= 0 {
		newShepherd := shepherdList.Get(int(newThread.ShepherdIdx))
		if newShepherd != nil {
			newShepherd.TicksStartedRunning = currentTime
		}
	}

	// Switch TTBR0 if the new thread has a different page table
	// This is needed when switching between different userspace processes (shepherds)
	// Use the thread's PID as the ASID for TLB tagging.
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
	// CurrentThread now points to the NEW thread. If we enable interrupts
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

// SaveCurrentThreadContext is defined in save_context_<arch>.go (per-architecture).

// printTickDistributionNoSplit prints tick distribution using only nosplit
// console functions. Called from checkThreadPreemptionImpl/doContextSwitchImpl at shutdown.
// now is the current CNTVCT_EL0 value.
//
//go:nosplit
func printTickDistributionNoSplit(now uint64) {
	freq := kirq.SystemTimerFrequency

	// "\n===TICKS===\n"
	serial.RawUARTPuts("\n===TICKS===\n")

	for i := 0; i < MaxThreads; i++ {
		if threadListInUse[i] {
			t := &threadListData[i]
			ticks := t.TotalTicksRunning
			if t.TicksStartedRunning != 0 {
				if now < t.TicksStartedRunning {
					serial.PollWrite('%') // Bogus: current time < started time
				} else {
					ticks += now - t.TicksStartedRunning
				}
			} else if t.State == ThreadRunning {
				serial.PollWrite('%') // Bogus: running but no start time
			}
			// "T<TID> P<PID> <hex> <secs>s\n"
			serial.PollWrite('T')
			serial.RawUARTHex8(byte(t.TID))
			serial.PollWrite(' ')
			serial.PollWrite('P')
			serial.RawUARTHex8(byte(t.PID))
			serial.PollWrite(' ')
			serial.RawUARTHex64(ticks)
			serial.PollWrite(' ')
			if freq > 0 {
				serial.RawUARTDecimal(ticks / freq)
			} else {
				serial.PollWrite('?')
			}
			serial.PollWrite('s')
			serial.PollWrite('\n')
		}
	}

	// "===SHEPHERD===\n"
	serial.RawUARTPuts("===SHEPHERD===\n")

	for i := 0; i < MaxShepherds; i++ {
		if proc.ShepherdListInUse[i] {
			p := &proc.ShepherdListData[i]
			ticks := p.TotalTicksRunning
			if p.TicksStartedRunning != 0 {
				if now < p.TicksStartedRunning {
					serial.PollWrite('%')
				} else {
					ticks += now - p.TicksStartedRunning
				}
			}
			// "P<PID> <hex> <secs>s\n"
			serial.PollWrite('P')
			serial.RawUARTHex8(byte(p.PID))
			serial.PollWrite(' ')
			serial.RawUARTHex64(ticks)
			serial.PollWrite(' ')
			if freq > 0 {
				serial.RawUARTDecimal(ticks / freq)
			} else {
				serial.PollWrite('?')
			}
			serial.PollWrite('s')
			serial.PollWrite('\n')
		}
	}

	// "===TOTAL===\n"
	serial.RawUARTPuts("===TOTAL===\n")
	if startingTicksProgram != 0 && now >= startingTicksProgram && freq > 0 {
		serial.RawUARTDecimal((now - startingTicksProgram) / freq)
	} else {
		serial.PollWrite('?')
	}
	serial.RawUARTPuts("s\n")

	// Brief spin for UART FIFO drain (main flush is in Exit() assembly)
	for i := uint64(0); i < 10000000; i++ {
		_ = i
	}
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
				ticks += kirq.ReadCounterValue() - t.TicksStartedRunning
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
				ticks += kirq.ReadCounterValue() - t.TicksStartedRunning
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
			case ThreadBlockedDelegate:
				stateStr = "DLG"
			case ThreadBlockedDelegateRecv:
				stateStr = "DLR"
			case ThreadBlockedDirtyNotify:
				stateStr = "DNT"
			case ThreadBlockedInputEvent:
				stateStr = "INP"
			}

			console.KPrintf("  T%02d P%02d [%s] ticks=%d (%d%%)\n",
				t.TID, t.PID, stateStr, ticks, pct)
		}
	}
	console.KPrint("================================\n")

	PrintThreadStateSummary()

	// Per-shepherd tick distribution
	console.KPrint("\n=== Shepherd Tick Distribution ===\n")
	var shepherdTotalTicks uint64
	for i := 0; i < MaxShepherds; i++ {
		if proc.ShepherdListInUse[i] {
			p := &proc.ShepherdListData[i]
			ticks := p.TotalTicksRunning
			if p.TicksStartedRunning != 0 {
				ticks += kirq.TimerIRQCount - p.TicksStartedRunning
			}
			shepherdTotalTicks += ticks
		}
	}
	for i := 0; i < MaxShepherds; i++ {
		if proc.ShepherdListInUse[i] {
			p := &proc.ShepherdListData[i]
			ticks := p.TotalTicksRunning
			if p.TicksStartedRunning != 0 {
				ticks += kirq.TimerIRQCount - p.TicksStartedRunning
			}
			var pct uint64
			if shepherdTotalTicks > 0 {
				pct = (ticks * 100) / shepherdTotalTicks
			}
			console.KPrintf("  P%02d ticks=%d (%d%%)\n", p.PID, ticks, pct)
		}
	}
	console.KPrint("================================\n")
}

// PrintThreadStateSummary prints a compact one-line summary of thread states.
func PrintThreadStateSummary() {
	var run, rdy, ftx, irq, slp, total int
	for i := 0; i < MaxThreads; i++ {
		if threadListInUse[i] {
			total++
			switch threadListData[i].State {
			case ThreadRunning:
				run++
			case ThreadReady:
				rdy++
			case ThreadBlockedFutex:
				ftx++
			case ThreadBlockedSoftIRQ:
				irq++
			case ThreadSleeping:
				slp++
			}
		}
	}
	avail := threadIdAllocator.Available()
	console.KPrintf("[Threads] total=%d run=%d rdy=%d ftx=%d irq=%d slp=%d free=%d\n",
		total, run, rdy, ftx, irq, slp, avail)
}
