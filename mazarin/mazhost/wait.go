package mazhost

import (
	"mazzy/mazarin/sys"
	"time"
)

// WaitForReady polls until the named shepherd has called SetReady(true),
// or returns false if the timeout expires. Delegates to sys.WaitForReady.
func WaitForReady(name string, timeout time.Duration) bool {
	return sys.WaitForReady(name, timeout)
}
