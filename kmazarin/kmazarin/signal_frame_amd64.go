//go:build amd64

package main

import (
	"unsafe"

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
//go:nosplit
func BuildSignalFrame(thread *Thread, signum int, action *SignalAction) {
	signalSP := thread.SignalSP
	if signalSP == 0 {
		serial.RawUARTPuts("[signal] ERROR: no signal stack for TID=")
		serial.RawUARTHex64(uint64(thread.TID))
		serial.RawUARTPuts("\r\n")
		return
	}

	// Allocate space on signal stack (grows downward)
	frameSP := signalSP - uint64(amd64SignalFrameSize)
	frameSP &= ^uint64(0xF) // 16-byte align

	// Pointers to siginfo and ucontext within the frame
	siginfoAddr := frameSP
	uctxAddr := frameSP + uint64(amd64SiginfoSize)

	// Zero the entire frame
	zeroMemory(unsafe.Pointer(uintptr(frameSP)), uintptr(amd64SignalFrameSize))

	// --- Populate siginfo ---
	siPtr := uintptr(siginfoAddr)
	*(*int32)(unsafe.Pointer(siPtr)) = int32(signum)   // si_signo
	*(*int32)(unsafe.Pointer(siPtr + 4)) = 0           // si_errno
	*(*int32)(unsafe.Pointer(siPtr + 8)) = _SI_KERNEL  // si_code

	// --- Populate ucontext ---
	ucPtr := uintptr(uctxAddr)

	// Save registers from ThreadContext into sigcontext
	scBase := ucPtr + amd64UcSigctx
	*(*uint64)(unsafe.Pointer(scBase + scR8)) = thread.Context.R8
	*(*uint64)(unsafe.Pointer(scBase + scR9)) = thread.Context.R9
	*(*uint64)(unsafe.Pointer(scBase + scR10)) = thread.Context.R10
	*(*uint64)(unsafe.Pointer(scBase + scR11)) = thread.Context.R11
	*(*uint64)(unsafe.Pointer(scBase + scR12)) = thread.Context.R12
	*(*uint64)(unsafe.Pointer(scBase + scR13)) = thread.Context.R13
	*(*uint64)(unsafe.Pointer(scBase + scR14)) = thread.Context.R14
	*(*uint64)(unsafe.Pointer(scBase + scR15)) = thread.Context.R15
	*(*uint64)(unsafe.Pointer(scBase + scRDI)) = thread.Context.RDI
	*(*uint64)(unsafe.Pointer(scBase + scRSI)) = thread.Context.RSI
	*(*uint64)(unsafe.Pointer(scBase + scRBP)) = thread.Context.RBP
	*(*uint64)(unsafe.Pointer(scBase + scRBX)) = thread.Context.RBX
	*(*uint64)(unsafe.Pointer(scBase + scRDX)) = thread.Context.RDX
	*(*uint64)(unsafe.Pointer(scBase + scRAX)) = thread.Context.RAX
	*(*uint64)(unsafe.Pointer(scBase + scRCX)) = thread.Context.RCX
	*(*uint64)(unsafe.Pointer(scBase + scRSP)) = thread.Context.RSP
	*(*uint64)(unsafe.Pointer(scBase + scRIP)) = thread.Context.RIP
	*(*uint64)(unsafe.Pointer(scBase + scEFLAGS)) = thread.Context.RFLAGS
	*(*uint16)(unsafe.Pointer(scBase + scCS)) = uint16(thread.Context.CS)

	// uc_stack: record the signal stack info
	*(*uint64)(unsafe.Pointer(ucPtr + amd64UcStack)) = thread.SignalStackBase     // ss_sp
	*(*int32)(unsafe.Pointer(ucPtr + amd64UcStack + 8)) = _SS_ONSTACK            // ss_flags
	*(*uint64)(unsafe.Pointer(ucPtr + amd64UcStack + 16)) = thread.SignalStackSize // ss_size

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
	*(*uint64)(unsafe.Pointer(uintptr(retAddrSP))) = restorerAddr

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

	// Restore registers from sigcontext
	scBase := ucPtr + amd64UcSigctx
	t.Context.R8 = *(*uint64)(unsafe.Pointer(scBase + scR8))
	t.Context.R9 = *(*uint64)(unsafe.Pointer(scBase + scR9))
	t.Context.R10 = *(*uint64)(unsafe.Pointer(scBase + scR10))
	t.Context.R11 = *(*uint64)(unsafe.Pointer(scBase + scR11))
	t.Context.R12 = *(*uint64)(unsafe.Pointer(scBase + scR12))
	t.Context.R13 = *(*uint64)(unsafe.Pointer(scBase + scR13))
	t.Context.R14 = *(*uint64)(unsafe.Pointer(scBase + scR14))
	t.Context.R15 = *(*uint64)(unsafe.Pointer(scBase + scR15))
	t.Context.RDI = *(*uint64)(unsafe.Pointer(scBase + scRDI))
	t.Context.RSI = *(*uint64)(unsafe.Pointer(scBase + scRSI))
	t.Context.RBP = *(*uint64)(unsafe.Pointer(scBase + scRBP))
	t.Context.RBX = *(*uint64)(unsafe.Pointer(scBase + scRBX))
	t.Context.RDX = *(*uint64)(unsafe.Pointer(scBase + scRDX))
	t.Context.RAX = *(*uint64)(unsafe.Pointer(scBase + scRAX))
	t.Context.RCX = *(*uint64)(unsafe.Pointer(scBase + scRCX))
	t.Context.RSP = *(*uint64)(unsafe.Pointer(scBase + scRSP))
	t.Context.RIP = *(*uint64)(unsafe.Pointer(scBase + scRIP))
	t.Context.RFLAGS = *(*uint64)(unsafe.Pointer(scBase + scEFLAGS))

	t.SignalUctxAddr = 0 // Consumed
}
