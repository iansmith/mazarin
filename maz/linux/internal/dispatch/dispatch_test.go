package dispatch

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// item is the test work unit: a SID plus a per-SID sequence number stamped
// at enqueue time (mirroring delegate arrival order on the file lane).
type item struct {
	sid int16
	seq int
	op  string
}

func itemKey(it item) int16 { return it.sid }

const waitTimeout = 5 * time.Second

func waitFor(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(waitTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// TestSameSIDExclusion is the deterministic core of the MAZ-156 contract:
// while an earlier same-SID item is still in its handler, a later item for
// that SID must NOT be handed to another worker — even when free workers
// are available and provably draining other work.
func TestSameSIDExclusion(t *testing.T) {
	aStarted := make(chan struct{})
	aGate := make(chan struct{})
	cDone := make(chan struct{})
	done := make(chan struct{})
	var bStarted atomic.Bool
	var handled atomic.Int32

	p := NewPool(4, itemKey, func(it item) {
		switch it.op {
		case "A":
			close(aStarted)
			<-aGate
		case "B":
			bStarted.Store(true)
		case "C":
			close(cDone)
		}
		if handled.Add(1) == 3 {
			close(done)
		}
	})
	defer p.Close()
	// Release the parked handler before Close during unwind (LIFO), so a
	// t.Fatal on the assertion path can't deadlock Close on the parked worker.
	releaseA := sync.OnceFunc(func() { close(aGate) })
	defer releaseA()

	p.Enqueue(item{sid: 5, seq: 0, op: "A"})
	waitFor(t, "A to enter its handler", aStarted)

	// B (same SID, later) then C (different SID). C completing proves the
	// pool had free workers and consumed the queue past B.
	p.Enqueue(item{sid: 5, seq: 1, op: "B"})
	p.Enqueue(item{sid: 6, seq: 0, op: "C"})
	waitFor(t, "C (sid 6) to complete while A parked", cDone)

	// Give a mis-ordered dispatcher every chance to start B wrongly.
	time.Sleep(100 * time.Millisecond)
	if bStarted.Load() {
		t.Fatal("same-SID exclusion violated: B (sid 5) entered its handler while A (sid 5) was still in-flight")
	}

	releaseA()
	waitFor(t, "all items to finish after releasing A", done)
	if !bStarted.Load() {
		t.Fatal("B was never handled after A completed")
	}
}

// TestExecveScenario encodes the observed stage-2 failure shape: a vfork
// transient's dup3(pipe→1) and chdir run on the parent's SID, followed by
// the execve that consumes their effects. The execve handler must observe
// both prior ops complete. The dup3 handler is deliberately slow, so a
// dispatcher that lets a later same-SID item win the race (the pre-fix
// worker-pool behavior) processes execve first.
func TestExecveScenario(t *testing.T) {
	var mu sync.Mutex
	var completed []string
	var atExecve []string
	done := make(chan struct{})

	p := NewPool(8, itemKey, func(it item) {
		if it.op == "dup3" {
			time.Sleep(10 * time.Millisecond) // descheduled worker, mid-storm
		}
		mu.Lock()
		if it.op == "execve" {
			atExecve = append([]string(nil), completed...)
		}
		completed = append(completed, it.op)
		if len(completed) == 3 {
			close(done)
		}
		mu.Unlock()
	})
	defer p.Close()

	p.Enqueue(item{sid: 10, seq: 0, op: "dup3"})
	p.Enqueue(item{sid: 10, seq: 1, op: "chdir"})
	p.Enqueue(item{sid: 10, seq: 2, op: "execve"})
	waitFor(t, "all three ops to complete", done)

	if len(atExecve) != 2 || atExecve[0] != "dup3" || atExecve[1] != "chdir" {
		t.Fatalf("execve ran with pre-exec setup incomplete: saw %v before execve, want [dup3 chdir]", atExecve)
	}
}

// TestSameSIDFIFOOrder stresses many SIDs concurrently and requires every
// SID's items to be handled in exact enqueue order.
func TestSameSIDFIFOOrder(t *testing.T) {
	const (
		sids         = 8
		itemsPerSID  = 200
		workerCount  = 8
	)
	var mu sync.Mutex
	got := make(map[int16][]int)
	var handled atomic.Int32
	done := make(chan struct{})

	p := NewPool(workerCount, itemKey, func(it item) {
		if it.seq%7 == 0 {
			time.Sleep(50 * time.Microsecond)
		}
		runtime.Gosched()
		mu.Lock()
		got[it.sid] = append(got[it.sid], it.seq)
		mu.Unlock()
		if handled.Add(1) == sids*itemsPerSID {
			close(done)
		}
	})
	defer p.Close()

	// Interleave SIDs the way the single delegate reader would see them.
	for seq := 0; seq < itemsPerSID; seq++ {
		for sid := int16(1); sid <= sids; sid++ {
			p.Enqueue(item{sid: sid, seq: seq})
		}
	}
	waitFor(t, "all items to be handled", done)

	for sid := int16(1); sid <= sids; sid++ {
		seqs := got[sid]
		if len(seqs) != itemsPerSID {
			t.Fatalf("sid %d: handled %d items, want %d", sid, len(seqs), itemsPerSID)
		}
		for i, s := range seqs {
			if s != i {
				t.Fatalf("sid %d: FIFO order violated at position %d: got seq %d (full order prefix: %v...)",
					sid, i, s, seqs[:min(i+3, len(seqs))])
			}
		}
	}
}

// TestCrossSIDNoHeadOfLine is the DoD's no-regression clause at unit level:
// one SID parked in a slow handler must not stall a different SID's item
// while a worker is free. This passes on the pre-fix pool too — it guards
// the property the pool exists to provide (MAZ-149 exp2).
func TestCrossSIDNoHeadOfLine(t *testing.T) {
	slowStarted := make(chan struct{})
	slowGate := make(chan struct{})
	fastDone := make(chan struct{})

	p := NewPool(2, itemKey, func(it item) {
		switch it.op {
		case "slow":
			close(slowStarted)
			<-slowGate
		case "fast":
			close(fastDone)
		}
	})
	defer p.Close()
	releaseSlow := sync.OnceFunc(func() { close(slowGate) })
	defer releaseSlow()

	p.Enqueue(item{sid: 1, seq: 0, op: "slow"})
	waitFor(t, "slow (sid 1) to enter its handler", slowStarted)
	p.Enqueue(item{sid: 2, seq: 0, op: "fast"})
	waitFor(t, "fast (sid 2) to complete while sid 1 is parked", fastDone)
	releaseSlow()
}

// TestBoundedConcurrency guards the fixed-pool constraint: even with far
// more runnable SIDs than workers, no more than `workers` handlers run
// concurrently (the .maz runtime cannot tolerate unbounded goroutine
// creation — see the fileLaneWorkers comment in maz/linux/main.go).
func TestBoundedConcurrency(t *testing.T) {
	const workerCount = 4
	var inFlight, highWater atomic.Int32
	var handled atomic.Int32
	done := make(chan struct{})

	p := NewPool(workerCount, itemKey, func(it item) {
		n := inFlight.Add(1)
		for {
			hw := highWater.Load()
			if n <= hw || highWater.CompareAndSwap(hw, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)
		if handled.Add(1) == 64 {
			close(done)
		}
	})
	defer p.Close()

	for i := 0; i < 64; i++ {
		p.Enqueue(item{sid: int16(i + 1), seq: 0}) // 64 distinct SIDs
	}
	waitFor(t, "all 64 items to be handled", done)

	if hw := highWater.Load(); hw > workerCount {
		t.Fatalf("concurrency exceeded pool size: high-water %d > %d workers", hw, workerCount)
	}
}

// TestSIDRequeueAfterDrain: once a SID's queue drains and it goes idle, a
// later item for the same SID must still be handled (idle → ready → in
// service → idle lifecycle correctness).
func TestSIDRequeueAfterDrain(t *testing.T) {
	var handled atomic.Int32
	round1 := make(chan struct{})
	round2 := make(chan struct{})

	p := NewPool(2, itemKey, func(it item) {
		switch handled.Add(1) {
		case 2:
			close(round1)
		case 4:
			close(round2)
		}
	})
	defer p.Close()

	p.Enqueue(item{sid: 3, seq: 0})
	p.Enqueue(item{sid: 3, seq: 1})
	waitFor(t, "first round for sid 3 to drain", round1)

	p.Enqueue(item{sid: 3, seq: 2})
	p.Enqueue(item{sid: 3, seq: 3})
	waitFor(t, "second round for sid 3 after idle", round2)
}

// TestCloseDrainsQueuedItems: Close must not drop items already enqueued.
func TestCloseDrainsQueuedItems(t *testing.T) {
	var handled atomic.Int32
	p := NewPool(2, itemKey, func(it item) {
		time.Sleep(time.Millisecond)
		handled.Add(1)
	})
	const n = 20
	for i := 0; i < n; i++ {
		p.Enqueue(item{sid: int16(i%4 + 1), seq: i / 4})
	}
	p.Close()
	if got := handled.Load(); got != n {
		t.Fatalf("Close dropped work: handled %d of %d enqueued items", got, n)
	}
}

