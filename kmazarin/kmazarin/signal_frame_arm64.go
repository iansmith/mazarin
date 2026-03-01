//go:build arm64

package main

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/serial"
)

// ARM64 signal frame layout constants.
// These match the Go runtime's ucontext/sigcontext structs in defs_linux_arm64.go.
const (
	arm64UcontextSize   = 4560 // sizeof(ucontext)
	arm64SiginfoSize    = 128  // sizeof(siginfo)
	arm64SignalFrameSize = arm64UcontextSize + arm64SiginfoSize + 16 // +16 for alignment

	// Offsets within ucontext_t
	ucFlags        = 0   // uc_flags
	ucLink         = 8   // uc_link
	ucStack        = 16  // uc_stack.ss_sp
	ucStackFlags   = 24  // uc_stack.ss_flags
	ucStackSize    = 32  // uc_stack.ss_size
	ucSigmask      = 40  // uc_sigmask
	ucMcontext     = 176 // uc_mcontext (sigcontext starts here)

	// Offsets within sigcontext (relative to uc_mcontext)
	scFaultAddr = 0   // fault_address
	scRegs      = 8   // regs[0] (X0)
	scSP        = 256 // sp (0x100 from sigcontext start)
	scPC        = 264 // pc (0x108)
	scPstate    = 272 // pstate (0x110)

	// Absolute offsets within ucontext (ucMcontext + sc*)
	ucMcontextRegs   = ucMcontext + scRegs   // 184 = regs[0]
	ucMcontextSP     = ucMcontext + scSP     // 432 = sp
	ucMcontextPC     = ucMcontext + scPC     // 440 = pc
	ucMcontextPstate = ucMcontext + scPstate // 448 = pstate
)

// BuildSignalFrame builds a signal frame on the thread's gsignal stack
// and modifies the ThreadContext to enter sigtramp.
//
// The signal frame is in userspace memory, so all writes go through the kernel
// linear map (VA→PA walk + KernelMMIOOffset) since direct userspace access is
// blocked by PAN (ARM64) / SUM (RISC-V) / SMAP (x86_64).
//
//go:nosplit
func BuildSignalFrame(thread *Thread, signum int, action *SignalAction) {
	// Determine signal stack to use
	signalSP := thread.SignalSP
	if signalSP == 0 {
		serial.RawUARTPuts("[signal] ERROR: no signal stack for TID=")
		serial.RawUARTHex64(uint64(thread.TID))
		serial.RawUARTPuts("\r\n")
		return
	}

	l0PA := thread.PageTableL0PA

	// Allocate space on signal stack (grows downward)
	frameSP := signalSP - uint64(arm64SignalFrameSize)
	frameSP &= ^uint64(0xF) // 16-byte align

	// NOTE: Signal stack pages must already be backed by physical memory.
	// The sigaltstack runtime overlay pre-touches the top 2 pages to trigger
	// demand paging in userspace before the kernel needs to write the frame.

	// Pointers to siginfo and ucontext within the frame
	siginfoAddr := frameSP
	uctxAddr := frameSP + uint64(arm64SiginfoSize)

	// Zero the entire frame through kernel mapping
	kmem.ZeroUserMemoryWithL0(uintptr(frameSP), uintptr(arm64SignalFrameSize), l0PA)

	// --- Populate siginfo ---
	siPtr := uintptr(siginfoAddr)
	kmem.WriteUserInt32WithL0(siPtr, int32(signum), l0PA)  // si_signo
	kmem.WriteUserInt32WithL0(siPtr+4, 0, l0PA)            // si_errno
	kmem.WriteUserInt32WithL0(siPtr+8, _SI_KERNEL, l0PA)   // si_code

	// --- Populate ucontext ---
	ucPtr := uintptr(uctxAddr)

	// Save all general-purpose registers from ThreadContext into ucontext
	for i := 0; i < 31; i++ {
		kmem.WriteUserUint64WithL0(ucPtr+ucMcontextRegs+uintptr(i)*8, thread.Context.X[i], l0PA)
	}
	kmem.WriteUserUint64WithL0(ucPtr+ucMcontextSP, thread.Context.SP, l0PA)
	kmem.WriteUserUint64WithL0(ucPtr+ucMcontextPC, thread.Context.ELR, l0PA)
	kmem.WriteUserUint64WithL0(ucPtr+ucMcontextPstate, thread.Context.SPSR, l0PA)

	// uc_stack: record the signal stack info
	kmem.WriteUserUint64WithL0(ucPtr+ucStack, thread.SignalStackBase, l0PA)      // ss_sp
	kmem.WriteUserInt32WithL0(ucPtr+ucStackFlags, _SS_ONSTACK, l0PA)             // ss_flags
	kmem.WriteUserUint64WithL0(ucPtr+ucStackSize, thread.SignalStackSize, l0PA)  // ss_size

	// Store ucontext address for rt_sigreturn to find later
	thread.SignalUctxAddr = uctxAddr

	// --- Modify ThreadContext to enter sigtramp ---
	// R0 = signal number
	thread.Context.X[0] = uint64(signum)
	// R1 = pointer to siginfo
	thread.Context.X[1] = siginfoAddr
	// R2 = pointer to ucontext
	thread.Context.X[2] = uctxAddr
	// SP = signal stack (below the frame we just built)
	thread.Context.SP = frameSP
	// PC = sigtramp address (registered as sa_handler during initsig)
	thread.Context.ELR = action.Handler
	// LR = sigreturn trampoline (for ARM64, sa_restorer is not set by setsig)
	if action.Restorer != 0 {
		thread.Context.X[30] = action.Restorer
	} else {
		thread.Context.X[30] = uint64(sigreturnTrampolinePC)
	}
	// R28 (g register) remains as-is — sigtrampgo reads g from ucontext.regs[28]
	// R29 (FP) = 0 for clean frame
	thread.Context.X[29] = 0
	// SPSR remains unchanged (same EL, same interrupt state)
}

// RestoreFromSignalFrame reads the ucontext from the signal frame and
// copies register values back into the thread's Context.
//
//go:nosplit
func RestoreFromSignalFrame(t *Thread) {
	ucPtr := uintptr(t.SignalUctxAddr)
	if ucPtr == 0 {
		return
	}

	l0PA := t.PageTableL0PA

	// Restore GPRs from ucontext.uc_mcontext.regs[0..30]
	for i := 0; i < 31; i++ {
		val, ok := kmem.ReadUserUint64WithL0(ucPtr+ucMcontextRegs+uintptr(i)*8, l0PA)
		if ok {
			t.Context.X[i] = val
		}
	}
	if val, ok := kmem.ReadUserUint64WithL0(ucPtr+ucMcontextSP, l0PA); ok {
		t.Context.SP = val
	}
	if val, ok := kmem.ReadUserUint64WithL0(ucPtr+ucMcontextPC, l0PA); ok {
		t.Context.ELR = val
	}
	if val, ok := kmem.ReadUserUint64WithL0(ucPtr+ucMcontextPstate, l0PA); ok {
		t.Context.SPSR = val
	}

	t.SignalUctxAddr = 0 // Consumed
}
