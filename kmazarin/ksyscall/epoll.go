package ksyscall

import (
	"mazzy/kmazarin/kirq"
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"sync/atomic"
)

const (
	magicEpollFd = 0x7ef
	magicEventFd = 0x7ee
)

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
	if sysID != SysIDRead && sysID != SysIDWrite && sysID != SysIDClose {
		return false
	}
	p := proc.CurrentShepherd()
	if p == nil {
		return false
	}
	return (p.EpollFd != 0 && fd == uint64(p.EpollFd)) ||
		(p.EventFd != 0 && fd == uint64(p.EventFd))
}

// SyscallEpollCreate creates an epoll file descriptor.
// Returns the magic epoll fd (0x7ef). Panics if called twice for the same shepherd
// (the Go runtime only calls epoll_create1 once per process).
//
//go:nosplit
func SyscallEpollCreate(_, _, _, _, _, _ uint64) int64 {
	p := proc.CurrentShepherd()
	if p != nil {
		if p.EpollFd != 0 {
			KernelPanic("SyscallEpollCreate: epoll fd already created for shepherd")
		}
		p.EpollFd = magicEpollFd
	}
	return magicEpollFd
}

// SyscallEventfd creates an event file descriptor.
// Returns the magic eventfd (0x7ee).
//
//go:nosplit
func SyscallEventfd(_, _, _, _, _, _ uint64) int64 {
	p := proc.CurrentShepherd()
	if p != nil {
		p.EventFd = magicEventFd
	}
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
	p := proc.CurrentShepherd()
	if p == nil {
		return -22 // EINVAL
	}
	if p.EpollFd != 0 && uint64(p.EpollFd) != epfd {
		return -9 // EBADF — epfd doesn't match our magic epoll fd
	}

	// When the runtime registers eventfd with epoll, it sets ev.Data = &netpollEventFd.
	// Capture that pointer so we can return it in synthetic epoll events.
	// EpollEvent layout: Events(uint32 @ offset 0), Data(uint64 @ offset 4).
	if op == EPOLL_CTL_ADD && p.EventFd != 0 && fd == uint64(p.EventFd) && evtPtr != 0 {
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
		serial.RawUARTPuts("[epoll_ctl] DemandMapUserPage failed for ev.Data\r\n")
		return -14 // EFAULT
	}

	// Read ev.Data through the explicit page table.
	evData, ok := kmem.ReadUserUint64WithL0(evDataAddr, req.L0PA)
	if !ok {
		serial.RawUARTPuts("[epoll_ctl] ReadUserUint64WithL0 failed after demand map\r\n")
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
	p := proc.CurrentShepherd()

	// Validate epfd matches the shepherd's epoll instance.
	if p != nil && p.EpollFd != 0 {
		if uint64(p.EpollFd) != epfd {
			return -9 // EBADF
		}
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
		if p != nil && atomic.SwapUint32(&p.EventFdPending, 0) != 0 {
			if eventsPtr != 0 && maxEvents > 0 && p.EventDataPtr != 0 {
				kmem.WriteUserUint32(uintptr(eventsPtr), 1) // Events = EPOLLIN
				kmem.WriteUserUint64(uintptr(eventsPtr+4), p.EventDataPtr)
				return 1
			}
			// Can't report event without EventDataPtr — put pending back.
			atomic.StoreUint32(&p.EventFdPending, 1)
		}
		return 0 // No events
	}

	currentTID := int32(GetCurrentThreadTID())

	if p != nil {
		// Check if a prior eventfd write is pending. On real Linux, eventfd
		// writes accumulate in a counter; when the fd is in the epoll set,
		// epoll_wait returns immediately because the fd is readable.
		if atomic.SwapUint32(&p.EventFdPending, 0) != 0 {
			if eventsPtr != 0 && maxEvents > 0 && p.EventDataPtr != 0 {
				kmem.WriteUserUint32(uintptr(eventsPtr), 1) // Events = EPOLLIN
				kmem.WriteUserUint64(uintptr(eventsPtr+4), p.EventDataPtr)
				return 1
			}
			// Can't deliver without EventDataPtr — put pending back.
			atomic.StoreUint32(&p.EventFdPending, 1)
			return 0
		}
		p.NetpollWaiterTID = currentTID
	}

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
	if p != nil {
		p.NetpollWaiterTID = 0
	}

	// Return 1 synthetic EPOLLIN event pointing to the eventfd.
	// Stock netpoll_epoll.go checks ev.Data == &netpollEventFd; when it matches,
	// it calls read(eventfd) and resets netpollWakeSig to 0.
	// EpollEvent layout: Events(uint32 @ +0), Data(uint64 @ +4).
	if eventsPtr != 0 && maxEvents > 0 && p != nil && p.EventDataPtr != 0 {
		kmem.WriteUserUint32(uintptr(eventsPtr), 1) // Events = EPOLLIN
		kmem.WriteUserUint64(uintptr(eventsPtr+4), p.EventDataPtr)
		return 1
	}
	return 0
}
