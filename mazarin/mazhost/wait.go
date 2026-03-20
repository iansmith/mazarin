package mazhost

import (
	"fmt"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/timer"
	"time"
)

// WaitForReady polls until the named shepherd has called SetReady(true),
// or panics if the timeout expires. Polls every 250ms.
func WaitForReady(name string, timeout time.Duration) {
	deadline := time.Duration(0)
	const pollInterval = 250 * time.Millisecond

	for deadline < timeout {
		if sys.GetReady(name) {
			fmt.Printf("[mazhost] WaitForReady(%q): ready\n", name)
			return
		}
		timer.Sleep(pollInterval)
		deadline += pollInterval
	}
	panic(fmt.Sprintf("[mazhost] WaitForReady(%q): timed out after %v", name, timeout))
}
