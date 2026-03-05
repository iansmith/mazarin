package main

// ipc_bridge.go — Bridge between ksyscall IPC handlers and the scheduler.
//
// The ksyscall package cannot directly access scheduler internals (thread states,
// ready queues). These functions are called from ksyscall via go:linkname to
// block/wake threads for IPC operations.

import (
	"mazzy/kmazarin/asm"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"unsafe"
)

// BlockForIPCCall blocks the current thread (client) waiting for an IPC reply.
// Returns the context pointer of the next thread to switch to, or 0.
//
//go:nosplit
//go:noinline
func BlockForIPCCall() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	// Find next ready thread (prefer userspace if caller is userspace)
	var next *Thread
	if t.PageTableL0PA != 0 {
		next = findReadyUserspaceThreadSchedLockHeld(-1)
	} else {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		processStaticDeadlinesSchedLockHeld()
		if t.PageTableL0PA != 0 {
			next = findReadyUserspaceThreadSchedLockHeld(-1)
		} else {
			next = findReadyThreadSchedLockHeld()
		}
	}
	if next == nil && t.PageTableL0PA != 0 {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t.State = ThreadBlockedIPC

	serial.RawUARTPuts("IC")
	serial.RawUARTDecimal(uint64(t.TID))
	serial.RawUARTPuts(">")
	serial.RawUARTDecimal(uint64(next.TID))
	serial.RawUARTPuts(" ")

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// BlockForIPCRecv blocks the current thread (server) waiting for an IPC request.
// The priest's IPCRecvTID is set before calling this.
// Returns the context pointer of the next thread to switch to, or 0.
//
//go:nosplit
//go:noinline
func BlockForIPCRecv() uintptr {
	savedDAIF := NormalSchedulerFunc.DisableAndSaveDAIF()
	schedulerLock.Lock()

	t := GetCurrentThread()
	if t == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	var next *Thread
	if t.PageTableL0PA != 0 {
		next = findReadyUserspaceThreadSchedLockHeld(-1)
	} else {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		processStaticDeadlinesSchedLockHeld()
		if t.PageTableL0PA != 0 {
			next = findReadyUserspaceThreadSchedLockHeld(-1)
		} else {
			next = findReadyThreadSchedLockHeld()
		}
	}
	if next == nil && t.PageTableL0PA != 0 {
		next = findReadyThreadSchedLockHeld()
	}
	if next == nil {
		schedulerLock.Unlock()
		NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)
		return 0
	}

	t.State = ThreadBlockedIPCRecv

	serial.RawUARTPuts("IR")
	serial.RawUARTDecimal(uint64(t.TID))
	serial.RawUARTPuts(">")
	serial.RawUARTDecimal(uint64(next.TID))
	serial.RawUARTPuts(" ")

	schedulerLock.Unlock()
	NormalSchedulerFunc.EnableAndRestoreDAIF(savedDAIF)

	return uintptr(unsafe.Pointer(&next.Context))
}

// WakeIPCThread wakes a thread blocked in ThreadBlockedIPC or ThreadBlockedIPCRecv,
// setting its return value.
//
//go:nosplit
//go:noinline
func WakeIPCThread(tid int32, returnVal int64) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	t := threadLookupByTID(tid)
	if t != nil && (t.State == ThreadBlockedIPC || t.State == ThreadBlockedIPCRecv) {
		t.Context.SetReturnValue(uint64(returnVal))
		t.PreemptElapsed = 0
		t.State = ThreadReady
		enqueueReadySchedLockHeld(t)
		asm.Dsb()

		serial.RawUARTPuts("IW")
		serial.RawUARTDecimal(uint64(tid))
		serial.RawUARTPuts(" ")
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}

// GetCurrentThreadPIDAndTID returns the PID and TID of the currently running thread.
//
//go:nosplit
//go:noinline
func GetCurrentThreadPIDAndTID() (proc.PriestId, int16) {
	t := GetCurrentThread()
	if t == nil {
		return -1, -1
	}
	return t.PID, int16(t.TID)
}

// IPCQueuePush enqueues an IPC request onto a priest's queue.
// Returns false if the queue is full.
//
//go:nosplit
func IPCQueuePush(p *proc.Priest, req proc.IPCRequest) bool {
	next := (p.IPCQueueTail + 1) % proc.MaxIPCPending
	if next == p.IPCQueueHead {
		return false // Queue full
	}
	p.IPCQueue[p.IPCQueueTail] = req
	p.IPCQueueTail = next
	return true
}

// IPCQueuePop dequeues an IPC request from a priest's queue.
// Returns false if the queue is empty.
//
//go:nosplit
func IPCQueuePop(p *proc.Priest) (proc.IPCRequest, bool) {
	if p.IPCQueueHead == p.IPCQueueTail {
		return proc.IPCRequest{}, false // Queue empty
	}
	req := p.IPCQueue[p.IPCQueueHead]
	p.IPCQueueHead = (p.IPCQueueHead + 1) % proc.MaxIPCPending
	return req, true
}

// WakeIPCThreadByPID finds a thread with the given PID in ThreadBlockedIPC state
// and wakes it with the given return value.
// Called from ksyscall.SyscallIPCReply via linkname.
//
//go:noinline
func WakeIPCThreadByPID(pid int16, returnVal int64) {
	savedDAIF := SaveAndDisableIRQs()
	schedulerLock.Lock()

	for i := 0; i < MaxThreads; i++ {
		if !threadListInUse[i] {
			continue
		}
		t := &threadListData[i]
		if t.PID == proc.PriestId(pid) && t.State == ThreadBlockedIPC {
			t.Context.SetReturnValue(uint64(returnVal))
			t.PreemptElapsed = 0
			t.State = ThreadReady
			enqueueReadySchedLockHeld(t)
			asm.Dsb()

			serial.RawUARTPuts("IWp")
			serial.RawUARTDecimal(uint64(t.TID))
			serial.RawUARTPuts(" ")
			break
		}
	}

	schedulerLock.Unlock()
	RestoreIRQs(savedDAIF)
}
