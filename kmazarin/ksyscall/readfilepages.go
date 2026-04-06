package ksyscall

// readfilepages.go — SysReadFilePages: read file data into caller's DMA pages.
//
// The caller allocates MAZARIN_CONTIGUOUS pages and passes the VA + size.
// The kernel delegates to the fs shepherd, which reads the file directly
// into the caller's DMA pages via BlockSubmit (cross-shepherd DMA).
// Zero-copy: data goes from disk → caller's physical pages via DMA.

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/proc"
	"mazzy/shared/sysid"
	"sync/atomic"
)

// SyscallReadFilePages delegates a file read into caller-provided DMA pages.
//
// arg0 = pointer to null-terminated path string (in caller's address space)
// arg1 = destVA (caller's DMA buffer VA — must be in a MAZARIN_CONTIGUOUS clump)
// arg2 = destSize (buffer size in bytes)
// arg3 = fileOffset (byte offset into file to start reading)
// arg4 = readLen (bytes to read; 0 = fill buffer or until EOF)
//
// Returns: 0 on success, negative errno / ErrorCode on failure.
// On success, the DMA buffer is filled with file data.
//
//go:noinline
func SyscallReadFilePages(arg0, arg1, arg2, arg3, arg4, _ uint64) int64 {
	pathPtr := arg0
	destVA := arg1
	destSize := arg2
	fileOffset := arg3
	readLen := arg4

	if pathPtr == 0 {
		return int64(errNullPointer)
	}
	if destVA == 0 || destSize == 0 {
		return -22 // EINVAL
	}

	// Validate that destVA is in a DMA clump belonging to the caller.
	callerShepherd := proc.CurrentShepherd()
	if callerShepherd == nil {
		return -1 // EPERM
	}
	clump := callerShepherd.FindClumpByVA(uintptr(destVA))
	if clump == nil {
		klog.Errf("[ReadFilePages] EFAULT: destVA not in any DMA clump\n")
		return -14 // EFAULT
	}
	endVA := uintptr(destVA + destSize - 1)
	if !clump.ContainsVA(endVA) {
		klog.Errf("[ReadFilePages] EFAULT: buffer extends beyond clump\n")
		return -14 // EFAULT
	}

	// Check if ReadFilePages delegate is registered and ready.
	hpid := atomic.LoadInt32(&syscallDelegates[sysid.ReadFilePages].pid)
	if hpid < 0 {
		klog.Errf("[ReadFilePages] no delegate registered\n")
		return int64(errNoDelegate)
	}
	handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(hpid))
	if handlerShepherd == nil {
		return int64(errNoDelegate)
	}
	if atomic.LoadInt32(&handlerShepherd.Ready) == 0 {
		return int64(errNotReady)
	}

	// Forward via delegate mechanism.
	// The delegate handler receives:
	//   Args[0] = path (copied into data page by DelegateSyscall)
	//   Args[1] = destVA (in caller's address space)
	//   Args[2] = destSize
	//   Args[3] = fileOffset
	//   Args[4] = readLen
	//   CallerSID = available from DelegateQueueEntry.CallerSID
	return DelegateSyscall(sysid.ReadFilePages, pathPtr, destVA, destSize, fileOffset, readLen, 0)
}
