// MAZZY USERSPACE OVERLAY: netpoll_maz_init.go
//
// Eager initialization for stock netpoll_epoll.go.
//
// Stock Go's netpollinit() is called lazily — only when the first pollable
// fd is opened (via internal/poll.runtime_pollServerInit). Mazzy shepherds
// have no network fds, so without this overlay netpollInited stays 0 and
// ALL netpoll paths are skipped:
//   - sysmon's 10ms netpoll(0) check (proc.go:6311)
//   - findRunnable's blocking netpoll(delay) for timers (proc.go:3720)
//   - wakeNetPoller's netpollBreak calls
//
// This init function calls netpollGenericInit() which triggers the
// epoll_create1, eventfd2, and epoll_ctl(ADD eventfd) SVCs, then sets
// netpollInited=1. After that, the standard Go runtime paths work:
//   - findRunnable enters netpoll(delay) when pollUntil != 0 (timers pending)
//   - sysmon calls netpoll(0) every 10ms as a backstop
//   - netpollBreak via write(eventfd) wakes blocked epoll_wait early
//
// Note: netpollWaiters is NOT set here. On real Linux, netpollWaiters is
// incremented by poll.runtime_pollOpen when pollable fds are registered.
// Mazzy shepherds have no pollable fds, so netpollWaiters stays 0. This is
// fine: findRunnable's blocking netpoll path is gated by
// (netpollAnyWaiters() || pollUntil != 0), and pollUntil is non-zero when
// timers are pending (which is almost always true). The stopm() fallback
// also works correctly — futex_wake from startm reliably wakes parked Ms.

//go:build linux

package runtime

func init() {
	netpollGenericInit()
}
