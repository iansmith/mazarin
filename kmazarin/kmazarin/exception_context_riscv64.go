//go:build riscv64

package main

import "unsafe"

// ExceptionContext contains RISC-V-specific exception state.
// Built by the trap handler and passed to Go dispatch code.
//
// scause values (with interrupt bit 63 clear = exceptions):
//
//	 8 = Environment call from U-mode
//	 9 = Environment call from S-mode
//	12 = Instruction page fault
//	13 = Load page fault
//	15 = Store/AMO page fault
type ExceptionContext struct {
	EC       uint64         // scause (exception cause code, without interrupt bit)
	ESR      uint64         // scause full value (with interrupt bit)
	ELR      uint64         // sepc (exception program counter)
	FAR      uint64         // stval (faulting address / instruction)
	SPSR     uint64         // sstatus at exception time
	FramePtr unsafe.Pointer // Pointer to full exception frame
	SavedFP  uint64         // x8/s0 (frame pointer) at exception time
	SavedLR  uint64         // x1/ra (return address) at exception time
	SavedG   uint64         // x27/s11 (g register) at exception time
}

// Size returns the size of ExceptionContext in bytes (72)
func (ctx *ExceptionContext) Size() int {
	return 72
}

// IsDataAbort returns true if this is a load or store page fault.
// scause 13 = load page fault, 15 = store/AMO page fault.
func (ctx *ExceptionContext) IsDataAbort() bool {
	return ctx.EC == 13 || ctx.EC == 15
}

// IsInstructionAbort returns true if this is an instruction page fault.
// scause 12 = instruction page fault.
func (ctx *ExceptionContext) IsInstructionAbort() bool {
	return ctx.EC == 12
}

// IsSyscall returns true if this is an environment call (ecall).
// scause 8 = ecall from U-mode, 9 = ecall from S-mode.
func (ctx *ExceptionContext) IsSyscall() bool {
	return ctx.EC == 8 || ctx.EC == 9
}

// IsWrite returns true if this page fault was caused by a write/store.
// scause 15 = store/AMO page fault.
func (ctx *ExceptionContext) IsWrite() bool {
	return ctx.EC == 15
}
