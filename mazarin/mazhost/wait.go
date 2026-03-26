package mazhost

import "mazzy/mazarin/sys"

// WaitForShepherdReady polls until the named shepherd has called SetReady(true),
// or returns an error if maxWaitSeconds expires. See sys.WaitForShepherdReady
// for error semantics.
func WaitForShepherdReady(name string, maxWaitSeconds int) error {
	return sys.WaitForShepherdReady(name, maxWaitSeconds)
}
