// Package sys provides kernel async messaging support for userspace.
package sys

import (
	"errors"
	"mazzy/shared/mazzy"
	"runtime"
	"unsafe"
)

// KernelAsyncOp represents the operation type in a kernel async message.
type KernelAsyncOp int32

const (
	KernelAsyncOpNone   KernelAsyncOp = 0
	KernelAsyncOpAdd    KernelAsyncOp = 1 // Channel added
	KernelAsyncOpRemove KernelAsyncOp = 2 // Channel removed
)

// KernelAsyncBundle is the message format received from the kernel.
// This struct must match the kernel's definition in kmazarin/kmazarin/channels.go.
type KernelAsyncBundle struct {
	Op        KernelAsyncOp // Operation type
	ChannelId int32         // Channel ID
	Message   [4]int32      // 4-int message payload
}

// ErrNoMessage is returned when no kernel async message is pending.
var ErrNoMessage = errors.New("no kernel async message pending")

// WaitKernelAsync waits for an async message from the kernel.
//
// This function blocks the calling goroutine (not the thread) until
// a message is available. Other goroutines continue to run while waiting.
//
// On the first call, the kernel returns the shepherd's API channel ID
// via an ADD operation.
//
// Returns the message bundle on success, or an error if the syscall failed.
func WaitKernelAsync(bundle *KernelAsyncBundle) error {
	if bundle == nil {
		return errors.New("WaitKernelAsync: nil bundle pointer")
	}

	for {
		r1, _, errno := RawSyscall(mazzy.SysWaitKernelAsync,
			uintptr(unsafe.Pointer(bundle)), 0, 0, 0, 0, 0)

		if errno != 0 {
			return errors.New("WaitKernelAsync: syscall error")
		}

		// r1 is the syscall return value (int64 stored in uintptr)
		result := int64(r1)

		if result == 0 {
			// Message received successfully
			return nil
		}

		if result == -11 {
			// EAGAIN - no message pending, yield and retry
			runtime.Gosched()
			continue
		}

		// Other error
		return errors.New("WaitKernelAsync: unexpected error")
	}
}

// TryWaitKernelAsync attempts to get a kernel async message without blocking.
//
// Returns the message and nil on success, or ErrNoMessage if no message
// is pending, or another error if the syscall failed.
func TryWaitKernelAsync(bundle *KernelAsyncBundle) error {
	if bundle == nil {
		return errors.New("TryWaitKernelAsync: nil bundle pointer")
	}

	r1, _, errno := RawSyscall(mazzy.SysWaitKernelAsync,
		uintptr(unsafe.Pointer(bundle)), 0, 0, 0, 0, 0)

	if errno != 0 {
		return errors.New("TryWaitKernelAsync: syscall error")
	}

	result := int64(r1)

	if result == 0 {
		return nil
	}

	if result == -11 {
		return ErrNoMessage
	}

	return errors.New("TryWaitKernelAsync: unexpected error")
}
