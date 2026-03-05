//go:build riscv64

package main

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/serial"
)

// RISC-V signal frame layout constants.
// These match the Linux kernel's ucontext/sigcontext for riscv64
// (see Go runtime's defs_linux_riscv64.go).
const (
	riscv64SiginfoSize    = 128
	riscv64UcontextSize   = 960 // approximate, we zero the full allocation
	riscv64SignalFrameSize = riscv64SiginfoSize + riscv64UcontextSize + 16 // +16 alignment

	// Offsets within ucontext_t
	rucFlags     = 0   // uc_flags (uint64)
	rucLink      = 8   // uc_link (uint64)
	rucStackSP   = 16  // uc_stack.ss_sp (uint64)
	rucStackFlags = 24 // uc_stack.ss_flags (int32)
	// 28: 4 bytes padding
	rucStackSize = 32  // uc_stack.ss_size (uint64)
	rucSigmask   = 40  // uc_sigmask (128 bytes = 16 × uint64)
	rucPadCgo    = 168 // __pad_cgo_0 (8 bytes)
	rucMcontext  = 176 // uc_mcontext.sc_regs (32 regs × 8 = 256 bytes)

	// sc_regs layout within uc_mcontext:
	//   [0]  = pc
	//   [1]  = ra (x1)
	//   [2]  = sp (x2)
	//   ...
	//   [31] = t6 (x31)
	rscPC = rucMcontext       // offset 176: program counter
	rscRA = rucMcontext + 8   // offset 184: x1 (ra)
	// General pattern: rscRegs + i*8 = x[i] for i=1..31
)

// BuildSignalFrame builds a signal frame on the thread's gsignal stack
// and modifies the ThreadContext to enter sigtramp.
//
// faultAddr: the faulting address (for hardware faults; 0 for software signals)
// siCode: the si_code value for siginfo (e.g., SEGV_MAPERR; 0 for software signals)
//
// The signal frame is in userspace memory, so all writes go through the kernel
// linear map (VA→PA walk + KernelMMIOOffset) since direct userspace access is
// blocked by SUM=0 on RISC-V.
//
// RISC-V register mapping:
//   - A0 (X10) = signal number
//   - A1 (X11) = pointer to siginfo
//   - A2 (X12) = pointer to ucontext
//   - RA (X1)  = sigreturn trampoline (link register)
//   - SP (X2)  = signal stack
//   - SEPC     = handler address
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
	frameSP := signalSP - uint64(riscv64SignalFrameSize)
	frameSP &= ^uint64(0xF) // 16-byte align

	// NOTE: Signal stack pages must already be backed by physical memory.
	// The sigaltstack runtime overlay pre-touches the top 2 pages to trigger
	// demand paging in userspace before the kernel needs to write the frame.

	// Pointers to siginfo and ucontext within the frame
	siginfoAddr := frameSP
	uctxAddr := frameSP + uint64(riscv64SiginfoSize)

	// Zero the entire frame through kernel mapping
	kmem.ZeroUserMemoryWithL0(uintptr(frameSP), uintptr(riscv64SignalFrameSize), l0PA)

	// --- Populate siginfo ---
	siPtr := uintptr(siginfoAddr)
	kmem.WriteUserInt32WithL0(siPtr, int32(signum), l0PA)   // si_signo
	kmem.WriteUserInt32WithL0(siPtr+4, 0, l0PA)             // si_errno
	kmem.WriteUserInt32WithL0(siPtr+8, thread.SignalSiCode, l0PA)        // si_code (e.g., SEGV_MAPERR)
	// si_addr at offset 16 (after 4 bytes padding for alignment)
	kmem.WriteUserUint64WithL0(siPtr+16, thread.SignalFaultAddr, l0PA) // si_addr (fault address)

	// --- Populate ucontext ---
	ucPtr := uintptr(uctxAddr)

	// Save PC (SEPC) at sc_regs[0]
	kmem.WriteUserUint64WithL0(ucPtr+rscPC, thread.Context.SEPC, l0PA)

	// Save general-purpose registers x1..x31 at sc_regs[1..31]
	// sc_regs[i] maps to ThreadContext.X[i]
	for i := 1; i <= 31; i++ {
		kmem.WriteUserUint64WithL0(ucPtr+rucMcontext+uintptr(i)*8, thread.Context.X[i], l0PA)
	}

	// uc_stack: record the signal stack info
	kmem.WriteUserUint64WithL0(ucPtr+rucStackSP, thread.SignalStackBase, l0PA)     // ss_sp
	kmem.WriteUserInt32WithL0(ucPtr+rucStackFlags, _SS_ONSTACK, l0PA)              // ss_flags
	kmem.WriteUserUint64WithL0(ucPtr+rucStackSize, thread.SignalStackSize, l0PA)   // ss_size

	// Store ucontext address for rt_sigreturn to find later
	thread.SignalUctxAddr = uctxAddr

	// --- Modify ThreadContext to enter sigtramp ---
	// A0 (X10) = signal number
	thread.Context.X[10] = uint64(signum)
	// A1 (X11) = pointer to siginfo
	thread.Context.X[11] = siginfoAddr
	// A2 (X12) = pointer to ucontext
	thread.Context.X[12] = uctxAddr
	// SP (X2) = signal stack (below the frame we just built)
	thread.Context.X[2] = frameSP
	// PC (SEPC) = handler address
	thread.Context.SEPC = action.Handler
	// RA (X1) = sigreturn trampoline
	// Only use action.Restorer if SA_RESTORER flag is set.
	// On RISC-V, the Go runtime does NOT set SA_RESTORER, so the sa_restorer
	// field contains garbage (e.g., 0xFFFFFFFFFFFFFFFF from the signal mask).
	if action.Flags&_SA_RESTORER != 0 && action.Restorer != 0 {
		thread.Context.X[1] = action.Restorer
	} else {
		thread.Context.X[1] = uint64(sigreturnTrampolinePC)
	}

	// FP (X8/s0) = 0 for clean frame
	thread.Context.X[8] = 0
	// SSTATUS remains unchanged (same privilege, same interrupt state)
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

	// Restore PC from sc_regs[0]
	if val, ok := kmem.ReadUserUint64WithL0(ucPtr+rscPC, l0PA); ok {
		t.Context.SEPC = val
	}

	// Restore x1..x31 from sc_regs[1..31]
	for i := 1; i <= 31; i++ {
		if val, ok := kmem.ReadUserUint64WithL0(ucPtr+rucMcontext+uintptr(i)*8, l0PA); ok {
			t.Context.X[i] = val
		}
	}

	// Note: SSTATUS is NOT restored from the signal frame — the kernel
	// controls privilege level, not userspace.

	t.SignalUctxAddr = 0 // Consumed
}
