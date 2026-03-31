package sys

import (
	"errors"
	"mazzy/shared/mazzy"
)

// RequestWindowManager claims the window manager role for the calling shepherd.
// First-come-first-served; only one WM per system.
func RequestWindowManager() error {
	r1, _, errno := RawSyscall(mazzy.SysRequestWindowManager, 0, 0, 0, 0, 0, 0)
	if errno != 0 || int64(r1) < 0 {
		return errors.New("RequestWindowManager: already claimed")
	}
	return nil
}
