package ksyscall

import (
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/sysid"
	"sync/atomic"
)

// SyscallLoadFile delegates a file load request to the filesystem shepherd (fs.maz).
// The filesystem shepherd reads the file, allocates pages, and transfers them to
// the caller via TransferAndUnmap.
//
// arg0 = pointer to null-terminated path string (in caller's address space)
// arg1 = pointer to LoadFileResult struct (in caller's writable memory)
//
// LoadFileResult layout: { StartVA uint64, NumPages uint64, BytesRead uint64 }
//
// Returns: 0 on success, ErrorCode on failure.
// On success, the result struct is filled with the file's page mapping info.
//
//go:noinline
func SyscallLoadFile(arg0, arg1, _, _, _, _ uint64) int64 {
	pathPtr := arg0
	resultPtr := arg1

	if pathPtr == 0 {
		return int64(errNullPointer)
	}
	if resultPtr == 0 {
		return int64(errNullPointer)
	}

	// Check if LoadFile delegate is registered
	hpid := atomic.LoadInt32(&syscallDelegates[sysid.LoadFile].pid)
	if hpid < 0 {
		serial.RawUARTPuts("[LoadFile] no delegate registered\r\n")
		return int64(errNoDelegate)
	}

	// Check if delegate shepherd is ready
	handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(hpid))
	if handlerShepherd == nil {
		return int64(errNoDelegate)
	}
	if atomic.LoadInt32(&handlerShepherd.Ready) == 0 {
		return int64(errNotReady)
	}

	// Forward via delegate mechanism.
	// arg0 = path pointer (copied into data page by DelegateSyscall)
	// arg1 = result struct pointer (stashed in DelegateCallInfo.CallerBufVA)
	return DelegateSyscall(sysid.LoadFile, pathPtr, resultPtr, 0, 0, 0, 0)
}
