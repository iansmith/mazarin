package sys

import "time"

// WaitForReady polls until the named shepherd has called SetReady(true),
// or returns false if the timeout expires. Polls every 250ms.
//
// Tolerates ErrNoShepherd and ErrNotReady during polling (the shepherd may
// still be launching). Returns false immediately on ErrAmbiguousShepherd.
func WaitForReady(name string, timeout time.Duration) bool {
	deadline := time.Duration(0)
	const pollInterval = 250 * time.Millisecond

	for deadline < timeout {
		_, err := GetShepherdByName(name)
		if err == nil {
			return true
		}
		if err == ErrAmbiguousShepherd {
			return false
		}
		// ErrNoShepherd or ErrNotReady — keep waiting
		time.Sleep(pollInterval)
		deadline += pollInterval
	}
	return false
}
