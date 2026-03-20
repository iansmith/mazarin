// notify.go — Client-side dirty notification API.
//
// WaitDirty blocks until one or more eager-notify attributes become dirty.
// OnDirty returns a channel that receives batches of dirty slot numbers.

package attr

import "mazzy/mazarin/sys"

// WaitDirty blocks until one or more eager-notify attributes become dirty.
// Returns the slot numbers of the dirty attributes, or nil on overflow
// (meaning the caller should re-check all eager attributes).
func WaitDirty() []uint16 {
	var buf [64]uint16
	n := sys.AttrWaitDirty(buf[:])
	if n < 0 {
		return nil // overflow — re-scan all
	}
	if n == 0 {
		return nil
	}
	result := make([]uint16, n)
	copy(result, buf[:n])
	return result
}

// OnDirty returns a channel that receives a batch of dirty slot numbers
// whenever eager-notify attributes change. Spawns a background goroutine.
// The channel has a buffer of 4 to absorb burst notifications.
func OnDirty() <-chan []uint16 {
	ch := make(chan []uint16, 4)
	go func() {
		for {
			slots := WaitDirty()
			ch <- slots
		}
	}()
	return ch
}
