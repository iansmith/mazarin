//go:build amd64

package main

import "mazzy/kmazarin/kmem"

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
//	144     FSBase (x86_64 FS_BASE MSR)
//	152     CS (code segment selector for IRETQ)
//	160     SS (stack segment selector for IRETQ)
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
	FSBase uint64 // x86_64 FS_BASE MSR value (per-thread TLS base)
	CS     uint64 // Code segment selector for IRETQ (Ring 0: 0x08, Ring 3: 0x1B)
	SS     uint64 // Stack segment selector for IRETQ (Ring 0: 0x10, Ring 3: 0x23)
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

// SetProcessorState sets the saved processor state (RFLAGS on x86_64).
//
//go:nosplit
func (ctx *ThreadContext) SetProcessorState(v uint64) { ctx.RFLAGS = v }

// RewindToSyscall rewinds the saved PC to re-execute the SYSCALL instruction.
// x86_64 SYSCALL saves RCX = RIP of next instruction. Our handler stores RCX
// as RIP. Subtracting 2 (SYSCALL is 0x0F 0x05) points back to the SYSCALL.
//
//go:nosplit
func (ctx *ThreadContext) RewindToSyscall() { ctx.RIP -= 2 }

// RestoreSyscallArg0 is a no-op on x86_64. The return value (RAX) is separate
// from the first argument (RDI), so the SVC handler doesn't overwrite arg0.
//
//go:nosplit
func (ctx *ThreadContext) RestoreSyscallArg0(v uint64) {}

// RestoreSyscallNum restores RAX to the original syscall number.
// On x86_64, RAX serves as BOTH the syscall number and return value.
// After a blocking syscall returns, RAX holds the return value.
// When rewinding to re-execute the SYSCALL instruction, RAX must be
// restored to the original syscall number so the kernel dispatches correctly.
//
//go:nosplit
func (ctx *ThreadContext) RestoreSyscallNum(v uint64) { ctx.RAX = v }

// SetCloneTLS sets FSBase for CLONE_SETTLS support on AMD64.
// Called during clone child setup to give the child its own FS_BASE,
// preventing TLS corruption when the child writes g to FS:-8.
//
//go:nosplit
func (ctx *ThreadContext) SetCloneTLS(tls uint64) { ctx.FSBase = tls }

// initThread0PageTable returns the kernel's root page table PA for Thread 0.
// On x86_64, there is no TTBR0/TTBR1 split — a single CR3 serves both kernel
// and user. When Thread 0 (kernel) is rescheduled after a user thread, the
// context switch must restore the kernel's page table, otherwise Thread 0
// runs with the user's CR3 and kernel heap accesses at 0xC000000000+ (PML4[1])
// go through user demand-paged entries instead of kernel entries.
//
//go:nosplit
func initThread0PageTable() uintptr {
	return kmem.GetKernelL0PA()
}

// FixIRQEnabled forces IF=1 (bit 9) in saved RFLAGS.
//
//go:nosplit
func (ctx *ThreadContext) FixIRQEnabled() { ctx.RFLAGS |= 0x200 }

// Exception frame indices for portable access from shared code.
// x86_64 frame: [0..14]=GPRs, [15]=error_code, [16]=RIP, [17]=CS, [18]=RFLAGS, [19]=RSP, [20]=SS
const (
	excFramePCIndex = 16 // RIP in x86_64 exception frame
	excFramePSIndex = 18 // RFLAGS in x86_64 exception frame
)

// Ring 3 segment selectors (standard GDT layout with RPL=3)
const (
	userCS = 0x1B // GDT offset 0x18 | RPL=3 (Ring 3 code)
	userSS = 0x23 // GDT offset 0x20 | RPL=3 (Ring 3 data)
)

// SetupForUserspace initializes the context for a new userspace thread.
//
//go:nosplit
func (ctx *ThreadContext) SetupForUserspace(entryPoint, stackPtr uint64) {
	*ctx = ThreadContext{}
	ctx.RSP = stackPtr
	ctx.RIP = entryPoint
	ctx.RFLAGS = 0x202 // IF=1 (interrupts enabled), bit 1 always set
	ctx.CS = userCS
	ctx.SS = userSS
}

// Ring 0 segment selectors (standard GDT layout)
const (
	kernelCS = 0x08 // GDT offset 0x08 (Ring 0 code)
	kernelSS = 0x10 // GDT offset 0x10 (Ring 0 data)
)

// initThread0Context initializes thread 0's context with valid kernel segment selectors
// and captures the current FS_BASE so the TLS sync in load_context_and_iretq writes
// to the correct TLS slot when Thread 0 is rescheduled.
// Without CS/SS, zero-initialization would cause #GP on IRETQ.
// Without FSBase, the TLS sync would use a stale FS_BASE from the previous thread.
//
//go:nosplit
func initThread0Context(ctx *ThreadContext) {
	ctx.CS = kernelCS
	ctx.SS = kernelSS
	// Capture the kernel's FS_BASE so Thread 0's TLS sync writes to the
	// correct location. Go's runtime.settls already set MSR_FS_BASE during
	// runtime init (before this init() function runs).
	ctx.FSBase = readMSR(0xC0000100) // MSR_FS_BASE
}

// SetupForCloneChild initializes the context for a clone child thread.
// Clears RAX (child returns 0), sets stack/return address, and sets the
// g register. CS/SS are set to Ring 0 (kernel clones);
// CloneNeedsParentRegs copy in doContextSwitchImpl will override with
// parent's CS/SS for userspace clones.
//
// RFLAGS is inherited from parentState. During Go runtime init (IF=0),
// children inherit IF=0 to avoid interrupts before the IDT/LAPIC is ready.
// After EnableIRQs, parents have IF=1, so children inherit IF=1.
//
// NOTE: Threads created during init with IF=0 are fixed up by
// FixCloneThreadIFFlags() which is called after EnableIRQs().
//
//go:nosplit
func (ctx *ThreadContext) SetupForCloneChild(stack, returnAddr, gReg, parentState uint64) {
	ctx.RAX = 0          // Child returns with TID = 0
	ctx.RSP = stack      // New stack
	ctx.RIP = returnAddr // Return address
	ctx.RFLAGS = parentState // Inherit parent's interrupt state
	ctx.R14 = gReg       // g register
	ctx.CS = kernelCS    // Kernel code segment (overridden by CloneNeedsParentRegs)
	ctx.SS = kernelSS    // Kernel data segment (overridden by CloneNeedsParentRegs)
}
