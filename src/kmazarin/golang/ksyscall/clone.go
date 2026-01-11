//go:build qemuvirt && aarch64

package ksyscall

import "unsafe"

// CloneThread is provided by main package via go:linkname.
// Creates a new thread with proper context for Go's clone wrapper.
func CloneThread(stack, returnAddr, mp, gp, fn uint64) int32

// GetSyscallELR is provided by main package via go:linkname.
// Returns the ELR_EL1 (return address) for the current syscall.
func GetSyscallELR() uint64

// SyscallClone implements the clone(2) syscall
// Go's runtime pushes mp, gp, fn onto the new stack before calling clone.
// We extract these values from the stack.
//
// Stack layout (positive offsets from passed stack pointer, after Go's SUB $32):
//   stack+0:  saved LR (Go's clone caller return address, not used by us)
//   stack+8:  fn (entry function)
//   stack+16: gp (g pointer)
//   stack+24: mp (m pointer)
//
//go:nosplit
func SyscallClone(flags, stack, ptid, tls, ctid, _ uint64) int64 {
	uart := uintptr(0xFFFFFFFF09000000)
	*(*byte)(unsafe.Pointer(uart)) = '['
	*(*byte)(unsafe.Pointer(uart)) = 'c'
	*(*byte)(unsafe.Pointer(uart)) = 'l'
	*(*byte)(unsafe.Pointer(uart)) = 'o'
	*(*byte)(unsafe.Pointer(uart)) = 'n'
	*(*byte)(unsafe.Pointer(uart)) = 'e'
	*(*byte)(unsafe.Pointer(uart)) = ']'

	// Extract mp, gp, fn from the stack (same as Cardinal)
	// Go writes values at negative offsets from the original stack pointer,
	// then does SUB $32, but the syscall apparently receives the PRE-SUB stack.
	// Stack layout (negative offsets from passed stack):
	//   stack-8:  mp (m pointer)
	//   stack-16: gp (g pointer)
	//   stack-24: fn (entry function)
	//   stack-32: saved LR
	stackPtr := unsafe.Pointer(uintptr(stack))
	mp := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 8))
	gp := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 16))
	fn := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 24))

	// Debug: print key parameters
	hexChars := "0123456789ABCDEF"

	*(*byte)(unsafe.Pointer(uart)) = 'S'
	*(*byte)(unsafe.Pointer(uart)) = '='
	for i := 60; i >= 0; i -= 4 {
		*(*byte)(unsafe.Pointer(uart)) = hexChars[(stack>>i)&0xF]
	}
	*(*byte)(unsafe.Pointer(uart)) = '\r'
	*(*byte)(unsafe.Pointer(uart)) = '\n'

	// Dump stack contents at various offsets (fixed for negative offsets)
	// Negative offsets: -32, -24, -16, -8
	for absOff := uint64(32); absOff >= 8; absOff -= 8 {
		addr := stack - absOff
		val := *(*uint64)(unsafe.Pointer(uintptr(addr)))
		*(*byte)(unsafe.Pointer(uart)) = '-'
		*(*byte)(unsafe.Pointer(uart)) = hexChars[(absOff>>4)&0xF]
		*(*byte)(unsafe.Pointer(uart)) = hexChars[absOff&0xF]
		*(*byte)(unsafe.Pointer(uart)) = ':'
		for i := 60; i >= 0; i -= 4 {
			*(*byte)(unsafe.Pointer(uart)) = hexChars[(val>>i)&0xF]
		}
		*(*byte)(unsafe.Pointer(uart)) = '\r'
		*(*byte)(unsafe.Pointer(uart)) = '\n'
	}
	// Positive offsets: 0, 8, 16, 24, 32
	for off := uint64(0); off <= 32; off += 8 {
		addr := stack + off
		val := *(*uint64)(unsafe.Pointer(uintptr(addr)))
		*(*byte)(unsafe.Pointer(uart)) = '+'
		*(*byte)(unsafe.Pointer(uart)) = hexChars[(off>>4)&0xF]
		*(*byte)(unsafe.Pointer(uart)) = hexChars[off&0xF]
		*(*byte)(unsafe.Pointer(uart)) = ':'
		for i := 60; i >= 0; i -= 4 {
			*(*byte)(unsafe.Pointer(uart)) = hexChars[(val>>i)&0xF]
		}
		*(*byte)(unsafe.Pointer(uart)) = '\r'
		*(*byte)(unsafe.Pointer(uart)) = '\n'
	}

	// Get the actual return address (instruction after SVC) for the child
	// Both parent and child should "return" to the same place,
	// but parent gets TID in x0 and child gets 0 in x0
	returnAddr := GetSyscallELR()

	// Create the thread using CloneThread from main package
	tid := CloneThread(stack, returnAddr, mp, gp, fn)

	if tid < 0 {
		*(*byte)(unsafe.Pointer(uart)) = '!'
		return -1 // EAGAIN - no free thread slots
	}

	// Return TID to parent
	// Child thread is now in READY state and will be scheduled later
	*(*byte)(unsafe.Pointer(uart)) = 'T'
	*(*byte)(unsafe.Pointer(uart)) = 'I'
	*(*byte)(unsafe.Pointer(uart)) = 'D'
	*(*byte)(unsafe.Pointer(uart)) = '='
	for i := 28; i >= 0; i -= 4 {
		*(*byte)(unsafe.Pointer(uart)) = hexChars[(int64(tid)>>i)&0xF]
	}
	*(*byte)(unsafe.Pointer(uart)) = '\r'
	*(*byte)(unsafe.Pointer(uart)) = '\n'

	return int64(tid)
}
