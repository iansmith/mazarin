//go:build amd64 && !test_stubs

package main

import "unsafe"

// ExceptionContext contains x86_64-specific exception state.
// Built by the IDT exception handler and passed to Go dispatch code.
type ExceptionContext struct {
	EC       uint64         // Exception vector number
	ESR      uint64         // Error code (from CPU, or 0 if none)
	ELR      uint64         // RIP at time of exception
	FAR      uint64         // CR2 (faulting address for page faults)
	SPSR     uint64         // RFLAGS at time of exception
	FramePtr unsafe.Pointer // Pointer to full exception frame
	SavedFP  uint64         // RBP at exception time
	SavedLR  uint64         // Return address (caller's RIP)
	SavedG   uint64         // R14 (g register) at exception time
}

// Size returns the size of ExceptionContext in bytes (72)
func (ctx *ExceptionContext) Size() int {
	return 72
}

// IsDataAbort returns true if this is a page fault exception.
// x86_64 vector 14 = #PF (Page Fault).
func (ctx *ExceptionContext) IsDataAbort() bool {
	return ctx.EC == 14
}

// IsInstructionAbort returns true if this is an instruction fetch page fault.
// On x86_64, page faults (vector 14) cover both data and instruction faults.
// The error code bit 4 indicates instruction fetch (1 = instruction, 0 = data).
func (ctx *ExceptionContext) IsInstructionAbort() bool {
	return ctx.EC == 14 && (ctx.ESR>>4)&1 == 1
}

// IsSyscall returns true if this is a syscall.
// We use INT 0x80 (vector 128) for syscalls.
func (ctx *ExceptionContext) IsSyscall() bool {
	return ctx.EC == 0x80
}

// IsWrite returns true if this page fault was caused by a write.
// Error code bit 1: 0 = read, 1 = write.
func (ctx *ExceptionContext) IsWrite() bool {
	return (ctx.ESR>>1)&1 == 1
}
