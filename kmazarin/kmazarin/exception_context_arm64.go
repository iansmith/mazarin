//go:build arm64

package main

import "unsafe"

// ExceptionContext contains ARM64-specific exception state.
// All fields are 8 bytes for clean assembly access.
// This struct is built by assembly exception handlers and passed to Go.
type ExceptionContext struct {
	EC       uint64         // Exception Class (bits 31:26 of ESR)
	ESR      uint64         // Exception Syndrome Register (full value)
	ELR      uint64         // Exception Link Register (return PC)
	FAR      uint64         // Fault Address Register (faulting VA)
	SPSR     uint64         // Saved Processor State Register
	FramePtr unsafe.Pointer // Pointer to full exception frame (all 30 registers)
	SavedFP  uint64         // X29 at exception time
	SavedLR  uint64         // X30 at exception time
	SavedG   uint64         // X28 (goroutine pointer) at exception time
}

// Size returns the size of ExceptionContext in bytes (72)
func (ctx *ExceptionContext) Size() int {
	return 72
}

// IsDataAbort returns true if this is a data abort exception
func (ctx *ExceptionContext) IsDataAbort() bool {
	return ctx.EC == 0x24 || ctx.EC == 0x25
}

// IsInstructionAbort returns true if this is an instruction abort exception
func (ctx *ExceptionContext) IsInstructionAbort() bool {
	return ctx.EC == 0x20 || ctx.EC == 0x21
}

// IsSyscall returns true if this is a syscall (SVC) exception
func (ctx *ExceptionContext) IsSyscall() bool {
	return ctx.EC == 0x15
}

// IsWrite returns true if this data abort was caused by a write
// Only valid for data aborts (check IsDataAbort() first)
func (ctx *ExceptionContext) IsWrite() bool {
	return (ctx.ESR>>6)&1 == 1
}
