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
//     while a worker is free. One shepherd parked inside a slow fsclient
//     call must not stall unrelated shepherds' delegates (the reason the
//     worker pool exists — MAZ-149 exp2).
//  3. Bounded concurrency: at most `workers` handler invocations run
//     concurrently. The pool is fixed-size because the .maz plugin runtime
//     is unstable under unbounded goroutine-creation rates (see the
//     fileLaneWorkers comment in maz/linux/main.go).
package dispatch

import "sync"

// Pool dispatches work items to a fixed set of worker goroutines.
type Pool[T any] struct {
	ch     chan T
	key    func(T) int16
	handle func(T)
	wg     sync.WaitGroup
}

// NewPool starts `workers` goroutines that serve items passed to Enqueue.
// key extracts the SID an item belongs to; handle processes one item.
func NewPool[T any](workers int, key func(T) int16, handle func(T)) *Pool[T] {
	p := &Pool[T]{
		ch:     make(chan T, workers),
		key:    key,
		handle: handle,
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for item := range p.ch {
				p.handle(item)
			}
		}()
	}
	return p
}

// Enqueue submits an item for dispatch.
func (p *Pool[T]) Enqueue(item T) {
	p.ch <- item
}

// Close stops accepting new items and waits for the workers to drain and
// exit. Only used at shepherd shutdown and in tests.
func (p *Pool[T]) Close() {
	close(p.ch)
	p.wg.Wait()
}
