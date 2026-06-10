package main

import (
	"time"

	"mazzy/kmazarin/klog"
)

// kernelNetpollEagerInit forces the KERNEL runtime's netpollGenericInit to
// run now, at a deterministic boot point, instead of at the first kernel
// timer-heap insertion (typically bgscavenge's timed sleep under memory
// pressure — an arbitrary, load-dependent moment; see the run-41 traceback
// in ticket-active/MAZ-136: bgscavenge → scavengerState.sleep → timer.reset
// → timers.addHeap → netpollGenericInit → throw at netpoll_epoll.go:40).
//
// Mechanism: timers.addHeap (runtime/time.go:455) calls netpollGenericInit
// when netpollInited == 0. ‼ time.NewTimer does NOT work as the trigger:
// since go1.23, channel-based timers are LAZY — they touch the heap only
// once the channel is received from, so NewTimer+Stop never reaches addHeap
// (verified: run 41 printed "init OK" yet the scavenger still ran the real
// init a minute later). time.Sleep arms a sleep timer via timer.reset —
// the scavenger's exact path — and reaches addHeap synchronously.
//
// netpollGenericInit issues epoll_create1 + eventfd + epoll_ctl through the
// kernel's own syscall path (ksyscall's magic-fd netpoll emulation,
// kmazarin/ksyscall/epoll.go). If that path regresses for KERNEL-context
// callers, the runtime throws ("runtime: epollctl failed") and the boot dies
// HERE, loudly and every time — instead of intermittently, minutes later.
// The userspace overlay (runtime-patches for shepherds, netpoll_maz_init.go)
// does the same eager init for every shepherd; this is the kernel's twin.
func kernelNetpollEagerInit() {
	klog.Logf("[netpoll] kernel eager netpoll init...\n")
	time.Sleep(time.Millisecond) // timer.reset → addHeap → netpollGenericInit
	klog.Logf("[netpoll] kernel eager netpoll init OK\n")
}
