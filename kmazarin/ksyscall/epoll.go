package ksyscall

import (
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"sync/atomic"
	"unsafe"
)

const (
	magicEpollFd = 0x7ef
	magicEventFd = 0x7ee
)

// callerShepherd resolves the syscall caller to its shepherd, mapping the
// KERNEL caller (PID 0 — proc.CurrentShepherd() returns nil) to
// proc.KernelShepherd. The kernel's OWN Go runtime does netpoll exactly like
// a shepherd's (epoll_create1 + eventfd + epoll_ctl at netpollGenericInit,
// epoll_pwait from findRunnable, eventfd write from netpollBreak) — without
// this mapping those syscalls saw a nil shepherd, epoll_ctl returned EINVAL,
// and the kernel runtime threw at its first timer-heap insertion (stock
// netpoll_epoll.go:40 "epollctl failed") — the MAZ-136 "netpoll family"
// KERNEL EXIT GROUP, intermittent because the trigger was bgscavenge's
// memory-pressure-timed sleep. isKernel tells callers to access the
// caller's pointers DIRECTLY (they are plain kernel VAs) instead of through
// the user-page-table accessors, which fail on kernel addresses.
//
//go:nosplit
func callerShepherd() (p *proc.Shepherd, isKernel bool) {
	if p := proc.CurrentShepherd(); p != nil {
		return p, false
	}
	return &proc.KernelShepherd, true
}

// epollDeliverEvent writes one synthetic EPOLLIN epoll_event {Events=1,
// Data=data} to the caller's events buffer. Kernel caller → direct store
// (eventsPtr is a kernel VA); shepherd caller → user-page-table write.
// EpollEvent layout: Events(uint32 @ +0), Data(uint64 @ +4).
//
// Returns false if a shepherd-side user write fails (unmapped / partially
// mapped eventsPtr). Callers MUST propagate that as -EFAULT rather than
// reporting a delivered event — otherwise an unmapped buffer silently looks
// like a successful epoll_wait return.
//
//go:nosplit
func epollDeliverEvent(eventsPtr, data uint64, isKernel bool) bool {
	if isKernel {
		*(*uint32)(unsafe.Pointer(uintptr(eventsPtr))) = 1 // EPOLLIN
		*(*uint64)(unsafe.Pointer(uintptr(eventsPtr + 4))) = data
		return true
	}
	if !kmem.WriteUserUint32(uintptr(eventsPtr), 1) { // Events = EPOLLIN
		return false
	}
	return kmem.WriteUserUint64(uintptr(eventsPtr+4), data)
}

// EpollWorkRequest contains the parameters for a goroutine-dispatched epoll_ctl.
type EpollWorkRequest struct {
	Args      [6]uint64
	Shepherd  *proc.Shepherd
	L0PA      uintptr
	CallerSID proc.ShepherdId
}

// IsMagicFdSyscall returns true if sysID is read/write/close AND fd is one of the
// shepherd's kernel-internal magic fds (epoll instance or eventfd).
// These syscalls must never be delegated to the linux shepherd — the shepherd
// does not know about the magic fds and would return errors.
//
//go:nosplit
func IsMagicFdSyscall(sysID SysID, fd uint64) bool {
	// Write to fd 1/2 (stdout/stderr) is handled by SyscallWrite directly:
	// it pushes bytes to the PL011 TX ring buffer, then delegates internally.
	// Must not be routed directly to DelegateSyscall.
	if sysID == SysIDWrite && (fd == 1 || fd == 2) {
		return true
	}
	if sysID != SysIDRead && sysID != SysIDWrite && sysID != SysIDClose {
		return false
	}
	// callerShepherd, NOT CurrentShepherd: the KERNEL's netpollBreak writes
	// to ITS eventfd, and the post-wake read(eventfd) follows — both must
	// stay local. With a nil shepherd here they fell through to the
	// delegation gate and were forwarded to the linux shepherd, which knows
	// nothing of the magic fds.
	p, _ := callerShepherd()
	return (p.EpollFd != 0 && fd == uint64(p.EpollFd)) ||
		(p.EventFd != 0 && fd == uint64(p.EventFd))
}

// SyscallEpollCreate creates an epoll file descriptor.
// Returns the magic epoll fd (0x7ef). Panics if called twice for the same shepherd
// (the Go runtime only calls epoll_create1 once per process).
//
//go:nosplit
func SyscallEpollCreate(_, _, _, _, _, _ uint64) int64 {
	p, _ := callerShepherd()
	if p.EpollFd != 0 {
		KernelPanic("SyscallEpollCreate: epoll fd already created for shepherd")
	}
	p.EpollFd = magicEpollFd
	return magicEpollFd
}

// SyscallEventfd creates an event file descriptor.
// Returns the magic eventfd (0x7ee).
//
//go:nosplit
func SyscallEventfd(_, _, _, _, _, _ uint64) int64 {
	p, _ := callerShepherd()
	p.EventFd = magicEventFd
	return magicEventFd
}

// SyscallEpollCtl controls an epoll file descriptor.
// Validates that epfd matches the shepherd's EpollFd.
// When the runtime registers the eventfd (EPOLL_CTL_ADD), captures ev.Data —
// the pointer to netpollEventFd — so SyscallEpollPwait can return a synthetic
// EPOLLIN event with the correct Data, letting the runtime call read(eventfd)
// and reset netpollWakeSig after each wakeup.
//
// Fast path: tries ReadUserUint64 directly. If the page is already mapped,
// captures EventDataPtr and returns immediately.
// Slow path: if ReadUserUint64 fails (page not demand-mapped), posts the
// request to the kernel worker goroutine which can call DemandMapUserPage.
//
//go:nosplit
func SyscallEpollCtl(epfd, op, fd, evtPtr, _, _ uint64) int64 {
	const EPOLL_CTL_ADD = 1
	p, isKernel := callerShepherd()
	if p.EpollFd != 0 && uint64(p.EpollFd) != epfd {
		return -9 // EBADF — epfd doesn't match our magic epoll fd
	}

	// When the runtime registers eventfd with epoll, it sets ev.Data = &netpollEventFd.
	// Capture that pointer so we can return it in synthetic epoll events.
	// EpollEvent layout: Events(uint32 @ offset 0), Data(uint64 @ offset 4).
	if op == EPOLL_CTL_ADD && p.EventFd != 0 && fd == uint64(p.EventFd) && evtPtr != 0 {
		if isKernel {
			// Kernel caller: ev is a kernel object (the runtime's own
			// netpollGenericInit local) — read it directly. The
			// user-page-table accessors below FAIL on kernel VAs, which
			// (with the old nil-shepherd EINVAL) was the MAZ-136
			// netpoll-family KERNEL EXIT GROUP.
			p.EventDataPtr = *(*uint64)(unsafe.Pointer(uintptr(evtPtr + 4)))
			return 0
		}
		// Fast path: try to read ev.Data directly.
		data, ok := kmem.ReadUserUint64(uintptr(evtPtr + 4))
		if ok {
			p.EventDataPtr = data
			return 0
		}

		// Slow path: page not mapped. Post to kernel worker goroutine
		// which can call DemandMapUserPage.
		ctxPtr := submitEpoll(EpollWorkRequest{
			Args:      [6]uint64{epfd, op, fd, evtPtr, 0, 0},
			Shepherd:  p,
			L0PA:      p.PageTableL0PA,
			CallerSID: p.PID,
		})
		if ctxPtr != 0 {
			SetSyscallSwitchTarget(ctxPtr)
		}
		// Return value injected by wakeBlockedThread via SetReturnValue
		return 0
	}

	return 0 // Success for non-ADD operations
}

// DoEpollCtlWork performs epoll_ctl work in normal Go context (growable stack).
// Called by the kernel worker goroutine from KernelIdleLoop.
// Can call DemandMapUserPage because it is NOT nosplit.
func DoEpollCtlWork(req *EpollWorkRequest) int64 {
	evtPtr := uintptr(req.Args[3])
	if evtPtr == 0 {
		return -14 // EFAULT
	}

	// Demand-map the page containing ev.Data if needed.
	evDataAddr := evtPtr + 4 // offset of Data field in struct epoll_event
	pa := kmem.DemandMapUserPage(evDataAddr, req.L0PA)
	if pa == 0 {
		klog.Errf("[epoll_ctl] DemandMapUserPage failed for ev.Data\n")
		return -14 // EFAULT
	}

	// Read ev.Data through the explicit page table.
	evData, ok := kmem.ReadUserUint64WithL0(evDataAddr, req.L0PA)
	if !ok {
		klog.Errf("[epoll_ctl] ReadUserUint64WithL0 failed after demand map\n")
		return -14 // EFAULT
	}

	// Store the event data pointer on the shepherd.
	req.Shepherd.EventDataPtr = evData
	return 0
}

// SyscallEpollPwait waits for events on an epoll fd.
// Go's runtime calls this from netpoll(delay) to sleep until the next timer
// deadline or until an event arrives (e.g., netpollBreak writes to eventfd).
//
// On real Linux, epoll_wait blocks and wakes on I/O events or timeout.
// We emulate this: the thread sleeps with a deadline, and write(eventfd)
// wakes it early via WakeNetpollThread (implementing netpollBreak).
//
// Timeout semantics match Linux epoll_wait:
//
//	-1 = block indefinitely  -> no deadline; woken by write(eventfd) -> WakeNetpollThread
//	 0 = non-blocking poll   -> return immediately
//	>0 = wait up to N ms     -> block for that duration
//
//go:nosplit
func SyscallEpollPwait(epfd, eventsPtr, maxEvents, timeoutMS, _, _ uint64) int64 {
	p, isKernel := callerShepherd()

	// Validate epfd matches the shepherd's epoll instance.
	if p.EpollFd != 0 && uint64(p.EpollFd) != epfd {
		return -9 // EBADF
	}

	ms := int32(timeoutMS)

	// Clamp indefinite timeouts to 10ms. Without this, findRunnable's
	// netpoll(-1) would block the thread forever (no eventfd write to wake
	// it). The 10ms cap gives findRunnable a retry loop to check all run
	// queues, preventing goroutines stranded on the global queue from
	// being missed. This matches the behavior of the futex-based netpoll
	// overlay (commit 998677c).
	if ms < 0 {
		ms = 10
	}

	// Non-blocking poll (ms==0): check if an event is pending, return immediately.
	if ms == 0 {
		if atomic.SwapUint32(&p.EventFdPending, 0) != 0 {
			if eventsPtr != 0 && maxEvents > 0 && p.EventDataPtr != 0 {
				if !epollDeliverEvent(eventsPtr, p.EventDataPtr, isKernel) {
					atomic.StoreUint32(&p.EventFdPending, 1) // undo consume
					return -14                               // EFAULT — events buffer unmapped
				}
				return 1
			}
			// Can't report event without EventDataPtr — put pending back.
			atomic.StoreUint32(&p.EventFdPending, 1)
		}
		return 0 // No events
	}

	currentTID := int32(GetCurrentThreadTID())

	// Check if a prior eventfd write is pending. On real Linux, eventfd
	// writes accumulate in a counter; when the fd is in the epoll set,
	// epoll_wait returns immediately because the fd is readable.
	if atomic.SwapUint32(&p.EventFdPending, 0) != 0 {
		if eventsPtr != 0 && maxEvents > 0 && p.EventDataPtr != 0 {
			if !epollDeliverEvent(eventsPtr, p.EventDataPtr, isKernel) {
				atomic.StoreUint32(&p.EventFdPending, 1) // undo consume
				return -14                               // EFAULT — events buffer unmapped
			}
			return 1
		}
		// Can't deliver without EventDataPtr — put pending back.
		atomic.StoreUint32(&p.EventFdPending, 1)
		return 0
	}
	p.NetpollWaiterTID = currentTID

	// Add deadline only for explicit timeouts (ms > 0).
	// Indefinite blocking (ms < 0) relies on write(eventfd) -> WakeNetpollThread.
	if ms > 0 {
		frequency := uint64(kirq.GetTimerFrequency())
		ticks := (uint64(ms) * frequency) / 1000
		if ticks == 0 {
			ticks = 1
		}
		currentTick := kirq.ReadCounterValue()
		deadline := currentTick + ticks
		AddDeadlineStatic(deadline, currentTID)
	}

	// Block thread until deadline fires or eventfd write wakes us.
	nextThread := ThreadBlockSleep()
	if nextThread != 0 {
		SetSyscallSwitchTarget(nextThread)
	}

	// Clear the waiter registration on return (we've been woken).
	p.NetpollWaiterTID = 0

	// Return 1 synthetic EPOLLIN event pointing to the eventfd.
	// Stock netpoll_epoll.go checks ev.Data == &netpollEventFd; when it matches,
	// it calls read(eventfd) and resets netpollWakeSig to 0.
	if eventsPtr != 0 && maxEvents > 0 && p.EventDataPtr != 0 {
		if !epollDeliverEvent(eventsPtr, p.EventDataPtr, isKernel) {
			return -14 // EFAULT — events buffer unmapped
		}
		return 1
	}
	return 0
}
