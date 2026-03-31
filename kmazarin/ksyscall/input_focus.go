package ksyscall

// SyscallRequestWindowManager claims the window manager role for the calling shepherd.
// First-come-first-served; only one WM per system.
// Returns: 0 on success, -1 (EPERM) if already claimed.
//
//go:noinline
func SyscallRequestWindowManager(_, _, _, _, _, _ uint64) int64 {
	sid := getCurrentThreadSID()
	if RequestWindowManagerKernel(int32(sid)) {
		return 0
	}
	return -1 // EPERM — already claimed
}
