//go:build arm64 && !test_stubs

package main

// ThreadContext holds saved CPU state for a thread (ARM64).
type ThreadContext struct {
	// General purpose registers x0-x30
	X [31]uint64

	// Special registers
	SP   uint64 // SP_EL0
	ELR  uint64 // Return address (ELR_EL1)
	SPSR uint64 // Processor state (SPSR_EL1)
}

// GetGRegister returns the g register (x28 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) GetGRegister() uint64 { return ctx.X[28] }

// SetGRegister sets the g register (x28 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) SetGRegister(v uint64) { ctx.X[28] = v }

// GetReturnValue returns the return value register (x0 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) GetReturnValue() uint64 { return ctx.X[0] }

// SetReturnValue sets the return value register (x0 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) SetReturnValue(v uint64) { ctx.X[0] = v }

// GetPC returns the program counter (ELR_EL1 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) GetPC() uint64 { return ctx.ELR }

// SetPC sets the program counter (ELR_EL1 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) SetPC(v uint64) { ctx.ELR = v }

// GetSP returns the stack pointer (SP_EL0 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) GetSP() uint64 { return ctx.SP }

// SetSP sets the stack pointer (SP_EL0 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) SetSP(v uint64) { ctx.SP = v }

// GetProcessorState returns the saved processor state (SPSR_EL1 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) GetProcessorState() uint64 { return ctx.SPSR }

// SetProcessorState sets the saved processor state (SPSR_EL1 on ARM64).
//
//go:nosplit
func (ctx *ThreadContext) SetProcessorState(v uint64) { ctx.SPSR = v }

// SetupForUserspace initializes the context for a new userspace thread.
//
//go:nosplit
func (ctx *ThreadContext) SetupForUserspace(entryPoint, stackPtr uint64) {
	for i := range ctx.X {
		ctx.X[i] = 0
	}
	ctx.SP = stackPtr
	ctx.ELR = entryPoint
	ctx.SPSR = 0 // EL0t, AArch64, interrupts unmasked
}

// SetupForCloneChild initializes the context for a clone child thread.
// Clears x0 (child returns 0), sets stack/return address, enables IRQs,
// and sets the g register.
//
//go:nosplit
func (ctx *ThreadContext) SetupForCloneChild(stack, returnAddr, gReg, parentState uint64) {
	ctx.X[0] = 0                          // Child returns with TID = 0
	ctx.SP = stack                        // New stack
	ctx.ELR = returnAddr                  // Return INTO clone.abi0
	ctx.SPSR = parentState & ^uint64(0x80) // Same state but with IRQs enabled (clear DAIF.I)
	ctx.X[28] = gReg                      // g register
}
