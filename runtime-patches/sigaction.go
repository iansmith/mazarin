// KMAZARIN OVERLAY: replaces runtime/sigaction.go for RISC-V
//
// On RISC-V, the standard rt_sigaction path uses EBREAK → trap handler →
// SyscallDispatch. During early boot, the Go code running inside the trap
// handler triggers load address misaligned exceptions (scause=4) which
// corrupt the syscall return value, causing sysSigaction to panic.
//
// This overlay bypasses the assembly rt_sigaction entirely by calling the
// kernel's SyscallRtSigaction handler directly via go:linkname. Since the
// handler is pure Go, no EBREAK/trap path is needed.

//go:build (linux && !amd64 && !arm64 && !ppc64le) || (freebsd && !amd64)

package runtime

import "unsafe"

//go:linkname kmazarinSyscallRtSigaction mazzy/kmazarin/ksyscall.SyscallRtSigaction
func kmazarinSyscallRtSigaction(signum, actPtr, oactPtr, sigsetsize, zero1, zero2 uint64) int64

//go:nosplit
//go:nowritebarrierrec
func sigaction(sig uint32, new, old *sigactiont) {
	kmazarinSyscallRtSigaction(
		uint64(sig),
		uint64(uintptr(unsafe.Pointer(new))),
		uint64(uintptr(unsafe.Pointer(old))),
		uint64(unsafe.Sizeof(sigactiont{}.sa_mask)),
		0, 0)
}
