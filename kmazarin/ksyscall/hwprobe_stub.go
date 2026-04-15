//go:build !riscv64

package ksyscall

// SyscallRiscvHWProbe is a no-op on non-RISC-V architectures.
// The dispatch table entry exists but is never reached because no native
// syscall number maps to SysIDRiscvHWProbe on ARM64 or x86_64.
//
//go:nosplit
func SyscallRiscvHWProbe(_, _, _, _, _, _ uint64) int64 {
	return -38 // ENOSYS
}
