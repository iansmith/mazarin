// Kernel → linux-shepherd notification protocol (MAZ-71).
//
// Defines the events the kernel pushes to the linux shepherd when process-
// lifecycle state changes, plus the in-kernel queue that holds them until
// the shepherd drains. The MECHANISM by which the shepherd actually
// consumes events from the queue (a syscall? a shared-memory ring? an
// existing IPC channel?) is implementation detail decided by the Phase B
// agent — Phase 0 pins the data shape and the queue semantics.
//
// Three event types in v1:
//
//   EventChildExit    — a child process has exited; the parent's wait4 can wake.
//                       Payload: Pid = child PID, ParentPid = parent PID,
//                                ExitStatus = exit status.
//
//   EventParentDeath  — a parent process has died; orphans should re-parent
//                       (and future Pdeathsig handling will fire here).
//                       Payload: Pid = the dying parent PID,
//                                ParentPid = unused, ExitStatus = unused.
//
//   EventExecComplete — a process has completed execve and is now running
//                       a new binary image.
//                       Payload: Pid = the exec'd process PID,
//                                ParentPid = unused, ExitStatus = unused.
//
// The queue is bounded by MaxNotificationEvents. Push during overflow
// returns ErrQueueFull — the kernel must decide what to do (drop oldest?
// block? panic?). For v1 we surface the error and let the caller decide
// per-event.

package proc

import "errors"

// EventType identifies the kind of kernel→shepherd notification.
type EventType uint8

const (
	// EventChildExit is raised when a child process exits.
	EventChildExit EventType = 1

	// EventParentDeath is raised when a process's parent dies and the
	// process is orphaned.
	EventParentDeath EventType = 2

	// EventExecComplete is raised when a process completes execve and
	// the new image is running.
	EventExecComplete EventType = 3
)

// NotificationEvent is a kernel→linux-shepherd lifecycle event.
//
// Payload semantics depend on Type — see the EventType constants.
type NotificationEvent struct {
	Type       EventType
	_pad       [3]byte // alignment padding so Pid is 4-byte aligned
	Pid        ShepherdId
	ParentPid  ShepherdId
	ExitStatus int32
}

// MaxNotificationEvents bounds the queue. 256 fits ~8 KB of events at
// the current sizeof(NotificationEvent) ≈ 12 bytes. Tunable later if
// real workloads need a deeper queue (a `go build -p 64` could
// hypothetically pile up many child-exit events if the shepherd lags).
const MaxNotificationEvents = 256

// ErrQueueFull is returned by NotificationQueue.Push when the queue is
// at capacity. The caller decides whether to drop, retry, or escalate.
var ErrQueueFull = errors.New("proc: notification queue full")

// NotificationQueue is a bounded FIFO queue of kernel→linux-shepherd
// lifecycle events.
//
// Concurrency: NOT goroutine-safe on its own. Callers are expected to
// hold an appropriate lock (typically the scheduler lock on the producer
// side, the shepherd's own state lock on the consumer side). The Phase B
// agent decides whether to add internal serialization based on actual
// caller patterns.
//
// Storage: a fixed-size ring buffer of NotificationEvent values; no
// heap allocations; nosplit-friendly. The events array is large
// (MaxNotificationEvents * sizeof(NotificationEvent) ≈ 3 KB) so callers
// should allocate the queue once and reuse it, not push it through the
// stack by value.
//
// Overflow policy (recommendation for callers):
//
//	Push returns ErrQueueFull when the queue is at capacity. The
//	recommended policy for kernel producers is **drop-oldest with a
//	printk-style warning**: on ErrQueueFull, the caller should Pop one
//	event, log a warning via klog (e.g. klog.Errf), and retry Push. This
//	keeps the most recent events — which are what wait4/SIGCHLD
//	consumers care about — at the cost of dropping a stale event. The
//	queue itself does NOT implement drop-oldest because (a) it would
//	silently lose events with no diagnostic, and (b) the producer side
//	already holds a lock and can do this trivially. See MAZ-77 for the
//	first real producer call site.
type NotificationQueue struct {
	events [MaxNotificationEvents]NotificationEvent
	head   uint32 // next slot to Pop
	tail   uint32 // next slot to Push
	count  uint32 // current depth (disambiguates head==tail full vs empty)
}

// NewNotificationQueue returns an empty queue.
func NewNotificationQueue() *NotificationQueue {
	return &NotificationQueue{}
}

// Push appends an event to the back of the queue. Returns ErrQueueFull
// if the queue is at MaxNotificationEvents — no mutation occurs in that
// case. See the type-level docstring for the recommended overflow
// policy (drop-oldest with a klog warning).
//
//go:nosplit
func (q *NotificationQueue) Push(ev NotificationEvent) error {
	if q.count == MaxNotificationEvents {
		return ErrQueueFull
	}
	q.events[q.tail] = ev
	q.tail = (q.tail + 1) % MaxNotificationEvents
	q.count++
	return nil
}

// Pop removes and returns the event at the front of the queue. The
// second return value is false if the queue is empty (and the event
// returned in that case is the zero value).
//
//go:nosplit
func (q *NotificationQueue) Pop() (NotificationEvent, bool) {
	if q.count == 0 {
		return NotificationEvent{}, false
	}
	ev := q.events[q.head]
	q.head = (q.head + 1) % MaxNotificationEvents
	q.count--
	return ev, true
}

// Len reports how many events are currently in the queue.
//
//go:nosplit
func (q *NotificationQueue) Len() int {
	return int(q.count)
}

// Cap reports the queue's capacity (= MaxNotificationEvents).
//
//go:nosplit
func (q *NotificationQueue) Cap() int {
	return MaxNotificationEvents
}
