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

	// Simple jitter state seeded from current time. We avoid importing
	// math/rand; a basic LCG is sufficient for sleep jitter.
	jitter := uint32(time.Now().UnixNano())

	for time.Now().Before(deadline) {
		_, err := GetShepherdByName(name)
		if err == nil {
			return nil
		}
		if err == ErrAmbiguousShepherd {
			return ErrAmbiguousShepherd
		}
		if err == ErrNotReady {
			sawShepherd = true
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

