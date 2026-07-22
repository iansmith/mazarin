package sys

import "time"

// WaitForShepherdReady polls until the named shepherd exists and has called
// SetReady(true), or returns an error if maxWaitSeconds expires.
//
// Each iteration: look up the shepherd by name, check readiness, then sleep
// for a jittered 200-500ms before retrying. Returns nil on success.
//
// Errors:
//   - ErrNoShepherd: the shepherd was never found during the entire wait period.
//   - ErrNotReady: the shepherd was found but never became ready.
//   - ErrAmbiguousShepherd: multiple shepherds match the name (returned immediately).
func WaitForShepherdReady(name string, maxWaitSeconds int) error {
	start := time.Now()
	deadline := start.Add(time.Duration(maxWaitSeconds) * time.Second)
	return waitLoop(deadline, time.Now, time.Sleep, func() error {
		_, err := GetShepherdByName(name)
		return err
	}, func(polls int, err error) {
		// Print the first 3 polls, then only every 8th — enough to show
		// cadence (and oversleep gaps, MAZ-161) without spamming the log.
		if polls > 3 && polls%8 != 0 {
			return
		}
		state := "not ready yet"
		if err == ErrNoShepherd {
			state = "not found"
		}
		UartWriteString("[waitready] " + name + " " + state + " (poll " + Itoa(int64(polls)) +
			" t+" + Itoa(time.Since(start).Milliseconds()) + "ms)\n")
	})
}

// waitLoop drives the poll/sleep cycle with injected clock and poller so the
// loop's contract is host-testable. The structure is poll-FIRST: a poll always
// runs after the last sleep, before the deadline verdict. Under boot-storm
// congestion a 200-500ms sleep wake can be delayed multiple seconds (timer /
// scheduler starvation, MAZ-8 family); with a check-deadline-then-poll loop a
// single oversleep consumed the whole window and converted "peer became ready
// during the sleep" into ErrNotReady (MAZ-161: sharetest polled once at t+1ms,
// overslept past its 5s deadline, and never re-polled a peer that had been
// ready since ~t+1s).
//
// poll returns nil (ready), ErrAmbiguousShepherd (fatal, returned
// immediately), ErrNotReady (exists, not ready) or ErrNoShepherd (not found).
// report, if non-nil, is called after every unsuccessful non-fatal poll.
func waitLoop(deadline time.Time, now func() time.Time, sleep func(time.Duration),
	poll func() error, report func(polls int, err error)) error {
	sawShepherd := false
	polls := 0

	// Simple jitter state seeded from current time. We avoid importing
	// math/rand; a basic LCG is sufficient for sleep jitter.
	jitter := uint32(now().UnixNano())

	for {
		polls++
		err := poll()
		if err == nil {
			return nil
		}
		if err == ErrAmbiguousShepherd {
			return ErrAmbiguousShepherd
		}
		if err == ErrNotReady {
			sawShepherd = true
		}
		if report != nil {
			report(polls, err)
		}
		if !now().Before(deadline) {
			break
		}

		// Sleep 200-500ms with jitter (LCG: next = a*prev + c mod 2^32)
		jitter = jitter*1664525 + 1013904223
		ms := 200 + int(jitter%301) // [200, 500]
		sleep(time.Duration(ms) * time.Millisecond)
	}

	if sawShepherd {
		return ErrNotReady
	}
	return ErrNoShepherd
}

// WaitForCoreServices waits for the three core service shepherds — fs, rachel,
// and linux — to all signal Ready. Returns the first error encountered. Every
// "ordinary" shepherd must call this before issuing path syscalls, font
// requests, or window operations. The core services themselves cannot use
// this (would self-deadlock): fs waits for rachel+linux directly, linux waits
// for fs+rachel, rachel waits for fs.
func WaitForCoreServices(maxWaitSeconds int) error {
	if err := WaitForShepherdReady("fs", maxWaitSeconds); err != nil {
		return err
	}
	if err := WaitForShepherdReady("rachel", maxWaitSeconds); err != nil {
		return err
	}
	if err := WaitForShepherdReady("linux", maxWaitSeconds); err != nil {
		return err
	}
	return nil
}
