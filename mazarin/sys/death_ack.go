package sys

import (
	"mazzy/shared/mazzy"
	"syscall"
)

// DeathAck notifies the kernel that all in-flight I/O for the dead shepherd
// has drained. The kernel defers page cleanup until this ACK arrives.
func DeathAck(deadSID int16) {
	syscall.RawSyscall6(mazzy.SysDeathAck, uintptr(uint16(deadSID)), 0, 0, 0, 0, 0)
}
