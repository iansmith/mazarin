//go:build arm64

package ds

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSpinlockBasic tests basic lock/unlock in single-threaded context
func TestSpinlockBasic(t *testing.T) {
	var s Spinlock

	// Initially unlocked
	if s.locked != 0 {
		t.Errorf("expected locked=0, got %d", s.locked)
	}

	// Lock should succeed
	s.Lock()
	if s.locked != 1 {
		t.Errorf("after Lock(): expected locked=1, got %d", s.locked)
	}

	// Unlock should clear
	s.Unlock()
	if s.locked != 0 {
		t.Errorf("after Unlock(): expected locked=0, got %d", s.locked)
	}

	// Should be able to lock again
	s.Lock()
	if s.locked != 1 {
		t.Errorf("after second Lock(): expected locked=1, got %d", s.locked)
	}
	s.Unlock()
}

// TestSpinlockConcurrent tests multiple goroutines contending for the lock
func TestSpinlockConcurrent(t *testing.T) {
	var s Spinlock
	var counter int64
	const numGoroutines = 10
	const increments = 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				s.Lock()
				// Critical section
				val := atomic.LoadInt64(&counter)
				val++
				atomic.StoreInt64(&counter, val)
				s.Unlock()
			}
		}()
	}

	wg.Wait()

	expected := int64(numGoroutines * increments)
	if counter != expected {
		t.Errorf("expected counter=%d, got %d", expected, counter)
	}
}

// TestSpinlockStress performs a high-contention stress test
func TestSpinlockStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	var s Spinlock
	var counter int64
	const numGoroutines = 20
	const duration = 100 * time.Millisecond

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s.Lock()
					val := atomic.LoadInt64(&counter)
					val++
					atomic.StoreInt64(&counter, val)
					s.Unlock()
				}
			}
		}()
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	t.Logf("stress test: %d increments in %v", counter, duration)

	// Just verify counter was incremented (exact count doesn't matter)
	if counter == 0 {
		t.Error("expected counter > 0, got 0")
	}
}

// TestNanoWait verifies nanoWait timing accuracy
func TestNanoWait(t *testing.T) {
	// Test that nanoWait actually waits
	// We can't test exact timing in a unit test (Go scheduler, OS jitter),
	// but we can verify it takes at least some time

	const ticks = 31 // 500ns at 62.5MHz

	start := time.Now()
	nanoWait(ticks)
	elapsed := time.Since(start)

	// Should take at least 100ns (very conservative, actual is 500ns)
	// Allow for measurement overhead and timer resolution
	if elapsed < 100*time.Nanosecond {
		t.Errorf("nanoWait(%d) took %v, expected >= 100ns", ticks, elapsed)
	}

	// Should not take an unreasonably long time (> 100μs)
	if elapsed > 100*time.Microsecond {
		t.Errorf("nanoWait(%d) took %v, expected < 100μs", ticks, elapsed)
	}

	t.Logf("nanoWait(%d ticks) took %v", ticks, elapsed)
}

// TestCompareAndSwapUint32 tests the CAS primitive directly
func TestCompareAndSwapUint32(t *testing.T) {
	var val uint32 = 0

	// CAS should succeed when old matches
	if !CompareAndSwapUint32(&val, 0, 1) {
		t.Error("CAS(0->1) failed")
	}
	if val != 1 {
		t.Errorf("after CAS: expected val=1, got %d", val)
	}

	// CAS should fail when old doesn't match
	if CompareAndSwapUint32(&val, 0, 2) {
		t.Error("CAS(0->2) succeeded when val=1")
	}
	if val != 1 {
		t.Errorf("after failed CAS: expected val=1, got %d", val)
	}

	// CAS should succeed when old matches again
	if !CompareAndSwapUint32(&val, 1, 42) {
		t.Error("CAS(1->42) failed")
	}
	if val != 42 {
		t.Errorf("after CAS: expected val=42, got %d", val)
	}
}

// TestStoreUint32 tests the atomic store primitive directly
func TestStoreUint32(t *testing.T) {
	var val uint32 = 0

	StoreUint32(&val, 99)
	if val != 99 {
		t.Errorf("after Store(99): expected val=99, got %d", val)
	}

	StoreUint32(&val, 0)
	if val != 0 {
		t.Errorf("after Store(0): expected val=0, got %d", val)
	}
}

// BenchmarkSpinlockUncontended benchmarks lock/unlock with no contention
func BenchmarkSpinlockUncontended(b *testing.B) {
	var s Spinlock
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Lock()
		s.Unlock()
	}
}

// BenchmarkSpinlockContended benchmarks lock/unlock with contention
func BenchmarkSpinlockContended(b *testing.B) {
	var s Spinlock
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Lock()
			s.Unlock()
		}
	})
}
