// Package dispatch provides the linux shepherd's file-lane work dispatcher:
// a fixed-size worker pool that serves delegated syscall requests.
//
// Ordering contract (MAZ-156):
//
//  1. Same-SID FIFO with exclusion: for any single SID, items are handled
//     strictly in enqueue order, with at most one item in flight at a time.
//     A vfork transient shares its parent's SID, so its pre-execve setup
//     (dup3, chdir) and the parent-SID execve that consumes that setup are
//     ordered ONLY by this guarantee — see MAZ-156 (stage-2 redirect flake).
//  2. Cross-SID independence: an item never waits on a busy different SID
//     while a worker is free, and ready SIDs are served oldest-first (no
//     starvation). One shepherd parked inside a slow fsclient call must not
//     stall unrelated shepherds' delegates (the reason the worker pool
//     exists — MAZ-149 exp2).
//  3. Bounded concurrency: at most `workers` handler invocations run
//     concurrently. The pool is fixed-size because the .maz plugin runtime
//     is unstable under unbounded goroutine-creation rates (see the
//     fileLaneWorkers comment in maz/linux/main.go).
//
// Same-SID FIFO is defined relative to Enqueue call order, which is
// well-defined because production has a single reader goroutine enqueueing.
package dispatch

import "sync"

// Pool dispatches work items to a fixed set of worker goroutines while
// enforcing the package-level ordering contract.
//
// THIS is the enforcement point for the MAZ-156 same-SID ordering guarantee:
// each SID has one FIFO queue; a SID is either idle (no pending items),
// ready (pending items, no worker on it), or in service (exactly one worker
// draining its queue). Because only the single in-service worker pops a
// SID's queue, same-SID items can neither reorder nor overlap. Ready SIDs
// are picked oldest-first from a FIFO list.
//
// Enqueue never blocks (per-SID queues are unbounded slices). Memory stays
// bounded in practice: every file-lane caller blocks awaiting its delegate
// reply, so a SID's pending items are capped by its runnable threads.
type Pool[T any] struct {
	key    func(T) int16
	handle func(T)

	mu        sync.Mutex
	cond      *sync.Cond
	queues    map[int16][]T   // per-SID FIFO of pending items
	ready     []int16         // FIFO of SIDs with pending items and no worker
	inService map[int16]bool  // SIDs currently owned by a worker
	closed    bool
	wg        sync.WaitGroup
}

// NewPool starts `workers` goroutines that serve items passed to Enqueue.
// key extracts the SID an item belongs to; handle processes one item and is
// never called with the pool's internal lock held (Enqueue is safe from
// inside a handler). Panics if workers <= 0 — a zero-worker pool would
// silently strand every enqueued item.
func NewPool[T any](workers int, key func(T) int16, handle func(T)) *Pool[T] {
	if workers <= 0 {
		panic("dispatch: NewPool requires workers > 0")
	}
	p := &Pool[T]{
		key:       key,
		handle:    handle,
		queues:    make(map[int16][]T),
		inService: make(map[int16]bool),
	}
	p.cond = sync.NewCond(&p.mu)
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

// Enqueue submits an item for dispatch. Never blocks. Items enqueued after
// Close are silently dropped (shutdown-race tolerance).
func (p *Pool[T]) Enqueue(item T) {
	sid := p.key(item)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	wasIdle := len(p.queues[sid]) == 0 && !p.inService[sid]
	p.queues[sid] = append(p.queues[sid], item)
	if wasIdle {
		p.ready = append(p.ready, sid)
		p.cond.Signal()
	}
	p.mu.Unlock()
}

// Close stops accepting new items, drains everything already enqueued, and
// waits for the workers to exit. Only used at shepherd shutdown and in tests.
func (p *Pool[T]) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cond.Broadcast()
	p.wg.Wait()
}

// worker pulls the oldest ready SID, takes it in service, and drains its
// queue one item at a time (the queue may keep growing mid-drain). The
// in-service mark is what makes same-SID exclusion airtight: no other
// worker can touch this SID until the drain observes an empty queue and
// releases it.
func (p *Pool[T]) worker() {
	defer p.wg.Done()
	p.mu.Lock()
	for {
		for len(p.ready) == 0 && !p.closed {
			p.cond.Wait()
		}
		if len(p.ready) == 0 {
			// Closed with no ready SIDs. Any still-in-service SID is being
			// drained to empty by its owning worker, so nothing is left for
			// this one.
			p.mu.Unlock()
			return
		}
		sid := p.ready[0]
		p.ready = p.ready[1:]
		p.inService[sid] = true
		for {
			q := p.queues[sid]
			if len(q) == 0 {
				delete(p.queues, sid)
				delete(p.inService, sid)
				break
			}
			p.queues[sid] = q[1:]
			p.mu.Unlock()
			p.handle(q[0])
			p.mu.Lock()
		}
	}
}
