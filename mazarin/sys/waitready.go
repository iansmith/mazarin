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
	deadline := time.Now().Add(time.Duration(maxWaitSeconds) * time.Second)
	sawShepherd := false
	polls := 0

	// Simple jitter state seeded from current time. We avoid importing
	// math/rand; a basic LCG is sufficient for sleep jitter.
	jitter := uint32(time.Now().UnixNano())

	for time.Now().Before(deadline) {
		polls++
		sid, err := GetShepherdByName(name)
		if err == nil {
			return nil
		}
		if err == ErrAmbiguousShepherd {
			return ErrAmbiguousShepherd
		}
		if err == ErrNotReady {
			sawShepherd = true
			// Only print first 3 polls — after that, silence.
			if polls <= 3 {
				UartWriteString("[waitready] " + name + " sid=" + itoa(int64(sid)) + " not ready yet (poll " + itoa(int64(polls)) + ")\n")
			}
		} else if polls <= 3 {
			UartWriteString("[waitready] " + name + " not found (poll " + itoa(int64(polls)) + ")\n")
		}

		// Sleep 200-500ms with jitter (LCG: next = a*prev + c mod 2^32)
		jitter = jitter*1664525 + 1013904223
		ms := 200 + int(jitter%301) // [200, 500]
		time.Sleep(time.Duration(ms) * time.Millisecond)
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

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
