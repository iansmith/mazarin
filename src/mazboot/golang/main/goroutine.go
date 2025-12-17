//go:build qemuvirt && aarch64

package main

import (
	"mazboot/asm"
	"unsafe"
)

// mainKernelGoroutine holds a pointer to the main kernel goroutine.
// This is stored in a global so it survives the stack switch from g0 to the
// main goroutine's stack. Local variables on g0's stack are not accessible
// after SwitchToGoroutine.
var mainKernelGoroutine *runtimeG

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
