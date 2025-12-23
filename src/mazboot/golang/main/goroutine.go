//go:build qemuvirt && aarch64

package main

import (
	"mazboot/asm"
	"runtime"
	"unsafe"
)

// mainKernelGoroutine holds a pointer to the main kernel goroutine.
// This is stored in a global so it survives the stack switch from g0 to the
// main goroutine's stack. Local variables on g0's stack are not accessible
// after SwitchToGoroutine.
var mainKernelGoroutine *runtimeG

// SimpleChannel is a minimal channel implementation that uses kmalloc
// instead of Go's heap allocator to avoid GC issues in bare-metal.
type SimpleChannel struct {
	count uint32 // Number of signals pending
}

// Global signal channel instance
var simpleSignalChan *SimpleChannel

// Global Go channel for timer signals (real Go channel, not SimpleChannel)
var goSignalChan chan struct{}

// timerPendingCount tracks pending timer signals from interrupt handler
// Incremented by interrupt handler (nosplit), decremented by goroutine
var timerPendingCount uint32

// createSimpleChannel creates a simple channel using kmalloc.
//
//go:nosplit
//go:noinline
func createSimpleChannel() *SimpleChannel {
	// Allocate channel structure with kmalloc (bypasses Go GC)
	ptr := kmalloc(uint32(unsafe.Sizeof(SimpleChannel{})))
	if ptr == nil {
		return nil
	}
	ch := (*SimpleChannel)(ptr)
	ch.count = 0
	return ch
}

// send increments the signal count (non-blocking).
//
//go:nosplit
//go:noinline
func (ch *SimpleChannel) send() {
	if ch != nil {
		ch.count++
	}
}

// receive waits for a signal and decrements the count.
// Blocks (busy-waits) until a signal is available.
//
//go:nosplit
//go:noinline
func (ch *SimpleChannel) receive() {
	if ch == nil {
		return
	}
	// Busy-wait until signal available
	for ch.count == 0 {
		// Spin
	}
	ch.count--
}

// timerPreempt is called from timer interrupt handler to implement preemption.
// This uses runtime.Gosched() which WILL return, but we handle the return
// specially in assembly by restoring from saved state.
//
//go:nosplit
//go:noinline
func timerPreempt() {
	// Breadcrumb: Show timer preemption started
	uartBase := getLinkerSymbol("__uart_base")
	asm.MmioWrite(uartBase, uint32('.')) // Print '.' for each timer interrupt

	// Signal the monitor channels (GC, scavenger, schedtrace)
	timerSignalMonitors()

	// Signal the simple test channel (for backwards compatibility)
	if simpleSignalChan != nil {
		simpleSignalChan.send()
	}

	// Call runtime.Gosched() to yield to scheduler
	// This MAY return if scheduler picks us again
	// The assembly wrapper will handle restoration properly
	runtime.Gosched()

	// If we get here, scheduler picked us again
	// Assembly wrapper will restore state and jump to interrupted PC
}

// timerSignal is called from timer interrupt handler to implement preemption.
// The interrupt handler switches to the current goroutine's stack before calling this,
// so we can safely call runtime.Gosched() to forcibly yield the goroutine.
//
// This implements timer-based preemptive multitasking - the goroutine is interrupted
// and forced to yield without its cooperation.
//
//go:nosplit
//go:noinline
func timerSignal() {
	// Signal the monitor channels (GC, scavenger, schedtrace)
	timerSignalMonitors()

	// Signal the simple test channel (for backwards compatibility)
	if simpleSignalChan != nil {
		simpleSignalChan.send()
	}

	// FORCIBLY PREEMPT: Call Gosched() on behalf of the interrupted goroutine
	// This makes the current goroutine yield and allows another to run
	// This is TRUE PREEMPTION - the goroutine didn't choose to yield!
	runtime.Gosched()
}

// timerSignalGo sends a signal to the real Go channel.
// Called from timer interrupt handler.
// Uses non-blocking send to avoid blocking in interrupt context.
// NOTE: Cannot use //go:nosplit because select may allocate
//
//go:noinline
func timerSignalGo() {
	if goSignalChan == nil {
		return
	}
	// Non-blocking send to avoid blocking in interrupt context
	select {
	case goSignalChan <- struct{}{}:
		// Signal sent successfully
	default:
		// Channel full, drop signal
	}
}

// Goroutine creation functions
// Simplified versions of runtime.newproc() and runtime.malg()

// createKernelGoroutine creates a new goroutine with a 32KB stack allocated from heap
// This is called on g0's stack (system stack) to create the main kernel goroutine
//
//go:nosplit
func createKernelGoroutine(fn func(), stackSize uint32) *runtimeG {
	// Allocate g structure from heap
	gSize := unsafe.Sizeof(runtimeG{})
	gPtr := (*runtimeG)(kmalloc(uint32(gSize)))
	if gPtr == nil {
		return nil
	}
	asm.Bzero(unsafe.Pointer(gPtr), uint32(gSize))

	// Allocate stack from heap (round to power of 2)
	stackSize = roundUpToPowerOf2(stackSize)
	stackMem := kmalloc(stackSize)
	if stackMem == nil {
		kfree(unsafe.Pointer(gPtr))
		return nil
	}

	// Initialize stack bounds
	stackLo := pointerToUintptr(stackMem)
	stackHi := stackLo + uintptr(stackSize)
	gPtr.stack.lo = stackLo
	gPtr.stack.hi = stackHi
	gPtr.stackguard0 = stackLo + _StackGuard
	gPtr.stackguard1 = stackLo + _StackGuard

	// Link to m0
	gPtr.m = (*runtimeM)(unsafe.Pointer(asm.GetM0Addr()))

	// Initialize scheduler state (SP at top of stack, 16-byte aligned)
	frameSize := uintptr(16)
	sp := stackHi - frameSize
	sp = sp &^ uintptr(15)
	sp = sp - 48 // Offset for call chain SP propagation

	if (sp & 0xF) != 0 {
		kfree(unsafe.Pointer(gPtr))
		kfree(stackMem)
		return nil
	}

	gPtr.sched.sp = sp
	gPtr.stktopsp = sp
	gPtr.sched.pc = 0
	gPtr.sched.g = uintptr(unsafe.Pointer(gPtr))
	gPtr.startpc = 0
	gPtr.atomicstatus = _Grunnable
	gPtr.goid = 1

	return gPtr
}

// roundUpToPowerOf2 rounds n up to the next power of 2
// Required by Go runtime's stackalloc()
//
//go:nosplit
func roundUpToPowerOf2(n uint32) uint32 {
	if n == 0 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n++
	return n
}

// Goroutine status constants (from Go runtime)
const (
	_Gidle = iota
	_Grunnable
	_Grunning
	_Gsyscall
	_Gwaiting
	_Gdead
	_Gcopystack
	_Gpreempted
	_Gscan         = 0x1000
	_Gscanrunnable = _Gscan + _Grunnable
	_Gscanrunning  = _Gscan + _Grunning
	_Gscansyscall  = _Gscan + _Gsyscall
	_Gscanwaiting  = _Gscan + _Gwaiting
)

// nextGoroutineID is the next goroutine ID to assign
var nextGoroutineID uint64 = 2 // 0 = g0, 1 = main kernel goroutine

// spawnGoroutine creates a new goroutine and runs the given function on it.
// When the function returns, control returns to the caller.
// This is a simple cooperative spawn - no preemption.
//
//go:noinline
func spawnGoroutine(fn func()) {
	// Create a new goroutine with 32KB stack
	const stackSize = 32 * 1024
	newG := createGoroutine(stackSize)
	if newG == nil {
		print("spawnGoroutine: failed to create goroutine\r\n")
		return
	}

	// Assign goroutine ID
	newG.goid = nextGoroutineID
	nextGoroutineID++

	// Run the function on the new goroutine's stack
	// This saves our state, switches to newG's stack, runs fn, then returns here
	asm.RunOnGoroutine(unsafe.Pointer(newG), fn)

	// Clean up the goroutine (free its stack and g struct)
	freeGoroutine(newG)
}

// createGoroutine allocates a new goroutine with the given stack size
//
//go:nosplit
func createGoroutine(stackSize uint32) *runtimeG {
	// Allocate g structure from heap
	gSize := unsafe.Sizeof(runtimeG{})
	gPtr := (*runtimeG)(kmalloc(uint32(gSize)))
	if gPtr == nil {
		return nil
	}
	asm.Bzero(unsafe.Pointer(gPtr), uint32(gSize))

	// Allocate stack from heap (round to power of 2)
	stackSize = roundUpToPowerOf2(stackSize)
	stackMem := kmalloc(stackSize)
	if stackMem == nil {
		kfree(unsafe.Pointer(gPtr))
		return nil
	}

	// Initialize stack bounds
	stackLo := pointerToUintptr(stackMem)
	stackHi := stackLo + uintptr(stackSize)
	gPtr.stack.lo = stackLo
	gPtr.stack.hi = stackHi
	gPtr.stackguard0 = stackLo + _StackGuard
	gPtr.stackguard1 = stackLo + _StackGuard

	// Link to m0
	gPtr.m = (*runtimeM)(unsafe.Pointer(asm.GetM0Addr()))

	// Set up SP at top of stack, 16-byte aligned
	sp := stackHi - 64 // Leave room for initial frame
	sp = sp &^ uintptr(15)
	gPtr.sched.sp = sp
	gPtr.stktopsp = sp
	gPtr.sched.g = uintptr(unsafe.Pointer(gPtr))
	gPtr.atomicstatus = _Grunnable

	return gPtr
}

// freeGoroutine frees a goroutine's resources
//
//go:nosplit
func freeGoroutine(g *runtimeG) {
	if g == nil {
		return
	}
	// Free stack
	if g.stack.lo != 0 {
		kfree(unsafe.Pointer(g.stack.lo))
	}
	// Free g struct
	kfree(unsafe.Pointer(g))
}
