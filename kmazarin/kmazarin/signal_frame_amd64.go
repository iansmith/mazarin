//go:build amd64

package main

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/serial"
)

// x86_64 signal frame layout constants.
// These match the Linux kernel's ucontext/sigcontext structs for x86_64.
const (
	amd64UcontextSize    = 936 // sizeof(ucontext_t)
	amd64SiginfoSize     = 128 // sizeof(siginfo_t)
	amd64SignalFrameSize = amd64UcontextSize + amd64SiginfoSize + 16 // +16 alignment

	// Offsets within ucontext_t
	amd64UcFlags   = 0  // uc_flags (uint64)
	amd64UcLink    = 8  // uc_link (uint64)
	amd64UcStack   = 16 // uc_stack (ss_sp=16, ss_flags=24, ss_size=32)
	amd64UcSigctx  = 40 // uc_mcontext / sigcontext starts here
	amd64UcSigmask = 296 // uc_sigmask (128 bytes)

	// Offsets within sigcontext (relative to amd64UcSigctx)
	scR8     = 0
	scR9     = 8
	scR10    = 16
	scR11    = 24
	scR12    = 32
	scR13    = 40
	scR14    = 48
	scR15    = 56
	scRDI    = 64
	scRSI    = 72
	scRBP    = 80
	scRBX    = 88
	scRDX    = 96
	scRAX    = 104
	scRCX    = 112
	scRSP    = 120
	scRIP    = 128
	scEFLAGS = 136
	scCS     = 144 // uint16
	scGS     = 146 // uint16
	scFS     = 148 // uint16
	// pad at 150
	scERR    = 152
	scTRAPNO = 160
	scOLDMASK = 168
	scCR2     = 176
	scFPSTATE = 184 // pointer to fpstate
)

// BuildSignalFrame builds a signal frame on the thread's gsignal stack
// and modifies the ThreadContext to enter sigtramp.
//
// The signal frame is in userspace memory, so all writes go through the kernel
// linear map (VA→PA walk + KernelMMIOOffset) since direct userspace access is
// blocked by SMAP on x86_64.
//
//go:nosplit
func BuildSignalFrame(thread *Thread, signum int, action *SignalAction) {
	signalSP := thread.SignalSP
	if signalSP == 0 {
		serial.RawUARTPuts("[signal] ERROR: no signal stack for TID=")
		serial.RawUARTHex64(uint64(thread.TID))
		serial.RawUARTPuts("\r\n")
		return
	}

	l0PA := thread.PageTableL0PA

	// Allocate space on signal stack (grows downward)
	frameSP := signalSP - uint64(amd64SignalFrameSize)
	frameSP &= ^uint64(0xF) // 16-byte align

	// NOTE: Signal stack pages must already be backed by physical memory.
	// The sigaltstack runtime overlay pre-touches the top 2 pages to trigger
	// demand paging in userspace before the kernel needs to write the frame.

	// Pointers to siginfo and ucontext within the frame
	siginfoAddr := frameSP
	uctxAddr := frameSP + uint64(amd64SiginfoSize)

	// Zero the entire frame through kernel mapping
	kmem.ZeroUserMemoryWithL0(uintptr(frameSP), uintptr(amd64SignalFrameSize), l0PA)

	// --- Populate siginfo ---
	siPtr := uintptr(siginfoAddr)
	kmem.WriteUserInt32WithL0(siPtr, int32(signum), l0PA)  // si_signo
	kmem.WriteUserInt32WithL0(siPtr+4, 0, l0PA)            // si_errno
	kmem.WriteUserInt32WithL0(siPtr+8, _SI_KERNEL, l0PA)   // si_code

	// --- Populate ucontext ---
	ucPtr := uintptr(uctxAddr)

	// Save registers from ThreadContext into sigcontext
	scBase := ucPtr + amd64UcSigctx
	kmem.WriteUserUint64WithL0(scBase+scR8, thread.Context.R8, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scR9, thread.Context.R9, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scR10, thread.Context.R10, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scR11, thread.Context.R11, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scR12, thread.Context.R12, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scR13, thread.Context.R13, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scR14, thread.Context.R14, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scR15, thread.Context.R15, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scRDI, thread.Context.RDI, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scRSI, thread.Context.RSI, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scRBP, thread.Context.RBP, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scRBX, thread.Context.RBX, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scRDX, thread.Context.RDX, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scRAX, thread.Context.RAX, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scRCX, thread.Context.RCX, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scRSP, thread.Context.RSP, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scRIP, thread.Context.RIP, l0PA)
	kmem.WriteUserUint64WithL0(scBase+scEFLAGS, thread.Context.RFLAGS, l0PA)
	kmem.WriteUserUint16WithL0(scBase+scCS, uint16(thread.Context.CS), l0PA)

	// uc_stack: record the signal stack info
	kmem.WriteUserUint64WithL0(ucPtr+amd64UcStack, thread.SignalStackBase, l0PA)      // ss_sp
	kmem.WriteUserInt32WithL0(ucPtr+amd64UcStack+8, _SS_ONSTACK, l0PA)                // ss_flags
	kmem.WriteUserUint64WithL0(ucPtr+amd64UcStack+16, thread.SignalStackSize, l0PA)   // ss_size

	// Store ucontext address for rt_sigreturn to find later
	thread.SignalUctxAddr = uctxAddr

	// --- Modify ThreadContext to enter sigtramp ---
	// System V calling convention: RDI=sig, RSI=siginfo, RDX=ucontext
	thread.Context.RDI = uint64(signum)
	thread.Context.RSI = siginfoAddr
	thread.Context.RDX = uctxAddr

	// Push restorer address on signal stack as return address
	// (sigtramp will RET to this address, which calls rt_sigreturn)
	restorerAddr := action.Restorer
	if restorerAddr == 0 {
		restorerAddr = uint64(sigreturnTrampolinePC)
	}
	// Place return address below the frame (x86_64 CALL convention)
	retAddrSP := frameSP - 8
	kmem.WriteUserUint64WithL0(uintptr(retAddrSP), restorerAddr, l0PA)

	thread.Context.RSP = retAddrSP
	thread.Context.RIP = action.Handler

	// RBP = 0 for clean frame
	thread.Context.RBP = 0
	// R14 (g), FSBase remain as-is — sigtrampgo reads g from TLS
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

	// Restore registers from sigcontext
	scBase := ucPtr + amd64UcSigctx
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scR8, l0PA); ok {
		t.Context.R8 = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scR9, l0PA); ok {
		t.Context.R9 = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scR10, l0PA); ok {
		t.Context.R10 = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scR11, l0PA); ok {
		t.Context.R11 = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scR12, l0PA); ok {
		t.Context.R12 = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scR13, l0PA); ok {
		t.Context.R13 = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scR14, l0PA); ok {
		t.Context.R14 = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scR15, l0PA); ok {
		t.Context.R15 = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scRDI, l0PA); ok {
		t.Context.RDI = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scRSI, l0PA); ok {
		t.Context.RSI = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scRBP, l0PA); ok {
		t.Context.RBP = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scRBX, l0PA); ok {
		t.Context.RBX = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scRDX, l0PA); ok {
		t.Context.RDX = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scRAX, l0PA); ok {
		t.Context.RAX = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scRCX, l0PA); ok {
		t.Context.RCX = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scRSP, l0PA); ok {
		t.Context.RSP = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scRIP, l0PA); ok {
		t.Context.RIP = v
	}
	if v, ok := kmem.ReadUserUint64WithL0(scBase+scEFLAGS, l0PA); ok {
		t.Context.RFLAGS = v
	}

	t.SignalUctxAddr = 0 // Consumed
}
