package ds

// Spinlock implements a simple spinlock with calibrated backoff.
//
// Lock acquisition strategy:
// - Try CAS 5 times with 500ns backoff between attempts
// - Total wait: 4 × 500ns = 2 microseconds
// - Panic if unable to acquire after 5 attempts
//
// Timing (at 62.5MHz QEMU default):
// - SpinBackoffTicks = 31 = 500 nanoseconds
//
// Usage:
//   var s Spinlock    // Zero-value uses real assembly functions
//   s.Lock()          // Ready to use immediately
//   s.Unlock()
//
// Testability:
// - Call Init() to override with test stubs
// - Default (zero-value) uses real atomics and timing
//
// Safety:
// - All methods are //go:nosplit safe
// - NO allocations - safe for interrupt handlers
// - Uses ARMv8.0 compatible atomic instructions (LDAXRW/STLXRW)
// - Compatible with all ARM64 variants
type Spinlock struct {
	locked   uint32                                              // 0 = unlocked, 1 = locked
	casFunc  func(addr *uint32, old, new uint32) uint64         // CAS (nil = real assembly)
	waitFunc func(ticks uint64)                                  // Wait operation (nil = real assembly)
}

const (
	SpinAttempts = 5000 // Total CAS attempts before giving up (increased for TCG emulation)
)

// SpinBackoffTicks is the number of clock ticks to wait between CAS attempts.
// This value is calculated at startup based on the detected timer frequency
// to achieve ~500ns backoff. Must be initialized before spinlocks are used.
//
// Default value of 31 corresponds to 500ns at 62.5MHz (QEMU default).
var SpinBackoffTicks uint64 = 31 // 500ns at 62.5MHz (default)

// InitSpinlockTiming calculates SpinBackoffTicks based on timer frequency.
// Should be called once at kernel startup after timer frequency is detected.
//
// Parameters:
//   timerFrequencyHz: Timer frequency in Hz (e.g., 62500000 for 62.5MHz)
//
// Calculation:
//   target = 500ns = 500 / 1,000,000,000 seconds
//   ticks = target × frequency = (500 × frequency) / 1,000,000,000
//
//go:nosplit
func InitSpinlockTiming(timerFrequencyHz uint64) {
	// Calculate ticks for 500ns: (500 × freq) / 1,000,000,000
	// Simplify: (freq) / 2,000,000
	SpinBackoffTicks = timerFrequencyHz / 2000000
	if SpinBackoffTicks == 0 {
		SpinBackoffTicks = 1 // Minimum 1 tick
	}
}

// Init initializes the spinlock with custom CAS and wait functions.
// This is ONLY for testing - production code should use zero-value Spinlock.
//
// Zero-value Spinlock (var s Spinlock) uses real assembly functions by default.
// Init() overrides these with test stubs.
//
// Parameters:
//   casFunc: CAS function (returns elapsed time; 0 = failure, non-zero = success)
//   waitFunc: Function to wait for specified ticks
//
// Example for testing:
//   var s Spinlock                    // Uses real functions by default
//   s.Init(SpinlockWin, NanoWaitStub) // Override with test stubs
//
//go:nosplit
func (s *Spinlock) Init(casFunc func(addr *uint32, old, new uint32) uint64, waitFunc func(ticks uint64)) {
	s.casFunc = casFunc
	s.waitFunc = waitFunc
}

// Lock acquires the spinlock.
//
// Makes 5 attempts to acquire the lock, waiting 500ns between each attempt.
// Panics if unable to acquire after all attempts (total ~2μs wait).
//
// Uses function pointers if set via Init(), otherwise uses default assembly functions.
// This allows testing with stub implementations while production code uses real atomics.
//
// CAS return value:
// - 0 indicates failure
// - Non-zero indicates success (elapsed time in ticks)
//
// Flow:
//   Attempt 1: CAS
//   Wait 500ns
//   Attempt 2: CAS
//   Wait 500ns
//   Attempt 3: CAS
//   Wait 500ns
//   Attempt 4: CAS
//   Wait 500ns
//   Attempt 5: CAS
//   → Panic if failed
//
//go:nosplit
func (s *Spinlock) Lock() {
	// Use custom functions if set, otherwise use defaults
	casFunc := s.casFunc
	if casFunc == nil {
		casFunc = CompareAndSwapUint32
	}
	waitFunc := s.waitFunc
	if waitFunc == nil {
		waitFunc = nanoWait
	}

	for attempt := 0; attempt < SpinAttempts; attempt++ {
		elapsed := casFunc(&s.locked, 0, 1)
		if elapsed != 0 {
			// Success (elapsed is non-zero)
			return
		}

		// Failed - wait before next attempt (except after last attempt)
		if attempt < SpinAttempts-1 {
			waitFunc(SpinBackoffTicks)
		}
	}

	// Failed to acquire after all attempts — call dead handler instead of panic.
	// Panic in nosplit/IRQ context causes triple fault on x86_64.
	spinlockDeadHandler()
}

// Unlock releases the spinlock.
//
// Uses atomic store with release semantics to ensure all prior
// memory operations are visible before the lock is released.
//
//go:nosplit
func (s *Spinlock) Unlock() {
	StoreUint32(&s.locked, 0)
}

// spinlockDeadHandler is called when the spinlock fails to acquire.
// Writes diagnostic to UART and halts (platform-specific assembly).
// Using this instead of panic() because panic in nosplit/IRQ context
// causes triple fault on x86_64 (the Go runtime's panic handler needs
// a working stack and g, neither of which are available in IRQ context).
func spinlockDeadHandler()

// Assembly functions implemented in spinlock_arm64.s
//
// CompareAndSwapUint32 atomically compares *addr to old, and if equal,
// stores new. Returns elapsed time (real hardware counter).
// Return value: 0 = failed, non-zero = success (elapsed ticks)
// Uses LDAXRW/STLXRW (ARMv8.0 compatible).
func CompareAndSwapUint32(addr *uint32, old, new uint32) uint64

// StoreUint32 atomically stores val to *addr with release semantics.
// Uses STLRW (ARMv8.0 compatible).
func StoreUint32(addr *uint32, val uint32)

// CurrentTime returns the current counter value.
// If fakeTime is non-zero, returns fakeTime (for testing).
// If fakeTime is zero, reads CNTVCT_EL0 (hardware counter).
// Used for timing measurements and testing.
func CurrentTime(fakeTime uint64) uint64

// nanoWait busy-waits for the specified number of clock ticks.
// Uses CNTVCT_EL0 (virtual counter) for precise timing.
// Does NOT yield CPU - spins in tight loop.
func nanoWait(ticks uint64)

// Test stubs - implemented in spinlock_arm64.s
//
// SpinlockWin simulates successful CAS.
// Returns non-zero (elapsed time, indicating success).
// For testing scenarios where lock should always succeed immediately.
func SpinlockWin(addr *uint32, old, new uint32) uint64

// SpinlockLose simulates failed CAS.
// Returns 0 (indicating failure).
// For testing scenarios where lock should always fail.
func SpinlockLose(addr *uint32, old, new uint32) uint64

// NanoWaitStub does nothing (no-op wait).
// For testing scenarios where timing waits should be skipped.
func NanoWaitStub(ticks uint64)
