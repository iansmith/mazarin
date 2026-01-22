package ksyscall

import (
	"kmazarin/console"
	"unsafe"
)

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
// Note: No //go:nosplit because CloneThread allocates memory for thread nodes.
func SyscallClone(flags, stack, ptid, tls, ctid, _ uint64) int64 {
	console.Breadcrumb('@') // Entry marker for SyscallClone
	console.KWriteString("\r\n[Clone] stack=")
	console.KPrintHex64(stack)
	console.KWriteString(" flags=")
	console.KPrintHex64(flags)
	console.KWriteString("\r\n")

	// Extract mp, gp, fn from the stack (same as Cardinal)
	// Go writes values at negative offsets from the original stack pointer,
	// then does SUB $32, but the syscall apparently receives the PRE-SUB stack.
	// Stack layout (negative offsets from passed stack):
	//   stack-8:  mp (m pointer)
	//   stack-16: gp (g pointer)
	//   stack-24: fn (entry function)
	//   stack-32: saved LR
	//
	// WARNING: This direct read may fail due to PAN (Privileged Access Never)!
	// If kernel can't read userspace memory, these will be garbage values.
	stackPtr := unsafe.Pointer(uintptr(stack))
	mp := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 8))
	gp := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 16))
	fn := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 24))

	// Also read the marker at stack-32 (should be 0x4D2 = 1234)
	marker := *(*uint64)(unsafe.Pointer(uintptr(stackPtr) - 32))

	console.KWriteString("[Clone] mp=")
	console.KPrintHex64(mp)
	console.KWriteString(" gp=")
	console.KPrintHex64(gp)
	console.KWriteString(" fn=")
	console.KPrintHex64(fn)
	console.KWriteString(" marker=")
	console.KPrintHex64(marker)
	if marker == 0x4D2 {
		console.KWriteString(" (OK)")
	} else {
		console.KWriteString(" (EXPECTED 0x4D2!)")
	}
	console.KWriteString("\r\n")

	// Suppress unused warnings
	_ = flags
	_ = ptid
	_ = tls
	_ = ctid

	// Get the actual return address (instruction after SVC) for the child
	// Both parent and child should "return" to the same place,
	// but parent gets TID in x0 and child gets 0 in x0
	returnAddr := GetSyscallELR()
	// Get the processor state (SPSR) from the parent - child should have same state
	spsr := GetSyscallSPSR()

	// Create the thread using CloneThread from main package
	tid := CloneThread(stack, returnAddr, spsr, mp, gp, fn)

	console.KWriteString("[Clone] TID=")
	console.KPrintHex64(uint64(tid))
	console.KWriteString("\r\n")

	if tid < 0 {
		return -1 // EAGAIN - no free thread slots
	}

	// Return TID to parent
	// CRITICAL: CloneThread has called SetSyscallSwitchTarget, so after this
	// syscall returns, the assembly will switch to the NEW thread (B).
	// The parent (A) will be saved to ready queue and will return from clone()
	// with this TID when it's eventually scheduled.
	console.KWriteString("[Clone] B will run, A goes to ready queue\r\n")
	return int64(tid)
}
