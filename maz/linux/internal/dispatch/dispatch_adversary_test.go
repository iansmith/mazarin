package dispatch

// Adversary gap tests (MAZ-156 Phase 0f). These harden the contract beyond
// dispatch_test.go and pin four deliberate contract decisions:
//
//   - NewPool panics on workers <= 0 (fail fast at boot, not deadlock later).
//   - Enqueue after Close is silently dropped — never a panic, never a hang
//     (shutdown-race tolerance).
//   - Cross-SID scheduling is oldest-ready-SID first (FIFO over ready SIDs,
//     anti-starvation) — not arbitrary map-iteration order.
//   - Enqueue may be called from inside a handler (reentrant) without
//     deadlock; the chained same-SID item runs after the handler returns.
//
// Scope note: same-SID FIFO is defined relative to Enqueue call order, which
// is well-defined because production has a SINGLE reader goroutine enqueueing.
// Multiple concurrent enqueuers for the SAME SID are a documented
// precondition violation, deliberately not exercised here.

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSameSIDDeepQueueWhileInService enqueues several same-SID items while
// the SID is busy, and requires they drain in exact enqueue order. The core
// exclusion test only ever queues ONE successor behind the in-flight item; a
// dispatcher that special-cases a single pending successor (or drops/reorders
// the middle of a deep backlog) would pass it and fail here.
func TestSameSIDDeepQueueWhileInService(t *testing.T) {
	const depth = 10
	aStarted := make(chan struct{})
	aGate := make(chan struct{})
	var mu sync.Mutex
	var order []int
	done := make(chan struct{})

	p := NewPool(4, itemKey, func(it item) {
		if it.seq == 0 {
			close(aStarted)
			<-aGate
		}
		mu.Lock()
		order = append(order, it.seq)
		if len(order) == depth+1 {
			close(done)
		}
		mu.Unlock()
	})
	defer p.Close()
	releaseA := sync.OnceFunc(func() { close(aGate) })
	defer releaseA()

	p.Enqueue(item{sid: 5, seq: 0})
	waitFor(t, "seq 0 to enter its handler", aStarted)
	for i := 1; i <= depth; i++ {
		p.Enqueue(item{sid: 5, seq: i})
	}
	releaseA()
	waitFor(t, "all queued same-SID items to drain", done)

	mu.Lock()
	defer mu.Unlock()
	for i, s := range order {
		if s != i {
			t.Fatalf("deep-queue FIFO violated at position %d: %v", i, order)
		}
	}
}

// TestOldestReadySIDFairness pins the anti-starvation half of cross-SID
// independence: with one worker, SIDs that became ready earlier are serviced
// before SIDs that became ready later. An implementation picking an
// arbitrary ready SID (e.g. Go map iteration) would starve early SIDs behind
// a stream of later arrivals.
func TestOldestReadySIDFairness(t *testing.T) {
	const nSIDs = 20
	var mu sync.Mutex
	var serviceOrder []int16
	done := make(chan struct{})

	p := NewPool(1, itemKey, func(it item) {
		mu.Lock()
		serviceOrder = append(serviceOrder, it.sid)
		if len(serviceOrder) == nSIDs {
			close(done)
		}
		mu.Unlock()
	})
	defer p.Close()

	for sid := int16(1); sid <= nSIDs; sid++ {
		p.Enqueue(item{sid: sid, seq: 0})
	}
	waitFor(t, "all SIDs to be serviced", done)

	mu.Lock()
	defer mu.Unlock()
	for i, sid := range serviceOrder {
		if sid != int16(i+1) {
			t.Fatalf("service order not oldest-ready-first: got %v, want 1..%d", serviceOrder, nSIDs)
		}
	}
}

// TestSIDManyIdleBusyRoundsPreservesOrder cycles one SID through many
// idle→busy→idle transitions and requires strict seq order across all rounds
// combined — catching state left over from a prior service period (a stale
// in-service flag, an unreset queue buffer) that a single-round test misses.
func TestSIDManyIdleBusyRoundsPreservesOrder(t *testing.T) {
	const rounds = 50
	var mu sync.Mutex
	var order []int
	done := make(chan struct{})

	p := NewPool(4, itemKey, func(it item) {
		mu.Lock()
		order = append(order, it.seq)
		if len(order) == rounds*2 {
			close(done)
		}
		mu.Unlock()
	})
	defer p.Close()

	seq := 0
	for r := 0; r < rounds; r++ {
		p.Enqueue(item{sid: 9, seq: seq})
		seq++
		p.Enqueue(item{sid: 9, seq: seq})
		seq++
		time.Sleep(200 * time.Microsecond) // let the SID drain to idle between rounds
	}
	waitFor(t, "all rounds to complete", done)

	mu.Lock()
	defer mu.Unlock()
	for i, s := range order {
		if s != i {
			t.Fatalf("idle/busy cycling violated FIFO at position %d: %v", i, order[:min(i+3, len(order))])
		}
	}
}

// TestEnqueueFromHandler: a handler enqueueing a follow-up item for its own
// SID must not deadlock (the pool must not hold its dispatch lock across
// handler invocations), and the chained item runs after the enqueuing
// handler returns.
func TestEnqueueFromHandler(t *testing.T) {
	var mu sync.Mutex
	var order []string
	done := make(chan struct{})

	var p *Pool[item]
	p = NewPool(4, itemKey, func(it item) {
		mu.Lock()
		order = append(order, it.op)
		n := len(order)
		mu.Unlock()
		if it.op == "first" {
			p.Enqueue(item{sid: 7, seq: 1, op: "chained"})
		}
		if n == 2 {
			close(done)
		}
	})
	defer p.Close()

	p.Enqueue(item{sid: 7, seq: 0, op: "first"})
	waitFor(t, "the chained item to run", done)

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "first" || order[1] != "chained" {
		t.Fatalf("reentrant enqueue misordered: %v", order)
	}
}

// TestEnqueueAfterClose pins the "stops accepting" half of the Close
// contract: a post-Close Enqueue is silently dropped — it must neither panic
// (the pre-fix channel semantics panic on send-after-close) nor hang.
func TestEnqueueAfterClose(t *testing.T) {
	p := NewPool(2, itemKey, func(it item) {})
	p.Close()

	enqDone := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Enqueue after Close panicked: %v (contract: silently dropped)", r)
			}
			close(enqDone)
		}()
		p.Enqueue(item{sid: 1, seq: 0})
	}()
	waitFor(t, "post-Close Enqueue to return (not hang)", enqDone)
}

// TestConcurrencyReachesPoolSize is the lower bound TestBoundedConcurrency
// lacks: with `workers` independent ready SIDs, the pool must actually run
// `workers` handlers concurrently. An accidental global serialization (a
// lock held across handler calls) would pass every upper-bound test forever.
func TestConcurrencyReachesPoolSize(t *testing.T) {
	const workerCount = 4
	var inFlight atomic.Int32
	reachedFull := make(chan struct{})
	var once sync.Once
	release := make(chan struct{})

	p := NewPool(workerCount, itemKey, func(it item) {
		if inFlight.Add(1) == workerCount {
			once.Do(func() { close(reachedFull) })
		}
		<-release
		inFlight.Add(-1)
	})
	releaseAll := sync.OnceFunc(func() { close(release) })
	defer p.Close()
	defer releaseAll()

	for i := 0; i < workerCount; i++ {
		p.Enqueue(item{sid: int16(i + 1), seq: 0}) // distinct SIDs — no exclusion applies
	}
	waitFor(t, "concurrency to reach the pool size", reachedFull)
	releaseAll()
}

// TestNewPoolRejectsNonPositiveWorkers: a zero-worker pool silently
// deadlocks every Enqueue forever; the constructor must reject it loudly.
func TestNewPoolRejectsNonPositiveWorkers(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewPool(0, ...) did not panic; a zero-worker pool deadlocks on first Enqueue")
		}
	}()
	_ = NewPool(0, itemKey, func(it item) {})
}

// TestSingleWorkerSingleItem is the minimal non-empty case, with sid=0 as
// the boundary key value (SIDs are plain int16 keys; 0 is not a sentinel).
func TestSingleWorkerSingleItem(t *testing.T) {
	done := make(chan struct{})
	p := NewPool(1, itemKey, func(it item) {
		close(done)
	})
	defer p.Close()
	p.Enqueue(item{sid: 0, seq: 0})
	waitFor(t, "the single item to be handled", done)
}

// TestCloseDrainsInFIFOOrder: Close-time draining must still respect
// same-SID FIFO — TestCloseDrainsQueuedItems only checks the count drained,
// and shutdown paths are where implementers special-case ordering away.
func TestCloseDrainsInFIFOOrder(t *testing.T) {
	var mu sync.Mutex
	got := make(map[int16][]int)
	p := NewPool(2, itemKey, func(it item) {
		time.Sleep(200 * time.Microsecond)
		mu.Lock()
		got[it.sid] = append(got[it.sid], it.seq)
		mu.Unlock()
	})
	const perSID = 10
	for sid := int16(1); sid <= 4; sid++ {
		for seq := 0; seq < perSID; seq++ {
			p.Enqueue(item{sid: sid, seq: seq})
		}
	}
	p.Close()

	for sid := int16(1); sid <= 4; sid++ {
		seqs := got[sid]
		if len(seqs) != perSID {
			t.Fatalf("sid %d: drained %d of %d enqueued items", sid, len(seqs), perSID)
		}
		for i, s := range seqs {
			if s != i {
				t.Fatalf("sid %d: Close-time drain violated FIFO at %d: %v", sid, i, seqs)
			}
		}
	}
}
