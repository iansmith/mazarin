package main

import (
	_ "unsafe" // for go:linkname

	"mazzy/kmazarin/klog"
)

// runtimeNetpollGenericInit pulls runtime.netpollGenericInit via linkname
// (established pattern, see linkname_impl.go). It is idempotent and
// lock-protected in the runtime, and — critically — it does NOT gopark.
//
//go:linkname runtimeNetpollGenericInit runtime.netpollGenericInit
func runtimeNetpollGenericInit()

// kernelNetpollEagerInit forces the KERNEL runtime's netpollGenericInit to
// run now, at a deterministic boot point, instead of at the first kernel
// timer-heap insertion (typically bgscavenge's timed sleep under memory
// pressure — an arbitrary, load-dependent moment; see the run-41 traceback
// in ticket-active/MAZ-136: bgscavenge → scavengerState.sleep → timer.reset
// → timers.addHeap → netpollGenericInit → throw at netpoll_epoll.go:40).
//
// netpollGenericInit issues epoll_create1 + eventfd + epoll_ctl through the
// kernel's own syscall path (ksyscall's magic-fd netpoll emulation,
// kmazarin/ksyscall/epoll.go). If that path regresses for KERNEL-context
// callers, the runtime throws ("runtime: epollctl failed") and the boot dies
// HERE, loudly and every time — instead of intermittently, minutes later.
// The userspace overlay (runtime-patches for shepherds, netpoll_maz_init.go)
// does the same eager init for every shepherd; this is the kernel's twin.
//
// ‼ The init MUST be triggered by the direct linkname call — NOT by
// time.Sleep, and NOT by time.NewTimer:
//   - time.Sleep (the original mechanism) GOPARKS kernel main. The whole
//     exception-dispatch design borrows the g0/m0 identity for vector-129
//     handlers, which is only safe while m0 holds a P — and that holds
//     precisely because kernel main runs on m0 forever (KernelIdleLoop
//     never goparks) so workers run ON m0 via async preemption and the P
//     never leaves. One gopark lets the scheduler resume kernel main on a
//     DIFFERENT M; m0 then sits parked WITHOUT a P and the first
//     allocating user-syscall handler nil-derefs in mallocgc
//     (getMCache(m0): m.p==0, mcache0 cleared post-bootstrap). Observed
//     5/5 under KVM (GMP dump: P=0 MC0=0; BPW chain QueryInputDevices →
//     newobject → mallocgc); TCG never interleaved tightly enough.
//     KERNEL MAIN MUST NEVER GOPARK (MAZ-136).
//     (runtime.LockOSThread is NOT the fix: a locked M runs ONLY its
//     locked goroutine, so with GOMAXPROCS=1 every worker timeslice would
//     force the P OFF m0 — reopening the window far more often.)
//   - time.NewTimer never reaches the timer heap at all: since go1.23
//     channel-based timers are LAZY (verified empirically, run 41 printed
//     OK without initializing).
func kernelNetpollEagerInit() {
	klog.Logf("[netpoll] kernel eager netpoll init...\n")
	runtimeNetpollGenericInit()
	klog.Logf("[netpoll] kernel eager netpoll init OK\n")
}
