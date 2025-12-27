//go:build qemuvirt && aarch64

package main

import (
	_ "unsafe"
)

// Syscall return codes for assembly
const (
	SyscallReturnNormal  = 0  // Return normally with value in x0
	SyscallReturnSwitch  = 1  // Context switch to thread in x1
	SyscallReturnBlock   = 2  // Block current thread, switch to thread in x1
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

// Maximum number of threads
const MaxThreads = 8

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
	State      int32  // ThreadFree, ThreadRunning, etc.
	TID        int32  // Thread ID (returned by clone)
	FutexAddr  uint64 // Address being waited on (for ThreadBlockedFutex)
	WakeupTick uint64 // Tick at which to wake (for ThreadSleeping)
	MPtr       uint64 // Pointer to Go M struct
	GPtr       uint64 // Pointer to Go g struct (g0 for this M)
	EntryFunc  uint64 // Entry function (mstart)
	Context    ThreadContext
}

// Thread table - all threads in the system
var threads [MaxThreads]Thread

// Current thread index (0 = M0)
var currentThreadIdx int32 = 0

// Number of threads created
var numThreads int32 = 1

// Next TID to assign
var nextTID int32 = 100

// Global tick counter (incremented by timer)
var globalTickCounter uint64 = 0

// InitThreads initializes the thread table with M0 as thread 0
//
//go:nosplit
//go:noinline
func InitThreads() {
	// M0 is thread 0, already running
	threads[0].State = ThreadRunning
	threads[0].TID = 1 // M0 gets TID 1 (like Linux main thread)
	numThreads = 1
	currentThreadIdx = 0

	// Mark all other slots as free
	for i := 1; i < MaxThreads; i++ {
		threads[i].State = ThreadFree
	}

	uartPutsDirect("InitThreads: M0 is thread 0\r\n")
}

// ThreadCreate creates a new thread entry for clone syscall
// Returns TID on success, -1 if no slots available
//
//go:nosplit
func ThreadCreate(stack, entryFunc, mPtr, gPtr uint64) int32 {
	// Find a free slot
	var slot int32 = -1
	for i := int32(0); i < MaxThreads; i++ {
		if threads[i].State == ThreadFree {
			slot = i
			break
		}
	}

	if slot < 0 {
		uartPutsDirect("ThreadCreate: no free slots!\r\n")
		return -1
	}

	// Allocate TID
	tid := nextTID
	nextTID++

	// Initialize thread
	threads[slot].State = ThreadReady
	threads[slot].TID = tid
	threads[slot].FutexAddr = 0
	threads[slot].WakeupTick = 0
	threads[slot].MPtr = mPtr
	threads[slot].GPtr = gPtr
	threads[slot].EntryFunc = entryFunc

	// Set up initial context
	// x28 = g pointer
	threads[slot].Context.X[28] = gPtr
	// SP = stack
	threads[slot].Context.SP = stack
	// ELR = entry function (mstart)
	threads[slot].Context.ELR = entryFunc
	// SPSR = EL1t mode, interrupts enabled
	threads[slot].Context.SPSR = 0x00000000

	numThreads++

	uartPutsDirect("ThreadCreate: created thread ")
	uartPutHex32Direct(uint32(tid))
	uartPutsDirect(" in slot ")
	uartPutHex32Direct(uint32(slot))
	uartPutsDirect(" entry=")
	uartPutHex64Direct(entryFunc)
	uartPutsDirect("\r\n")

	return tid
}

// ThreadFindReady finds the next READY thread using round-robin
// Returns thread index, or -1 if none found
//
//go:nosplit
func ThreadFindReady() int32 {
	// Start from current+1, wrap around
	start := (currentThreadIdx + 1) % numThreads

	for i := int32(0); i < numThreads; i++ {
		idx := (start + i) % numThreads
		if threads[idx].State == ThreadReady {
			return idx
		}
	}

	return -1
}

// ThreadBlockFutex marks current thread as blocked on futex and finds next thread
// Returns index of next thread to run, or -1 if none (caller should spin/idle)
//
//go:nosplit
func ThreadBlockFutex(futexAddr uint64) int32 {
	// Mark current thread as blocked
	threads[currentThreadIdx].State = ThreadBlockedFutex
	threads[currentThreadIdx].FutexAddr = futexAddr

	uartPutsDirect("ThreadBlockFutex: thread ")
	uartPutHex32Direct(uint32(currentThreadIdx))
	uartPutsDirect(" blocked on ")
	uartPutHex64Direct(futexAddr)
	uartPutsDirect("\r\n")

	// Find next ready thread
	return ThreadFindReady()
}

// ThreadWakeFutex wakes threads blocked on the given futex address
// Returns number of threads woken
//
//go:nosplit
func ThreadWakeFutex(futexAddr uint64, maxWake int32) int32 {
	woken := int32(0)

	for i := int32(0); i < numThreads && woken < maxWake; i++ {
		if threads[i].State == ThreadBlockedFutex && threads[i].FutexAddr == futexAddr {
			threads[i].State = ThreadReady
			threads[i].FutexAddr = 0
			woken++

			uartPutsDirect("ThreadWakeFutex: woke thread ")
			uartPutHex32Direct(uint32(i))
			uartPutsDirect("\r\n")
		}
	}

	return woken
}

// ThreadBlockSleep marks current thread as sleeping until wakeupTick
// Returns index of next thread to run, or -1 if none
//
//go:nosplit
func ThreadBlockSleep(durationTicks uint64) int32 {
	wakeupTick := globalTickCounter + durationTicks
	threads[currentThreadIdx].State = ThreadSleeping
	threads[currentThreadIdx].WakeupTick = wakeupTick

	uartPutsDirect("ThreadBlockSleep: thread ")
	uartPutHex32Direct(uint32(currentThreadIdx))
	uartPutsDirect(" sleeping until tick ")
	uartPutHex64Direct(wakeupTick)
	uartPutsDirect("\r\n")

	return ThreadFindReady()
}

// ThreadCheckSleepers wakes threads whose sleep time has elapsed
// Called from timer interrupt
//
//go:nosplit
func ThreadCheckSleepers() {
	for i := int32(0); i < numThreads; i++ {
		if threads[i].State == ThreadSleeping {
			if globalTickCounter >= threads[i].WakeupTick {
				threads[i].State = ThreadReady
				threads[i].WakeupTick = 0

				uartPutsDirect("ThreadCheckSleepers: woke thread ")
				uartPutHex32Direct(uint32(i))
				uartPutsDirect("\r\n")
			}
		}
	}
}

// ThreadIncrementTick increments the global tick counter
// Called from timer interrupt
//
//go:nosplit
func ThreadIncrementTick() {
	globalTickCounter++
}

// GetCurrentThreadIdx returns the current thread index
//
//go:nosplit
func GetCurrentThreadIdx() int32 {
	return currentThreadIdx
}

// SetCurrentThreadIdx sets the current thread index (called after context switch)
//
//go:nosplit
func SetCurrentThreadIdx(idx int32) {
	if idx >= 0 && idx < MaxThreads {
		threads[currentThreadIdx].State = ThreadReady // Old thread becomes ready (unless blocked)
		currentThreadIdx = idx
		threads[idx].State = ThreadRunning
	}
}

// GetThreadContext returns a pointer to a thread's context
// Used by assembly for context switch
//
//go:nosplit
func GetThreadContext(idx int32) *ThreadContext {
	if idx >= 0 && idx < MaxThreads {
		return &threads[idx].Context
	}
	return nil
}

// GetThread returns a pointer to a thread entry
//
//go:nosplit
func GetThread(idx int32) *Thread {
	if idx >= 0 && idx < MaxThreads {
		return &threads[idx]
	}
	return nil
}

// SaveCurrentThreadContext saves the current thread's context
// Called from assembly before context switch
// The context is passed from the exception frame
//
//go:nosplit
func SaveCurrentThreadContext(
	x0, x1, x2, x3, x4, x5, x6, x7 uint64,
	x8, x9, x10, x11, x12, x13, x14, x15 uint64,
	x16, x17, x18, x19, x20, x21, x22, x23 uint64,
	x24, x25, x26, x27, x28, x29, x30 uint64,
	sp, elr, spsr uint64,
) {
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

// ============================================================================
// SYSCALL HANDLERS - Called from assembly
// ============================================================================
//
// These functions implement the syscall logic. Assembly calls these and then
// performs context switches based on the return values.
//
// Return convention:
//   x0 = syscall return value (or action code)
//   x1 = target thread index (if context switch needed)

// SyscallCloneHandler handles the clone syscall
// Called from assembly with clone parameters already extracted from stack
// Returns: (tid int64, switchTo int32)
//   - tid: TID of new thread (returned to parent)
//   - switchTo: -1 to return normally, >=0 to switch to that thread
//
//go:nosplit
//go:noinline
func SyscallCloneHandler(stack, entryFunc, mPtr, gPtr uint64) (tid int64, switchTo int32) {
	uartPutsDirect("SyscallCloneHandler: stack=")
	uartPutHex64Direct(stack)
	uartPutsDirect(" entry=")
	uartPutHex64Direct(entryFunc)
	uartPutsDirect("\r\n")

	// Create new thread in READY state
	newTID := ThreadCreate(stack, entryFunc, mPtr, gPtr)
	if newTID < 0 {
		return -1, -1 // Error, return -1 to caller
	}

	// Return TID to parent, don't switch yet
	// The new thread will run when parent blocks
	return int64(newTID), -1
}

// Futex operations
const (
	FutexWait        = 0
	FutexWake        = 1
	FutexWaitPrivate = 128
	FutexWakePrivate = 129
)

// SyscallFutexHandler handles the futex syscall
// Returns: (result int64, switchTo int32)
//   - result: 0 for WAIT, number woken for WAKE
//   - switchTo: -1 to return normally, >=0 to switch to that thread
//
//go:nosplit
//go:noinline
func SyscallFutexHandler(uaddr uint64, op int32, val uint32) (result int64, switchTo int32) {
	// Mask off private flag
	opMasked := op & 0x7F

	switch opMasked {
	case FutexWait:
		// FUTEX_WAIT: Block until someone wakes us
		uartPutsDirect("FutexWait: addr=")
		uartPutHex64Direct(uaddr)
		uartPutsDirect("\r\n")

		// Find next thread to run
		nextThread := ThreadBlockFutex(uaddr)
		if nextThread >= 0 {
			// Context switch to next thread
			return 0, nextThread
		}
		// No other thread ready - this is a problem!
		// For now, return immediately (spurious wakeup)
		uartPutsDirect("FutexWait: no ready threads, spurious wakeup\r\n")
		threads[currentThreadIdx].State = ThreadRunning // Unblock ourselves
		return 0, -1

	case FutexWake:
		// FUTEX_WAKE: Wake up to 'val' threads blocked on this address
		uartPutsDirect("FutexWake: addr=")
		uartPutHex64Direct(uaddr)
		uartPutsDirect(" val=")
		uartPutHex32Direct(val)
		uartPutsDirect("\r\n")

		woken := ThreadWakeFutex(uaddr, int32(val))
		return int64(woken), -1

	default:
		uartPutsDirect("FutexHandler: unknown op ")
		uartPutHex32Direct(uint32(op))
		uartPutsDirect("\r\n")
		return 0, -1
	}
}

// SyscallNanosleepHandler handles the nanosleep syscall
// duration is in nanoseconds
// Returns: (result int64, switchTo int32)
//
//go:nosplit
//go:noinline
func SyscallNanosleepHandler(seconds uint64, nanoseconds uint64) (result int64, switchTo int32) {
	// Calculate total nanoseconds
	totalNs := seconds*1000000000 + nanoseconds

	// Convert to timer ticks
	// ticks = ns * freq / 1e9
	// To avoid overflow: ticks = (ns / 1000) * (freq / 1000000)
	//                         = (ns / 1000) * (freq_mhz)
	freqMHz := timerFrequencyHz / 1000000 // 62 for 62.5 MHz
	ticks := (totalNs / 1000) * freqMHz

	if ticks == 0 {
		ticks = 1 // Minimum 1 tick
	}

	uartPutsDirect("NanosleepHandler: ")
	uartPutHex64Direct(totalNs)
	uartPutsDirect("ns = ")
	uartPutHex64Direct(ticks)
	uartPutsDirect(" ticks\r\n")

	// Block current thread
	nextThread := ThreadBlockSleep(ticks)
	if nextThread >= 0 {
		return 0, nextThread
	}

	// No other thread ready - busy wait
	// This shouldn't happen in practice, but handle it
	uartPutsDirect("NanosleepHandler: no ready threads, busy wait\r\n")
	threads[currentThreadIdx].State = ThreadRunning
	return 0, -1
}

// TimerTickHandler is called from timer interrupt
// Checks for threads to wake and optionally preempts
// Returns: switchTo int32 (-1 for no switch, >=0 to switch)
//
//go:nosplit
//go:noinline
func TimerTickHandler() int32 {
	// Increment global tick counter
	globalTickCounter++

	// Check sleeping threads
	ThreadCheckSleepers()

	// For now, no preemption - just wake sleepers
	// Could add round-robin preemption here later

	return -1
}
