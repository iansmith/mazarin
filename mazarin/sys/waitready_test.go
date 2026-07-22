package sys

import (
	"errors"
	"testing"
	"time"
)

// MAZ-161 — ready-wait loop spec. Under boot-storm congestion a 200–500ms
// sleep wake can be delayed multiple seconds (timer/scheduler starvation,
// MAZ-8 family). The wait loop must therefore ALWAYS poll again after its
// last sleep before declaring the deadline exhausted: an oversleep that
// carries time past the deadline must not convert "peer became ready during
// the sleep" into ErrNotReady. sharetest/shareprobe failed exactly this way
// (one poll at t+1ms, one oversleeping ~5s, no re-poll, FAIL — while the
// peer had been ready since ~t+1s).

// fakeReadyClock drives waitLoop with a scripted clock: each sleep advances
// time by the scripted delta (simulating oversleeps), and polls report ready
// starting at a given instant.
type fakeReadyClock struct {
	now        time.Time
	sleepJumps []time.Duration // consumed per sleep; last value repeats
	sleeps     int
	readyAt    time.Time
	polls      int
}

func (c *fakeReadyClock) Now() time.Time { return c.now }

func (c *fakeReadyClock) Sleep(d time.Duration) {
	jump := d
	if len(c.sleepJumps) > 0 {
		i := c.sleeps
		if i >= len(c.sleepJumps) {
			i = len(c.sleepJumps) - 1
		}
		jump = c.sleepJumps[i]
	}
	c.sleeps++
	c.now = c.now.Add(jump)
}

func (c *fakeReadyClock) Poll() error {
	c.polls++
	if !c.now.Before(c.readyAt) {
		return nil
	}
	return ErrNotReady
}

// TestWaitLoopFinalPollAfterOversleep — the MAZ-161 shape: peer ready at
// t+1s, first sleep oversleeps to t+6s (past the 5s deadline). The loop must
// poll once more after the oversleep and succeed, not report ErrNotReady.
func TestWaitLoopFinalPollAfterOversleep(t *testing.T) {
	start := time.Unix(1000, 0)
	c := &fakeReadyClock{
		now:        start,
		sleepJumps: []time.Duration{6 * time.Second},
		readyAt:    start.Add(1 * time.Second),
	}
	err := waitLoop(start.Add(5*time.Second), c.Now, c.Sleep, c.Poll, nil)
	if err != nil {
		t.Fatalf("waitLoop = %v, want nil (peer became ready during the oversleep; the post-sleep poll must see it)", err)
	}
	if c.polls < 2 {
		t.Fatalf("polls = %d, want ≥2 (one before the sleep, one after)", c.polls)
	}
}

// TestWaitLoopNotReadyAfterHonestExhaustion — the peer never readies: normal
// cadence, deadline passes, ErrNotReady. The final poll changes nothing here.
func TestWaitLoopNotReadyAfterHonestExhaustion(t *testing.T) {
	start := time.Unix(1000, 0)
	c := &fakeReadyClock{
		now:     start,
		readyAt: start.Add(time.Hour), // never within the window
	}
	err := waitLoop(start.Add(5*time.Second), c.Now, c.Sleep, c.Poll, nil)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("waitLoop = %v, want ErrNotReady", err)
	}
	if c.polls < 10 {
		t.Fatalf("polls = %d, want ≥10 (5s window / ≤500ms cadence)", c.polls)
	}
}

// TestWaitLoopSucceedsImmediately — peer already ready: no sleep at all.
func TestWaitLoopSucceedsImmediately(t *testing.T) {
	start := time.Unix(1000, 0)
	c := &fakeReadyClock{now: start, readyAt: start}
	if err := waitLoop(start.Add(5*time.Second), c.Now, c.Sleep, c.Poll, nil); err != nil {
		t.Fatalf("waitLoop = %v, want nil", err)
	}
	if c.sleeps != 0 {
		t.Fatalf("sleeps = %d, want 0 (ready on the first poll)", c.sleeps)
	}
}

// TestWaitLoopAmbiguousIsFatal — ErrAmbiguousShepherd aborts immediately,
// no retries.
func TestWaitLoopAmbiguousIsFatal(t *testing.T) {
	start := time.Unix(1000, 0)
	c := &fakeReadyClock{now: start}
	polls := 0
	err := waitLoop(start.Add(5*time.Second), c.Now, c.Sleep, func() error {
		polls++
		return ErrAmbiguousShepherd
	}, nil)
	if !errors.Is(err, ErrAmbiguousShepherd) {
		t.Fatalf("waitLoop = %v, want ErrAmbiguousShepherd", err)
	}
	if polls != 1 {
		t.Fatalf("polls = %d, want 1 (fatal, no retry)", polls)
	}
}

// TestWaitLoopNoShepherdWhenNeverSeen — the shepherd never exists at all:
// ErrNoShepherd, distinguishing "never found" from "found but not ready".
func TestWaitLoopNoShepherdWhenNeverSeen(t *testing.T) {
	start := time.Unix(1000, 0)
	c := &fakeReadyClock{now: start}
	err := waitLoop(start.Add(1*time.Second), c.Now, c.Sleep, func() error {
		return ErrNoShepherd
	}, nil)
	if !errors.Is(err, ErrNoShepherd) {
		t.Fatalf("waitLoop = %v, want ErrNoShepherd", err)
	}
}
