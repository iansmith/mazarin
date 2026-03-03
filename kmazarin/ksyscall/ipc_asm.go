//go:build !test_stubs

package ksyscall

import (
	"mazzy/kmazarin/proc"
	_ "unsafe" // for go:linkname
)

// Forward declarations for IPC bridge functions provided via go:linkname.

// blockForIPCCall blocks the current thread (client) waiting for IPC reply.
//
//go:linkname blockForIPCCall main.BlockForIPCCall
func blockForIPCCall() uintptr

// blockForIPCRecv blocks the current thread (server) waiting for IPC request.
//
//go:linkname blockForIPCRecv main.BlockForIPCRecv
func blockForIPCRecv() uintptr

// wakeIPCThread wakes a thread blocked in IPC by TID, setting its return value.
//
//go:linkname wakeIPCThread main.WakeIPCThread
func wakeIPCThread(tid int32, returnVal int64)

// wakeIPCThreadByPID finds a thread with the given PID in ThreadBlockedIPC state and wakes it.
//
//go:linkname wakeIPCThreadByPID main.WakeIPCThreadByPID
func wakeIPCThreadByPID(pid int16, returnVal int64)

// getCurrentThreadPIDAndTID returns the PID and TID of the current thread.
//
//go:linkname getCurrentThreadPIDAndTID main.GetCurrentThreadPIDAndTID
func getCurrentThreadPIDAndTID() (proc.PriestId, int16)

// ipcQueuePush enqueues an IPC request onto a priest's queue.
//
//go:linkname ipcQueuePush main.IPCQueuePush
func ipcQueuePush(p *proc.Priest, req proc.IPCRequest) bool

// ipcQueuePop dequeues an IPC request from a priest's queue.
//
//go:linkname ipcQueuePop main.IPCQueuePop
func ipcQueuePop(p *proc.Priest) (proc.IPCRequest, bool)

// getBlockDeviceOwnerPID returns the PID of the block device owner priest.
//
//go:linkname getBlockDeviceOwnerPID main.GetBlockDeviceOwnerPID
func getBlockDeviceOwnerPID() int16
