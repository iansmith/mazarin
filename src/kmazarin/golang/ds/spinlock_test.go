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
// Note: Minimal contention test because spinlock has 2μs timeout (kernel-appropriate)
func TestSpinlockConcurrent(t *testing.T) {
	var s Spinlock
	var counter int64
	const numGoroutines = 2 // Minimal contention for 2μs timeout
	const increments = 50   // Minimal iterations to reduce contention

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				s.Lock()
				// Critical section - keep it very short
				counter++
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
	elapsed := CompareAndSwapUint32(&val, 0, 1)
	if elapsed == 0 {
		t.Error("CAS(0->1) failed (returned 0)")
	}
	if val != 1 {
		t.Errorf("after CAS: expected val=1, got %d", val)
	}
	t.Logf("CAS(0->1) succeeded in %d ticks", elapsed)

	// CAS should fail when old doesn't match
	elapsed = CompareAndSwapUint32(&val, 0, 2)
	if elapsed != 0 {
		t.Error("CAS(0->2) succeeded when val=1")
	}
	if val != 1 {
		t.Errorf("after failed CAS: expected val=1, got %d", val)
	}

	// CAS should succeed when old matches again
	elapsed = CompareAndSwapUint32(&val, 1, 42)
	if elapsed == 0 {
		t.Error("CAS(1->42) failed (returned 0)")
	}
	if val != 42 {
		t.Errorf("after CAS: expected val=42, got %d", val)
	}
	t.Logf("CAS(1->42) succeeded in %d ticks", elapsed)
}

// TestCurrentTime tests the CurrentTime function with fake and real time
func TestCurrentTime(t *testing.T) {
	// Test with fakeTime = 0 (should read hardware counter)
	realTime1 := CurrentTime(0)
	realTime2 := CurrentTime(0)

	if realTime2 < realTime1 {
		t.Errorf("real time went backwards: %d -> %d", realTime1, realTime2)
	}
	t.Logf("Real time: %d -> %d (delta: %d)", realTime1, realTime2, realTime2-realTime1)

	// Test with fakeTime = 12345 (should return fake value)
	fakeTime := CurrentTime(12345)
	if fakeTime != 12345 {
		t.Errorf("expected fake time 12345, got %d", fakeTime)
	}

	// Test with another fake value
	fakeTime = CurrentTime(99999)
	if fakeTime != 99999 {
		t.Errorf("expected fake time 99999, got %d", fakeTime)
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

// ==============================================================================
// Test stub verification tests
// ==============================================================================

// TestSpinlockWin verifies the SpinlockWin stub always succeeds
func TestSpinlockWin(t *testing.T) {
	var s Spinlock
	s.Init(SpinlockWin, NanoWaitStub)

	// Should succeed immediately without waiting (no panic)
	// Note: SpinlockWin returns true but doesn't modify s.locked
	// (it's a stub for testing logic flow, not actual lock state)
	s.Lock()

	// Lock() returned without panic, so SpinlockWin worked
	// We can't check s.locked because the stub doesn't modify it
	t.Log("SpinlockWin allowed Lock() to succeed immediately")
}

// TestSpinlockLose verifies the SpinlockLose stub always fails
func TestSpinlockLose(t *testing.T) {
	var s Spinlock
	s.Init(SpinlockLose, NanoWaitStub)

	// Should panic after all attempts fail (no actual wait due to NanoWaitStub)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when all CAS attempts fail")
		}
	}()
	s.Lock()
}

// TestNanoWaitStub verifies the NanoWaitStub does nothing
func TestNanoWaitStub(t *testing.T) {
	start := time.Now()
	NanoWaitStub(31) // Would normally wait 500ns
	elapsed := time.Since(start)

	// Should be nearly instant (< 1ms including overhead)
	if elapsed > time.Millisecond {
		t.Errorf("NanoWaitStub took %v, expected < 1ms", elapsed)
	}
}

// TestZeroValueSpinlock verifies zero-value Spinlock works immediately
func TestZeroValueSpinlock(t *testing.T) {
	var s Spinlock // Zero-value, no initialization required

	// Should be able to lock/unlock immediately (uses real assembly functions)
	s.Lock()
	if s.locked != 1 {
		t.Errorf("expected locked=1, got %d", s.locked)
	}
	s.Unlock()
	if s.locked != 0 {
		t.Errorf("expected locked=0, got %d", s.locked)
	}
}

// TestInitOverridesDefaults verifies Init() overrides zero-value behavior
func TestInitOverridesDefaults(t *testing.T) {
	var s Spinlock

	// Initially uses defaults (should work)
	s.Lock()
	s.Unlock()

	// After Init with SpinlockWin, should still work
	s.Init(SpinlockWin, NanoWaitStub)
	s.Lock()
	s.Unlock()

	// After Init with SpinlockLose, should panic
	s.Init(SpinlockLose, NanoWaitStub)
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic after Init with SpinlockLose")
		}
	}()
	s.Lock()
}
