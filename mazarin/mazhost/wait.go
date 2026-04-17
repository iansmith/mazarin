//go:build mazhost

package mazhost

import "mazzy/mazarin/sys"

// WaitForShepherdReady polls until the named shepherd has called SetReady(true),
// or returns an error if maxWaitSeconds expires. See sys.WaitForShepherdReady
// for error semantics.
func WaitForShepherdReady(name string, maxWaitSeconds int) error {
	return sys.WaitForShepherdReady(name, maxWaitSeconds)
}

// WaitForCoreServices waits for fs, rachel, and linux to all signal Ready.
// See sys.WaitForCoreServices for the rationale.
func WaitForCoreServices(maxWaitSeconds int) error {
	return sys.WaitForCoreServices(maxWaitSeconds)
}
