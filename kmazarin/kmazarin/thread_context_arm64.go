//go:build arm64

package main

import "mazzy/kmazarin/serial"

// ThreadContext holds saved CPU state for a thread (ARM64).
type ThreadContext struct {
	// General purpose registers x0-x30
	X [31]uint64

	// Special registers
	SP   uint64 // SP_EL0
	ELR  uint64 // Return address (ELR_EL1)
	SPSR uint64 // SPSR_EL1. frameaudit:exempt-asm — rides the ELR LDP pair (ELR+8) in CTX_RESTORE_TO_FRAME; checked on the Go save side + boot selftest.
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

// FixIRQEnabled clears DAIF.I (bit 7) in saved SPSR to enable interrupts.
//
//go:nosplit
func (ctx *ThreadContext) FixIRQEnabled() { ctx.SPSR &^= 0x80 }

// RewindToSyscall rewinds the saved PC to re-execute the SVC instruction.
// ARM64 SVC saves ELR_EL1 = PC+4 (next instruction). Subtracting 4 points
// back to the SVC itself, so the thread re-enters the syscall handler on resume.
//
//go:nosplit
func (ctx *ThreadContext) RewindToSyscall() { ctx.ELR -= 4 }

// RestoreSyscallArg0 restores the first syscall argument register.
// On ARM64, X0 serves as both arg0 and return value. The SVC handler
// overwrites X0 in the saved context with the return value, so we must
// restore the original arg0 before re-executing the SVC.
//
//go:nosplit
func (ctx *ThreadContext) RestoreSyscallArg0(v uint64) { ctx.X[0] = v }

// RestoreSyscallNum is a no-op on ARM64. The syscall number is in X8 which
// is NOT overwritten by the return value (X0), so no restoration needed.
//
//go:nosplit
func (ctx *ThreadContext) RestoreSyscallNum(v uint64) {}

// SetCloneTLS is a no-op on ARM64 (no FS_BASE, TLS is handled via TPIDR_EL0).
//
//go:nosplit
func (ctx *ThreadContext) SetCloneTLS(tls uint64) {}

// SetupForUserspace initializes the context for a new userspace thread.
//
//go:nosplit
func (ctx *ThreadContext) SetupForUserspace(entryPoint, stackPtr uint64) {
	if entryPoint == 0 {
		serial.PollWrite('Q') // Q = SetupForUserspace with entry=0
	}
	for i := range ctx.X {
		ctx.X[i] = 0
	}
	ctx.SP = stackPtr
	ctx.ELR = entryPoint
	ctx.SPSR = 0 // EL0t, AArch64, interrupts unmasked
}

// initThread0Context is a no-op on ARM64 (no segment selectors needed).
//
//go:nosplit
func initThread0Context(ctx *ThreadContext) {}

// initThread0PageTable returns the kernel's root page table PA for Thread 0.
// On ARM64, TTBR1 handles kernel space independently, so Thread 0 doesn't need
// a specific page table. Returns 0 to indicate "use whatever is in TTBR0".
//
//go:nosplit
func initThread0PageTable() uintptr { return 0 }

// Exception frame indices for portable access from shared code.
const (
	excFramePCIndex = 32 // ELR_EL1 in ARM64 exception frame
	excFramePSIndex = 33 // SPSR_EL1 in ARM64 exception frame
)

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
