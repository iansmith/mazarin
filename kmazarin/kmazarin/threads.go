package main

import (
	"fmt"
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/ds"
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/ksyscall"
	"mazzy/kmazarin/ktime"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/util"
	"mazzy/shared/constants"
	"mazzy/shared/ipc"
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

// smpDebugPrintRun prints "R<cpu> <tid> <pid>" when a thread starts running.
func smpDebugPrintRun(cpuID uint64, tid ThreadId, pid ShepherdId) {
	if !SMPDebugEnabled {
		return
	}
	klog.Logf("R%d %02x %02x\n", cpuID, uint8(tid), uint8(pid))
}

// smpDebugPrintSteal prints "S<cpu> <tid> <from>" when work is stolen.
func smpDebugPrintSteal(thisCPU uint64, tid ThreadId, victimCPU uint64) {
	if !SMPDebugEnabled {
		return
	}
	klog.Logf("S%d %02x %d\n", thisCPU, uint8(tid), victimCPU)
}

// smpDebugPrintIRQ prints "I<cpu> <irq>" when an IRQ fires.
func smpDebugPrintIRQ(cpuID uint64, irqNum uint32) {
	if !SMPDebugEnabled {
		return
	}
	klog.Logf("I%d %04x\n", cpuID, irqNum)
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

// topHalfTimeUpdateHook is called from ProcessDeadlinesTopHalf via function
// pointer (indirect call) so the nosplit checker doesn't trace the
// TopHalfTickTimeUpdate → dirtyPropagate chain against ExceptionVectorTable's
// budget. Set by SetupTopHalfTimeUpdate after kernel attrs are published.
var topHalfTimeUpdateHook func()

// topHalfTimeUpdateAndFlush updates time attributes and wakes any
// threads blocked on WaitDirty. Must be called with schedulerLock held.
//
//go:nosplit
//go:noinline
func topHalfTimeUpdateAndFlush() {
	ksyscall.TopHalfTickTimeUpdate()
	ksyscall.FlushPendingDirtyWakesSchedLockHeld()
}

// SetupTopHalfTimeUpdate installs the time update hook for the timer top-half.
// Called from main after StartKernelAttrUpdaters.
func SetupTopHalfTimeUpdate() {
	topHalfTimeUpdateHook = topHalfTimeUpdateAndFlush
}

// topHalfGCWakeHook is called from ProcessDeadlinesTopHalf via indirect
// function pointer to wake sleeping kernel threads when GC needs STW.
// Uses the same nosplit-budget pattern as topHalfTimeUpdateHook.
// Set by InitTopHalfGCWake after thread system is initialized.
var topHalfGCWakeHook func()

// topHalfGCWakeImpl wakes all sleeping kernel threads (TIDs 0-7) when
// the Go runtime's GC is waiting to stop the world. sysmon sleeps for
// 10s to minimize SVC overhead; this tick-driven wake bounds GC STW
// latency to 4ms (one tick period). Must be called with schedulerLock held.
//
//go:nosplit
//go:noinline
func topHalfGCWakeImpl() {
	if !kmazarinGCWaiting() {
		return
	}
	for i := 0; i < ReservedKernelThreads; i++ {
		t := threadList.ReservedGet(i)
		if t != nil && t.State == ThreadSleeping {
			t.State = ThreadReady
			sleepingQueue.Pluck(t.TID)
			enqueueReadySchedLockHeld(t)
		}
	}
}

// deliverSignalHook calls DeliverPendingSignal via an indirect function
// pointer so the nosplit checker cannot trace through the call. This breaks
// the ExceptionVectorTable → checkThreadPreemptionImpl → DeliverPendingSignal
// → BuildSignalFrame → ZeroUserMemoryWithL0 → WalkUserPTLean chain that
// otherwise exceeds the 792-byte nosplit stack limit by 8 bytes.
var deliverSignalHook func(t *Thread)

// tryPickupWorkHook calls tryPickupWorkIdleCPU via an indirect function
// pointer to break the nosplit chain through findReadyThreadWithStealing →
// stealWorkFromOtherCPUs → StaticQueue.PopBack → panicIndex.
var tryPickupWorkHook func(sf *SchedulerFunc) uint64

// InitTopHalfGCWake installs the GC wake hook for the timer top-half.
// Called from InitThreads after the thread system is ready.
func InitTopHalfGCWake() {
	topHalfGCWakeHook = topHalfGCWakeImpl
	deliverSignalHook = DeliverPendingSignal
	tryPickupWorkHook = tryPickupWorkIdleCPU
	buddyFreeHook = kmem.BuddyFreeTyped
}

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
	ThreadFree              ThreadState = 0  // Slot available
	ThreadRunning           ThreadState = 1  // Currently executing
	ThreadReady             ThreadState = 2  // Runnable, waiting to be scheduled
	ThreadBlockedFutex      ThreadState = 3  // Blocked on futex_wait
	ThreadSleeping          ThreadState = 4  // Blocked on nanosleep
	ThreadExited            ThreadState = 5  // Thread has exited (being cleaned up)
	ThreadBlockedSoftIRQ    ThreadState = 6  // Blocked waiting for soft IRQ
	ThreadBlockedKernelWork ThreadState = 7  // Blocked waiting for KernelSVCWorker to complete
	ThreadBlockedDelegate   ThreadState = 10 // Caller blocked waiting for delegated syscall reply
	// ThreadBlockedDelegateRecv (11) — removed, handlers now receive via uring
	ThreadBlockedDirtyNotify ThreadState = 12 // Blocked waiting for constraint dirty notification
	ThreadBlockedInputEvent  ThreadState = 13 // Blocked waiting for input focus event
	// ThreadBlockedMailbox (14) — removed, all IPC uses uring now
	// ThreadBlockedEpoll (15) — removed, unified into ThreadBlockedKernelWork (7)
	ThreadBlockedIOUring        ThreadState = 16 // Blocked waiting for io_uring completions
	ThreadBlockedUringRecv      ThreadState = 17 // Blocked waiting for IPC uring message
	ThreadBlockedWaitingIO      ThreadState = 18 // Blocked waiting for TX ring space (write fd 1/2)
	ThreadBlockedUringSend      ThreadState = 19 // Blocked in SyscallUringSend (target ring full); woken by drainer or 10ms deadline
	ThreadBlockedKernelRingPush ThreadState = 20 // Thread 0 blocked in pushStringFull (topHalfUartRing full); woken by consumer pop or 10ms deadline
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
	PID           ShepherdId  // Shepherd (process) ID for ASID (-1 = kernel thread)
	FutexAddr     uint64      // Address being waited on (for ThreadBlockedFutex)
	MPtr          uint64      // Pointer to Go M struct
	GPtr          uint64      // Pointer to Go g struct (g0 for this M)
	EntryFunc     uint64      // Entry function (mstart)
	PageTableL0PA uintptr     // Physical address of L0 page table (0 = use kernel's)
	Context       ThreadContext

	// Preemption tracking - deadline-based (raw tick counts, no division in timer handler)
	LastSeenG                uint64 // g pointer seen at last timer tick
	StartTick                uint64 // timer tick when this THREAD started running
	GoroutineStart           uint64 // timer tick when this GOROUTINE started running
	ThreadPreemptDeadline    uint64 // timer tick when thread preemption should occur
	GoroutinePreemptDeadline uint64 // timer tick when goroutine preemption should occur
	PreemptElapsed           uint64 // elapsed ticks saved when thread switched away
	GoroutineElapsed         uint64 // elapsed goroutine ticks saved when thread switched away
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

	// Delegate-block instrumentation: when a thread enters ThreadBlockedDelegate
	// in BlockForDelegatedSyscall, ksyscall first calls RecordDelegateBlock to
	// stash the timer tick + sysID. printEpochStatus reads these for any thread
	// still in ThreadBlockedDelegate so we can identify stuck delegated syscalls.
	// Both fields are zero when the thread is not blocked on a delegate.
	DelegateBlockSinceTick uint64 // kirq counter when blocking started (0 = not blocked)
	DelegateBlockSysID     uint16 // sysid.ID being delegated

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

	// UringSendBlockedSlotPtr points at the UringIPCSlot the thread is
	// blocked on while in ThreadBlockedUringSend. Used by the deadline
	// expiry path to clear slot.BlockedSenderTID without scanning all slots.
	// 0 when the thread is not blocked on send.
	//
	// Args 1 and 2 (msgPtr and ringIdx) are not stashed separately because
	// the SVC return path only overwrites X0 (return value); X1/X2 in
	// t.Context retain their original SVC-entry values across block/wake.
	// arg0 (targetSID) is restored from SoftIRQSlotArg via RestoreSyscallArg0.
	UringSendBlockedSlotPtr uintptr

	// UringSendDeadlineExpired is set to 1 by the deadline handler when a
	// blocked sender's 10ms timer fires before any drainer woke it. On the
	// rewind+retry, UringSendKernel checks this flag: if set, return -EAGAIN
	// immediately rather than re-blocking. Cleared when the thread enters
	// the block path or when send succeeds.
	UringSendDeadlineExpired uint32

	// WaitingIO state — used when write(fd=1/2) blocks because the PL011 TX
	// ring buffer is full. The kernel copies user data (with CR expansion) into
	// WaitingIOBuf before blocking. The TX interrupt top-half drains from this
	// buffer into the TX ring. When fully consumed, the top-half wakes the thread.
	WaitingIOBuf       []byte // kernel copy of user data (CR-expanded)
	WaitingIOOffset    int    // bytes consumed by interrupt handler so far
	WaitingIOTotal     int    // total bytes in WaitingIOBuf
	WaitingIOUserBytes int    // original user byte count (return value to caller)
	WaitingIOComplete  uint32 // atomic: 1 = interrupt handler finished draining
	WaitingIOFd        byte   // fd (1 or 2), saved for delegation after wake

	// Signal delivery state
	PendingSignals   uint64 // Bitmask of pending signals (bit N = signal N+1)
	SignalSP         uint64 // gsignal stack top (stack grows down from here)
	SignalStackBase  uint64 // gsignal stack bottom
	SignalStackSize  uint64 // gsignal stack size in bytes
	SignalUctxAddr   uint64 // Address of ucontext in current signal frame
	SignalFaultAddr  uint64 // Fault address for hardware signal (SIGSEGV, etc.)
	SignalSiCode     int32  // si_code for siginfo (e.g., SEGV_MAPERR)
	InSignalHandler  uint32 // 1 = executing signal handler, 0 = normal
	SigreturnPending uint32 // 1 = rt_sigreturn called, load Context for ERET

	// PriorityWoken: set when this thread is woken from a blocking IPC state
	// (uring recv, io_uring). The scheduler favors priority-woken threads so
	// they run before sysmon's 10ms P-retake window, allowing Go's exitsyscall
	// fast path to reacquire the P immediately (no 100ms futex safety-net delay).
	// Cleared by the scheduler when the thread is picked.
	PriorityWoken bool
}

// Thread struct field offsets for assembly access.
// These are computed from unsafe.Offsetof() in initThreadOffsets() and MUST be
// initialized before any assembly code reads them (before timer IRQ is enabled).
var (
	ThreadContextOffset          uintptr // Offset of Context field within Thread struct
	ThreadStartTickOffset        uintptr
	ThreadPreemptDeadlineOffset  uintptr
	ThreadInCloneSetupOffset     uintptr
	ThreadSigreturnPendingOffset uintptr
	ThreadInSignalHandlerOffset  uintptr
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
		t.Context.RewindToSyscall()
		t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
		t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
		enqueueReadySchedLockHeld(t)
	case ThreadBlockedKernelWork:
		// Defer signal delivery until kernel work completes — the worker
		// goroutine will wake this thread with the result.
	case ThreadBlockedDelegate:
		// Defer signal delivery until delegated syscall reply arrives.
	// ThreadBlockedDelegateRecv removed — handlers now receive via uring
	case ThreadBlockedDirtyNotify:
		t.State = ThreadReady
		t.Context.RewindToSyscall()
		t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
		t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
		enqueueReadySchedLockHeld(t)
	case ThreadBlockedInputEvent:
		t.State = ThreadReady
		t.Context.RewindToSyscall()
		t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
		t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
		enqueueReadySchedLockHeld(t)
	case ThreadBlockedUringRecv:
		t.State = ThreadReady
		t.Context.RewindToSyscall()
		t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
		t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
		enqueueReadySchedLockHeld(t)
	case ThreadBlockedIOUring:
		// Defer signal delivery until io_uring completions arrive —
		// the IRQ top-half or timeout will wake this thread.
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
var readyQueueData [threadArraySize]ThreadId    // Stores TIDs (unique thread IDs)
var readyQueueInUse [threadArraySize]bool       // Tracks holes in ready queue
var blockedQueueData [threadArraySize]ThreadId  // Stores TIDs (unique thread IDs)
var blockedQueueInUse [threadArraySize]bool     // Tracks holes in blocked queue
var sleepingQueueData [threadArraySize]ThreadId // Stores TIDs (unique thread IDs)
var sleepingQueueInUse [threadArraySize]bool    // Tracks holes in sleeping queue

// Static deadline queue backing arrays - used by timer top-half (nosplit path)
var staticDeadlineData [threadArraySize]int16
var staticDeadlineOrderBy [threadArraySize]uint64
var staticDeadlineQueue ds.StaticOrderedList

// ID allocator backing arrays - statically allocated
var threadIdStackData [threadArraySize]ThreadId            // Backing array for thread ID allocator
var shepherdIdStackData [proc.MaxShepherds]proc.ShepherdId // Backing array for shepherd ID allocator

// nextKernelThreadId is a counter for allocating kernel thread IDs (0 to ReservedKernelThreads-1).
// Kernel threads are identified by PID == 0 (the kernel shepherd) and get IDs from this counter.
// Starts at 0, incremented each time a kernel thread is created.
// Panics if exhausted (kernel has limited threads).
var nextKernelThreadId ThreadId = 0

// Data structures - will be initialized in InitThreads()
// DO NOT initialize slices here - Go's initialization order causes them to be length 0!
var threadList ds.StaticList[*Thread, Thread]                 // StaticList stores Thread VALUES, returns pointers
var shepherdList ds.StaticList[*proc.Shepherd, proc.Shepherd] // StaticList stores Shepherd VALUES, returns pointers

var readyQueue ds.StaticQueue[ThreadId]

var blockedQueue ds.StaticQueue[ThreadId]

var sleepingQueue ds.StaticQueue[ThreadId]

// thread0PendingDeadline is set by KernelBlockSleep before calling KernelYield.
// SaveThread0AndYield checks this under the scheduler lock: if nonzero, thread 0
// is put to sleep with a deadline instead of being placed on the ready queue.
// Only accessed by thread 0 (set before yield, consumed during yield), so no
// cross-thread races.
var thread0PendingDeadline uint64

// pendingYieldBlockState overrides the state SaveThread0AndYield assigns on the
// deadline path. Zero (default) means "use ThreadSleeping", which preserves the
// existing KernelBlockSleep semantics. Setting it to ThreadBlockedKernelRingPush
// (or another future block-state) causes the yielding thread to be parked in
// that state, on the static deadline queue only — the wake side handles the
// queue and state transitions. Consumed (zeroed) inside SaveThread0AndYield.
var pendingYieldBlockState ThreadState

// ID allocators - initialized in InitIdAllocators()
var threadIdAllocator ds.StaticAllocator[ThreadId]          // Manages unique thread IDs (0..MaxThreads-1)
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
	t0.TID = firstThreadId                    // Should be 0
	t0.PID = 0                                // Belongs to kernel shepherd (slot 0)
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
	InitTopHalfGCWake()
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
		} else if t.State == ThreadBlockedUringSend {
			// Sender's 10ms deadline fired before any drainer woke it.
			// Clear the per-slot blocker hook so future drainers don't
			// try to wake an already-readied thread, mark the deadline-
			// expired flag so the rewind+retry path returns -EAGAIN
			// instead of re-blocking, then ready the thread.
			if t.UringSendBlockedSlotPtr != 0 {
				slot := (*UringIPCSlot)(unsafe.Pointer(t.UringSendBlockedSlotPtr))
				if slot.BlockedSenderTID == int16(tid) {
					slot.BlockedSenderTID = -1
					slot.BlockedSenderPtr = 0
				}
				t.UringSendBlockedSlotPtr = 0
			}
			atomic.StoreUint32(&t.UringSendDeadlineExpired, 1)
			t.Context.RewindToSyscall()
			t.Context.RestoreSyscallArg0(t.SoftIRQSlotArg)
			t.Context.RestoreSyscallNum(t.SoftIRQSyscallNum)
			t.State = ThreadReady
			enqueueReadySchedLockHeld(t)
		} else if t.State == ThreadBlockedKernelRingPush {
			// Kernel pusher's 10ms deadline fired before the consumer
			// drained the topHalfUartRing. Flag the expiry so pushStringFull
			// drops the remainder + bumps softIRQDroppedBytes on resume,
			// clear the global blocker hook (so a late drain doesn't try
			// to wake an already-ready thread), and ready the thread.
			// Unlike ThreadBlockedUringSend, this is not an SVC rewind —
			// pushStringFull is regular kernel code, so YieldToReadyThread
			// returns straight to the caller after the wake.
			atomic.StoreUint32(&pushBlockerDeadlineExpired, 1)
			if int32(tid) == atomic.LoadInt32(&kernelRingPushBlockerTID) {
				atomic.StoreInt32(&kernelRingPushBlockerTID, 0)
				pushBlockerThreadPtr = 0
			}
			t.State = ThreadReady
			enqueueReadySchedLockHeld(t)
		} else {
			// Deadline fired but thread in unexpected state — dropped
		}
	}

	// io_uring timeout check is done via topHalfIOUringTimeoutHook (indirect
	// call) in ProcessDeadlinesTopHalf to stay within nosplit stack budget.
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
	// io_uring 10ms safety timeout. Called via indirect function pointer
	// (same pattern as topHalfTimeUpdateHook) so the nosplit checker cannot
	// trace it and the exception stack budget is not exceeded.
	ioFn := topHalfIOUringTimeoutHook
	if ioFn != nil {
		ioFn()
	}
	// Update time attributes and propagate dirty notifications
	// at ~10Hz (counter-based threshold). Called through function pointer
	// to keep nosplit stack budget within limits — the checker cannot trace
	// indirect calls, and our exception stack is large enough for the actual
	// chain (~550 bytes worst case vs 4KB+ stack).
	fn := topHalfTimeUpdateHook
	if fn != nil {
		fn()
	}
	// Wake sleeping kernel threads if GC needs to stop the world.
	// Called via indirect function pointer to stay within nosplit budget
	// (same pattern as topHalfIOUringTimeoutHook / topHalfTimeUpdateHook).
	gcFn := topHalfGCWakeHook
	if gcFn != nil {
		gcFn()
	}
	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

	// Post-lock work that may allocate or do channel ops.
	// Extracted to a separate function so it is NOT nosplit.
	processDeadlinesPostLock(cnt)
}

// processDeadlinesPostLock handles work after the scheduler lock is released
// in ProcessDeadlinesTopHalf. NOT nosplit — can do channel sends safely.
//
//go:noinline
func processDeadlinesPostLock(cnt uint64) {
	// Flush any pending console ring data to userspace.
	if softIRQConsole != nil {
		softIRQConsole.CheckPendingWake()
	}

	// Epoch status every ~10 seconds (~330 Hz timer → 3300 ticks).
	// Increment counter and wake channel. The bottom-half goroutine deduplicates
	// by comparing its last-seen counter value, so multiple wakeups between
	// goroutine scheduling produce only one status print.
	if cnt%3300 == 0 && cnt > 0 {
		atomic.AddUint64(&epochStatusCounter, 1)
		select {
		case epochStatusChan <- struct{}{}:
		default:
		}
	}

	// Page audit every ~30 seconds (~330 Hz timer → 9900 ticks).
	if cnt%9900 == 0 && cnt > 0 {
		select {
		case pageAuditChan <- struct{}{}:
		default:
		}
	}
}

// printEpochStatus formats a human-readable kernel status report and sends
// it via klog.Criticalf every ~10 seconds. The short tag "[status]" always
// hits the UART; the detailed breakdown goes to stderr (ring when linux is
// up, UART when not).
//
// epochStatusCounter is incremented by processDeadlinesPostLock every ~10s.
// The bottom-half goroutine compares against its local copy to deduplicate.
var epochStatusCounter uint64

// RequestEpochStatusDump asks the bottom-half goroutine to print one
// extra [status] snapshot. Wired into ksyscall.EpochStatusDumpFn so
// that .maz programs can request a dump via sys.DumpKernelStatus when
// they observe an interesting event (slow syscall, async error, etc.).
// Safe to call from any context — it just bumps a counter and pokes
// a buffered channel (drops if already pending).
//
//go:nosplit
func RequestEpochStatusDump() {
	atomic.AddUint64(&epochStatusCounter, 1)
	select {
	case epochStatusChan <- struct{}{}:
	default:
	}
}

//go:noinline
func printEpochStatus() {
	// Uptime
	var uptimeSec uint64
	freq := uint64(kirq.GetTimerFrequency())
	if freq > 0 && kernelBootTick > 0 {
		elapsed := kirq.ReadCounterValue() - kernelBootTick
		uptimeSec = elapsed / freq
	}

	// Thread state summary + collect per-stuck-delegate diagnostics.
	var nReady, nFutex, nSleep, nSoftIRQ, nRunning, nMailbox, nDelegate, nIOUring int
	delegateInfo := ""
	nowTick := kirq.ReadCounterValue()
	for i := 0; i < threadArraySize; i++ {
		if !threadListInUse[i] {
			continue
		}
		t := &threadListData[i]
		switch t.State {
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
		case ThreadBlockedUringRecv:
			nMailbox++
		case ThreadBlockedIOUring:
			nIOUring++
		case ThreadBlockedDelegate:
			nDelegate++
			var blockedMs uint64
			if freq > 0 && t.DelegateBlockSinceTick != 0 && nowTick > t.DelegateBlockSinceTick {
				blockedMs = ((nowTick - t.DelegateBlockSinceTick) * 1000) / freq
			}
			delegateInfo += fmt.Sprintf(" tid=%d/sid=%d/sysid=%d/for=%dms",
				int(t.TID), int(t.PID), int(t.DelegateBlockSysID), blockedMs)
		}
	}

	// Syscall counts
	totalSVC := atomic.LoadUint64(&ksyscall.TotalSVCCount)
	yieldCalls := atomic.LoadUint64(&ksyscall.YieldCallCount)
	yieldSwitch := atomic.LoadUint64(&ksyscall.YieldSwitchCount)
	futexWait := atomic.LoadUint64(&ksyscall.FutexWaitBlocked)
	futexWake := atomic.LoadUint64(&ksyscall.FutexWakeCalls)

	// Timer stats
	tcs := atomic.LoadUint64(&timerCtxSwitchCount)
	irqCount := atomic.LoadUint64(&kirq.TimerIRQCount)
	var actualHz uint64
	firstC := atomic.LoadUint64(&kirq.DbgTimerFirstCounter)
	latestC := atomic.LoadUint64(&kirq.DbgTimerLatestCounter)
	if firstC != 0 && latestC > firstC && irqCount > 1 {
		actualHz = ((irqCount - 1) * kirq.SystemTimerFrequency) / (latestC - firstC)
	}

	// Memory
	khPages := kmem.KernelHeapPageCount()
	khMB := khPages / 256
	pageFaults := kmem.KernelPageFaultCount()

	// Per-SID SVC deltas
	svcDelta := ""
	for i := 0; i < 32; i++ {
		cur := atomic.LoadUint64(&ksyscall.SVCCountBySID[i])
		delta := cur - prevSVCCountBySID[i]
		if delta > 0 {
			svcDelta += fmt.Sprintf(" s%d=%d", i, delta)
		}
		prevSVCCountBySID[i] = cur
	}

	// GC counts
	gcInfo := ""
	for i := 0; i < len(ksyscall.GCCountBySID); i++ {
		gc := atomic.LoadUint64(&ksyscall.GCCountBySID[i])
		if gc > 0 {
			gcInfo += fmt.Sprintf(" s%d=%d", i, gc)
		}
	}

	// Per-SysID deltas (non-zero only)
	sysIDDelta := ""
	sysIDDelegDelta := ""
	for i := 0; i < int(ksyscall.NumSyscallIDs); i++ {
		cur := atomic.LoadUint64(&ksyscall.SysIDCounts[i])
		d := cur - prevSysIDCounts[i]
		if d > 0 {
			sysIDDelta += fmt.Sprintf(" %d=%d", i, d)
		}
		prevSysIDCounts[i] = cur
		curD := atomic.LoadUint64(&ksyscall.SysIDDelegated[i])
		dD := curD - prevSysIDDelegated[i]
		if dD > 0 {
			sysIDDelegDelta += fmt.Sprintf(" %d=%d", i, dD)
		}
		prevSysIDDelegated[i] = curD
	}

	futexPIDMismatch := atomic.LoadUint64(&DbgFutexPIDMismatch)

	// VirtIO block I/O + io_uring wake diagnostics.
	blkIRQs := atomic.LoadUint32(&dbgBlockIRQCount)
	blkDrained := atomic.LoadUint32(&dbgBlockTotalDrained)
	blkCQEW := atomic.LoadUint32(&dbgBlockCQEWritten)
	blkCQEM := atomic.LoadUint32(&dbgBlockCQEMissed)
	blkEmpty := atomic.LoadUint32(&dbgBlockEmptyIRQ)
	blkEmptySnapped := atomic.LoadUint32(&dbgBlockEmptySnapped)
	blkEmptyRawIdx := atomic.LoadUint32(&dbgBlockEmptyRawUsedIdx)
	blkEmptyLastIdx := atomic.LoadUint32(&dbgBlockEmptyLastUsedIdx)
	wakeOK := atomic.LoadUint32(&dbgWakeURWoke)
	wakeLow := atomic.LoadUint32(&dbgWakeURNotEnough)
	wakeNW := atomic.LoadUint32(&dbgWakeURNoWaiter)
	tmoBlkNE := atomic.LoadUint32(&dbgTimeoutBlkNE)

	uartDropped := atomic.LoadUint64(&softIRQDroppedBytes)

	klog.Criticalf("[status] ",
		"uptime=%ds syscalls=%d timer=%dHz ctx_switches=%d\n"+
			"  threads: running=%d ready=%d futex=%d sleep=%d softirq=%d uring=%d blk_io=%d delegate=%d\n"+
			"  yield: calls=%d switched=%d futex: wait=%d wake=%d pid_mismatch=%d\n"+
			"  memory: kernel_heap=%d_pages(%dMB) page_faults=%d\n"+
			"  blk: irqs=%d drained=%d emptyIRQ=%d cqe=%d missed=%d wakeOK=%d wakeLow=%d wakeNW=%d tmoBlkNE=%d emptySnap=%d/raw=%d/last=%d\n"+
			"  svc/shepherd:%s\n"+
			"  svc/sysid:%s\n"+
			"  svc/delegated:%s\n"+
			"  gc cycles:%s\n"+
			"  delegate stuck:%s\n"+
			"  uart-ring: dropped=%d\n",
		uptimeSec, totalSVC, actualHz, tcs,
		nRunning, nReady, nFutex, nSleep, nSoftIRQ, nMailbox, nIOUring, nDelegate,
		yieldCalls, yieldSwitch, futexWait, futexWake, futexPIDMismatch,
		khPages, khMB, pageFaults,
		blkIRQs, blkDrained, blkEmpty, blkCQEW, blkCQEM, wakeOK, wakeLow, wakeNW, tmoBlkNE, blkEmptySnapped, blkEmptyRawIdx, blkEmptyLastIdx,
		svcDelta,
		sysIDDelta,
		sysIDDelegDelta,
		gcInfo,
		delegateInfo,
		uartDropped,
	)
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
var dbgIdleCount uint64
var DbgFutexPIDMismatch uint64 // futex_wake: address matched but PID didn't
var wfiCount uint64
var trapReturnCount uint64
var dbgZeroProgressCount uint64
var dbgZeroProgressT0Count uint64 // zero-progress for TID 0 only (idle loop, expected)
var dbgZPLastTID uint64           // TID of last non-TID0 zero-progress thread
var dbgZPLastPC uint64            // PC of last non-TID0 zero-progress thread
var dbgZPLastPS uint64            // Processor state of last non-TID0 zero-progress thread
var dbgBadPC uint64               // DEBUG: kernel PC saved for userspace thread
// Timer preemption path diagnostics (written by exceptions_{arm64,riscv64}.s)
var dbgTimerEL0 uint64           // timer IRQ interrupted EL0 (userspace)
var dbgTimerSkipEL1h uint64      // skipped: EL1h (exception handler mode)
var dbgTimerSkipSVC uint64       // skipped: svcDepth > 0
var dbgTimerPreemptNotSet uint64 // reached check but NeedsThreadPreempt was 0
var dbgBadPS uint64
var dbgBadTID uint64
var dbgBadPCCount uint64
var dbgLastEL1hELR uint64  // ELR when timer skipped due to EL1h
var dbgLastEL1hSPSR uint64 // SPSR when timer skipped due to EL1h

// prevSVCCountBySID holds the previous epoch's per-SID SVC counts for delta computation.
var prevSVCCountBySID [32]uint64
var prevSID0Syscalls [256]uint64
var prevSysIDCounts [ksyscall.NumSyscallIDs]uint64
var prevSysIDDelegated [ksyscall.NumSyscallIDs]uint64
var dbgBoostAttempt uint64    // times boostThread0ForPendingWork was called
var dbgBoostSuccess uint64    // times boost succeeded (thread 0 was Ready)
var dbgBoostFailState uint64  // thread 0 state when boost failed (last value)
var dbgYieldMlocksSkip uint64 // SaveThread0AndYield skipped due to m.locks
var dbgYieldSleepPath uint64  // SaveThread0AndYield took deadline sleep path
var dbgYieldYieldPath uint64  // SaveThread0AndYield took normal yield path
var dbgYieldNoNext uint64     // SaveThread0AndYield found no ready thread

//go:noinline
func KernelIdleLoop() {
	for {
		dbgIdleCount++

		// Relay pending SVC work requests to worker goroutines.
		// Each Relay converts an atomic flag to a channel send.
		runShepherdKW.Relay()
		epollKW.Relay()
		uringConnectKW.Relay()

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

		// Time attribute updates are now driven by TopHalfTickTimeUpdate in
		// ProcessDeadlinesTopHalf (timer ISR, 10Hz). The idle loop no longer
		// writes time attributes — doing so here would double-fire dirty
		// propagation and exceed the intended 10Hz cadence.

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
			// A thread is ready — yield to it with a deadline-based sleep.
			// Instead of putting thread 0 at the back of the ready queue
			// (which means waiting behind all shepherd threads for up to
			// N*100ms), we sleep with a deadline set to one preemption
			// interval in the future. The timer ISR wakes thread 0 when
			// the deadline fires, giving precise scheduling independent of
			// how many shepherds are active.
			thread0PendingDeadline = kirq.ReadCounterValue() + kirq.ThreadPreemptTicks
			YieldToReadyThread()
			// Resumed from deadline — loop back to process deadlines and
			// update time attributes.
			continue
		}

		// No ready threads — wait for an interrupt (timer tick, etc.)
		// IRQs MUST be enabled for WFI so the timer interrupt can fire
		//
		// Before sleeping, nudge the linux shepherd to flush any pending write
		// buffers. Fire-and-forget: no response, no blocking. If the ring is
		// full (shepherd busy) KernelWriteToRing drops the message silently.
		if linuxSID := ksyscall.LinuxDelegateSID(); linuxSID >= 0 {
			hintMsg := ipc.EncodeIdleFlushHint()
			KernelWriteToRing(linuxSID, &hintMsg)
		}
		wfiCount++
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
					atomic.AddUint64(&dbgYieldMlocksSkip, 1)
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

	// Check if caller requested a deadline-based sleep (KernelBlockSleep).
	deadline := thread0PendingDeadline
	thread0PendingDeadline = 0
	blockState := pendingYieldBlockState
	pendingYieldBlockState = 0

	if deadline != 0 {
		// Sleep mode: add deadline and put thread 0 to sleep.
		// The timer ISR will wake us when the deadline fires.
		atomic.AddUint64(&dbgYieldSleepPath, 1)
		staticDeadlineQueue.Remove(int16(t0.TID))
		staticDeadlineQueue.Insert(int16(t0.TID), deadline)
		pluckFromAllQueues(t0.TID)
		if blockState == 0 {
			t0.State = ThreadSleeping
			sleepingQueue.PushNoDuplicate(t0.TID)
		} else {
			// Custom block state (e.g. ThreadBlockedKernelRingPush). The
			// thread lives on the static deadline queue only; the wake
			// path (drain hook or deadline expiry) flips state and pushes
			// it back onto a ready queue.
			t0.State = blockState
			if blockState == ThreadBlockedKernelRingPush {
				atomic.StoreInt32(&kernelRingPushBlockerTID, int32(t0.TID))
				pushBlockerThreadPtr = uintptr(unsafe.Pointer(t0))
				atomic.StoreUint32(&pushBlockerDeadlineExpired, 0)
			}
		}
	} else {
		// Normal yield: thread 0 goes to back of ready queue.
		atomic.AddUint64(&dbgYieldYieldPath, 1)
		t0.State = ThreadReady
		pluckFromAllQueues(t0.TID)
		GetPerCPU().LocalReadyQueue.PushNoDuplicate(t0.TID)
	}

	// Update tick accounting for thread 0
	currentTime := kirq.ReadCounterValue()
	if t0.TicksStartedRunning != 0 {
		t0.TotalTicksRunning += currentTime - t0.TicksStartedRunning
	}
	t0.TicksStartedRunning = 0

	// Find next ready thread (prefer a shepherd, not kernel)
	next := findReadyThreadPreferDifferentShepherdSchedLockHeld(t0.PID)
	if next == nil {
		// No thread available — continue running thread 0.
		// If we were in sleep mode, undo the sleep state: remove from
		// sleepingQueue and cancel the deadline we just added.
		atomic.AddUint64(&dbgYieldNoNext, 1)
		if deadline != 0 {
			sleepingQueue.Pluck(t0.TID)
			staticDeadlineQueue.Remove(int16(t0.TID))
			if blockState == ThreadBlockedKernelRingPush {
				atomic.StoreInt32(&kernelRingPushBlockerTID, 0)
				pushBlockerThreadPtr = 0
			}
		} else {
			// Normal yield: remove from ready queue (we just pushed it).
			GetPerCPU().LocalReadyQueue.Pluck(t0.TID)
		}
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
		klog.Criticalf("[BUG] ", "Yield RIP=0 TID=%d\n", next.TID)
		for {
			WaitForInterrupt()
		}
	}

	// Deliver pending signals before ERET to this thread.
	if next.PendingSignals != 0 && next.InSignalHandler == 0 {
		deliverSignalHook(next)
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

	// Don't restore DAIF - the ERET will set SPSR which controls IRQ state
	_ = savedDAIF

	// Deliver pending signals before ERET to this thread.
	if thread.PendingSignals != 0 && thread.InSignalHandler == 0 {
		deliverSignalHook(thread)
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
	t.PreemptElapsed = 0                // Fresh thread, no elapsed time yet
	t.TotalTicksRunning = 0             // Fresh thread, no accumulated runtime yet
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
				t.SignalSP = stackHiVal // Stack grows down
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
			releaseShepherdSchedLockHeld(t.ShepherdIdx, exitingPID, false)
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

	// DO NOT call SetCurrentThreadGlobal(next) here!
	// CurrentThread still points to the dying thread. This is intentional:
	// the SVC return path will call DoContextSwitch, which:
	// 1. Calls SaveContextFromFrame on the dying thread (harmless — slot released)
	// 2. Calls SetCurrentThreadGlobal(newThread) to complete the transition
	// If we set CurrentThread here, DoContextSwitch would save the dying thread's
	// exception frame into the NEW thread's context (corrupting its registers and SPSR).
	// doContextSwitchImpl handles all scheduling state (State, StartTick, etc.).

	if sf.StateCheck != nil {
		sf.StateCheck("thread-exit-switch")
	}

	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

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

// DeferredCleanupEntry holds state for two-phase shepherd death cleanup.
// When a shepherd dies with in-flight delegate calls as CALLER, we defer
// page cleanup until the handler (linux shepherd) ACKs via SysDeathAck.
// L0PA is stored; CleanupShepherdPages is called with freeLeaves=true so
// Phase 2 walks L3 entries and frees all leaf + PT pages without needing Spans.
type DeferredCleanupEntry struct {
	InUse bool
	SID   ShepherdId
	L0PA  uintptr
}

// deferredCleanups holds deferred page cleanup entries indexed by shepherd slot.
var deferredCleanups [MaxShepherds]DeferredCleanupEntry

// CompleteDeferredCleanup is called from SyscallDeathAck when the linux shepherd
// has drained all in-flight I/O for a dead shepherd. Frees the dead shepherd's
// pages and cleans up any remaining delegate call entries as a safety net.
func CompleteDeferredCleanup(deadSID int16) {
	for i := range deferredCleanups {
		e := &deferredCleanups[i]
		if !e.InUse || int16(e.SID) != deadSID {
			continue
		}
		// Safety net: reclaim any delegate call entries that SyscallReply didn't clear.
		ksyscall.CleanupRemainingDelegateCallsForCaller(deadSID)
		// Free all the dead shepherd's pages. freeLeaves=true causes Phase 2 to
		// walk L3 entries and free leaf pages (Spans is empty in deferred path).
		var emptySpans proc.LockedSpanGroup
		kmem.CleanupShepherdPages(e.SID, &emptySpans, e.L0PA, true)
		e.InUse = false
		return
	}
}

// FlushAllDeferredCleanups is called when the linux shepherd itself dies.
// Any deferred entries that were waiting for an ACK will never receive one,
// so we clean them up now.
func FlushAllDeferredCleanups() {
	for i := range deferredCleanups {
		e := &deferredCleanups[i]
		if !e.InUse {
			continue
		}
		ksyscall.CleanupRemainingDelegateCallsForCaller(int16(e.SID))
		var emptySpans proc.LockedSpanGroup
		kmem.CleanupShepherdPages(e.SID, &emptySpans, e.L0PA, true)
		e.InUse = false
	}
}

// TerminateShepherd kills all threads belonging to a shepherd and cleans up resources.
// Walks all thread slots, exits every thread with matching PID (except current),
// then exits the current thread last. Calls releaseShepherdSchedLockHeld when
// the last thread exits.
//
// Two-phase cleanup: if the dying shepherd has in-flight delegate calls as CALLER,
// page cleanup is deferred until the linux shepherd ACKs via SysDeathAck.
// Returns pointer to next ready thread's ThreadContext (or 0 if none remain).
//
// NOT nosplit — breaks the nosplit chain from SyscallExitGroup/SyscallMazzyExit
// to allow delegate cleanup functions to run stack checks.
func TerminateShepherd(pid ShepherdId, status int64) uintptr {
	// Clean up delegation resources BEFORE acquiring the scheduler lock.
	// NOT nosplit, which breaks the nosplit chain and avoids exceeding
	// the 792-byte stack limit. Safe because the delegate data structures are
	// protected by IRQ disabling (we're in SVC handler context).
	deferPages := false
	if ksyscall.HasInFlightDelegateCallsAsCaller(int16(pid)) {
		// Two-phase path: only clean up handler-side delegate state.
		// Leave caller-side entries intact so SyscallReply can still
		// reclaim data pages normally.
		terminateShepherdDelegateCleanupHandlerOnly(int16(pid))
		deferPages = true
	} else {
		// Normal path: full delegate cleanup (no in-flight caller calls).
		terminateShepherdDelegateCleanup(int16(pid))
	}
	// Write back MAP_SHARED writable mmap pages before freeing them.
	// Must happen pre-lock: blocks the calling thread while linux shepherd writes.
	// Skip if the linux shepherd itself is dying (no handler available).
	if !isLinuxShepherd(pid) {
		ksyscall.WriteBackSharedMmapOnDeath(int16(pid))
	}
	CleanupBlockCompletionRing(int16(pid))
	CleanupInputCompletionRing(int16(pid))
	CleanupSoftIRQSlotsForShepherd(int16(pid))
	CleanupInputFocusForShepherd(int16(pid))
	CleanupPageSharingForShepherd(int16(pid))
	// CleanupUringIPCForShepherd sends ProtoDeath to peers BEFORE page cleanup.
	CleanupUringIPCForShepherd(int16(pid))
	// Notify global death subscribers (e.g. rachel for window cleanup).
	// Peers were notified above; subscribers get notified after.
	ksyscall.NotifyDeathSubscribers(int16(pid), func(targetSID int16, msg *ipc.UringIPCMsg) {
		KernelWriteToRing(targetSID, msg)
	})
	// Remove the dying shepherd from the subscriber list.
	ksyscall.CleanupDeathSubscriptionsForShepherd(int16(pid))
	// Flush deferred cleanups if the linux shepherd itself is dying.
	if isLinuxShepherd(pid) {
		FlushAllDeferredCleanups()
	}
	return terminateShepherdImpl(&NormalSchedulerFunc, pid, status, deferPages)
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

// terminateShepherdDelegateCleanupHandlerOnly runs handler-only delegate cleanup
// (Parts 2+3) and wakes orphaned callers. Used in two-phase death when the dying
// shepherd has in-flight delegate calls as CALLER that must not be reclaimed yet.
func terminateShepherdDelegateCleanupHandlerOnly(pid int16) {
	orphanCount := ksyscall.CleanupDelegateForDeadShepherdHandlerOnly(pid)
	for i := 0; i < orphanCount; i++ {
		tid := ksyscall.DelegateOrphanedCallerTIDs[i]
		cpid := ksyscall.DelegateOrphanedCallerSIDs[i]
		WakeDelegateCallerThread(cpid, int32(tid), -3) // -ESRCH
	}
}

// isLinuxShepherd returns true if the shepherd with the given PID is the linux
// shepherd. Used to flush deferred cleanups when the linux shepherd itself dies.
func isLinuxShepherd(pid ShepherdId) bool {
	for i := int16(0); i < int16(MaxShepherds); i++ {
		if proc.ShepherdListInUse[i] && proc.ShepherdListData[i].PID == pid {
			return proc.ShepherdListData[i].Filename == "linux"
		}
	}
	return false
}

// terminateShepherdImpl is the internal implementation of TerminateShepherd.
// When deferPages is true, page cleanup is deferred (two-phase death protocol).
//
//go:nosplit
func terminateShepherdImpl(sf *SchedulerFunc, pid ShepherdId, status int64, deferPages bool) uintptr {
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
		releaseShepherdSchedLockHeld(shepherdIdx, pid, deferPages)
	}

	// Find next ready thread
	next := findReadyThreadPreferDifferentShepherdSchedLockHeld(pid)
	if next == nil {
		schedulerLock.Unlock()
		sf.EnableAndRestoreDAIF(savedDAIF)
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

	return uintptr(unsafe.Pointer(&next.Context))
}

// terminateShepherdInternal is the ABI0-compatible wrapper for TerminateShepherd.
// Called from assembly via TerminateShepherdAsm tail-call stub.
//
//go:nosplit
func terminateShepherdInternal(pid uint64, status int64) uint64 {
	return uint64(TerminateShepherd(ShepherdId(pid), status))
}

// releaseShepherdSchedLockHeld releases a shepherd when its last thread exits.
// MUST be called with schedulerLock held.
// Performs TLB shootdown for the ASID before releasing the shepherd ID,
// enabling aggressive ASID reuse to expose bugs.
//
// When deferPages is true (two-phase death protocol), page cleanup is deferred:
// L0PA and Spans are saved into a DeferredCleanupEntry and CleanupShepherdPages
// is skipped. The linux shepherd will ACK via SysDeathAck to trigger cleanup.
//
//go:nosplit
func releaseShepherdSchedLockHeld(shepherdIdx int16, pid ShepherdId, deferPages bool) {
	if shepherdIdx < 0 || !proc.ShepherdListInUse[shepherdIdx] {
		return // Invalid or already released
	}

	// CRITICAL: TLB shootdown before releasing the ASID.
	// When this ASID is reused by a new shepherd, we must ensure no stale
	// TLB entries remain that could cause incorrect address translations.
	// TLBI ASIDE1IS broadcasts to all CPUs in the inner shareable domain.
	kmem.TlbiASIDE1IS(uint16(pid))

	// Clean up DMA clumps: mark dead, free pages if no I/O in flight.
	// Clumps with InFlight > 0 will be freed by the completion handler.
	ksyscall.CleanupShepherdDMAClumps(&proc.ShepherdListData[shepherdIdx])

	// Read l0PA and spans BEFORE zeroing the shepherd struct.
	// CleanupShepherdPages needs these to walk the page tables.
	l0PA := proc.ShepherdListData[shepherdIdx].PageTableL0PA
	spans := &proc.ShepherdListData[shepherdIdx].Spans

	if deferPages {
		// Two-phase death: save L0PA for deferred cleanup.
		// freeLeaves=true in CompleteDeferredCleanup will walk L3 entries to
		// free all leaf pages, since Spans is empty in the deferred path.
		deferredCleanups[shepherdIdx] = DeferredCleanupEntry{
			InUse: true,
			SID:   pid,
			L0PA:  l0PA,
		}
	} else {
		// Normal path: free all physical pages now.
		kmem.CleanupShepherdPages(pid, spans, l0PA, false)
	}

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
	t.TID = tid           // Unique ID from allocator
	t.PID = shepherdId    // Shepherd (process) ID for ASID
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
// All threads go to the BACK (FIFO round-robin). Kernel threads no longer
// get HEAD priority — this caused starvation where sysmon (PID=0) waking
// from sleep deadlines always went to HEAD, blocking userspace threads at
// TAIL from ever being scheduled. The boostThread0ForPendingWork mechanism
// in checkThreadPreemptionImpl handles the urgent case where thread 0
// must run to dispatch LoadMaz/RunMaz/RunShepherd work.
// REQUIRES: schedulerLock held.
//
//go:nosplit
func enqueueReadySchedLockHeld(t *Thread) {
	enqueueReadyToHomeCPU(t)
}

// enqueueReadyPrioritySchedLockHeld enqueues a thread at the HEAD of its
// HomeCPU's ready queue, giving it scheduling priority over other ready threads.
// Used for mailbox-woken threads so they run before sysmon retakes their P.
// REQUIRES: schedulerLock held.
//
//go:nosplit
func enqueueReadyPrioritySchedLockHeld(t *Thread) {
	targetCPU := t.HomeCPU
	cpuCount := int8(GetCPUCount())
	if targetCPU < 0 || targetCPU >= cpuCount {
		targetCPU = int8(GetCPUID())
	}
	perCPU := GetPerCPUByID(uint64(targetCPU))
	perCPU.LocalReadyQueue.PushHeadNoDuplicate(t.TID)
}

// enqueueReadyToHomeCPU adds thread to its HomeCPU's local queue (TAIL).
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
	perCPU.LocalReadyQueue.PushNoDuplicate(t.TID)
}

// enqueueReadyToCurrentCPU adds thread to current CPU's local queue (TAIL).
// Used when no HomeCPU affinity is desired (e.g., newly created threads).
// REQUIRES: schedulerLock held.
//
//go:nosplit
func enqueueReadyToCurrentCPU(t *Thread) {
	cpuID := GetCPUID()
	t.HomeCPU = int8(cpuID) // Set affinity to current CPU

	perCPU := GetPerCPU()
	perCPU.LocalReadyQueue.PushNoDuplicate(t.TID)
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

	// Priority pass: scan for mailbox-woken threads (head of queue first)
	q := &myPerCPU.LocalReadyQueue
	idx := q.Head()
	for seen := 0; seen < len(q.Data); seen++ {
		if q.InUse[idx] {
			tid := q.Data[idx]
			t := threadLookupByTID(int32(tid))
			if t != nil && t.State == ThreadReady && t.PriorityWoken {
				q.PluckAt(idx)
				t.PriorityWoken = false
				return t
			}
		}
		idx = (idx + 1) % len(q.Data)
	}

	// Normal path: pop from head
	for !myPerCPU.LocalReadyQueue.IsEmpty() {
		tid := myPerCPU.LocalReadyQueue.Pop()

		t := threadLookupByTID(int32(tid))
		if t != nil && t.State == ThreadReady {
			t.PriorityWoken = false
			return t
		}
	}

	// Local queue empty - try work stealing from other CPUs
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
				t.PriorityWoken = false
				// Mark that this thread was stolen (for debug output after lock release)
				t.StolenFromCPU = int8(targetCPU)
				return t
			}
			// Thread became invalid - continue trying other CPUs
		}
	}

	return nil
}

// findNextThreadForBlockSchedLockHeld finds the next ready thread for a blocking
// SVC operation. Must be called with schedulerLock held and IRQs disabled.
//
// This is the single implementation of the "find next thread" fallback chain
// used by all blocking SVC paths (mailbox, delegate, io_uring, softIRQ, etc.).
// Having one copy eliminates the bug class where individual callers forget to
// wake sleeping thread 0, causing WFI death loops inside SVC handlers.
//
// Search order:
//  1. Pluck current thread from ready queues (safety: prevent finding self)
//  2. Prefer userspace threads (if caller is userspace)
//  3. Process static deadlines (nanosleep/futex) to wake sleeping threads
//  4. Fall back to any ready thread (including kernel thread 0)
//  5. Wake sleeping thread 0 as last resort — thread 0 normally sleeps with
//     short (~4ms) deadlines in the idle loop; without this step callers fall
//     through to WFI loops where svcDepth>0 blocks timer preemption
//
// Returns nil if no thread is available (caller should undo and return 0).
//
//go:nosplit
func findNextThreadForBlockSchedLockHeld(current *Thread) *Thread {
	pluckFromAllQueues(current.TID)

	var next *Thread
	if current.PageTableL0PA != 0 {
		next = findReadyUserspaceThreadSchedLockHeld(-1)
	} else {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		processStaticDeadlinesSchedLockHeld()
		if current.PageTableL0PA != 0 {
			next = findReadyUserspaceThreadSchedLockHeld(-1)
		} else {
			next = findReadyThreadSchedLockHeld()
		}
	}
	if next == nil && current.PageTableL0PA != 0 {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil && current.TID != 0 {
		t0 := threadLookupByTID(0)
		if t0 != nil && t0.State == ThreadSleeping {
			t0.State = ThreadReady
			pluckFromAllQueues(t0.TID)
			enqueueReadySchedLockHeld(t0)
			next = findReadyThreadSchedLockHeld()
		} else if t0 != nil && t0.State == ThreadReady {
			pluckFromAllQueues(t0.TID)
			next = t0
		}
	}
	return next
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

	// Priority pass: mailbox-woken threads get highest priority (any shepherd)
	q := &myPerCPU.LocalReadyQueue
	idx := q.Head()
	for seen := 0; seen < len(q.Data); seen++ {
		if q.InUse[idx] {
			tid := q.Data[idx]
			t := threadLookupByTID(int32(tid))
			if t != nil && t.State == ThreadReady && t.PriorityWoken {
				q.PluckAt(idx)
				t.PriorityWoken = false
				return t
			}
		}
		idx = (idx + 1) % len(q.Data)
	}

	// First pass: look for a different shepherd in local queue
	idx = q.Head()
	for seen := 0; seen < len(q.Data); seen++ {
		if q.InUse[idx] {
			tid := q.Data[idx]
			t := threadLookupByTID(int32(tid))
			if t != nil && t.State == ThreadReady && t.PID != currentPID {
				q.PluckAt(idx)
				t.PriorityWoken = false
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
					t.PriorityWoken = false
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
			t.PriorityWoken = false
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

	// Priority pass: mailbox-woken userspace threads get highest priority
	q := &myPerCPU.LocalReadyQueue
	idx := q.Head()
	for seen := 0; seen < len(q.Data); seen++ {
		if q.InUse[idx] {
			tid := q.Data[idx]
			t := threadLookupByTID(int32(tid))
			if t != nil && t.State == ThreadReady && t.PID > 0 && t.PriorityWoken {
				q.PluckAt(idx)
				t.PriorityWoken = false
				return t
			}
		}
		idx = (idx + 1) % len(q.Data)
	}

	// Scan local queue for a userspace thread from a different shepherd.
	// Filter by PID > 0 (not just PageTableL0PA != 0) because on AMD64,
	// kernel threads also have non-zero PageTableL0PA (single CR3, no
	// TTBR0/TTBR1 split). Without the PID check, kernel threads at the
	// head of the queue are mistakenly returned, starving userspace shepherds.
	idx = q.Head()
	for seen := 0; seen < len(q.Data); seen++ {
		if q.InUse[idx] {
			tid := q.Data[idx]
			t := threadLookupByTID(int32(tid))
			if t != nil && t.State == ThreadReady && t.PID > 0 && t.PID != currentPID {
				q.PluckAt(idx)
				t.PriorityWoken = false
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
				t.PriorityWoken = false
				return t
			}
		}
		idx = (idx + 1) % len(q.Data)
	}

	return nil
}

// ThreadFindReady finds the next READY thread to yield to.
// Returns CONTEXT POINTER of ready thread, or 0 (nil) if none found.
//
// When the current thread is userspace, only userspace threads are considered.
// The old approach popped a kernel thread from HEAD, rejected it, and re-enqueued
// it — creating a livelock where userspace threads at TAIL were never reached.
// Now we use findReadyUserspaceThreadSchedLockHeld which scans without popping,
// skipping kernel threads in place.
//
// NOTE: Returns context pointer (not index) so 0 unambiguously means "no switch".
// Thread index 0 is valid and would return a non-zero context pointer.
//
//go:nosplit
func ThreadFindReady() uintptr {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	current := GetCurrentThread()
	var t *Thread
	if current != nil && current.PID > 0 {
		// Userspace caller: only find userspace threads.
		// -1 means no shepherd preference (accept any userspace thread).
		t = findReadyUserspaceThreadSchedLockHeld(-1)
	} else {
		// Kernel caller: any thread type is fine.
		t = findReadyThreadSchedLockHeld()
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)

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
// CRITICAL: The expectedVal parameter is used to re-check the futex value under
// the scheduler lock. This prevents the classic futex missed-wakeup race where:
//  1. Thread A checks value, sees it matches expected
//  2. Thread B wakes the futex (but A isn't blocked yet)
//  3. Thread A marks itself blocked (will never be woken)
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

// ThreadWakeFutexWithSwitch wakes futex waiters and returns a context switch
// target for the first woken thread. This matches Linux behavior: futex_wake
// triggers immediate scheduling of the woken thread, preempting the waker.
// Returns (woken count, context pointer). Context pointer is 0 if no thread
// was woken or the woken thread shouldn't be switched to.
//
//go:nosplit
func ThreadWakeFutexWithSwitch(futexAddr uint64, maxWake int32) (int32, uintptr) {
	sf := &NormalSchedulerFunc
	savedDAIF := sf.DisableAndSaveDAIF()
	schedulerLock.Lock()

	callerSID := ShepherdId(-1)
	if caller := GetCurrentThread(); caller != nil {
		callerSID = caller.PID
	}

	woken := int32(0)
	var firstWoken *Thread
	queueSize := blockedQueue.Size()

	for i := 0; i < queueSize && woken < int32(maxWake); i++ {
		tid := blockedQueue.Pop()
		t := threadLookupByTID(int32(tid))
		if t == nil {
			continue
		}
		if t.FutexAddr == futexAddr && t.PID == callerSID {
			t.State = ThreadReady
			t.FutexAddr = 0
			pluckFromAllQueues(tid)
			enqueueReadySchedLockHeld(t)
			if firstWoken == nil {
				firstWoken = t
			}
			woken++
		} else {
			if t.FutexAddr == futexAddr && t.PID != callerSID {
				atomic.AddUint64(&DbgFutexPIDMismatch, 1)
			}
			blockedQueue.PushNoDuplicate(tid)
		}
	}

	schedulerLock.Unlock()
	sf.EnableAndRestoreDAIF(savedDAIF)

	if firstWoken != nil {
		return woken, uintptr(unsafe.Pointer(&firstWoken.Context))
	}
	return woken, 0
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
			klog.Errf("ThreadWakeFutex: invalid TID %d in blockedQueue\n", tid)
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
			// Track PID mismatches — if address matches but PID doesn't,
			// this indicates cross-thread futex wake failure (e.g. sysmon
			// waking a shepherd M's note).
			if t.FutexAddr == futexAddr && t.PID != callerSID {
				atomic.AddUint64(&DbgFutexPIDMismatch, 1)
			}
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
		klog.Criticalf("[BUG] ", "Pickup RIP=0 TID=%d\n", next.TID)
		for {
			WaitForInterrupt()
		}
	}

	// NOTE: Signal delivery moved to caller (checkThreadPreemptionImpl)
	// to reduce nosplit stack depth. tryPickupWorkIdleCPU adds 80 bytes
	// to the chain which pushes BuildSignalFrame's page walks over limit.

	return uint64(uintptr(unsafe.Pointer(&next.Context)))
}

// Thread0HasPendingWork returns true if the current thread is thread 0
// AND there is pending kernel dispatch work (RunShepherd/UringConnect).
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
		ctxPtr := tryPickupWorkHook(sf)
		if ctxPtr != 0 {
			// Deliver pending signals at this stack depth (shallower than inside
			// tryPickupWorkIdleCPU) to stay within nosplit stack budget.
			next := GetCurrentThread()
			if next != nil && next.PendingSignals != 0 && next.InSignalHandler == 0 {
				deliverSignalHook(next)
			}
		}
		return ctxPtr
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

	// All threads — including thread 0 — go to TAIL of the ready queue.
	// Thread 0 participates in normal round-robin scheduling. Its idle
	// loop calls YieldToReadyThread() after doing housekeeping work, so
	// it voluntarily donates most of its quantum to other threads.
	pluckFromAllQueues(oldThread.TID)
	GetPerCPU().LocalReadyQueue.PushNoDuplicate(oldThread.TID) // TAIL

	// Reset preemption tracking for the preempted thread
	oldThread.PreemptElapsed = 0

	// Update runtime accounting for preempted thread
	currentTime := sf.CurrentTime(0)
	if oldThread.TicksStartedRunning != 0 {
		oldThread.TotalTicksRunning += currentTime - oldThread.TicksStartedRunning
	}
	oldThread.TicksStartedRunning = 0 // Mark as not running

	// Pick next ready thread — no special-casing for thread 0.
	// All threads are treated equally in the ready queue.
	next := findReadyThreadSchedLockHeld()

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

	atomic.AddUint64(&dbgPreemptSwitchCount, 1)
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
	next.PreemptElapsed = 0                // Fresh time slice
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
		klog.Criticalf("[BUG] ", "Preempt RIP=0 TID=%d\n", next.TID)
		for {
			WaitForInterrupt()
		}
	}

	// Deliver pending signals before ERET to this thread.
	if next.PendingSignals != 0 && next.InSignalHandler == 0 {
		deliverSignalHook(next)
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
		childRetVal := newThread.Context.GetReturnValue()    // 0 (child TID)
		childSP := newThread.Context.GetSP()                 // new stack
		childGReg := newThread.Context.GetGRegister()        // new g pointer
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

// printTickDistributionNoSplit is a no-op retained for call-site compatibility.
//
//go:nosplit
func printTickDistributionNoSplit(now uint64) {
}

// PrintTickDistribution is a no-op retained for call-site compatibility.
func PrintTickDistribution() {
}

// PrintThreadStateSummary is a no-op retained for call-site compatibility.
func PrintThreadStateSummary() {
}
