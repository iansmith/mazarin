package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/serial"
)

// Signal constants matching Linux definitions.
const (
	sigNSIG      = 65 // Linux signal count (1-64 + sentinel)
	siKernel     = 0x80
	ssDisable    = 2
)

// signalAction mirrors main.SignalAction for use in ksyscall.
// Layout matches Go runtime's sigactiont struct.
type signalAction struct {
	Handler  uint64
	Flags    uint64
	Restorer uint64
	Mask     uint64
}

// SyscallRtSigaction records signal handlers installed by the Go runtime.
//
// Linux signature: rt_sigaction(int sig, const struct sigaction *act,
//
//	struct sigaction *oact, size_t sigsetsize)
//
// The Go runtime's sigactiont struct layout (all architectures):
//
//	sa_handler  uintptr   (offset 0)
//	sa_flags    uint64    (offset 8)
//	sa_restorer uintptr   (offset 16)
//	sa_mask     uint64    (offset 24)
//
// Total: 32 bytes
//
//go:nosplit
func SyscallRtSigaction(signum, actPtr, oactPtr, sigsetsize, _, _ uint64) int64 {
	sig := int(signum)
	if sig <= 0 || sig >= sigNSIG {
		return -22 // EINVAL
	}

	// If oactPtr is non-nil, copy the old action to it.
	// Use safe accessors — direct dereference fails on RISC-V (SUM=0).
	if oactPtr != 0 {
		oldAction := GetSignalAction(sig)
		addr := uintptr(oactPtr)
		kmem.WriteUserUint64(addr, oldAction.Handler)
		kmem.WriteUserUint64(addr+8, oldAction.Flags)
		kmem.WriteUserUint64(addr+16, oldAction.Restorer)
		kmem.WriteUserUint64(addr+24, oldAction.Mask)
	}

	// If actPtr is non-nil, install the new action.
	// Use safe accessors — direct dereference fails on RISC-V (SUM=0).
	if actPtr != 0 {
		addr := uintptr(actPtr)
		handler, _ := kmem.ReadUserUint64(addr)
		flags, _ := kmem.ReadUserUint64(addr + 8)
		restorer, _ := kmem.ReadUserUint64(addr + 16)
		mask, _ := kmem.ReadUserUint64(addr + 24)

		SetSignalAction(sig, handler, flags, restorer, mask)
	}

	return 0
}

// SyscallSigaltstack manages the alternate signal stack for the current thread.
//
// Linux signature: sigaltstack(const stack_t *ss, stack_t *oss)
//
// stack_t layout:
//
//	ss_sp:    *byte   (offset 0, 8 bytes)
//	ss_flags: int32   (offset 8, 4 bytes)
//	pad:      [4]byte (offset 12)
//	ss_size:  uintptr (offset 16, 8 bytes)
//
//go:nosplit
func SyscallSigaltstack(newPtr, oldPtr, _, _, _, _ uint64) int64 {
	tPtr := GetCurrentThread()
	if tPtr == 0 {
		return 0 // No thread context, ignore
	}

	// Return current state if oldPtr is non-nil.
	// Use safe accessors — direct dereference fails on RISC-V (SUM=0).
	if oldPtr != 0 {
		base, _, size := GetThreadSignalStack(tPtr)
		addr := uintptr(oldPtr)
		if base != 0 {
			kmem.WriteUserUint64(addr, base)      // ss_sp
			kmem.WriteUserUint32(addr+8, 0)       // ss_flags (active)
			kmem.WriteUserUint64(addr+16, size)    // ss_size
		} else {
			kmem.WriteUserUint64(addr, 0)                    // ss_sp
			kmem.WriteUserUint32(addr+8, uint32(ssDisable))  // SS_DISABLE
			kmem.WriteUserUint64(addr+16, 0)                 // ss_size
		}
	}

	// Install new signal stack if newPtr is non-nil.
	// Use safe accessors — direct dereference fails on RISC-V (SUM=0).
	if newPtr != 0 {
		addr := uintptr(newPtr)
		sp, _ := kmem.ReadUserUint64(addr)
		flagsVal, _ := kmem.ReadUserUint32(addr + 8)
		size, _ := kmem.ReadUserUint64(addr + 16)

		if flagsVal == uint32(ssDisable) {
			SetThreadSignalStack(tPtr, 0, 0, 0)
		} else {
			SetThreadSignalStack(tPtr, sp, sp+uint64(size), uint64(size))
		}
	}

	return 0
}

// SyscallTgkill sends a signal to a specific thread.
// Linux: tgkill(tgid, tid, sig)
//
// For kmazarin: sets a pending signal bit on the target thread.
// The signal is delivered when the thread is next scheduled.
// Validates that tgid matches the target thread's PID (same process).
//
//go:nosplit
func SyscallTgkill(tgid, tid, sig, _, _, _ uint64) int64 {
	signum := int(sig)
	if signum <= 0 || signum >= sigNSIG {
		return -22 // EINVAL
	}

	// Find the target thread by TID.
	targetThread := ThreadLookupByTID(int32(tid))
	if targetThread == 0 {
		return -3 // ESRCH — no such process/thread
	}

	// Validate tgid matches target thread's PID (cross-priest signal rejected)
	if tgid != 0 {
		targetPID := GetThreadPID(targetThread)
		if int16(tgid) != targetPID {
			return -3 // ESRCH — tgid mismatch
		}
	}

	// Debug: trace tgkill calls
	serial.RawUARTPuts("[TK] sig=")
	serial.RawUARTDecimal(sig)
	serial.RawUARTPuts(" tid=")
	serial.RawUARTDecimal(tid)
	serial.RawUARTPuts(" tgid=")
	serial.RawUARTDecimal(tgid)
	serial.PollWrite('\n')

	// Set the pending signal bit.
	SetThreadPendingSignal(targetThread, signum)

	return 0
}

// SyscallRtSigreturn restores thread state from signal frame.
//
// Called when the signal handler returns via the sigreturn trampoline.
// This reads the saved ucontext from the thread's SignalUctxAddr, restores
// all registers back into the thread's Context, clears InSignalHandler,
// and sets SigreturnPending so the exception return path loads the restored
// context instead of the normal exception frame.
//
// The return value is ignored — the thread resumes at the pre-signal PC.
//
//go:nosplit
func SyscallRtSigreturn(_, _, _, _, _, _ uint64) int64 {
	tPtr := GetCurrentThread()
	if tPtr == 0 {
		return -22 // EINVAL
	}

	serial.RawUARTPuts("[SR] sigreturn\n")

	// RestoreFromSignalFrame reads ucontext, copies regs to thread.Context,
	// clears InSignalHandler, and sets SigreturnPending = 1.
	RestoreFromSignalFrame(tPtr)

	return 0
}
