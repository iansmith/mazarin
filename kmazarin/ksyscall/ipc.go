package ksyscall

// ipc.go — SysIPCCall, SysIPCRecv, SysIPCReply kernel syscall implementations.
//
// L4-style synchronous IPC between priests:
// - Client calls SysIPCCall(targetPID, reqVA, reqPages) → blocks until reply
// - Server calls SysIPCRecv(resultPtr) → blocks until request arrives
// - Server calls SysIPCReply(clientPID, replyVA, replyPages) → wakes client
//
// Pages are transferred (unmapped from sender, mapped into receiver) using
// the same page transfer mechanism as SyscallTransferPages.

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
)

// SyscallIPCCall sends an IPC request to a target priest and blocks until reply.
//
// arg0 = targetPID    (priest to send request to)
// arg1 = requestVA    (start of request pages in caller's address space, page-aligned)
// arg2 = requestPages (number of 4KB pages, 1..256)
//
// Returns: packed int64 with replyVA (bits 63:12) | replyPages (bits 11:0).
// replyVA is page-aligned so bits 11:0 are free for the page count.
//
//go:noinline
func SyscallIPCCall(arg0, arg1, arg2, _, _, _ uint64) int64 {
	targetPID := int16(arg0)
	requestVA := uintptr(arg1)
	requestPages := int(arg2)

	if requestPages < 1 || requestPages > MaxTransferPages {
		return -22 // EINVAL
	}
	if requestVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	callerPriest := proc.CurrentPriest()
	if callerPriest == nil {
		return -1 // EPERM
	}
	callerPID := callerPriest.PID
	_, callerTID := getCurrentThreadPIDAndTID()

	if int16(callerPID) == targetPID {
		return -22 // EINVAL — can't IPC to self
	}

	targetPriest := proc.FindPriestByPID(proc.PriestId(targetPID))
	if targetPriest == nil || targetPriest.PageTableL0PA == 0 {
		return -3 // ESRCH
	}

	// Transfer request pages from caller to target
	targetReqVA := transferPagesForIPC(callerPriest, targetPriest, requestVA, requestPages)
	if targetReqVA < 0 {
		return targetReqVA
	}

	// Enqueue request on target's IPC queue
	req := proc.IPCRequest{
		SenderPID:    callerPID,
		SenderTID:    callerTID,
		RequestVA:    uint64(targetReqVA),
		RequestPages: uint32(requestPages),
	}

	if !ipcQueuePush(targetPriest, req) {
		serial.RawUARTPuts("[IPC] queue full\r\n")
		return -11 // EAGAIN
	}

	serial.RawUARTPuts("[IPC] Call P")
	serial.RawUARTDecimal(uint64(callerPID))
	serial.RawUARTPuts("->P")
	serial.RawUARTDecimal(uint64(targetPID))
	serial.RawUARTPuts(" ")

	// If target has a thread blocked in SysIPCRecv, fill its result struct
	// and wake it. The request is now in the queue; we fill the result directly
	// so the server thread sees it when it resumes.
	if targetPriest.IPCRecvTID >= 0 {
		recvTID := targetPriest.IPCRecvTID
		resultPtr := targetPriest.IPCRecvResultPtr
		targetPriest.IPCRecvTID = -1
		targetPriest.IPCRecvResultPtr = 0

		// Dequeue the request we just enqueued and write it to the server's result struct
		dequeuedReq, _ := ipcQueuePop(targetPriest)
		writeIPCRecvResult(resultPtr, targetPriest.PageTableL0PA, dequeuedReq)
		wakeIPCThread(int32(recvTID), 0)
	}

	// Block caller until reply arrives
	ctx := blockForIPCCall()
	if ctx == 0 {
		return -11 // EAGAIN
	}
	SetSyscallSwitchTarget(ctx)

	// Return value overwritten by WakeIPCThread when reply arrives.
	// The packed value encodes replyVA | replyPages.
	return 0
}

// SyscallIPCRecv blocks until an IPC request arrives from a client.
//
// arg0 = resultPtr (VA of IPCRecvResult struct: {SenderPID int64, RequestVA uint64, RequestPages uint64})
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallIPCRecv(arg0, _, _, _, _, _ uint64) int64 {
	resultPtr := uintptr(arg0)

	callerPriest := proc.CurrentPriest()
	if callerPriest == nil {
		return -1 // EPERM
	}
	if resultPtr == 0 {
		return -14 // EFAULT
	}

	// Check if there's already a pending request
	req, ok := ipcQueuePop(callerPriest)
	if ok {
		writeIPCRecvResult(resultPtr, callerPriest.PageTableL0PA, req)
		serial.RawUARTPuts("[IPC] Recv P")
		serial.RawUARTDecimal(uint64(req.SenderPID))
		serial.RawUARTPuts(" ")
		return 0
	}

	// No request pending — stash resultPtr and block
	_, callerTID := getCurrentThreadPIDAndTID()
	callerPriest.IPCRecvTID = callerTID
	callerPriest.IPCRecvResultPtr = resultPtr

	ctx := blockForIPCRecv()
	if ctx == 0 {
		callerPriest.IPCRecvTID = -1
		callerPriest.IPCRecvResultPtr = 0
		return -11 // EAGAIN
	}
	SetSyscallSwitchTarget(ctx)

	// When woken by SysIPCCall, the result struct is already filled
	// and the return value is set to 0 by wakeIPCThread.
	return 0
}

// SyscallIPCReply sends a reply to a client priest blocked in SysIPCCall.
//
// arg0 = clientPID  (priest to send reply to)
// arg1 = replyVA    (start of reply pages in caller's address space, page-aligned)
// arg2 = replyPages (number of 4KB pages, 1..256)
//
// Returns: 0 on success, negative errno on error.
//
//go:noinline
func SyscallIPCReply(arg0, arg1, arg2, _, _, _ uint64) int64 {
	clientPID := int16(arg0)
	replyVA := uintptr(arg1)
	replyPages := int(arg2)

	if replyPages < 1 || replyPages > MaxTransferPages {
		return -22 // EINVAL
	}
	if replyVA&(kmem.PageSize-1) != 0 {
		return -22 // EINVAL
	}

	callerPriest := proc.CurrentPriest()
	if callerPriest == nil {
		return -1 // EPERM
	}

	clientPriest := proc.FindPriestByPID(proc.PriestId(clientPID))
	if clientPriest == nil {
		return -3 // ESRCH
	}

	// Transfer reply pages from server to client
	clientReplyVA := transferPagesForIPC(callerPriest, clientPriest, replyVA, replyPages)
	if clientReplyVA < 0 {
		return clientReplyVA
	}

	// Pack replyVA | replyPages into the wake value.
	// replyVA is page-aligned (bits 11:0 = 0), so we pack pages there.
	wakeVal := int64(uint64(clientReplyVA) | uint64(replyPages))

	serial.RawUARTPuts("[IPC] Reply P")
	serial.RawUARTDecimal(uint64(callerPriest.PID))
	serial.RawUARTPuts("->P")
	serial.RawUARTDecimal(uint64(clientPID))
	serial.RawUARTPuts(" ")

	// Wake the client thread
	wakeIPCThreadByPID(clientPID, wakeVal)

	return 0
}

// transferPagesForIPC transfers pages from source priest to target priest.
// Returns the target VA base on success, or negative errno on failure.
//
//go:noinline
func transferPagesForIPC(sourcePriest, targetPriest *proc.Priest, sourceVA uintptr, numPages int) int64 {
	sourcePID := int16(sourcePriest.PID)
	targetPID := int16(targetPriest.PID)
	sourceL0PA := sourcePriest.PageTableL0PA

	var pas [MaxTransferPages]uintptr
	for i := 0; i < numPages; i++ {
		va := sourceVA + uintptr(i)*kmem.PageSize
		pa := kmem.WalkUserPageTableWithL0(va, sourceL0PA)
		if pa == 0 {
			return -14 // EFAULT
		}
		pa = pa &^ (kmem.PageSize - 1)
		desc := kmem.GetPageDescriptor(pa)
		if desc == nil || desc.Owner != sourcePID {
			return -1 // EPERM
		}
		pas[i] = pa
	}

	totalSize := uint64(numPages) * uint64(kmem.PageSize)
	targetVABase := bumpAllocForPriest(targetPriest, totalSize)
	if targetVABase == 0 {
		return -12 // ENOMEM
	}
	targetPriest.Spans.Add(targetVABase, totalSize)

	for i := 0; i < numPages; i++ {
		va := sourceVA + uintptr(i)*kmem.PageSize
		pa := pas[i]
		kmem.UnmapUserPageWithL0(va, sourceL0PA)
		kmem.TransferPageOwnership(pa, sourcePID, targetPID)
		targetVA := uintptr(targetVABase) + uintptr(i)*kmem.PageSize
		kmem.MapPageInProcess(targetPID, targetVA, pa, 0) // RW
	}

	sourcePriest.Spans.Remove(uint64(sourceVA), totalSize)
	return int64(targetVABase)
}

// writeIPCRecvResult writes an IPCRecvResult struct to the receiver's memory
// via kernel scratch mapping.
//
//go:noinline
func writeIPCRecvResult(resultPtr uintptr, l0PA uintptr, req proc.IPCRequest) {
	writeU64ToUser(resultPtr+0, uint64(req.SenderPID), l0PA)
	writeU64ToUser(resultPtr+8, req.RequestVA, l0PA)
	writeU64ToUser(resultPtr+16, uint64(req.RequestPages), l0PA)
}
