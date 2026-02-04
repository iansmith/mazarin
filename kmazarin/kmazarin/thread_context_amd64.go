//go:build amd64 && !test_stubs

package main

// ThreadContext holds saved CPU state for a thread (x86_64).
//
// Exception frame layout (pushed by handler, low to high):
//
//	Offset  Field
//	  0     RAX
//	  8     RBX
//	 16     RCX
//	 24     RDX
//	 32     RSI
//	 40     RDI
//	 48     RBP
//	 56     R8
//	 64     R9
//	 72     R10
//	 80     R11
//	 88     R12
//	 96     R13
//	104     R14 (Go g register)
//	112     R15
//	120     RIP
//	128     RFLAGS
//	136     RSP
type ThreadContext struct {
	RAX    uint64
	RBX    uint64
	RCX    uint64
	RDX    uint64
	RSI    uint64
	RDI    uint64
	RBP    uint64
	R8     uint64
	R9     uint64
	R10    uint64
	R11    uint64
	R12    uint64
	R13    uint64
	R14    uint64 // Go g register on amd64
	R15    uint64
	RIP    uint64 // Instruction pointer
	RFLAGS uint64 // Processor flags
	RSP    uint64 // Stack pointer
}

// GetGRegister returns the g register (R14 on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) GetGRegister() uint64 { return ctx.R14 }

// SetGRegister sets the g register (R14 on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) SetGRegister(v uint64) { ctx.R14 = v }

// GetReturnValue returns the return value register (RAX on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) GetReturnValue() uint64 { return ctx.RAX }

// SetReturnValue sets the return value register (RAX on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) SetReturnValue(v uint64) { ctx.RAX = v }

// GetPC returns the program counter (RIP on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) GetPC() uint64 { return ctx.RIP }

// SetPC sets the program counter (RIP on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) SetPC(v uint64) { ctx.RIP = v }

// GetSP returns the stack pointer (RSP on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) GetSP() uint64 { return ctx.RSP }

// SetSP sets the stack pointer (RSP on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) SetSP(v uint64) { ctx.RSP = v }

// GetProcessorState returns the saved processor state (RFLAGS on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) GetProcessorState() uint64 { return ctx.RFLAGS }

// SetupForUserspace initializes the context for a new userspace thread.
//
//go:nosplit
func (ctx *ThreadContext) SetupForUserspace(entryPoint, stackPtr uint64) {
	*ctx = ThreadContext{}
	ctx.RSP = stackPtr
	ctx.RIP = entryPoint
	ctx.RFLAGS = 0x202 // IF=1 (interrupts enabled), bit 1 always set
}

// SetupForCloneChild initializes the context for a clone child thread.
// Clears RAX (child returns 0), sets stack/return address, enables IRQs,
// and sets the g register.
//
//go:nosplit
func (ctx *ThreadContext) SetupForCloneChild(stack, returnAddr, gReg, parentState uint64) {
	ctx.RAX = 0                     // Child returns with TID = 0
	ctx.RSP = stack                 // New stack
	ctx.RIP = returnAddr            // Return address
	ctx.RFLAGS = parentState | 0x200 // Same state but with IF set (interrupts enabled)
	ctx.R14 = gReg                  // g register
}
