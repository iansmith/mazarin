//go:build qemuvirt && aarch64

package main

import (
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

// Syscall context switch signaling
// Set by syscall handlers (futex, nanosleep) when they need to block
// Checked by assembly after DispatchSyscall returns
// -1 = normal return, >=0 = switch to that thread index
var syscallSwitchTarget int32 = -1

// syscallELR stores the ELR_EL1 for the current syscall
// Set by assembly before calling DispatchSyscall
// Used by clone to get the proper return address for child threads
var syscallELR uint64 = 0

// syscallSPSR stores the SPSR_EL1 for the current syscall
// Set by assembly before calling DispatchSyscall
// Used by clone to get the proper processor state for child threads
var syscallSPSR uint64 = 0

// setSyscallELRInternal is called by assembly via ABI stub to store the current ELR
//
//go:nosplit
//go:noinline
func setSyscallELRInternal(elr uint64) {
	syscallELR = elr
}

// setSyscallSPSRInternal is called by assembly via ABI stub to store the current SPSR
//
//go:nosplit
//go:noinline
func setSyscallSPSRInternal(spsr uint64) {
	syscallSPSR = spsr
}

// GetSyscallELR returns the ELR for the current syscall
// Called by clone to get the child's return address
//
//go:nosplit
//go:noinline
//go:linkname GetSyscallELR kmazarin/ksyscall.GetSyscallELR
func GetSyscallELR() uint64 {
	return syscallELR
}

// GetSyscallSPSR returns the SPSR for the current syscall
// Called by clone to get the child's processor state
//
//go:nosplit
//go:noinline
//go:linkname GetSyscallSPSR kmazarin/ksyscall.GetSyscallSPSR
func GetSyscallSPSR() uint64 {
	return syscallSPSR
}

// getSyscallSwitchTargetInternal returns the switch target and resets it
// Called from assembly via ABI stub after syscall dispatch
// Returns int64 to avoid sign extension issues in assembly
//
//go:nosplit
//go:noinline
func getSyscallSwitchTargetInternal() int64 {
	target := int64(syscallSwitchTarget)
	syscallSwitchTarget = -1 // Reset for next syscall
	return target
}

// SetSyscallSwitchTarget sets the thread to switch to
// Called by syscall handlers that need to block
//
//go:nosplit
//go:noinline
func SetSyscallSwitchTarget(target int32) {
	syscallSwitchTarget = target
}

// InitThreads initializes the thread table with M0 as thread 0
//
//go:nosplit
//go:noinline
func InitThreads() {
	// DEBUG: Print marker to confirm InitThreads runs before clone
	uartPutc('[')
	uartPutc('I')
	uartPutc('N')
	uartPutc('I')
	uartPutc('T')
	uartPutc(' ')
	uartPutc('T')
	uartPutc('0')
	uartPutc(']')

	// M0 is thread 0, already running
	threads[0].State = ThreadRunning
	threads[0].TID = 1 // M0 gets TID 1 (like Linux main thread)
	numThreads = 1
	currentThreadIdx = 0

	// Mark all other slots as free
	for i := 1; i < MaxThreads; i++ {
		threads[i].State = ThreadFree
	}
}

// ThreadCreate creates a new thread entry for clone syscall
// Returns TID on success, -1 if no slots available
//
//go:nosplit
//go:noinline
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
		// TODO: error output
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

	return tid
}

// CloneThread creates a new thread for Go runtime's clone syscall.
// This properly handles Go's clone wrapper which expects:
// - Child returns from clone with x0=0
// - x10=mp, x11=gp, x12=fn preserved (used by Go's wrapper after clone returns)
// - ELR = return address (same as parent - instruction after SVC)
//
// Parameters:
//   - stack: New thread's stack pointer
//   - returnAddr: Return address (ELR_EL1 from parent - where both parent/child "return")
//   - mp: M pointer (goes in x10, used by Go's settls)
//   - gp: G pointer (goes in x11, Go's wrapper sets g register from this)
//   - fn: Entry function (goes in x12, Go's wrapper calls this)
//
// Returns TID on success, -1 if no slots available
//
//go:nosplit
//go:noinline
//go:linkname CloneThread kmazarin/ksyscall.CloneThread
func CloneThread(stack, returnAddr, spsr, mp, gp, fn uint64) int32 {
	hexChars := "0123456789ABCDEF"

	// Check if thread 0 hasn't been initialized yet (InitThreads not called)
	// If so, reserve slot 0 for M0 by marking it as ThreadRunning now
	if threads[0].State == ThreadFree {
		// Initialize thread 0 for M0 (the main thread that's already running)
		threads[0].State = ThreadRunning
		threads[0].TID = 1 // M0 gets TID 1
		currentThreadIdx = 0
		nextTID = 2 // Next clone gets TID 2
		numThreads = 1
		// Print debug marker
		uartPutc('[')
		uartPutc('T')
		uartPutc('0')
		uartPutc('!')
		uartPutc(']')
	}

	// Find a free slot (will skip slot 0 since we just marked it as Running)
	var slot int32 = -1
	for i := int32(0); i < MaxThreads; i++ {
		if threads[i].State == ThreadFree {
			slot = i
			break
		}
	}

	if slot < 0 {
		uartPutc('!')
		return -1
	}

	// DEBUG: Print slot assignment
	uartPutc('<')
	uartPutc('s')
	uartPutc('=')
	uartPutc(hexChars[slot&0xF])
	uartPutc(' ')
	uartPutc('g')
	uartPutc('=')
	// Print gp value (last 4 hex digits)
	uartPutc(hexChars[(gp>>12)&0xF])
	uartPutc(hexChars[(gp>>8)&0xF])
	uartPutc(hexChars[(gp>>4)&0xF])
	uartPutc(hexChars[gp&0xF])
	uartPutc('>')

	// Allocate TID
	tid := nextTID
	nextTID++

	// Initialize thread
	threads[slot].State = ThreadReady
	threads[slot].TID = tid
	threads[slot].FutexAddr = 0
	threads[slot].WakeupTick = 0
	threads[slot].MPtr = mp
	threads[slot].GPtr = gp
	threads[slot].EntryFunc = fn

	// Set up initial context for cloned thread
	// CRITICAL: Go's runtime/sys_linux_arm64.s clone wrapper expects:
	//   - x0 = 0 (clone returns 0 to child)
	//   - x10 = mp (for settls call)
	//   - x11 = gp (for setting g register)
	//   - x12 = fn (entry function to call)
	//   - ELR = return address (CMP $0, R0 instruction after SVC)
	//
	// The wrapper does:
	//   CMP $0, R0      // Check if child (x0 == 0)
	//   BEQ child       // Branch to child label if x0 == 0
	//   RET             // Parent returns with TID in x0
	// child:
	//   MOVD R11, g     // Set g register from x11 (gp)
	//   MOVD R10, R0    // arg for settls
	//   BL settls       // Set up TLS
	//   MOVD R12, R0    // fn to call
	//   BL (R0)         // Call the entry function (mstart)

	threads[slot].Context.X[0] = 0        // Clone returns 0 to child
	threads[slot].Context.SP = stack      // New stack
	threads[slot].Context.ELR = returnAddr // Return to instruction after SVC
	// Use the SPSR from the parent thread so child has same processor state
	threads[slot].Context.SPSR = spsr
	// CRITICAL: Set X[28] (g register) to gp so the g register is valid immediately
	// after context switch. Go's wrapper will also set it from X[11], but we need
	// it valid BEFORE the first instruction runs in case of any exceptions.
	threads[slot].Context.X[28] = gp

	// CRITICAL: Write mp, gp, fn to the stack so clone wrapper's ldur instructions work.
	// Clone wrapper expects to find them at:
	//   [stack-8]  = mp (loaded into X10)
	//   [stack-16] = gp (loaded into X11)
	//   [stack-24] = fn (loaded into X12)
	// This is the same layout that clone() sets up before calling SVC.
	stackPtr := unsafe.Pointer(uintptr(stack))

	// DEBUG: Print stack address and gp value
	uartPutc('[')
	uartPutc('S')
	uartPutc('T')
	uartPutc('K')
	uartPutc('=')
	stackVal := uint64(uintptr(stackPtr))
	for i := 60; i >= 0; i -= 4 {
		nibble := (stackVal >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc(' ')
	uartPutc('g')
	uartPutc('p')
	uartPutc('=')
	for i := 60; i >= 0; i -= 4 {
		nibble := (gp >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc(']')

	*(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 8)) = mp
	*(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 16)) = gp
	*(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 24)) = fn
	*(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 32)) = 1234 // Magic number for stack validation

	numThreads++

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
		old := currentThreadIdx

		// Only mark old thread as ready if it's not blocked and not the same thread
		if old != idx {
			oldState := threads[old].State
			if oldState != ThreadBlockedFutex && oldState != ThreadSleeping {
				threads[old].State = ThreadReady
			}
		}

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
// This is easier to call from assembly than SaveCurrentThreadContext
// framePtr = pointer to the exception frame (SP value in exception handler)
//
//go:nosplit
//go:noinline
func SaveContextFromFrame(framePtr uintptr) {
	hexChars := "0123456789ABCDEF"
	t := &threads[currentThreadIdx]

	// Read all registers from exception frame
	// x0-x27 are stored sequentially starting at offset 0 (each is 8 bytes)
	frame := (*[40]uint64)(unsafe.Pointer(framePtr))

	for i := 0; i < 28; i++ {
		t.Context.X[i] = frame[i]
	}

	// x28 is at offset 224 = 28*8, so frame[28]
	t.Context.X[28] = frame[28]

	// DEBUG: Print thread idx and X28 being saved
	uartPutc('{')
	uartPutc('S')
	uartPutc('A')
	uartPutc('V')
	uartPutc('E')
	uartPutc(' ')
	uartPutc('t')
	uartPutc('=')
	uartPutc(hexChars[currentThreadIdx&0xF])
	uartPutc(' ')
	uartPutc('x')
	uartPutc('2')
	uartPutc('8')
	uartPutc('=')
	x28val := t.Context.X[28]
	for i := 60; i >= 0; i -= 4 {
		nibble := (x28val >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc('}')

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

// doContextSwitchABI0 is the ABI0 entry point for context switching
// Takes uint64/int32 args from assembly, returns pointer as uint64
//
//go:nosplit
//go:noinline
func doContextSwitchABI0(framePtr uint64, targetIdx int32) uint64 {
	// DEBUG: Print what targetIdx we received
	uartPutc('~')
	uartPutc('t')
	uartPutc('=')
	// Print targetIdx as hex digits
	hexChars := "0123456789ABCDEF"
	uartPutc(hexChars[(targetIdx>>12)&0xF])
	uartPutc(hexChars[(targetIdx>>8)&0xF])
	uartPutc(hexChars[(targetIdx>>4)&0xF])
	uartPutc(hexChars[targetIdx&0xF])
	uartPutc('~')

	ctx := doContextSwitchImpl(uintptr(framePtr), targetIdx)

	// DEBUG: Print the context pointer
	uartPutc('~')
	uartPutc('c')
	uartPutc('=')
	ctxAddr := uint64(uintptr(unsafe.Pointer(ctx)))
	for i := 60; i >= 0; i -= 4 {
		nibble := (ctxAddr >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc('~')

	return ctxAddr
}

// doContextSwitchImpl performs a context switch from current thread to targetIdx
// Saves current context from frame, updates thread states, returns new context
// Returns pointer to new thread's Context (for assembly to load)
//
//go:nosplit
//go:noinline
func doContextSwitchImpl(framePtr uintptr, targetIdx int32) *ThreadContext {
	hexChars := "0123456789ABCDEF"

	// DEBUG: Print threads array base and thread context addresses
	uartPutc('[')
	uartPutc('A')
	uartPutc('=')
	threadsBase := uint64(uintptr(unsafe.Pointer(&threads[0])))
	for i := 60; i >= 0; i -= 4 {
		nibble := (threadsBase >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc(']')

	uartPutc('[')
	uartPutc('1')
	uartPutc('=')
	ctx1 := uint64(uintptr(unsafe.Pointer(&threads[1].Context)))
	for i := 60; i >= 0; i -= 4 {
		nibble := (ctx1 >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc(']')

	uartPutc('[')
	uartPutc('2')
	uartPutc('=')
	ctx2 := uint64(uintptr(unsafe.Pointer(&threads[2].Context)))
	for i := 60; i >= 0; i -= 4 {
		nibble := (ctx2 >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc(']')

	// DEBUG: Print X[28] from each thread's context
	uartPutc('[')
	uartPutc('X')
	uartPutc('1')
	uartPutc('=')
	x28_1 := threads[1].Context.X[28]
	for i := 60; i >= 0; i -= 4 {
		nibble := (x28_1 >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc(']')

	uartPutc('[')
	uartPutc('X')
	uartPutc('2')
	uartPutc('=')
	x28_2 := threads[2].Context.X[28]
	for i := 60; i >= 0; i -= 4 {
		nibble := (x28_2 >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc(']')

	// Save current thread's context from exception frame
	SaveContextFromFrame(framePtr)

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

	// DEBUG: Print thread we're restoring and its X28
	uartPutc('{')
	uartPutc('R')
	uartPutc('E')
	uartPutc('S')
	uartPutc('T')
	uartPutc(' ')
	uartPutc('t')
	uartPutc('=')
	uartPutc(hexChars[targetIdx&0xF])
	uartPutc(' ')
	uartPutc('x')
	uartPutc('2')
	uartPutc('8')
	uartPutc('=')
	x28restore := threads[targetIdx].Context.X[28]
	for i := 60; i >= 0; i -= 4 {
		nibble := (x28restore >> i) & 0xF
		uartPutc(hexChars[nibble])
	}
	uartPutc('}')

	return &threads[targetIdx].Context
}
