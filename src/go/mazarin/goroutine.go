//go:build qemuvirt && aarch64

package main

import (
	"unsafe"
)

// Goroutine creation functions
// Simplified versions of runtime.newproc() and runtime.malg()

// createKernelGoroutine creates a new goroutine with a 32KB stack allocated from heap
// This is called on g0's stack (system stack) to create the main kernel goroutine
//
//go:nosplit
func createKernelGoroutine(fn func(), stackSize uint32) *runtimeG {
	// Must be called on g0's stack (system stack)
	// This function itself runs on g0's stack

	// 1. Allocate g structure from heap
	gSize := unsafe.Sizeof(runtimeG{})
	gPtr := (*runtimeG)(kmalloc(uint32(gSize)))
	if gPtr == nil {
		uartPuts("ERROR: Failed to allocate g structure\r\n")
		return nil
	}

	// Zero the g structure
	bzero(unsafe.Pointer(gPtr), uint32(gSize))

	// 2. Allocate stack from heap
	// Round stack size to power of 2 (required by Go runtime)
	stackSize = roundUpToPowerOf2(stackSize)

	stackMem := kmalloc(stackSize)
	if stackMem == nil {
		uartPuts("ERROR: Failed to allocate stack\r\n")
		kfree(unsafe.Pointer(gPtr))
		return nil
	}

	// 3. Initialize stack bounds
	stackLo := pointerToUintptr(stackMem)
	stackHi := stackLo + uintptr(stackSize)

	gPtr.stack.lo = stackLo
	gPtr.stack.hi = stackHi
	gPtr.stackguard0 = stackLo + _StackGuard
	// IMPORTANT: stackguard1 should be same as stackguard0 for user goroutines
	// Setting it to ~0 causes spurious morestack calls!
	gPtr.stackguard1 = stackLo + _StackGuard // Same as stackguard0

	// 4. Link to m0
	m0Ptr := (*runtimeM)(unsafe.Pointer(uintptr(0x401013e0)))
	gPtr.m = m0Ptr

	// 5. Initialize scheduler state
	// Set up stack pointer at top of stack (16-byte aligned for AArch64)
	// AArch64 requires 16-byte stack alignment
	// Reserve space for initial frame: 2 registers (x29, x30) = 16 bytes
	frameSize := uintptr(16) // Frame pointer + return address
	sp := stackHi - frameSize
	// Ensure 16-byte alignment (AArch64 requirement)
	sp = sp &^ uintptr(15) // Clear lower 4 bits

	// TESTING: Subtract 48 bytes to fix call chain SP propagation
	// Based on analysis showing SP is consistently 48 bytes too high in call chain
	sp = sp - 48

	// SP ALIGNMENT CHECK: Verify stack pointer is aligned when creating goroutine
	if (sp & 0xF) != 0 {
		uartPuts("ERROR: Goroutine stack pointer is misaligned: 0x")
		uartPutHex64(uint64(sp))
		uartPuts("\r\n")
		kfree(unsafe.Pointer(gPtr))
		kfree(stackMem)
		return nil
	}

	gPtr.sched.sp = sp
	gPtr.stktopsp = sp
	// Set PC to goexit (simplified - real Go uses abi.FuncPCABI0(goexit))
	// For now, we'll set it to the function we want to run
	gPtr.sched.pc = 0                            // Will be set by gostartcallfn equivalent
	gPtr.sched.g = uintptr(unsafe.Pointer(gPtr)) // guintptr is just uintptr

	// 6. Set up function call
	// This is simplified - in real Go, gostartcallfn() does this
	gPtr.startpc = 0 // Will be set when we actually switch to this goroutine

	// 7. Set goroutine state
	// atomicstatus needs to be set to _Grunnable
	// For now, we'll use a simple store (real Go uses casgstatus)
	// Note: In real Go, this is done with casgstatus(newg, _Gdead, _Grunnable)
	gPtr.atomicstatus = _Grunnable
	gPtr.goid = 1 // Main goroutine gets goid=1

	// Debug: print actual stack addresses
	uartPuts("createKernelGoroutine: Created goroutine with 32KB stack\r\n")
	uartPuts("  stackLo: ")
	uartPutHex64(uint64(stackLo))
	uartPuts("\r\n  stackHi: ")
	uartPutHex64(uint64(stackHi))
	uartPuts("\r\n  sched.sp: ")
	uartPutHex64(uint64(gPtr.sched.sp))
	uartPuts("\r\n  sched offset: ")
	uartPutHex64(uint64(unsafe.Offsetof(gPtr.sched)))
	uartPuts("\r\n")

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
