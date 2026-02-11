//go:build amd64 && !test_stubs

package ksyscall

// arch_prctl constants
const (
	archSetFS = 0x1002 // Set FS base address
)

// SyscallArchPrctl handles the arch_prctl syscall (x86_64 specific).
// Stock Go runtime uses arch_prctl(ARCH_SET_FS, addr) to set the TLS base.
//
//go:nosplit
func SyscallArchPrctl(code, addr, _, _, _, _ uint64) int64 {
	if code == archSetFS {
		writeFS(addr)
		return 0
	}
	return -22 // EINVAL
}

// writeFS sets the FS_BASE MSR. Defined in abi_stubs_amd64.s.
func writeFS(addr uint64)
