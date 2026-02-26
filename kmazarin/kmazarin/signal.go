package main

import (
	"mazzy/kmazarin/proc"
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

// signalActions is the global signal action table (kernel threads only).
// Index 0 is unused (signal numbers are 1-based).
// Userspace threads use per-priest tables in proc.Priest.SignalActions.
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
//
//go:nosplit
func InitSignals() {
	sigreturnTrampolinePC = getSigreturnTrampolineAddr()
}

// GetSignalAction returns the signal action for the given signal number.
// For userspace threads (PriestIdx >= 0), reads from the priest's per-process table.
// For kernel threads, reads from the global table.
//
//go:nosplit
func GetSignalAction(sig int) SignalAction {
	t := GetCurrentThread()
	if t != nil && t.PriestIdx >= 0 {
		p := &proc.PriestListData[t.PriestIdx]
		sa := &p.SignalActions[sig]
		return SignalAction{Handler: sa.Handler, Flags: sa.Flags,
			Restorer: sa.Restorer, Mask: sa.Mask}
	}
	return signalActions[sig]
}

// GetSignalActionForThread returns the signal action for a specific thread's priest.
// Used by DeliverPendingSignal where the target thread may differ from the current thread.
//
//go:nosplit
func GetSignalActionForThread(thread *Thread, sig int) SignalAction {
	if thread.PriestIdx >= 0 {
		p := &proc.PriestListData[thread.PriestIdx]
		sa := &p.SignalActions[sig]
		return SignalAction{Handler: sa.Handler, Flags: sa.Flags,
			Restorer: sa.Restorer, Mask: sa.Mask}
	}
	return signalActions[sig]
}

// SetSignalAction installs a signal action for the given signal number.
// For userspace threads (PriestIdx >= 0), writes to the priest's per-process table.
// For kernel threads, writes to the global table.
//
//go:nosplit
func SetSignalAction(sig int, sa *SignalAction) {
	t := GetCurrentThread()
	if t != nil && t.PriestIdx >= 0 {
		p := &proc.PriestListData[t.PriestIdx]
		p.SignalActions[sig] = proc.PriestSignalAction{
			Handler: sa.Handler, Flags: sa.Flags,
			Restorer: sa.Restorer, Mask: sa.Mask,
		}
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

	// Look up action from the TARGET thread's priest, not the current thread
	action := GetSignalActionForThread(thread, signum)
	if action.Handler == 0 {
		// No handler — clear the signal and return
		atomicAndUint64(&thread.PendingSignals, ^(uint64(1) << uint(signum-1)))
		return
	}

	// Clear this signal from pending (before delivery)
	atomicAndUint64(&thread.PendingSignals, ^(uint64(1) << uint(signum-1)))

	// Record delivery event (nosplit-safe — no serial prints here)
	atomic.StoreUint64(&signalDeliverLastSig, uint64(signum))
	atomic.StoreUint64(&signalDeliverLastTID, uint64(thread.TID))
	atomic.StoreUint64(&signalDeliverLastHandler, action.Handler)
	atomic.AddUint64(&signalDeliverCount, 1)

	// Build the signal frame on the thread's signal stack
	// and modify the ThreadContext to enter sigtramp
	BuildSignalFrame(thread, signum, &action)

	thread.InSignalHandler = 1
}

// SignalSelfTest prints a diagnostic summary of the signal infrastructure state.
// Called after each userspace program launch to verify signal readiness.
// Reads from the current priest's table for userspace threads, global for kernel.
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
	// Count total registered handlers from the appropriate table
	var count int
	t := GetCurrentThread()
	if t != nil && t.PriestIdx >= 0 {
		p := &proc.PriestListData[t.PriestIdx]
		for i := 1; i < _NSIG; i++ {
			if p.SignalActions[i].Handler != 0 {
				count++
			}
		}
	} else {
		for i := 1; i < _NSIG; i++ {
			if signalActions[i].Handler != 0 {
				count++
			}
		}
	}
	serial.RawUARTPuts(" total=")
	serial.RawUARTDecimal(uint64(count))
	serial.RawUARTPuts("\r\n")
}

// SignalDeliveryStats prints a summary of signal delivery activity.
// Safe to call from non-nosplit context (uses serial output).
func SignalDeliveryStats() {
	count := atomic.LoadUint64(&signalDeliverCount)
	if count == 0 {
		serial.RawUARTPuts("[SD] no signals delivered\n")
		return
	}
	serial.RawUARTPuts("[SD] delivered=")
	serial.RawUARTDecimal(count)
	serial.RawUARTPuts(" last: sig=")
	serial.RawUARTDecimal(atomic.LoadUint64(&signalDeliverLastSig))
	serial.RawUARTPuts(" tid=")
	serial.RawUARTDecimal(atomic.LoadUint64(&signalDeliverLastTID))
	serial.RawUARTPuts(" handler=0x")
	serial.RawUARTHex64(atomic.LoadUint64(&signalDeliverLastHandler))
	serial.PollWrite('\n')
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
