package main

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/proc"
	"sync/atomic"
	"unsafe"
)

// Signal constants matching Linux definitions.
const (
	_NSIG      = 65 // Linux signal count (1-64 + sentinel)
	_SIGURG    = 23 // Async preemption signal (used by Go runtime GC STW)
	_SIGPROF   = 27 // Profiling signal (stub for now)
	_SI_KERNEL = 0x80

	// Hardware fault signals
	_SIGILL  = 4  // Illegal instruction
	_SIGTRAP = 5  // Trace/breakpoint trap
	_SIGABRT = 6  // Abort
	_SIGBUS  = 7  // Bus error (misaligned access)
	_SIGFPE  = 8  // Floating point exception
	_SIGSEGV = 11 // Segmentation fault

	// SIGSEGV si_code values (from Linux asm-generic/siginfo.h)
	_SEGV_MAPERR = 1 // Address not mapped to object
	_SEGV_ACCERR = 2 // Invalid permissions for mapped object

	_SA_SIGINFO  = 0x00000004
	_SA_ONSTACK  = 0x08000000
	_SA_RESTORER = 0x04000000
	_SA_RESTART  = 0x10000000

	_SIG_BLOCK   = 0
	_SIG_UNBLOCK = 1
	_SIG_SETMASK = 2

	_SS_DISABLE = 2
	_SS_ONSTACK = 1
)

// SignalAction records a registered signal handler.
// Layout matches Go runtime's sigactiont struct:
//
//	sa_handler  uintptr  (offset 0)
//	sa_flags    uint64   (offset 8)
//	sa_restorer uintptr  (offset 16)
//	sa_mask     uint64   (offset 24)
type SignalAction struct {
	Handler  uint64 // Function pointer (sa_handler / sa_sigaction)
	Flags    uint64 // SA_SIGINFO | SA_ONSTACK | SA_RESTART | SA_RESTORER
	Restorer uint64 // sa_restorer (sigreturn trampoline address)
	Mask     uint64 // sa_mask (signals blocked during handler)
}

// signalActions is the global signal action table (kernel threads only).
// Index 0 is unused (signal numbers are 1-based).
// Userspace threads use per-shepherd tables in proc.Shepherd.SignalActions.
var signalActions [_NSIG]SignalAction

// Signal delivery counters — updated atomically from nosplit context,
// printed from non-nosplit diagnostic functions.
var signalDeliverCount uint64 // Total signals delivered (frame built)
var signalDeliverLastSig uint64 // Last signal number delivered
var signalDeliverLastTID uint64 // Last TID that received a signal
var signalDeliverLastHandler uint64 // Last handler address used

// sigreturnTrampolinePC holds the address of the kmazarin sigreturn trampoline.
// Set during signal init from getSigreturnTrampolineAddr().
var sigreturnTrampolinePC uintptr

// InitSignals initializes signal infrastructure.
// Called during kmazarin startup after assembly functions are available.
func InitSignals() {
	sigreturnTrampolinePC = getSigreturnTrampolineAddr()
	kernelTramp := sigreturnTrampolinePC

	// On RISC-V, allocate a user-accessible vDSO page for the sigreturn
	// trampoline and update sigreturnTrampolinePC to the user VA.
	// U-mode can't execute kernel pages on RISC-V (PTE.U=0).
	initSigreturnVDSO()

	_ = kernelTramp
}

// GetSignalAction returns the signal action for the given signal number.
// For userspace threads (PID > 0), reads from the shepherd's per-process table.
// For kernel threads, reads from the global table.
//
//go:nosplit
func GetSignalAction(sig int) SignalAction {
	t := GetCurrentThread()
	if t != nil && t.PID > 0 {
		if p := proc.FindShepherdBySID(t.PID); p != nil {
			sa := &p.SignalActions[sig]
			return SignalAction{Handler: sa.Handler, Flags: sa.Flags,
				Restorer: sa.Restorer, Mask: sa.Mask}
		}
		// Userspace thread whose shepherd has been torn down — fail closed
		// instead of leaking the kernel's signalActions table to userspace.
		return SignalAction{}
	}
	return signalActions[sig]
}

// GetSignalActionForThread returns the signal action for a specific thread's shepherd.
// Used by DeliverPendingSignal where the target thread may differ from the current thread.
//
//go:nosplit
func GetSignalActionForThread(thread *Thread, sig int) SignalAction {
	if thread != nil && thread.PID > 0 {
		if p := proc.FindShepherdBySID(thread.PID); p != nil {
			sa := &p.SignalActions[sig]
			return SignalAction{Handler: sa.Handler, Flags: sa.Flags,
				Restorer: sa.Restorer, Mask: sa.Mask}
		}
		// Userspace thread whose shepherd has been torn down — fail closed
		// instead of leaking the kernel's signalActions table to userspace.
		return SignalAction{}
	}
	return signalActions[sig]
}

// SetSignalAction installs a signal action for the given signal number.
// For userspace threads (PID > 0), writes to the shepherd's per-process table.
// For kernel threads, writes to the global table.
//
//go:nosplit
func SetSignalAction(sig int, sa *SignalAction) {
	t := GetCurrentThread()
	if t != nil && t.PID > 0 {
		if p := proc.FindShepherdBySID(t.PID); p != nil {
			p.SignalActions[sig] = proc.ShepherdSignalAction{
				Handler: sa.Handler, Flags: sa.Flags,
				Restorer: sa.Restorer, Mask: sa.Mask,
			}
			return
		}
		// Userspace thread whose shepherd has been torn down — drop the
		// write instead of clobbering the kernel's signalActions table.
		return
	}
	signalActions[sig] = *sa
}

// readSignalAction reads a sigactiont struct from memory into a SignalAction.
//
//go:nosplit
func readSignalAction(addr uintptr, sa *SignalAction) {
	sa.Handler = *(*uint64)(unsafe.Pointer(addr))
	sa.Flags = *(*uint64)(unsafe.Pointer(addr + 8))
	sa.Restorer = *(*uint64)(unsafe.Pointer(addr + 16))
	sa.Mask = *(*uint64)(unsafe.Pointer(addr + 24))
}

// writeSignalAction writes a SignalAction to memory as a sigactiont struct.
//
//go:nosplit
func writeSignalAction(addr uintptr, sa *SignalAction) {
	*(*uint64)(unsafe.Pointer(addr)) = sa.Handler
	*(*uint64)(unsafe.Pointer(addr + 8)) = sa.Flags
	*(*uint64)(unsafe.Pointer(addr + 16)) = sa.Restorer
	*(*uint64)(unsafe.Pointer(addr + 24)) = sa.Mask
}

// atomicOrUint64 atomically ORs a value into a uint64.
//
//go:nosplit
func atomicOrUint64(addr *uint64, val uint64) {
	for {
		old := atomic.LoadUint64(addr)
		if atomic.CompareAndSwapUint64(addr, old, old|val) {
			return
		}
	}
}

// atomicAndUint64 atomically ANDs a value into a uint64.
//
//go:nosplit
func atomicAndUint64(addr *uint64, val uint64) {
	for {
		old := atomic.LoadUint64(addr)
		if atomic.CompareAndSwapUint64(addr, old, old&val) {
			return
		}
	}
}

// GetCurrentThreadPtr returns a raw pointer to the current Thread struct.
// Used by assembly for offset-based field access (e.g., SigreturnPending check).
//
//go:nosplit
//go:noinline
func GetCurrentThreadPtr() uintptr {
	t := GetCurrentThread()
	if t == nil {
		return 0
	}
	return uintptr(unsafe.Pointer(t))
}

// DeliverPendingSignal checks for pending signals and sets up a signal frame
// for the highest-priority pending signal.
//
// PRECONDITION: thread.PendingSignals != 0
// PRECONDITION: thread.InSignalHandler == 0
// PRECONDITION: scheduler lock held, IRQs disabled
//
//go:nosplit
func DeliverPendingSignal(thread *Thread) {
	pending := atomic.LoadUint64(&thread.PendingSignals)
	if pending == 0 {
		return
	}

	// Find lowest-numbered pending signal (highest priority)
	var signum int
	for i := 0; i < 64; i++ {
		if pending&(1<<uint(i)) != 0 {
			signum = i + 1 // Signals are 1-based
			break
		}
	}

	// Look up action from the TARGET thread's shepherd, not the current thread
	action := GetSignalActionForThread(thread, signum)

	if action.Handler == 0 {
		// No handler — clear the signal and return
		atomicAndUint64(&thread.PendingSignals, ^(uint64(1) << uint(signum-1)))
		return
	}

	// Clear this signal from pending (before delivery)
	atomicAndUint64(&thread.PendingSignals, ^(uint64(1) << uint(signum-1)))

	// Record delivery event
	atomic.StoreUint64(&signalDeliverLastSig, uint64(signum))
	atomic.StoreUint64(&signalDeliverLastTID, uint64(thread.TID))
	atomic.StoreUint64(&signalDeliverLastHandler, action.Handler)
	atomic.AddUint64(&signalDeliverCount, 1)

	// Build the signal frame on the thread's signal stack
	// and modify the ThreadContext to enter sigtramp.
	// Software signals (tgkill, etc.) have no fault address or si_code.
	thread.SignalFaultAddr = 0
	thread.SignalSiCode = 0
	BuildSignalFrame(thread, signum, &action)

	thread.InSignalHandler = 1
}

// SignalSelfTest is a no-op retained for call-site compatibility.
func SignalSelfTest(label string) {
}

// SignalDeliveryStats is a no-op retained for call-site compatibility.
func SignalDeliveryStats() {
}

// HandleUnhandledException handles a userspace exception that was not resolved
// by the page fault handler. Maps the hardware exception to a Linux signal,
// delivers it to a registered handler, or terminates the shepherd.
//
// excInfo: architecture-specific exception info (ESR on ARM64, vector on x86_64, scause on RISC-V)
// faultAddr: faulting address (FAR on ARM64, CR2 on x86_64, stval on RISC-V)
// faultPC: faulting instruction PC (ELR on ARM64, RIP on x86_64, SEPC on RISC-V)
//
// Returns: 0 if signal was queued (caller should return via normal exception path,
// which will load the modified ThreadContext that now points to the signal handler).
// Non-zero: pointer to next ThreadContext (shepherd was killed, load this context).
func HandleUnhandledException(excInfo, faultAddr, faultPC uint64) uintptr {
	t := GetCurrentThread()
	if t == nil {
		return 0
	}

	// Map hardware exception to signal number (arch-specific)
	signum := mapExceptionToSignal(excInfo)
	if signum == 0 {
		// Unknown exception type — kill the shepherd
		signum = _SIGSEGV
	}

	// If we're already inside a signal handler and take ANOTHER hardware fault,
	// that's a double-fault (the handler itself crashed). Kill the shepherd
	// immediately — re-delivering the signal would loop forever.
	if t.InSignalHandler != 0 {
		pid := t.PID
		PrintProcessDeathDiag(pid, signum, faultAddr, faultPC)
		return TerminateShepherd(pid, int64(128+signum))
	}

	// Look up signal handler
	action := GetSignalActionForThread(t, signum)


	if action.Handler != 0 {
		// Handler registered — build signal frame so the thread enters the
		// handler on the next exception return.
		//
		// We return the current thread's Context pointer (not 0) because the
		// assembly exception return path (el0_return / iretq / sret) restores
		// from the exception frame on the stack, NOT from ThreadContext.
		// BuildSignalFrame modifies ThreadContext, so we must tell the assembly
		// to load from the modified ThreadContext instead of the stale exception frame.

		// NOTE: SaveContextFromFrame is now called from assembly before this
		// function, so ThreadContext already has the correct SP, LR, PC, and
		// all registers from the exception frame. No manual SetPC needed.

		klog.Errf("[SIG] delivering sig=%d PC=0x%x addr=0x%x\n", signum, faultPC, faultAddr)

		// Map hardware exception info to Linux si_code (e.g., SEGV_MAPERR)
		t.SignalFaultAddr = faultAddr
		t.SignalSiCode = mapExceptionToSICode(signum, excInfo)
		BuildSignalFrame(t, signum, &action)
		t.InSignalHandler = 1
		return uintptr(unsafe.Pointer(&t.Context))
	}

	// No handler — kill the shepherd
	pid := t.PID
	PrintProcessDeathDiag(pid, signum, faultAddr, faultPC)
	return TerminateShepherd(pid, int64(128+signum))
}

// handleUnhandledExceptionInternal is the ABI0-compatible wrapper.
// Called from assembly via HandleUnhandledExceptionAsm tail-call stub.
func handleUnhandledExceptionInternal(excInfo, faultAddr, faultPC uint64) uint64 {
	return uint64(HandleUnhandledException(excInfo, faultAddr, faultPC))
}

// PrintProcessDeathDiag prints diagnostic info when a process is killed by a signal.
// Output goes to both serial UART and the soft IRQ console ring (linux shepherd display).
// Uses only nosplit-safe functions.
//
//go:nosplit
func PrintProcessDeathDiag(pid ShepherdId, signum int, faultAddr, faultPC uint64) {
	dualPuts("[KILLED] shepherd PID=")
	dualDecimal(uint64(pid))
	dualPuts(" ")
	dualPuts(signalName(signum))
	dualPuts("(")
	dualDecimal(uint64(signum))
	dualPuts(") PC=0x")
	dualHex64(faultPC)
	dualPuts(" addr=0x")
	dualHex64(faultAddr)
	dualPuts("\r\n")
}

// dualPuts writes a string to the soft IRQ console ring for display by
// the linux shepherd. Output goes through the ring only — no direct UART.
//
//go:nosplit
func dualPuts(s string) {
	for i := 0; i < len(s); i++ {
		PushByteToUartRing(2, s[i]) // fd=2 (stderr) for red text in linux shepherd
	}
}

// dualDecimal writes a decimal number to the console ring.
//
//go:nosplit
func dualDecimal(v uint64) {
	if v == 0 {
		PushByteToUartRing(2, '0')
		return
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	for i < len(buf) {
		PushByteToUartRing(2, buf[i])
		i++
	}
}

// dualHex64 writes a 64-bit hex value to the console ring.
//
//go:nosplit
func dualHex64(v uint64) {
	for i := 60; i >= 0; i -= 4 {
		nibble := (v >> uint(i)) & 0xF
		if nibble < 10 {
			PushByteToUartRing(2, byte('0'+nibble))
		} else {
			PushByteToUartRing(2, byte('A'+nibble-10))
		}
	}
}

// signalName returns a human-readable name for common signals.
//
//go:nosplit
func signalName(sig int) string {
	switch sig {
	case _SIGILL:
		return "SIGILL"
	case _SIGTRAP:
		return "SIGTRAP"
	case _SIGABRT:
		return "SIGABRT"
	case _SIGBUS:
		return "SIGBUS"
	case _SIGFPE:
		return "SIGFPE"
	case _SIGSEGV:
		return "SIGSEGV"
	default:
		return "SIG?"
	}
}

