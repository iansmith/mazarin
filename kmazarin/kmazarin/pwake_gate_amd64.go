//go:build amd64

package main

// MAZ-135 — woken-side gate for the x86_64 priority-wake fast path.
//
// The priority-wake fast switch itself lives in exceptions_amd64.s (the
// handle_device_irq block, which calls ·CheckThreadPreemption — the same
// scheduler entry the timer path uses) and is made correct by the faithful
// dual-home g save/restore (ThreadContext.TLSG; see save_context_amd64.go and
// load_context_and_iretq).

// fastWakeAllowedForWaiter reports whether a freshly-woken waiter may trigger the
// immediate IRQ-return priority-wake switch. On amd64 we forbid it for P-released
// waiters (entersyscallblock — IOUringEnterBlocking / RecvWithRing): fast-resuming
// a thread that handed off its P races the Go runtime's P-handoff and corrupts
// g/SP. P-held waiters (IOUringEnter, RawSyscall) keep the fast path. MAZ-135.
//
//go:nosplit
func fastWakeAllowedForWaiter(pReleased bool) bool { return !pReleased }
