package main

import (
	"mazzy/kmazarin/serial"
	"sync/atomic"
	"unsafe"
)

// Signal constants matching Linux definitions.
const (
	_NSIG      = 65 // Linux signal count (1-64 + sentinel)
	_SIGURG    = 23 // Async preemption signal (used by Go runtime GC STW)
	_SIGPROF   = 27 // Profiling signal (stub for now)
	_SI_KERNEL = 0x80

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

// signalActions is the global signal action table.
// Index 0 is unused (signal numbers are 1-based).
// No locking needed: single-core, initsig runs sequentially during startup,
// table is read-only after that.
var signalActions [_NSIG]SignalAction

// sigreturnTrampolinePC holds the address of the kmazarin sigreturn trampoline.
// Set during signal init from getSigreturnTrampolineAddr().
var sigreturnTrampolinePC uintptr

// InitSignals initializes signal infrastructure.
// Called during kmazarin startup after assembly functions are available.
//
//go:nosplit
func InitSignals() {
	sigreturnTrampolinePC = getSigreturnTrampolineAddr()
}

// GetSignalAction returns the signal action for the given signal number.
//
//go:nosplit
func GetSignalAction(sig int) SignalAction {
	return signalActions[sig]
}

// SetSignalAction installs a signal action for the given signal number.
//
//go:nosplit
func SetSignalAction(sig int, sa *SignalAction) {
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

	// Check if we have a handler registered for this signal
	action := GetSignalAction(signum)
	if action.Handler == 0 {
		// No handler — clear the signal and return
		atomicAndUint64(&thread.PendingSignals, ^(uint64(1) << uint(signum-1)))
		return
	}

	// Clear this signal from pending (before delivery)
	atomicAndUint64(&thread.PendingSignals, ^(uint64(1) << uint(signum-1)))

	// Build the signal frame on the thread's signal stack
	// and modify the ThreadContext to enter sigtramp
	BuildSignalFrame(thread, signum, &action)

	thread.InSignalHandler = 1
}

// SignalSelfTest prints a diagnostic summary of the signal infrastructure state.
// Called after each userspace program launch to verify signal readiness.
// This is a lightweight check — full signal delivery is deferred to future work.
func SignalSelfTest(label string) {
	serial.RawUARTPuts("[SigTest] ")
	serial.RawUARTPuts(label)
	serial.RawUARTPuts(": SIGURG=")
	action := GetSignalAction(_SIGURG)
	if action.Handler != 0 {
		serial.RawUARTPuts("0x")
		serial.RawUARTHex64(action.Handler)
		serial.RawUARTPuts(" flags=0x")
		serial.RawUARTHex64(action.Flags)
	} else {
		serial.RawUARTPuts("(none)")
	}
	serial.RawUARTPuts(" tramp=")
	if sigreturnTrampolinePC != 0 {
		serial.RawUARTPuts("0x")
		serial.RawUARTHex64(uint64(sigreturnTrampolinePC))
	} else {
		serial.RawUARTPuts("(none)")
	}
	// Count total registered handlers
	var count int
	for i := 1; i < _NSIG; i++ {
		if signalActions[i].Handler != 0 {
			count++
		}
	}
	serial.RawUARTPuts(" total=")
	serial.RawUARTDecimal(uint64(count))
	serial.RawUARTPuts("\r\n")
}

// zeroMemory zeroes n bytes starting at ptr.
// Used to zero signal frames on the gsignal stack.
//
//go:nosplit
func zeroMemory(ptr unsafe.Pointer, n uintptr) {
	p := (*[1 << 30]byte)(ptr)
	for i := uintptr(0); i < n; i++ {
		p[i] = 0
	}
}
