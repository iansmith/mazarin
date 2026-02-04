//go:build test_stubs

package main

import "unsafe"

// ExceptionContext contains exception state (test stub matching ARM64 layout).
type ExceptionContext struct {
	EC       uint64
	ESR      uint64
	ELR      uint64
	FAR      uint64
	SPSR     uint64
	FramePtr unsafe.Pointer
	SavedFP  uint64
	SavedLR  uint64
	SavedG   uint64
}

func (ctx *ExceptionContext) Size() int              { return 72 }
func (ctx *ExceptionContext) IsDataAbort() bool       { return ctx.EC == 0x24 || ctx.EC == 0x25 }
func (ctx *ExceptionContext) IsInstructionAbort() bool { return ctx.EC == 0x20 || ctx.EC == 0x21 }
func (ctx *ExceptionContext) IsSyscall() bool          { return ctx.EC == 0x15 }
func (ctx *ExceptionContext) IsWrite() bool            { return (ctx.ESR>>6)&1 == 1 }
