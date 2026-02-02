// Package timer provides a Go-idiomatic timer API built on the kernel's
// soft IRQ timer mechanism. Callers receive channels instead of dealing
// with syscalls, slots, or ring buffers.
//
// The package auto-initializes on first use: it discovers the kernel timer
// device, registers a soft IRQ slot, and starts a background goroutine that
// dispatches expired deadlines to their channels.
//
// Use Now() (not time.Now()) for deadlines — the kernel timer uses RTC
// wall-clock time, which differs from the tick-based monotonic clock that
// time.Now() reads via clock_gettime.
package timer

import (
	"errors"
	"mazzy/mazarin/sys"
	"mazzy/shared/hid"
	"sort"
	"sync"
	"time"
)

// defaultSlot is the soft IRQ slot used by this package.
// Chosen high to avoid conflicts with per-device slots (keyboard=0, mouse=1, etc.).
const defaultSlot = 30

var (
	initOnce  sync.Once
	initErr   error
	timerSlot int

	mu      sync.Mutex
	cond    *sync.Cond
	pending []entry // sorted by deadline, earliest first
)

type entry struct {
	deadline time.Time
	ch       chan time.Time
}

func doInit() {
	cond = sync.NewCond(&mu)

	devices, err := sys.QueryInputDevices()
	if err != nil {
		initErr = err
		return
	}
	for _, dev := range devices {
		if dev.DeviceType == hid.DeviceTypeTimer {
			if err := sys.RegisterSoftIRQ(dev.IRQNum, defaultSlot); err != nil {
				initErr = err
				return
			}
			timerSlot = defaultSlot
			go loop()
			return
		}
	}
	initErr = errors.New("timer: no timer device found")
}

// Now returns the current wall-clock time from the kernel.
// Use this instead of time.Now() when computing deadlines.
func Now() time.Time {
	ts, err := sys.GetTime()
	if err != nil {
		return time.Time{}
	}
	return time.Unix(int64(ts.Seconds), int64(ts.Nanoseconds))
}

// SetDeadline registers a wall-clock deadline and returns a channel that
// will receive the current time once the deadline expires. The channel is
// buffered (capacity 1) so the send never blocks.
//
// Deadlines should be computed from Now(), not time.Now().
func SetDeadline(t time.Time) (chan time.Time, error) {
	initOnce.Do(doInit)
	if initErr != nil {
		return nil, initErr
	}

	ch := make(chan time.Time, 1)
	e := entry{deadline: t, ch: ch}

	mu.Lock()
	i := sort.Search(len(pending), func(j int) bool {
		return pending[j].deadline.After(t)
	})
	pending = append(pending, entry{})
	copy(pending[i+1:], pending[i:])
	pending[i] = e
	isEarliest := i == 0
	mu.Unlock()

	// If this is the new earliest deadline, tell the kernel immediately
	// so the blocked WaitSoftIRQ wakes at the right time.
	if isEarliest {
		sys.SetTimerDeadline(timerSlot, uint64(t.Unix()), uint64(t.Nanosecond()))
	}
	cond.Signal()

	return ch, nil
}

// Sleep blocks for at least the given duration.
func Sleep(d time.Duration) error {
	ch, err := SetDeadline(Now().Add(d))
	if err != nil {
		return err
	}
	<-ch
	return nil
}

// loop is the background goroutine that blocks on the kernel timer slot
// and dispatches expired deadlines.
func loop() {
	var buf hid.SoftIRQReturn
	for {
		// Wait until there is at least one pending deadline.
		mu.Lock()
		for len(pending) == 0 {
			cond.Wait()
		}
		earliest := pending[0].deadline
		mu.Unlock()

		// Set kernel deadline to the earliest pending entry.
		sys.SetTimerDeadline(timerSlot, uint64(earliest.Unix()), uint64(earliest.Nanosecond()))

		// Block until the kernel pushes timer events.
		n, err := sys.WaitSoftIRQ(timerSlot, &buf)
		if err != nil || n == 0 {
			continue
		}

		// Extract current wall-clock time from the timer events.
		var nowSec uint64
		nowSec = uint64(buf.Events[0].Value)
		nowNsec := uint64(buf.Events[1].Value)
		if n >= 3 {
			nowSec |= uint64(buf.Events[2].Value) << 32
		}
		now := time.Unix(int64(nowSec), int64(nowNsec))

		// Collect all expired deadlines.
		mu.Lock()
		cutoff := 0
		for cutoff < len(pending) && !pending[cutoff].deadline.After(now) {
			cutoff++
		}
		expired := make([]entry, cutoff)
		copy(expired, pending[:cutoff])
		pending = pending[cutoff:]
		mu.Unlock()

		// Notify waiters outside the lock.
		for _, e := range expired {
			e.ch <- now
		}
	}
}
