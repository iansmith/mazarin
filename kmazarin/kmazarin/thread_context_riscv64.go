//go:build riscv64

package main

// ThreadContext holds saved CPU state for a thread (RISC-V).
//
// X[0] is unused (x0 is hardwired zero on RISC-V).
// X[1]=ra, X[2]=sp, X[3]=gp, X[4]=tp, X[5..7]=t0..t2,
// X[8..9]=s0..s1, X[10..17]=a0..a7, X[18..27]=s2..s11,
// X[28..31]=t3..t6.
//
// Go uses X27 (s11) as the g register on RISC-V.
//
// Exception frame layout (31 GPRs x1-x31 + sepc + sstatus):
//
//	Frame[0]=x1(ra), Frame[1]=x2(sp), ..., Frame[30]=x31(t6)
//	Frame[31]=sepc, Frame[32]=sstatus
type ThreadContext struct {
	X       [32]uint64 // x0-x31 (X[0] unused, x0 is always zero)
	SEPC    uint64     // Supervisor exception program counter
	SSTATUS uint64     // Supervisor status register
}

// GetGRegister returns the g register (x27/s11 on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) GetGRegister() uint64 { return ctx.X[27] }

// SetGRegister sets the g register (x27/s11 on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) SetGRegister(v uint64) { ctx.X[27] = v }

// GetReturnValue returns the return value register (x10/a0 on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) GetReturnValue() uint64 { return ctx.X[10] }

// SetReturnValue sets the return value register (x10/a0 on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) SetReturnValue(v uint64) { ctx.X[10] = v }

// GetPC returns the program counter (SEPC on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) GetPC() uint64 { return ctx.SEPC }

// SetPC sets the program counter (SEPC on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) SetPC(v uint64) { ctx.SEPC = v }

// GetSP returns the stack pointer (x2/sp on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) GetSP() uint64 { return ctx.X[2] }

// SetSP sets the stack pointer (x2/sp on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) SetSP(v uint64) { ctx.X[2] = v }

// GetProcessorState returns the saved processor state (SSTATUS on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) GetProcessorState() uint64 { return ctx.SSTATUS }

// SetProcessorState sets the saved processor state (SSTATUS on RISC-V).
//
//go:nosplit
func (ctx *ThreadContext) SetProcessorState(v uint64) { ctx.SSTATUS = v }

// FixIRQEnabled sets SPIE (bit 5) in saved SSTATUS to enable interrupts on SRET.
//
//go:nosplit
func (ctx *ThreadContext) FixIRQEnabled() { ctx.SSTATUS |= 1 << 5 }

// RewindToSyscall rewinds the saved PC to re-execute the ECALL instruction.
// RISC-V ECALL sets SEPC = PC of ECALL. Our handler adds 4 before saving to
// context so SRET returns to the next instruction. Subtracting 4 re-executes ECALL.
//
//go:nosplit
func (ctx *ThreadContext) RewindToSyscall() { ctx.SEPC -= 4 }

// RestoreSyscallArg0 restores the first syscall argument register.
// On RISC-V, a0 (X[10]) serves as both arg0 and return value. The ECALL handler
// overwrites a0 in the saved context with the return value, so we must restore
// the original arg0 before re-executing the ECALL.
//
//go:nosplit
func (ctx *ThreadContext) RestoreSyscallArg0(v uint64) { ctx.X[10] = v }

// RestoreSyscallNum is a no-op on RISC-V. The syscall number is in A7 (X17) which
// is NOT overwritten by the return value (A0/X10), so no restoration needed.
//
//go:nosplit
func (ctx *ThreadContext) RestoreSyscallNum(v uint64) {}

// SetCloneTLS is a no-op on RISC-V (no FS_BASE, TLS is handled via TP register).
//
//go:nosplit
func (ctx *ThreadContext) SetCloneTLS(tls uint64) {}

// SetupForUserspace initializes the context for a new userspace thread.
//
//go:nosplit
func (ctx *ThreadContext) SetupForUserspace(entryPoint, stackPtr uint64) {
	*ctx = ThreadContext{}
	ctx.X[2] = stackPtr  // sp
	ctx.SEPC = entryPoint // return address for SRET
	// SSTATUS: set SPIE (bit 5) so interrupts are enabled after SRET,
	// SPP (bit 8) = 0 for U-mode return,
	// FS (bits 14:13) = 01 (Initial) to enable floating-point instructions.
	// Without FS enabled, FP instructions trap as illegal instructions (cause 2).
	// Unlike ARM64 where CPACR_EL1 persists across exception returns,
	// RISC-V FS is in sstatus which is context-switched on every SRET.
	ctx.SSTATUS = (1 << 5) | (1 << 13)
}

// initThread0Context is a no-op on RISC-V (no segment selectors needed).
//
//go:nosplit
func initThread0Context(ctx *ThreadContext) {}

// initThread0PageTable returns the kernel's root page table PA for Thread 0.
// On RISC-V, initProcessL0 copies L3[1-511] from the kernel page table, so
// the user page table includes kernel heap entries. Thread 0 can access
// kernel heap through the user's SATP. Returns 0 to keep existing behavior.
//
//go:nosplit
func initThread0PageTable() uintptr { return 0 }

// Exception frame indices for portable access from shared code.
const (
	excFramePCIndex = 31 // SEPC in RISC-V trap frame
	excFramePSIndex = 32 // SSTATUS in RISC-V trap frame
)

// SetupForCloneChild initializes the context for a clone child thread.
// Clears a0 (child returns 0), sets stack/return address, enables IRQs,
// and sets the g register.
//
//go:nosplit
func (ctx *ThreadContext) SetupForCloneChild(stack, returnAddr, gReg, parentState uint64) {
	ctx.X[10] = 0                    // a0 = 0, child returns with TID = 0
	ctx.X[2] = stack                 // sp = new stack
	ctx.SEPC = returnAddr            // Return address for SRET
	ctx.SSTATUS = parentState | (1 << 5) // Set SPIE to enable IRQs on SRET
	ctx.X[27] = gReg                 // g register (s11)
}
