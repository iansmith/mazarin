// netpoll_maz.go — Force netpoll initialization for Mazzy priests.
//
// On Linux, netpollGenericInit is lazily called by addHeap when the first
// Go timer is created. But on Mazzy's cooperative uniprocessor scheduler,
// the main goroutine may block before any timer exists. Without netpoll
// initialized, findRunnable skips the netpoll(delay) path and falls through
// to stopm(), blocking the M forever — no timer fires, no goroutine runs.
//
// This file forces netpoll initialization during runtime init and increments
// netpollWaiters so that findRunnable always enters the netpoll(delay) path.
// Our kernel's SyscallEpollPwait properly blocks the thread with a deadline,
// allowing the scheduler to retry and process timers when they eventually appear.

package runtime

func init() {
	netpollGenericInit()
	netpollWaiters.Add(1)
}
