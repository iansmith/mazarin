//go:build test_stubs

package main

// ThreadContext holds saved CPU state for a thread (test stub).
type ThreadContext struct {
	// General purpose registers (test version matches ARM64 layout)
	X [31]uint64

	// Special registers
	SP   uint64 // Stack pointer
	ELR  uint64 // Return address
	SPSR uint64 // Processor state
}

// GetGRegister returns the g register (x28).
//
//go:nosplit
func (ctx *ThreadContext) GetGRegister() uint64 { return ctx.X[28] }

// SetGRegister sets the g register (x28).
//
//go:nosplit
func (ctx *ThreadContext) SetGRegister(v uint64) { ctx.X[28] = v }

// GetReturnValue returns the return value register (x0).
//
//go:nosplit
func (ctx *ThreadContext) GetReturnValue() uint64 { return ctx.X[0] }

// SetReturnValue sets the return value register (x0).
//
//go:nosplit
func (ctx *ThreadContext) SetReturnValue(v uint64) { ctx.X[0] = v }

// GetPC returns the program counter.
//
//go:nosplit
func (ctx *ThreadContext) GetPC() uint64 { return ctx.ELR }

// SetPC sets the program counter.
//
//go:nosplit
func (ctx *ThreadContext) SetPC(v uint64) { ctx.ELR = v }

// GetSP returns the stack pointer.
//
//go:nosplit
func (ctx *ThreadContext) GetSP() uint64 { return ctx.SP }

// SetSP sets the stack pointer.
//
//go:nosplit
func (ctx *ThreadContext) SetSP(v uint64) { ctx.SP = v }

// GetProcessorState returns the saved processor state.
//
//go:nosplit
func (ctx *ThreadContext) GetProcessorState() uint64 { return ctx.SPSR }

// SetupForUserspace initializes the context for a new userspace thread.
//
//go:nosplit
func (ctx *ThreadContext) SetupForUserspace(entryPoint, stackPtr uint64) {
	for i := range ctx.X {
		ctx.X[i] = 0
	}
	ctx.SP = stackPtr
	ctx.ELR = entryPoint
	ctx.SPSR = 0
}

// initThread0Context is a no-op for test stubs.
//
//go:nosplit
func initThread0Context(ctx *ThreadContext) {}

// SetupForCloneChild initializes the context for a clone child thread.
//
//go:nosplit
func (ctx *ThreadContext) SetupForCloneChild(stack, returnAddr, gReg, parentState uint64) {
	ctx.X[0] = 0
	ctx.SP = stack
	ctx.ELR = returnAddr
	ctx.SPSR = parentState & ^uint64(0x80)
	ctx.X[28] = gReg
}
