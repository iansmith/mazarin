//go:build arm64

package ksyscall

import (
	"mazzy/kmazarin/kmem"
	_ "unsafe" // for go:linkname
)

// KernelAsyncOp mirrors the type in main package
type KernelAsyncOp int32

// KernelAsyncBundle mirrors the type in main package for syscall boundary
type KernelAsyncBundle struct {
	Op        KernelAsyncOp
	ChannelId int32
	Message   [4]int32
}

// SyscallWaitKernelAsync waits for an async message from the kernel.
//
// This syscall does NOT block the thread. It returns immediately with:
//   - 0 if a message was available (written to bundlePtr)
//   - -EAGAIN (-11) if no message is pending
//
// The calling goroutine should handle blocking at the Go level (using
// channels, futex, or yield loops) while other goroutines continue to run.
//
// Args:
//
//	arg0: pointer to KernelAsyncBundle to fill
//
// Returns: 0 on success, -EFAULT if bundlePtr is null, -EAGAIN if no message
//
//go:nosplit
func SyscallWaitKernelAsync(bundlePtr, _, _, _, _, _ uint64) int64 {
	if bundlePtr == 0 {
		return -14 // EFAULT
	}

	// Get current thread's PID
	pid := getCurrentThreadPID()
	if pid < 0 {
		return -22 // EINVAL - no valid priest
	}

	// Try to dequeue a pending message for this priest
	bundle, hasPending := dequeueKernelAsyncWrapper(int16(pid))
	if !hasPending {
		return -11 // EAGAIN - no message pending
	}

	// Write the bundle to userspace field by field via safe accessors
	// KernelAsyncBundle: Op (int32), ChannelId (int32), Message ([4]int32)
	// Total: 24 bytes. Write as 3 uint64s (packed naturally).
	base := uintptr(bundlePtr)
	// First 8 bytes: Op (4) + ChannelId (4)
	header := uint64(uint32(bundle.Op)) | (uint64(uint32(bundle.ChannelId)) << 32)
	if !kmem.WriteUserUint64(base, header) {
		return -14 // EFAULT
	}
	// Next 8 bytes: Message[0] + Message[1]
	msg01 := uint64(uint32(bundle.Message[0])) | (uint64(uint32(bundle.Message[1])) << 32)
	if !kmem.WriteUserUint64(base+8, msg01) {
		return -14 // EFAULT
	}
	// Last 8 bytes: Message[2] + Message[3]
	msg23 := uint64(uint32(bundle.Message[2])) | (uint64(uint32(bundle.Message[3])) << 32)
	if !kmem.WriteUserUint64(base+16, msg23) {
		return -14 // EFAULT
	}

	return 0
}

// getCurrentThreadPID returns the PID of the current thread.
// Returns -1 if no valid thread.
//
//go:nosplit
func getCurrentThreadPID() int16 {
	return getCurrentThreadPIDWrapper()
}

// Linkname wrappers to access main package functions

//go:linkname getCurrentThreadPIDWrapper main.getCurrentThreadPIDWrapper
func getCurrentThreadPIDWrapper() int16

//go:linkname dequeueKernelAsyncWrapper main.dequeueKernelAsyncWrapper
func dequeueKernelAsyncWrapper(pid int16) (KernelAsyncBundle, bool)
