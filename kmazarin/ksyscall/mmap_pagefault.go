package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/kmazarin/proc"
	"mazzy/kmazarin/serial"
	"mazzy/shared/ipc"
	"mazzy/shared/sysid"
	"sync/atomic"
	"unsafe"
)

func init() {
	kmem.OnFileMappedPageFault = handleFileMappedPageFault
}

// handleFileMappedPageFault handles a page fault in a file-backed mmap region.
// It allocates a physical frame, sends a read request to the linux shepherd
// to fill it with file data, and blocks the faulting thread. The assembly
// exception handler checks GetSyscallSwitchTarget after we return and performs
// the context switch.
//
//go:noinline
func handleFileMappedPageFault(faultAddr uintptr, fm *proc.FileMapping) bool {
	pageAddr := faultAddr &^ (kmem.PageSize - 1)

	// Find the linux shepherd (delegate handler for Read)
	handlerSID := int16(atomic.LoadInt32(&syscallDelegates[sysid.Read].pid))
	if handlerSID < 0 {
		serial.RawUARTPuts("[mmap-pf] no Read delegate handler\r\n")
		return false
	}
	handlerShepherd := proc.FindShepherdBySID(proc.ShepherdId(handlerSID))
	if handlerShepherd == nil {
		serial.RawUARTPuts("[mmap-pf] handler shepherd gone\r\n")
		return false
	}

	// Allocate a physical frame for the page
	callerSID := int16(proc.CurrentShepherd().PID)
	framePA := kmem.AllocPage(kmem.PageFileMmap, callerSID)
	if framePA == 0 {
		serial.RawUARTPuts("[mmap-pf] ENOMEM\r\n")
		return false
	}

	// Zero the frame
	scratchVA := kmem.MapPAToKernelScratch(framePA)
	if scratchVA == 0 {
		kmem.ReleasePageByPA(framePA)
		return false
	}
	zeroPage(scratchVA)

	// Map the frame into the linux shepherd's address space so it can fill it
	handlerDataVA := bumpAllocForShepherd(handlerShepherd, 4096)
	if handlerDataVA == 0 {
		kmem.ReleasePageByPA(framePA)
		return false
	}
	kmem.MapPageInProcess(handlerSID, uintptr(handlerDataVA), framePA, 0) // RW
	handlerShepherd.Spans.Add(handlerDataVA, 4096)

	// Compute file offset for this page
	pageOffset := uint64(pageAddr) - fm.StartVA
	fileOffset := fm.FileOffset + pageOffset

	// Get current thread info for delegation tracking
	_, callerTID := getCurrentThreadSIDAndTID()

	// Set up DelegateCallInfo for the reply path
	if int(callerTID) >= MaxDelegateThreads {
		reclaimDataPage(framePA, handlerDataVA, handlerSID, handlerShepherd)
		return false
	}
	info := &delegateCallInfos[callerTID]
	info.DataPagePA = framePA
	info.DataPageVA = handlerDataVA
	info.HandlerSID = handlerSID
	info.CallerSID = callerSID
	info.CallerBufVA = uintptr(pageAddr) // page-aligned fault VA (mapping target)
	info.CallerBufLen = 4096
	info.CallerL0PA = uintptr(kmem.ReadCurrentL0PA())
	info.SysID = sysid.MmapPageFill
	info.InUse = true

	// Send MmapPageFill request to linux shepherd
	reqPayload := ipc.FSDelegateReqPayload{
		SysID:     uint16(sysid.MmapPageFill),
		CallerSID: fm.CallerSID,
		CallerTID: callerTID,
		Args:      [6]uint64{uint64(fm.FD), fileOffset, 4096, 0, 0, 0},
		DataVA:    handlerDataVA,
		DataLen:   4096,
	}
	msg := ipc.EncodeFSDelegateReq(&reqPayload)
	result, _ := uringSendKernel(-1, handlerSID, uintptr(unsafe.Pointer(&msg)))
	if result < 0 {
		info.InUse = false
		reclaimDataPage(framePA, handlerDataVA, handlerSID, handlerShepherd)
		serial.RawUARTPuts("[mmap-pf] uring send failed\r\n")
		return false
	}

	// Block the faulting thread until the linux shepherd fills the page
	ctx := blockForDelegatedSyscall()
	if ctx == 0 {
		serial.RawUARTPuts("[mmap-pf] NO NEXT THREAD\r\n")
		return false
	}
	SetSyscallSwitchTarget(ctx)

	return true
}
