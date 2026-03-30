// MAZZY USERSPACE OVERLAY: netpoll_epoll.go
//
// Replaces the stock epoll-based netpoll with a futex-based timed sleep.
// Mazzy shepherds have no network stack, so netpoll never returns ready
// goroutines. Its sole purpose is to give findRunnable a timer-based
// retry loop instead of falling through to stopm() and parking forever.
//
// Without this overlay, the following deadlock can occur:
//   1. Goroutine returns from blocking syscall (e.g. WaitSoftIRQ)
//   2. exitsyscall can't reacquire P (GC stole it; GOGC=5 fires often)
//   3. exitsyscall0 puts goroutine on global runqueue, parks M via stopm()
//   4. Other M finishes GC, enters findRunnable, races with step 3
//   5. findRunnable sees empty global queue → stopm() → both Ms parked
//   6. Goroutine stranded on global queue forever
//
// With this overlay: findRunnable → netpoll(delay) → timed futex sleep →
// returns after timeout → loops back to top → checks global queue →
// finds the stranded goroutine.
//
// Pattern copied from runtime/netpoll_stub.go (Plan 9), which solves
// the same problem for platforms without a real network poller.

//go:build linux

package runtime

var netpollStubLock mutex
var netpollNote note

// netpollBroken, protected by netpollBrokenLock, avoids a double notewakeup.
var netpollBrokenLock mutex
var netpollBroken bool

func netpollinit() {
	// No-op. Mazzy has no epoll — netpollGenericInit (in netpoll.go)
	// sets netpollInited=1 after this returns.
}

func netpollIsPollDescriptor(fd uintptr) bool {
	return false
}

func netpollopen(fd uintptr, pd *pollDesc) uintptr {
	return 0
}

func netpollclose(fd uintptr) uintptr {
	return 0
}

func netpollarm(pd *pollDesc, mode int) {
	throw("runtime: unused")
}

// netpollBreak interrupts a blocking netpoll.
func netpollBreak() {
	lock(&netpollBrokenLock)
	broken := netpollBroken
	netpollBroken = true
	if !broken {
		notewakeup(&netpollNote)
	}
	unlock(&netpollBrokenLock)
}

// netpoll checks for ready network connections.
// Returns a list of goroutines that become runnable,
// and a delta to add to netpollWaiters.
// This must never return an empty list with a non-zero delta.
//
// delay < 0: blocks indefinitely
// delay == 0: does not block, just polls
// delay > 0: block for up to that many nanoseconds
func netpoll(delay int64) (gList, int32) {
	if delay != 0 {
		// This lock ensures that only one goroutine tries to use
		// the note. It should normally be completely uncontended.
		lock(&netpollStubLock)

		lock(&netpollBrokenLock)
		noteclear(&netpollNote)
		netpollBroken = false
		unlock(&netpollBrokenLock)

		notetsleep(&netpollNote, delay)
		unlock(&netpollStubLock)
		// Guard against starvation in case the lock is contended.
		osyield()
	}
	return gList{}, 0
}
