//go:build qemuvirt && aarch64

package kthread

import (
	"unsafe"
)

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

// Timer frequency (Hz) - set during initialization
var timerFrequencyHz uint64 = 62500000 // Default 62.5 MHz for QEMU

// Debug output functions - implemented in main package
func uartPutsDirect(s string)
func uartPutHex32Direct(v uint32)
func uartPutHex64Direct(v uint64)

// Assembly function declarations
func DoContextSwitch(framePtr uintptr, targetIdx int32) *ThreadContext

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

	uartPutsDirect("[kthread] InitThreads: M0 is thread 0\r\n")
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
		uartPutsDirect("[kthread] ThreadCreate: no free slots!\r\n")
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
	// x28 = g pointer (Go's g register)
	threads[slot].Context.X[28] = gPtr
	// SP = stack pointer for the new thread
	threads[slot].Context.SP = stack
	// ELR = entry function (mstart) - where to start executing
	threads[slot].Context.ELR = entryFunc
	// SPSR = 0x344 = EL1t mode (M=0100) with D=A=F masked, I=0 (IRQs enabled)
	// D=1, A=1, I=0, F=1 -> 0011 0100 0100 = 0x344
	// IRQs MUST be enabled for timer-based preemption to work!
	threads[slot].Context.SPSR = 0x344

	numThreads++

	uartPutsDirect("[kthread] ThreadCreate: created thread ")
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

	uartPutsDirect("[kthread] ThreadBlockFutex: thread ")
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

			uartPutsDirect("[kthread] ThreadWakeFutex: woke thread ")
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

	uartPutsDirect("[kthread] ThreadBlockSleep: thread ")
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

				uartPutsDirect("[kthread] ThreadCheckSleepers: woke thread ")
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

// SaveContextFromFrame saves the current thread's context from an exception frame
// This is easier to call from assembly than SaveCurrentThreadContext
// framePtr = pointer to the exception frame (SP value in exception handler)
//
//go:nosplit
//go:noinline
func SaveContextFromFrame(framePtr uintptr) {
	t := &threads[currentThreadIdx]

	// Read all registers from exception frame
	// x0-x27 are stored sequentially starting at offset 0 (each is 8 bytes)
	frame := (*[40]uint64)(unsafe.Pointer(framePtr))

	for i := 0; i < 28; i++ {
		t.Context.X[i] = frame[i]
	}

	// x28 is at offset 224 = 28*8, so frame[28]
	t.Context.X[28] = frame[28]

	// x29, x30 are at offset 232 = 29*8, so frame[29] and frame[30]
	t.Context.X[29] = frame[29]
	t.Context.X[30] = frame[30]

	// SP_EL0 is at offset 288 = 36*8, so frame[36]
	t.Context.SP = frame[36]

	// ELR_EL1 is at offset 256 = 32*8, so frame[32]
	t.Context.ELR = frame[32]

	// SPSR_EL1 is at offset 264 = 33*8, so frame[33]
	t.Context.SPSR = frame[33]
}

// doContextSwitchInternal performs a context switch from current thread to targetIdx
// Saves current context from frame, updates thread states, returns new context
// Returns pointer to new thread's Context (for assembly to load)
//
//go:nosplit
//go:noinline
func doContextSwitchInternal(framePtr uintptr, targetIdx int32) *ThreadContext {
	// Save current thread's context from exception frame
	SaveContextFromFrame(framePtr)

	// Debug output
	uartPutsDirect("[kthread] ContextSwitch: ")
	uartPutHex32Direct(uint32(currentThreadIdx))
	uartPutsDirect(" -> ")
	uartPutHex32Direct(uint32(targetIdx))
	uartPutsDirect("\r\n")

	// Update thread indices and states
	// Note: The blocking state was already set by the syscall handler
	// (e.g., ThreadBlockFutex sets state to ThreadBlockedFutex)
	oldIdx := currentThreadIdx
	currentThreadIdx = targetIdx

	// New thread becomes running
	threads[targetIdx].State = ThreadRunning

	// If old thread was still marked Running, set it to Ready
	// (This shouldn't happen - syscall handlers should have set blocking state)
	if threads[oldIdx].State == ThreadRunning {
		threads[oldIdx].State = ThreadReady
	}

	return &threads[targetIdx].Context
}
